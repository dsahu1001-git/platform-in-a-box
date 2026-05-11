variable "aws_profile" {
  description = "AWS CLI profile used for the training account."
  type        = string
  default     = "pe-training"
}

variable "aws_region" {
  description = "AWS region for all Day 2 labs."
  type        = string
  default     = "us-east-1"
}

variable "cluster_name" {
  description = "Name of the EKS cluster."
  type        = string
  default     = "pe-training-cluster"
}

variable "cluster_version" {
  description = "EKS Kubernetes version for the lab cluster."
  type        = string
  default     = "1.34"
}

variable "vpc_cidr" {
  description = "CIDR block for the training VPC."
  type        = string
  default     = "10.40.0.0/16"
}

variable "desired_node_count" {
  description = "Desired size for the default managed node group."
  type        = number
  default     = 2
}

variable "node_instance_types" {
  description = "Small instance types used for the default managed node group."
  type        = list(string)
  default     = ["t3.medium"]
}

variable "repository_name" {
  description = "ECR repository name for the sample app image."
  type        = string
  default     = "sample-platform-app"
}

variable "tags" {
  description = "Additional tags for training resources."
  type        = map(string)
  default     = {}
}
