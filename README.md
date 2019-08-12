Knit
====

This is a CLI program for bootstrapping Kubernetes clusters that has already been provisioned by
Rancher. We install the following components on the cluster to make it operation:

 - Tiller (for Helm Charts)
 - nginx-ingress-controller
 - [cert-manager](https://docs.cert-manager.io/en/latest/tutorials/acme/quick-start/index.html)
 - [sealed-secrets](https://github.com/bitnami-labs/sealed-secrets)
 - [fluxcd](https://docs.fluxcd.io/en/latest/tutorials/get-started.html) (GitOps)
 
 By installing `fluxcd` we also restore all the workloads that comprise the cluster stored in Git. Thus, this program
 can be used to both initially provision an environment / cluster as well as recover its workloads
 