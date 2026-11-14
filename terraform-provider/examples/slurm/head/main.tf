# Slurm head node (controller), for setups without an explicit network attachment:
# the instance uses the tenant's default Subnet and SecurityGroup.
#
# This is the first half of a two-step flow for clusters that can't be expressed
# with a single configuration:
#
#   1. Apply this template. It creates the head node, waits until it is running,
#      and prints its IP address.
#   2. Apply ../worker with that IP, once per worker node.
#
# Copy terraform.tfvars.example to terraform.tfvars and fill in the key files and
# the names of the cluster resources.

terraform {
  required_version = ">= 1.6"
  required_providers {
    osac = {
      source = "registry.opentofu.org/osac-project/osac"
    }
  }
}

variable "tenant" {
  description = "Tenant where the head node is created."
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

variable "ssh_public_key_file" {
  description = "SSH public key injected into the node's default user."
  type        = string
}

variable "ssh_work_public_key_file" {
  description = "Public key of the work key pair, used by the workers to SSH to this node."
  type        = string
}

resource "osac_bare_metal_instance" "head" {
  metadata = {
    name   = "slurm-head-1"
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
      inline = templatefile("${path.module}/../slurm-head.yaml.tftpl", {
        work_public_key = trimspace(file(var.ssh_work_public_key_file))
      })
    }

    run_strategy = "BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS"
  }
}

output "head_id" {
  description = "Identifier of the head node."
  value       = osac_bare_metal_instance.head.id
}

output "head_ip" {
  description = "IP address of the head node. Pass it to the worker template."
  value = one(compact([
    for status in try(osac_bare_metal_instance.head.status.network_attachment_statuses, []) :
    status.ip_address
  ]))
}
