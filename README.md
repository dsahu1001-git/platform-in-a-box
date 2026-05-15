# Sample Platform App

A small training service used to demonstrate a platform engineering golden path.

Day 2 uses this repository manually:

```text
app code -> Docker image -> ECR -> Helm -> EKS -> kubectl inspection
```

Day 3 automates the same path with GitHub Actions and Argo CD. Day 4 adds multi-app Argo CD, observability, and a minimal portal trigger flow.

## Project Layout

```text
app/                              # Go HTTP service and Dockerfile
platform/service.json              # Small service contract for training discussion
infrastructure/terraform/training/ # VPC, EKS, ECR, and outputs
helm/sample-platform-app/          # Helm chart for the app
deploy/sample-platform-app/          # GitOps desired state for Argo CD
deploy/apps/                         # Day 4 per-app GitOps values for multi-app demo
.github/workflows/                  # Day 3 GitHub Actions workflows
observability/                      # Prometheus, Loki, and OTel collector values/manifests
portal/                             # Minimal Day 4 portal that triggers GitHub workflow dispatch
Makefile                           # Training command menu
```

## Start Here

Show the training command menu:

```bash
make help
```

Plan the platform infrastructure:

```bash
make infra-init
make infra-plan
```

After reviewing the plan, the instructor may apply manually:

```bash
make infra-apply
```

Build, push, and deploy the app after Terraform creates ECR and EKS:

```bash
make image-build IMAGE_TAG=v1
make ecr-login
make image-push IMAGE_TAG=v1
make kubeconfig
make app-deploy IMAGE_TAG=v1
make app-status
```

This repo is intentionally small. It is not a production platform baseline.

## Day 4 - Code Tour, Practice, and Commands

Day 4 focus:

```text
single-app GitOps -> multi-app GitOps -> observability -> portal trigger
```

### 1) Day 4 Project Code Tour

Use this section while walking a trainee through the repository.

#### 1.1 Single-app GitOps baseline

- `deploy/sample-platform-app/kustomization.yaml`
  - Kustomize entrypoint for the single-app path.
  - Includes Deployment and Service.
  - Rewrites image repository/tag for the final manifest.
- `deploy/sample-platform-app/deployment.yaml`
  - Pod template, probes, env, resources, replica intent.
- `deploy/sample-platform-app/service.yaml`
  - Stable cluster endpoint used by port-forward and in-cluster access.
- `gitops/argocd/sample-platform-app-application.yaml`
  - Argo CD `Application` for single-app reconciliation.
  - Watches this repo/branch/path and applies to `platform-demo`.

#### 1.2 Multi-app GitOps model

- `deploy/apps/shared-values.yaml`
  - Platform-level defaults for all apps (`image.repository`, shared env like `RELEASE_RING`).
- `deploy/apps/app-a.values.yaml` ... `deploy/apps/app-e.values.yaml`
  - Per-app overrides (`tag`, `REPORT_NAME`, optional app-local overrides).
- `gitops/argocd/platform-apps-appset.yaml`
  - Argo CD `ApplicationSet` that generates `app-a` to `app-e` Applications.
  - Each app uses:
    - `deploy/apps/shared-values.yaml` (base)
    - `deploy/apps/<app>.values.yaml` (overlay)

#### 1.3 Automation and runtime

- `.github/workflows/day4-gitops-multi-app.yml`
  - Builds/pushes image.
  - Updates GitOps values.
  - Commits back to `main` so Argo syncs from Git truth.
- `app/main.go`
  - Runtime payload includes `service`, `version`, `environment`, `reportName`, `releaseRing`.
  - Structured request logging for observability demos.

#### 1.4 Observability stack

- `observability/kube-prometheus-values.yaml`
  - Prometheus + Grafana stack values.
- `observability/sample-platform-app-servicemonitor.yaml`
  - Prometheus scrape rule for app metrics (`/metrics`).
- `observability/loki-values.yaml`
  - Loki single-binary setup used for Day 4.
  - Tuned for training cluster footprint and compatibility.
- `observability/promtail-values.yaml`
  - Log shipping to Loki.
- `observability/otel-collector-values.yaml`
  - OTel collector wiring for OTLP and export path discussion.

#### 1.5 Portal

- `portal/main.go`
  - Minimal internal platform UI that triggers Day 4 workflow dispatch.
  - Tracks workflow status and links to run URL.
  - Now surfaces GitHub dispatch error body/status for faster troubleshooting.

### 2) Day 4 Practice Session (Step-by-step)

#### 2.1 Connect and verify cluster

```bash
cd ~/work/garage/training/sample-platform-app
export AWS_PROFILE=pe-training
export AWS_REGION=us-east-1
export AWS_DEFAULT_REGION=us-east-1
make kubeconfig
kubectl get nodes
kubectl -n argocd get pods
```

#### 2.2 Single-app GitOps demo first

```bash
kubectl apply -f gitops/argocd/sample-platform-app-application.yaml
kubectl -n argocd get applications
kubectl -n platform-demo rollout status deploy/sample-platform-app --timeout=180s
```

Port-forward and verify:

```bash
kubectl -n argocd port-forward svc/argocd-server 8081:443
kubectl -n platform-demo port-forward svc/sample-platform-app 8080:80
```

Open:

- `https://localhost:8081`
- `http://localhost:8080/version`

#### 2.3 Drift + self-heal proof

```bash
kubectl -n platform-demo scale deploy/sample-platform-app --replicas=5
kubectl -n platform-demo get deploy sample-platform-app -w
```

Point out Argo reconciles back to Git-defined replicas.

#### 2.4 Multi-app rollout model

```bash
kubectl apply -f gitops/argocd/platform-apps-appset.yaml
kubectl -n argocd get applications
kubectl -n platform-demo get deploy,svc,pods
```

Port-forward two apps for side-by-side comparison:

```bash
kubectl -n platform-demo port-forward svc/app-a 8083:80
kubectl -n platform-demo port-forward svc/app-b 8084:80
```

Open:

- `http://localhost:8083/version`
- `http://localhost:8084/version`

#### 2.5 Selective adoption then shared promotion

1. Add an app-local override in `deploy/apps/app-a.values.yaml`:
   - `env.RELEASE_RING: "stage"`
2. Commit/push and observe only `app-a` changed.
3. Move shared default in `deploy/apps/shared-values.yaml` to `stage`.
4. Remove app-local override from `app-a`.
5. Commit/push and observe all apps inherit shared value.

### 3) Day 4 Command-Only Quick Runbook

Use this when rehearsing quickly.

#### 3.1 Optional local port-forward cleanup

```bash
pkill -f "kubectl -n argocd port-forward svc/argocd-server 8081:443" || true
pkill -f "kubectl -n platform-demo port-forward svc/sample-platform-app 8080:80" || true
pkill -f "kubectl -n platform-demo port-forward svc/app-a 8083:80" || true
pkill -f "kubectl -n platform-demo port-forward svc/app-b 8084:80" || true
pkill -f "kubectl -n monitoring port-forward svc/kube-prometheus-stack-grafana 3000:80" || true
pkill -f "kubectl -n monitoring port-forward svc/kube-prometheus-stack-prometheus 9090:9090" || true
```

#### 3.2 Observability install and checks

```bash
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

Port-forward:

```bash
kubectl -n monitoring port-forward svc/kube-prometheus-stack-prometheus 9090:9090
kubectl -n monitoring port-forward svc/kube-prometheus-stack-grafana 3000:80
```

Prometheus quick checks:

```bash
curl -s 'http://127.0.0.1:9090/api/v1/query?query=up'
curl -s 'http://127.0.0.1:9090/api/v1/query?query=sample_platform_app_requests_total'
```

Grafana log query (Loki datasource):

```text
{namespace="platform-demo"}
```

Narrow:

```text
{namespace="platform-demo", app_kubernetes_io_instance="app-a"} |= "GET"
```

#### 3.3 Portal run

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
