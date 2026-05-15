#!/usr/bin/env bash
set -euo pipefail

# Full cleanup for training environment resources.
# Keeps the code repository intact.
#
# What it cleans:
# - Argo CD apps/appsets and repo secret created for training
# - App namespaces/workloads
# - Observability stack (Helm releases + monitoring namespace)
# - Optional AWS add-ons used during troubleshooting
# - Terraform-managed AWS infra (EKS, ECR, VPC, etc.) via terraform destroy

AWS_PROFILE="${AWS_PROFILE:-pe-training}"
AWS_REGION="${AWS_REGION:-us-east-1}"
TF_DIR="${TF_DIR:-infrastructure/terraform/training}"
NAMESPACE="${NAMESPACE:-platform-demo}"
CONFIRM="${CONFIRM:-}"
SKIP_TERRAFORM="${SKIP_TERRAFORM:-false}"
SKIP_AWS_ADDONS="${SKIP_AWS_ADDONS:-false}"

log() {
  echo "[cleanup] $*"
}

run() {
  log "$*"
  "$@"
}

best_effort() {
  "$@" || true
}

require_confirm() {
  if [[ "${CONFIRM}" != "YES" ]]; then
    cat <<EOF
This script will delete training resources and can remove billable infrastructure.

To continue, run:
  CONFIRM=YES ./scripts/cleanup-all.sh

Optional flags:
  SKIP_TERRAFORM=true    # keep AWS infra
  SKIP_AWS_ADDONS=true   # keep EKS add-ons
  AWS_PROFILE=<profile>
  AWS_REGION=<region>
  NAMESPACE=<app-namespace>
EOF
    exit 1
  fi
}

main() {
  require_confirm

  log "starting cleanup"
  log "AWS_PROFILE=${AWS_PROFILE} AWS_REGION=${AWS_REGION} NAMESPACE=${NAMESPACE}"
  log "SKIP_TERRAFORM=${SKIP_TERRAFORM} SKIP_AWS_ADDONS=${SKIP_AWS_ADDONS}"

  # 1) Argo cleanup
  log "phase 1: removing Argo CD appsets/apps/secrets"
  best_effort kubectl -n argocd delete applicationset platform-apps --ignore-not-found
  best_effort kubectl -n argocd delete application app-a app-b app-c app-d app-e sample-platform-app --ignore-not-found
  best_effort kubectl -n argocd delete secret sample-platform-app-repo --ignore-not-found

  # 2) App workload cleanup
  log "phase 2: removing app namespace resources"
  best_effort kubectl -n "${NAMESPACE}" delete deploy,svc,ingress --all
  best_effort kubectl -n "${NAMESPACE}" delete pods --all
  best_effort kubectl delete namespace "${NAMESPACE}" --ignore-not-found

  # 3) Observability cleanup
  log "phase 3: removing observability stack"
  best_effort helm -n monitoring uninstall kube-prometheus-stack
  best_effort helm -n monitoring uninstall loki
  best_effort helm -n monitoring uninstall promtail
  best_effort helm -n monitoring uninstall otel-collector
  best_effort kubectl -n monitoring delete servicemonitor sample-platform-app --ignore-not-found
  best_effort kubectl delete namespace monitoring --ignore-not-found

  # 4) Optional AWS add-on cleanup
  if [[ "${SKIP_AWS_ADDONS}" != "true" ]]; then
    log "phase 4: removing optional AWS add-ons (best effort)"
    cluster_name="$(cd "${TF_DIR}" && AWS_PROFILE="${AWS_PROFILE}" AWS_REGION="${AWS_REGION}" terraform output -raw cluster_name 2>/dev/null || true)"
    if [[ -n "${cluster_name}" ]]; then
      best_effort aws eks delete-addon --cluster-name "${cluster_name}" --addon-name aws-ebs-csi-driver --region "${AWS_REGION}"
    else
      log "cluster_name not found from terraform output; skipping addon cleanup"
    fi
  fi

  # 5) Terraform destroy
  if [[ "${SKIP_TERRAFORM}" != "true" ]]; then
    log "phase 5: destroying terraform-managed infra"
    run bash -lc "cd '${TF_DIR}' && AWS_PROFILE='${AWS_PROFILE}' AWS_REGION='${AWS_REGION}' terraform init -input=false"
    run bash -lc "cd '${TF_DIR}' && AWS_PROFILE='${AWS_PROFILE}' AWS_REGION='${AWS_REGION}' terraform destroy -auto-approve"
  else
    log "phase 5 skipped: SKIP_TERRAFORM=true"
  fi

  log "cleanup complete"
}

main "$@"
