# Training Terraform

This Terraform stack creates the Day 2 platform substrate:

- VPC across two Availability Zones
- one NAT gateway to control cost while supporting private EKS nodes
- EKS cluster with a small managed node group
- ECR repository for the sample app image
- outputs consumed by Docker, Helm, GitHub Actions, and Argo CD later

Run `terraform plan` before every apply and do not leave the cluster running longer than needed after the training.
