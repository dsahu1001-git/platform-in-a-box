AWS_PROFILE ?= pe-training
AWS_REGION ?= us-east-1
APP_NAME ?= sample-platform-app
NAMESPACE ?= platform-demo
RELEASE ?= sample-platform-app
IMAGE_TAG ?= v1
TF_DIR := infrastructure/terraform/training
CHART_DIR := helm/sample-platform-app

ACCOUNT_ID = $(shell AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) aws sts get-caller-identity --query Account --output text 2>/dev/null)
ECR_REGISTRY = $(ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com
ECR_REPOSITORY_URL = $(shell cd $(TF_DIR) && AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) terraform output -raw ecr_repository_url 2>/dev/null)
CLUSTER_NAME = $(shell cd $(TF_DIR) && AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) terraform output -raw cluster_name 2>/dev/null)

.PHONY: help platform-plan app-test infra-init infra-plan infra-apply infra-output kubeconfig ecr-login image-build image-push app-deploy app-status app-logs app-scale app-restart app-port-forward app-uninstall

help:
	@echo "Sample Platform App targets"
	@echo ""
	@echo "  make platform-plan        Show the service contract"
	@echo "  make app-test             Run Go tests"
	@echo "  make infra-init           Terraform init"
	@echo "  make infra-plan           Terraform plan"
	@echo "  make infra-apply          Terraform apply tfplan manually"
	@echo "  make infra-output         Show Terraform outputs"
	@echo "  make kubeconfig           Configure kubectl for EKS"
	@echo "  make ecr-login            Log Docker in to ECR"
	@echo "  make image-build          Build local Docker image"
	@echo "  make image-push           Tag and push image to ECR"
	@echo "  make app-deploy           Helm deploy the app"
	@echo "  make app-status           Show Kubernetes runtime state"
	@echo "  make app-logs             Show recent app logs"
	@echo "  make app-scale REPLICAS=3 Scale the deployment"
	@echo "  make app-port-forward     Forward http://localhost:8080"

platform-plan:
	@python3 -m json.tool platform/service.json

app-test:
	cd app && go test ./...

infra-init:
	cd $(TF_DIR) && AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) terraform init

infra-plan:
	cd $(TF_DIR) && AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) terraform plan -out tfplan

infra-apply:
	cd $(TF_DIR) && AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) terraform apply tfplan

infra-output:
	cd $(TF_DIR) && AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) terraform output

kubeconfig:
	@test -n "$(CLUSTER_NAME)" || (echo "cluster_name output not found. Run Terraform apply first." && exit 1)
	AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) aws eks update-kubeconfig --name $(CLUSTER_NAME) --region $(AWS_REGION)

ecr-login:
	@test -n "$(ACCOUNT_ID)" || (echo "AWS account not found. Run aws sso login --profile $(AWS_PROFILE)." && exit 1)
	AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $(ECR_REGISTRY)

image-build:
	docker build -t $(APP_NAME):$(IMAGE_TAG) ./app

image-push:
	@test -n "$(ECR_REPOSITORY_URL)" || (echo "ecr_repository_url output not found. Run Terraform apply first." && exit 1)
	docker tag $(APP_NAME):$(IMAGE_TAG) $(ECR_REPOSITORY_URL):$(IMAGE_TAG)
	docker push $(ECR_REPOSITORY_URL):$(IMAGE_TAG)

app-deploy:
	@test -n "$(ECR_REPOSITORY_URL)" || (echo "ecr_repository_url output not found. Run Terraform apply first." && exit 1)
	helm upgrade --install $(RELEASE) $(CHART_DIR) \
		--namespace $(NAMESPACE) \
		--create-namespace \
		--set image.repository=$(ECR_REPOSITORY_URL) \
		--set image.tag=$(IMAGE_TAG)

app-status:
	kubectl -n $(NAMESPACE) get deploy,svc,pods
	kubectl -n $(NAMESPACE) rollout status deploy/$(RELEASE)

app-logs:
	kubectl -n $(NAMESPACE) logs deploy/$(RELEASE) --tail=80

app-scale:
	@test -n "$(REPLICAS)" || (echo "Usage: make app-scale REPLICAS=3" && exit 1)
	kubectl -n $(NAMESPACE) scale deploy/$(RELEASE) --replicas=$(REPLICAS)
	kubectl -n $(NAMESPACE) rollout status deploy/$(RELEASE)

app-restart:
	kubectl -n $(NAMESPACE) rollout restart deploy/$(RELEASE)
	kubectl -n $(NAMESPACE) rollout status deploy/$(RELEASE)

app-port-forward:
	kubectl -n $(NAMESPACE) port-forward svc/$(RELEASE) 8080:80

app-uninstall:
	helm uninstall $(RELEASE) --namespace $(NAMESPACE)
