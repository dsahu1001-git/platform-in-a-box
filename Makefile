AWS_PROFILE ?= pe-training
AWS_REGION ?= us-east-1
APP_NAME ?= sample-platform-app
NAMESPACE ?= platform-demo
RELEASE ?= sample-platform-app
IMAGE_TAG ?= v1
IMAGE_PLATFORM ?= linux/amd64
TF_DIR := infrastructure/terraform/training
CHART_DIR := helm/sample-platform-app

ACCOUNT_ID = $(shell AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) aws sts get-caller-identity --query Account --output text 2>/dev/null)
ECR_REGISTRY = $(ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com
ECR_REPOSITORY_URL = $(shell cd $(TF_DIR) && AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) terraform output -raw ecr_repository_url 2>/dev/null)
CLUSTER_NAME = $(shell cd $(TF_DIR) && AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) terraform output -raw cluster_name 2>/dev/null)
VPC_ID = $(shell cd $(TF_DIR) && AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) terraform output -raw vpc_id 2>/dev/null)
ALB_CONTROLLER_ROLE_ARN = $(shell cd $(TF_DIR) && AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) terraform output -raw aws_load_balancer_controller_role_arn 2>/dev/null)

.PHONY: help setup test run platform-plan app-test infra-init infra-plan infra-apply infra-output kubeconfig ecr-login image-build image-push alb-controller-install alb-controller-status app-deploy app-deploy-ingress app-url app-disable-ingress app-status app-logs app-scale app-restart app-port-forward app-uninstall appset-apply appset-status portal-run cleanup-all

help:
	@echo "Sample Platform App targets"
	@echo ""
	@echo "  make platform-plan        Show the service contract"
	@echo "  make setup               Install/sync local runtime context"
	@echo "  make test                Run safe local tests"
	@echo "  make run                 Run portal locally"
	@echo "  make app-test             Run Go tests"
	@echo "  make infra-init           Terraform init"
	@echo "  make infra-plan           Terraform plan"
	@echo "  make infra-apply          Terraform apply tfplan manually"
	@echo "  make infra-output         Show Terraform outputs"
	@echo "  make kubeconfig           Configure kubectl for EKS"
	@echo "  make ecr-login            Log Docker in to ECR"
	@echo "  make image-build          Build local Docker image for $(IMAGE_PLATFORM)"
	@echo "  make image-push           Tag and push image to ECR"
	@echo "  make alb-controller-install Install AWS Load Balancer Controller"
	@echo "  make alb-controller-status  Check AWS Load Balancer Controller"
	@echo "  make app-deploy           Helm deploy the app"
	@echo "  make app-deploy-ingress   Helm deploy with public ALB Ingress"
	@echo "  make app-url              Print public app URL from Ingress"
	@echo "  make app-disable-ingress  Delete app Ingress and ALB"
	@echo "  make app-status           Show Kubernetes runtime state"
	@echo "  make app-logs             Show recent app logs"
	@echo "  make app-scale REPLICAS=3 Scale the deployment"
	@echo "  make app-port-forward     Forward http://localhost:8080"
	@echo "  make appset-apply         Apply the Day 4 Argo CD ApplicationSet"
	@echo "  make appset-status        Show Argo CD applications"
	@echo "  make portal-run           Start the Day 4 portal on http://localhost:9090"
	@echo "  make cleanup-all          Delete training resources (requires CONFIRM=YES)"

platform-plan:
	@python3 -m json.tool platform/service.json

setup:
	@echo "Run your auth/profile setup first (example):"
	@echo "  aws sso login --profile $(AWS_PROFILE)"
	@echo "Then sync kube context:"
	@$(MAKE) kubeconfig

test:
	@$(MAKE) app-test
	cd portal && go test ./...

run:
	cd portal && go run .

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
	docker build --platform $(IMAGE_PLATFORM) -t $(APP_NAME):$(IMAGE_TAG) ./app

image-push:
	@test -n "$(ECR_REPOSITORY_URL)" || (echo "ecr_repository_url output not found. Run Terraform apply first." && exit 1)
	docker tag $(APP_NAME):$(IMAGE_TAG) $(ECR_REPOSITORY_URL):$(IMAGE_TAG)
	docker push $(ECR_REPOSITORY_URL):$(IMAGE_TAG)

alb-controller-install:
	@test -n "$(CLUSTER_NAME)" || (echo "cluster_name output not found. Run Terraform apply first." && exit 1)
	@test -n "$(VPC_ID)" || (echo "vpc_id output not found. Run Terraform apply first." && exit 1)
	@test -n "$(ALB_CONTROLLER_ROLE_ARN)" || (echo "aws_load_balancer_controller_role_arn output not found. Run Terraform apply again after pulling the latest Terraform." && exit 1)
	helm repo add eks https://aws.github.io/eks-charts || true
	helm repo update eks
	helm upgrade --install aws-load-balancer-controller eks/aws-load-balancer-controller \
		--namespace kube-system \
		--set clusterName=$(CLUSTER_NAME) \
		--set region=$(AWS_REGION) \
		--set vpcId=$(VPC_ID) \
		--set serviceAccount.create=true \
		--set serviceAccount.name=aws-load-balancer-controller \
		--set-string 'serviceAccount.annotations.eks\.amazonaws\.com/role-arn=$(ALB_CONTROLLER_ROLE_ARN)' \
		--version 1.14.0

alb-controller-status:
	kubectl -n kube-system rollout status deploy/aws-load-balancer-controller
	kubectl -n kube-system get deploy aws-load-balancer-controller

app-deploy:
	@test -n "$(ECR_REPOSITORY_URL)" || (echo "ecr_repository_url output not found. Run Terraform apply first." && exit 1)
	helm upgrade --install $(RELEASE) $(CHART_DIR) \
		--namespace $(NAMESPACE) \
		--create-namespace \
		--set image.repository=$(ECR_REPOSITORY_URL) \
		--set image.tag=$(IMAGE_TAG)

app-deploy-ingress:
	@test -n "$(ECR_REPOSITORY_URL)" || (echo "ecr_repository_url output not found. Run Terraform apply first." && exit 1)
	helm upgrade --install $(RELEASE) $(CHART_DIR) \
		--namespace $(NAMESPACE) \
		--create-namespace \
		--set image.repository=$(ECR_REPOSITORY_URL) \
		--set image.tag=$(IMAGE_TAG) \
		--set ingress.enabled=true

app-url:
	@kubectl -n $(NAMESPACE) get ingress $(RELEASE) -o jsonpath='http://{.status.loadBalancer.ingress[0].hostname}{"\n"}'

app-disable-ingress:
	kubectl -n $(NAMESPACE) delete ingress $(RELEASE) --ignore-not-found

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

appset-apply:
	kubectl apply -f gitops/argocd/platform-apps-appset.yaml

appset-status:
	kubectl -n argocd get applications

portal-run:
	cd portal && go run .

app-uninstall:
	helm uninstall $(RELEASE) --namespace $(NAMESPACE)

cleanup-all:
	@test -x scripts/cleanup-all.sh || chmod +x scripts/cleanup-all.sh
	CONFIRM=$(CONFIRM) AWS_PROFILE=$(AWS_PROFILE) AWS_REGION=$(AWS_REGION) NAMESPACE=$(NAMESPACE) ./scripts/cleanup-all.sh
