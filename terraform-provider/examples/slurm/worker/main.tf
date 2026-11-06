# Slurm worker node (compute), for setups without an explicit network attachment:
# the instance uses the tenant's default Subnet and SecurityGroup.
#
# This is the second half of the two-step flow: apply ../head first and pass the
# IP address it prints to the head_ip variable:
#
#   cd ../head && tofu apply           # prints head_ip
#   cd ../worker && tofu apply -var head_ip=<head_ip>
#
# Copy terraform.tfvars.example to terraform.tfvars and fill in the key files, the
# head IP, and the names of the cluster resources. One state per worker node: use
# different working directories (or workspaces) for additional workers.

terraform {
  required_version = ">= 1.6"
  required_providers {
    osac = {
      source = "registry.opentofu.org/osac-project/osac"
    }
  }
}

variable "tenant" {
  description = "Tenant where the worker node is created."
  type        = string
  default     = "tenant1"
}

variable "catalog_item" {
  description = "Catalog item used by the node."
  type        = string
  default     = "slurm-node"
}

variable "instance_type" {
  description = "Bare metal instance type used by the node."
  type        = string
  default     = "e2e-bmi-type-4eceda2c"
}

variable "disk_image" {
  description = "Disk image, by name. Empty uses the catalog item default."
  type        = string
  default     = ""
}

variable "head_ip" {
  description = "IP address of the head node, from the output of the head template."
  type        = string

  validation {
    condition     = can(regex("^(\\d+\\.){3}\\d+$", var.head_ip))
    error_message = "The head_ip must be the head node's IPv4 address, from the head template's output."
  }
}

variable "ssh_user" {
  description = "Default user of the nodes' OS image, used to SSH to the head node."
  type        = string
  default     = "fedora"
}

variable "ssh_public_key_file" {
  description = "SSH public key injected into the node's default user."
  type        = string
}

variable "ssh_work_private_key_file" {
  description = "Private key of the work key pair, used to SSH to the head node."
  type        = string
}

resource "osac_bare_metal_instance" "worker" {
  metadata = {
    name   = "slurm-worker-1"
    tenant = var.tenant
  }
  spec = {
    catalog_item   = { name = var.catalog_item }
    instance_type  = { name = var.instance_type }
    disk_image     = var.disk_image == "" ? null : { name = var.disk_image }
    ssh_public_key = file(var.ssh_public_key_file)

    # No network_attachments: the instance uses the tenant default Subnet and
    # SecurityGroup.
    user_data_source = {
      inline = templatefile("${path.module}/../slurm-worker.yaml.tftpl", {
        head_ip          = var.head_ip
        ssh_user         = var.ssh_user
        work_private_key = trimspace(file(var.ssh_work_private_key_file))
      })
    }

    run_strategy = "BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS"
  }
}

output "worker_id" {
  description = "Identifier of the worker node."
  value       = osac_bare_metal_instance.worker.id
}

output "worker_ip" {
  description = "IP address of the worker node."
  value = one(compact([
    for status in try(osac_bare_metal_instance.worker.status.network_attachment_statuses, []) :
    status.ip_address
  ]))
}
