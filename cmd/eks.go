package cmd

import (
	"github.com/spf13/cobra"
	"io"
	"log"
	"os/exec"
	"strings"
)

var gitRepo string
var email string

var EKSCmd = &cobra.Command{
	Use:   "eks",
	Short: "Work with an EKS cluster",
	Run: func(cmd *cobra.Command, args []string) {
		installTiller()
		installNginx()
		installCertManager()
		installLetsEncryptIssuer(email)
		installFluxd(gitRepo)
	},
}

func init() {
	EKSCmd.Flags().StringVar(&gitRepo, "git-repo", "", "The Git repository to monitor")
	EKSCmd.Flags().StringVar(&email, "email", "", "The email used to procure TLS certificates, this is passed to cert-manager")
	EKSCmd.MarkFlagRequired("git-repo")
	EKSCmd.MarkFlagRequired("email")
}

func installNginx() {
	log.Println(execute("helm", "install", "stable/nginx-ingress", "--name", "default"))
	//
	// TODO figure out command for finding nginx ELB name so we can attach route 53 record
	//
}

func installFluxd(gitRepo string) {
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
	log.Println(executeWithStdin(deployment, "kubectl", "apply", "-f", "-"))
	log.Println(execute("kubectl", "apply", "-f", "https://raw.githubusercontent.com/fluxcd/flux/"+version+"/deploy/flux-secret.yaml"))
	log.Println(execute("kubectl", "apply", "-f", "https://raw.githubusercontent.com/fluxcd/flux/"+version+"/deploy/memcache-dep.yaml"))
	log.Println(execute("kubectl", "apply", "-f", "https://raw.githubusercontent.com/fluxcd/flux/"+version+"/deploy/memcache-svc.yaml"))
}

func installTiller() {
	log.Println(execute("kubectl", "create", "serviceaccount", "tiller", "--namespace=kube-system"))
	log.Println(
		execute("kubectl",
			"create",
			"clusterrolebinding",
			"tiller-admin",
			"--serviceaccount=kube-system:tiller",
			"--clusterrole=cluster-admin",
		),
	)
	log.Println(execute("helm", "init", "--service-account=tiller"))
	log.Println(execute("helm", "repo", "update"))
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
		       email: `+ email +`
		       privateKeySecretRef:
		         name: letsencrypt-prod
		       http01: {}
		`
	executeWithStdin(issuer, "kubectl", "apply", "-f", "-")
}

func installCertManager() {
	log.Println(execute(
		"kubectl",
		"apply",
		"-f",
		"https://raw.githubusercontent.com/jetstack/cert-manager/release-0.8/deploy/manifests/00-crds.yaml",
	))
	log.Println(execute(
		"helm",
		"repo",
		"add",
		"jetstack",
		"https://charts.jetstack.io",
	))
	log.Println(execute("helm", "repo", "update"))
	log.Println(
		execute(
			"helm",
			"install",
			"--name",
			"cert-manager",
			"--namespace",
			"cert-manager",
			"--version",
			"v0.8.1",
			"jetstack/cert-manager",
		),
	)
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
