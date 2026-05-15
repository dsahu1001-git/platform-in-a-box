# Security Notes

This repository is provided as public training material.

## Responsible Use

- Do not run workflows in this upstream repository.
- Fork this repository to your own account and run workflows only in your fork.
- Use your own AWS account, IAM role, ECR repository, and Kubernetes cluster.

## No Shared Credentials

- Never commit access keys, tokens, kubeconfig files, or Terraform state.
- Configure your own repository variables/secrets in your fork.

## Cost Warning

This project can create billable cloud resources (EKS, ECR, load balancers, storage, monitoring components).
Always run cleanup when finished.

## Cleanup

Use:

```bash
make cleanup-all CONFIRM=YES
```

or run `scripts/cleanup-all.sh` with options shown in `README.md`.
