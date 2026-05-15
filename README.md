# Sample Platform App

A small training service used to demonstrate a platform engineering golden path.

Core delivery path in this repository:

```text
app code -> Docker image -> ECR -> Helm -> EKS -> kubectl inspection
```

The same path is then automated with GitHub Actions and Argo CD, and expanded to multi-app rollout, observability, and a minimal portal trigger flow.

## Project Layout

```text
app/                              # Go HTTP service and Dockerfile
platform/service.json              # Small service contract for training discussion
infrastructure/terraform/training/ # VPC, EKS, ECR, and outputs
helm/sample-platform-app/          # Helm chart for the app
deploy/sample-platform-app/         # GitOps desired state for Argo CD
deploy/apps/                        # per-app GitOps values for multi-app rollout
.github/workflows/                  # GitHub Actions workflows
observability/                      # Prometheus, Loki, and OTel collector values/manifests
portal/                             # Minimal portal that triggers GitHub workflow dispatch
Makefile                           # Training command menu
```

## Command Runner

This repository uses a `Makefile` as the primary command runner so common workflows stay consistent across machines.

Before running any setup/deploy steps, view the available targets:

```bash
make help
```

Use `make <target>` for common operations (infra planning, kubeconfig setup, image build/push, and app deploy/status) instead of retyping long commands.

## Customize For Your Environment

If a trainee uses this same codebase but runs with their own AWS/GitHub setup, these values must be customized.

### 1) Local shell environment (before running make/terraform/kubectl)

Set these based on the trainee machine/account:

```bash
export AWS_PROFILE=<trainee-aws-profile>
export AWS_REGION=us-east-1
export AWS_DEFAULT_REGION=us-east-1
export TF_VAR_cluster_name=<trainee-cluster-name>
export TF_VAR_repository_name=<trainee-ecr-repo-name>
export TF_VAR_vpc_cidr=<trainee-vpc-cidr>
export TF_VAR_github_repository="<trainee-github-user-or-org>/<repo-name>"
export TF_VAR_github_branch=main
```

### 2) GitHub repository variables (Actions)

In GitHub repo settings, set:

- `AWS_REGION`
- `AWS_ROLE_ARN`
- `ECR_REPOSITORY`

These must point to the trainee AWS account and ECR repo.

### 3) GitOps image references in repo files

Ensure image repo/tag references align with trainee ECR:

- `deploy/sample-platform-app/kustomization.yaml`
- `deploy/apps/shared-values.yaml`

If these point to another account/repo, Argo may sync but pods fail with image pull errors.

### 4) Optional: trainee can keep this repo structure

A trainee can reuse this exact repo layout and workflows, as long as:

- GitHub Actions variables use trainee AWS role/repo values.
- ECR image paths in GitOps values/manifests use trainee account repo.
- Cluster kubeconfig points to trainee cluster.

## Training Guide

Use this as a single operational reference for platform setup, delivery, GitOps, observability, and portal flow.

### Platform Foundation (Terraform + EKS + ECR)

**Focus**

```text
terraform plan/apply -> create platform primitives -> verify cluster access
```

**Code Tour**

- `infrastructure/terraform/training/`
  - VPC, EKS, ECR, IAM, and outputs for the lab setup.
- `Makefile`
  - Shortcut targets to keep execution consistent during training.

**Practice**

```bash
make help
make infra-init
make infra-plan
make infra-apply
make infra-output
make kubeconfig
kubectl get nodes
```

### Manual App Delivery Path

**Focus**

```text
app code -> container image -> ECR -> Helm deploy -> Kubernetes verification
```

**Code Tour**

- `app/`
  - Go service, endpoints, Dockerfile.
- `helm/sample-platform-app/`
  - Chart templates and values used for release.

**Practice**

```bash
make image-build IMAGE_TAG=v1
make ecr-login
make image-push IMAGE_TAG=v1
make app-deploy IMAGE_TAG=v1
make app-status
kubectl -n platform-demo get deploy,svc,pods
```

### CI/CD + Single-App GitOps

**Focus**

```text
github actions automation -> gitops desired state -> argo cd reconciliation
```

**Code Tour**

- `.github/workflows/day3-ci-deploy.yml`
  - CI build + push + deploy path.
- `.github/workflows/day3-gitops.yml`
  - Single-app GitOps workflow path.
- `deploy/sample-platform-app/`
  - Desired state for single-app deployment.
- `gitops/argocd/sample-platform-app-application.yaml`
  - Argo Application for single-app auto-sync.

**Practice**

```bash
kubectl apply -f gitops/argocd/sample-platform-app-application.yaml
kubectl -n argocd get applications
kubectl -n platform-demo rollout status deploy/sample-platform-app --timeout=180s
kubectl -n argocd port-forward svc/argocd-server 8081:443
kubectl -n platform-demo port-forward svc/sample-platform-app 8080:80
```

Open:

- `https://localhost:8081`
- `http://localhost:8080/version`

### Multi-App GitOps + Observability + Portal

**Focus**

```text
single-app gitops mastery -> appset scale-out -> selective rollout -> full rollout -> observability -> portal trigger
```

**Code Tour**

- `deploy/apps/shared-values.yaml`
  - Shared platform defaults used by all generated apps.
- `deploy/apps/app-a.values.yaml` ... `deploy/apps/app-e.values.yaml`
  - Per-app override layer.
- `gitops/argocd/platform-apps-appset.yaml`
  - ApplicationSet that generates five Argo Applications.
- `.github/workflows/day4-gitops-multi-app.yml`
  - Workflow for `single-app` vs `all-apps` rollout behavior.
- `observability/kube-prometheus-values.yaml`
  - Prometheus + Grafana baseline.
- `observability/loki-values.yaml`
  - Loki single-binary setup tuned for this lab cluster.
- `observability/promtail-values.yaml`
  - Promtail shipping logs to Loki.
- `observability/otel-collector-values.yaml`
  - OTel collector config for telemetry flow discussion.
- `observability/sample-platform-app-servicemonitor.yaml`
  - Prometheus scrape config for app metrics.
- `portal/main.go`
  - UI trigger for workflow dispatch and status polling.

**Practice**

```bash
kubectl apply -f gitops/argocd/platform-apps-appset.yaml
kubectl -n argocd get applications
kubectl -n platform-demo get deploy,svc,pods
kubectl -n platform-demo port-forward svc/app-a 8083:80
kubectl -n platform-demo port-forward svc/app-b 8084:80
```

Open:

- `http://localhost:8083/version`
- `http://localhost:8084/version`

Selective rollout demonstration:

1. Add `env.RELEASE_RING: "stage"` in `deploy/apps/app-a.values.yaml`.
2. Commit and push.
3. Verify only `app-a` changes.
4. Move shared `RELEASE_RING` in `deploy/apps/shared-values.yaml`.
5. Remove app-a override and push.
6. Verify all apps inherit shared value.

## Command-Only Rehearsal Block

```bash
pkill -f "kubectl -n argocd port-forward svc/argocd-server 8081:443" || true
pkill -f "kubectl -n platform-demo port-forward svc/sample-platform-app 8080:80" || true
pkill -f "kubectl -n platform-demo port-forward svc/app-a 8083:80" || true
pkill -f "kubectl -n platform-demo port-forward svc/app-b 8084:80" || true
pkill -f "kubectl -n monitoring port-forward svc/kube-prometheus-stack-grafana 3000:80" || true
pkill -f "kubectl -n monitoring port-forward svc/kube-prometheus-stack-prometheus 9090:9090" || true

kubectl apply -f gitops/argocd/sample-platform-app-application.yaml
kubectl -n platform-demo rollout status deploy/sample-platform-app --timeout=180s
kubectl apply -f gitops/argocd/platform-apps-appset.yaml

helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update
helm upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
  --namespace monitoring --create-namespace \
  --values observability/kube-prometheus-values.yaml
helm upgrade --install loki grafana/loki \
  --namespace monitoring --create-namespace \
  --values observability/loki-values.yaml
kubectl apply -f observability/sample-platform-app-servicemonitor.yaml
```

## Portal Run

```bash
export GITHUB_TOKEN="<fine-grained-token-with-actions-write>"
export GITHUB_OWNER="dsahu1001-git"
export GITHUB_REPO="sample-platform-app"
export GITHUB_WORKFLOW_FILE="day4-gitops-multi-app.yml"
export GITHUB_WORKFLOW_REF="main"
export PORTAL_APP_NAME="app-a"
export KUBE_NAMESPACE="platform-demo"
export PORT=9091
make portal-run
```

This repo is intentionally small. It is not a production platform baseline.
