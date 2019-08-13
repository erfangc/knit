package cmd

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
	"github.com/spf13/cobra"
	"io"
	"log"
	"os/exec"
	"strings"
	"time"
)

var dnsName string
var hostedZone string
var gitRepo string
var email string
var sess *session.Session

var EKSCmd = &cobra.Command{
	Use:   "eks",
	Short: "Work with an EKS cluster",
	Run: func(cmd *cobra.Command, args []string) {
		cmds := []string{"kubectl", "helm"}
		for _, cmd := range cmds {
			if !commandExists(cmd) {
				panic(cmd + " does not exist")
			}
		}
		log.Println("Setting up cluster ...")
		installTiller()
		time.Sleep(1000 * time.Millisecond)
		installNginx()
		time.Sleep(1000 * time.Millisecond)
		installCertManager()
		time.Sleep(1000 * time.Millisecond)
		installLetsEncryptIssuer(email)
		time.Sleep(1000 * time.Millisecond)
		installFluxCD(gitRepo)
		time.Sleep(1000 * time.Millisecond)
		installSealedSecrets()
	},
}

func installSealedSecrets() {
	version := "v0.8.1"
	executeP(
		"kubectl",
		"apply",
		"-f",
		"https://github.com/bitnami-labs/sealed-secrets/releases/download/"+version+"/controller.yaml",
	)
	sm := secretsmanager.New(sess)
	//
	// see if a RSA keypair for already exist on the AWS account
	//
	publicKeyResponse, err := sm.GetSecretValue(&secretsmanager.GetSecretValueInput{
		SecretId: aws.String("sealed-secrets/master-key/public"),
	})
	privateKeyResponse, err := sm.GetSecretValue(&secretsmanager.GetSecretValueInput{
		SecretId: aws.String("sealed-secrets/master-key/private"),
	})
	if err != nil {
		if awsError, ok := err.(awserr.Error); ok {
			switch awsError.Code() {
			case secretsmanager.ErrCodeResourceNotFoundException:
				log.Println("master key for sealed-secrets does not exist, attempting to create a new one")
				//
				// extract the public & private key
				//
				publicKey := execute(
					"kubectl",
					"-n",
					"kube-system",
					"get",
					"secret",
					"-l",
					"sealedsecrets.bitnami.com/sealed-secrets-key=active",
					"-o",
					"jsonpath='{.items[0].data.tls\\.crt}'",
				)
				privateKey := execute(
					"kubectl",
					"-n",
					"kube-system",
					"get",
					"secret",
					"-l",
					"sealedsecrets.bitnami.com/sealed-secrets-key=active",
					"-o",
					"jsonpath='{.items[0].data.tls\\.key}'",
				)
				resp1, err := sm.CreateSecret(
					&secretsmanager.CreateSecretInput{
						Name:         aws.String("sealed-secrets/master-key/public"),
						SecretString: aws.String(publicKey),
					})
				resp2, err := sm.CreateSecret(
					&secretsmanager.CreateSecretInput{
						Name:         aws.String("sealed-secrets/master-key/private"),
						SecretString: aws.String(privateKey),
					},
				)
				if err != nil {
					panic(err)
				}
				log.Println("public key ARN: " + *resp1.ARN)
				log.Println("private key ARN: " + *resp2.ARN)
			default:
				panic(awsError)
			}
		}
	} else {
		//
		// kubectl apply the restored master keys into the cluster
		//
		log.Println("Restoring sealed-secrets master key from secrets manager")
		doc := `
		apiVersion: v1
		data:
		  tls.crt: ` + *publicKeyResponse.SecretString + `
		  tls.key: ` + *privateKeyResponse.SecretString + `
		kind: Secret
		metadata:
		  creationTimestamp: null
		  namespace: kube-system
		  name: sealed-secrets-key
		  selfLink: /api/v1/namespaces/kube-system/secrets/sealed-secrets-key
		type: kubernetes.io/tls
		`
		log.Println(
			executeWithStdin(
				doc,
				"kubectl",
				"apply",
				"-f",
				"-",
			),
		)
		//
		// delete the old controller so the newly launched controller pod can pick up the new secrets
		//
		executeP(
			"kubectl",
			"delete",
			"-n",
			"kube-system",
			"pod",
			"-l",
			"name=sealed-secrets-controller",
		)
	}
}

func init() {
	EKSCmd.Flags().StringVar(&gitRepo, "git-repo", "", "The Git repository to monitor")
	EKSCmd.Flags().StringVar(&email, "email", "", "The email used to procure TLS certificates, this is passed to cert-manager")
	EKSCmd.Flags().StringVar(&dnsName, "dns-name", "", "The DNS name of the environment & cluster being initialized")
	EKSCmd.Flags().StringVar(&hostedZone, "hosted-zone", "", "The route 53 hosted zone to use for creating CNAME or A DNS record")
	EKSCmd.MarkFlagRequired("git-repo")
	EKSCmd.MarkFlagRequired("email")
	EKSCmd.MarkFlagRequired("dns-name")
	EKSCmd.MarkFlagRequired("hosted-zone")
	// FIXME do not hard code region
	sess = session.Must(session.NewSession(&aws.Config{Region: aws.String("us-east-1")}))
}

func installNginx() {
	executeP("helm", "install", "stable/nginx-ingress", "--name", "default")
	var externalHostName = execute(
		"kubectl",
		"get",
		"svc",
		"default-nginx-ingress-controller",
		"-o",
		"jsonpath='{.status.loadBalancer.ingress[].hostname}'",
	)
	for externalHostName == "''" {
		log.Println("No external host name found for nginx-ingress-controller, waiting ...")
		time.Sleep(10 * time.Second)
		externalHostName = execute(
			"kubectl",
			"get",
			"svc",
			"default-nginx-ingress-controller",
			"-o",
			"jsonpath='{.status.loadBalancer.ingress[].hostname}'",
		)
	}
	log.Println("External host name found for nginx-ingress-controller: " + strings.ReplaceAll(externalHostName, "'", ""))
	log.Println("Creating a DNS record for domain " + dnsName + " on hosted zone " + hostedZone)
	r53 := route53.New(sess)
	response, err := r53.ChangeResourceRecordSets(
		&route53.ChangeResourceRecordSetsInput{
			ChangeBatch: &route53.ChangeBatch{
				Changes: []*route53.Change{
					{
						Action: aws.String("UPSERT"),
						ResourceRecordSet: &route53.ResourceRecordSet{
							Name: aws.String(dnsName),
							ResourceRecords: []*route53.ResourceRecord{
								{Value: aws.String(externalHostName)},
							},
							TTL:  aws.Int64(300),
							Type: aws.String("CNAME"),
						},
					}},
				Comment: aws.String("CREATE/DELETE/UPSERT a record"),
			},
			HostedZoneId: aws.String(hostedZone),
		},
	)
	if err != nil {
		panic(err)
	}
	log.Println("DNS Change Info Status: " + *response.ChangeInfo.Status)
	log.Println("DNS ChangeInfo Id: " + *response.ChangeInfo.Id)
}

//
// We install fluxcd, which will monitor our Git repository and deploy workloads
// into the EKS cluster as changes are made to the git repo in a continuous deployment
// pattern called GitOps
//
func installFluxCD(gitRepo string) {
	version := "1.13.3"
	log.Println(execute("kubectl", "apply", "-f", "https://raw.githubusercontent.com/fluxcd/flux/"+version+"/deploy/flux-account.yaml"))
	deployment := `
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: flux
spec:
  replicas: 1
  selector:
    matchLabels:
      name: flux
  strategy:
    type: Recreate
  template:
    metadata:
      annotations:
        prometheus.io/port: "3031"
      labels:
        name: flux
    spec:
      serviceAccountName: flux
      volumes:
      - name: git-key
        secret:
          secretName: flux-git-deploy
          defaultMode: 0400
      - name: git-keygen
        emptyDir:
          medium: Memory
      containers:
      - name: flux
        image: docker.io/fluxcd/flux:` + version + `
        imagePullPolicy: IfNotPresent
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
        ports:
        - containerPort: 3030
        volumeMounts:
        - name: git-key
          mountPath: /etc/fluxd/ssh
          readOnly: true
        - name: git-keygen
          mountPath: /var/fluxd/keygen
        args:
        - --memcached-service=
        - --ssh-keygen-dir=/var/fluxd/keygen
        - --git-url=` + gitRepo + `
        - --git-branch=master
        - --listen-metrics=:3031
`
	executeWithStdin(deployment, "kubectl", "apply", "-f", "-")
	executeP("kubectl", "apply", "-f", "https://raw.githubusercontent.com/fluxcd/flux/"+version+"/deploy/flux-secret.yaml")
	executeP("kubectl", "apply", "-f", "https://raw.githubusercontent.com/fluxcd/flux/"+version+"/deploy/memcache-dep.yaml")
	executeP("kubectl", "apply", "-f", "https://raw.githubusercontent.com/fluxcd/flux/"+version+"/deploy/memcache-svc.yaml")
}

func installTiller() {
	sa := `
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tiller
  namespace: kube-system
`
	executeWithStdin(sa, "kubectl", "apply", "-f", "-")

	clusterRoleBinding := `
apiVersion: rbac.authorization.k8s.io/v1beta1
kind: ClusterRoleBinding
metadata:
  labels:
    name: tiller-admin
  name: tiller-admin
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
  - kind: ServiceAccount
    name: tiller
    namespace: kube-system
`
	executeWithStdin(
		clusterRoleBinding,
		"kubectl",
		"apply",
		"-f",
		"-",
	)
	executeP("helm", "init", "--service-account=tiller")
	executeP("helm", "repo", "update")
}

func installLetsEncryptIssuer(email string) {
	issuer := `
apiVersion: certmanager.k8s.io/v1alpha1
kind: Issuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: ` + email + `
    privateKeySecretRef:
      name: letsencrypt-prod
    http01: {}
`
	executeWithStdin(issuer, "kubectl", "apply", "-f", "-")
}

func installCertManager() {
	executeP(
		"kubectl",
		"apply",
		"-f",
		"https://raw.githubusercontent.com/jetstack/cert-manager/release-0.8/deploy/manifests/00-crds.yaml",
	)
	executeP(
		"helm",
		"repo",
		"add",
		"jetstack",
		"https://charts.jetstack.io",
	)
	executeP("helm", "repo", "update")
	executeP(
		"helm",
		"install",
		"--name",
		"cert-manager",
		"--namespace",
		"cert-manager",
		"--version",
		"v0.8.1",
		"jetstack/cert-manager",
	)
}

func executeP(cmd string, args ...string) {
	log.Println(execute(cmd, args...))
}

func execute(cmd string, args ...string) string {
	log.Println("Executing Command: " + cmd + " " + strings.Join(args, " "))
	command := exec.Command(cmd, args...)
	bytes, e := command.CombinedOutput()
	if e != nil {
		log.Fatal(string(bytes))
	}
	return string(bytes)
}

func executeWithStdin(input, cmd string, args ...string) string {
	log.Println("Executing Command (stdin): ")
	log.Println(cmd + " " + strings.Join(args, " "))
	log.Println(input)
	command := exec.Command(cmd, args...)
	stdinPipe, e := command.StdinPipe()
	dieOnError(e)
	_, e = io.WriteString(stdinPipe, input)
	stdinPipe.Close()
	dieOnError(e)
	bytes, e := command.CombinedOutput()
	if e != nil {
		log.Fatal(string(bytes))
	}
	return string(bytes)
}

func dieOnError(e error) {
	if e != nil {
		log.Fatal(e.Error())
	}
}

func commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}
