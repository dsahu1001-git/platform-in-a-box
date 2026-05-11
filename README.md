# Sample Platform App

A small training service used to demonstrate a platform engineering golden path.

Day 2 uses this repository manually:

```text
app code -> Docker image -> ECR -> Helm -> EKS -> kubectl inspection
```

Day 3 automates the same path with GitHub Actions and Argo CD. Day 4 adds observability, policy, cost, and a minimal portal view.

## Project Layout

```text
app/                              # Go HTTP service and Dockerfile
platform/service.json              # Small service contract for training discussion
infrastructure/terraform/training/ # VPC, EKS, ECR, and outputs
helm/sample-platform-app/          # Helm chart for the app
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
