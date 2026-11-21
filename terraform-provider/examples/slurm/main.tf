# Slurm cluster on OSAC: one head node (slurmctld) and one worker node (slurmd).
#
# Both nodes use the same catalog item and are differentiated only by their cloud-init
# user data. The worker is created after the head is running, and its cloud-init is
# rendered with the head node's IP address, taken from the head's status.
#
# Nobody can SSH into the nodes from outside, so the cluster manages itself over
# worker-to-head SSH using a dedicated work key pair:
#
#   - the head node authorizes the work public key for root
#   - each worker carries the work private key and two systemd units:
#     - slurm-register: registers itself in the head node's slurm.conf. The
#       check-and-append runs on the head under flock, so concurrent registrations
#       from multiple workers can't race or corrupt the file.
#     - slurm-conf-sync: downloads slurm.conf from the head and restarts slurmd
#       when it changes, keeping the configuration identical on all nodes.
#
# Prerequisites:
#
#   osac create virtualnetwork --name slurm-net --ipv4-cidr 10.100.0.0/16
#   osac create subnet --virtual-network slurm-net --name slurm-subnet --ipv4-cidr 10.100.1.0/24
#   # Slurm daemon ports AND SSH (22) from the subnet, for the worker-to-head units:
#   osac create securitygroup --virtual-network slurm-net --name slurm-sg \
#     --ingress protocol=tcp,port-from=6817,port-to=6819,ipv4-cidr=10.100.1.0/24 \
#     --ingress protocol=tcp,port-from=22,port-to=22,ipv4-cidr=10.100.1.0/24
#   ssh-keygen -t ed25519 -f ~/.ssh/slurm-cluster -N ""
#   ssh-keygen -t ed25519 -f ~/.ssh/slurm-work -N ""
#   osac --tenant shared create -f ../../fulfillment-service/examples/catalog-items/00-shared-fedora-disk-image.yaml
#   osac create -f ../../fulfillment-service/examples/catalog-items/slurm-node.yaml
#
# Copy terraform.tfvars.example to terraform.tfvars and fill in the key files and
# the names of the resources created above.
#
# Note: the work private key is injected through the worker's user data, so it is
# recorded in the OpenTofu state like any other attribute.

terraform {
  required_version = ">= 1.6"
  required_providers {
    osac = {
      source = "registry.opentofu.org/osac-project/osac"
    }
  }
}

variable "tenant" {
  description = "Tenant where the cluster is created."
  type        = string
  default     = "tenant1"
}

variable "catalog_item" {
  description = "Catalog item used by all the nodes."
  type        = string
  default     = "slurm-node"
}

variable "instance_type" {
  description = "Bare metal instance type used by all the nodes."
  type        = string
  default     = "e2e-bmi-type-4eceda2c"
}

variable "disk_image" {
  description = "Disk image, by name. Empty uses the catalog item default."
  type        = string
  default     = ""
}

variable "subnet_name" {
  description = "Subnet the nodes are attached to."
  type        = string
  default     = "slurm-subnet"
}

variable "security_group_name" {
  description = "Security group applied to the nodes. Must allow TCP 6817-6819 and TCP 22 from the subnet."
  type        = string
  default     = "slurm-sg"
}

variable "ssh_public_key_file" {
  description = "SSH public key injected into the nodes' default user."
  type        = string
}

variable "ssh_work_public_key_file" {
  description = "Public key of the work key pair, used by the workers to SSH to the head node."
  type        = string
}

variable "ssh_work_private_key_file" {
  description = "Private key of the work key pair, carried by the workers to SSH to the head node."
  type        = string
}

variable "ssh_user" {
  description = "Default user of the nodes' OS image, used by the workers to SSH to the head node."
  type        = string
  default     = "fedora"
}

data "osac_bare_metal_instance_catalog_item" "slurm_node" {
  name = var.catalog_item
}

locals {
  ssh_public_key  = file(var.ssh_public_key_file)
  work_public_key = file(var.ssh_work_public_key_file)
  work_private_key = trimspace(file(var.ssh_work_private_key_file))

  # The head node's IP address, from the DHCP lease reported in its status. Empty
  # until the head is provisioned; the worker has a precondition guarding against
  # that, so the apply fails with a clear message instead of a broken slurm.conf.
  head_ip = one(compact([
    for status in try(osac_bare_metal_instance.head.status.network_attachment_statuses, []) :
    status.ip_address
  ]))
}

resource "osac_bare_metal_instance" "head" {
  metadata = {
    name   = "slurm-head-1"
    tenant = var.tenant
  }
  spec = {
    catalog_item   = { name = data.osac_bare_metal_instance_catalog_item.slurm_node.metadata.name }
    instance_type  = { name = var.instance_type }
    disk_image     = var.disk_image == "" ? null : { name = var.disk_image }
    ssh_public_key = local.ssh_public_key

    user_data_source = {
      inline = templatefile("${path.module}/slurm-head.yaml.tftpl", {
        work_public_key = local.work_public_key
      })
    }

    network_attachments = [
      {
        subnet          = { name = var.subnet_name }
        security_groups = [{ name = var.security_group_name }]
      },
    ]

    run_strategy = "BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS"
  }
}

resource "osac_bare_metal_instance" "worker" {
  metadata = {
    name   = "slurm-worker-1"
    tenant = var.tenant
  }
  spec = {
    catalog_item   = { name = data.osac_bare_metal_instance_catalog_item.slurm_node.metadata.name }
    instance_type  = { name = var.instance_type }
    disk_image     = var.disk_image == "" ? null : { name = var.disk_image }
    ssh_public_key = local.ssh_public_key

    # Rendered with the head node's IP address and the work private key; the reference
    # to the head resource makes OpenTofu create and wait for the head first.
    user_data_source = {
      inline = templatefile("${path.module}/slurm-worker.yaml.tftpl", {
        head_ip          = local.head_ip
        ssh_user         = var.ssh_user
        work_private_key = local.work_private_key
      })
    }

    network_attachments = [
      {
        subnet          = { name = var.subnet_name }
        security_groups = [{ name = var.security_group_name }]
      },
    ]

    run_strategy = "BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS"
  }

  lifecycle {
    precondition {
      condition     = local.head_ip != ""
      error_message = "The head node has no IP address yet. The apply can't render the worker's slurm.conf."
    }
  }
}

output "head_ip" {
  description = "IP address of the head node."
  value       = local.head_ip
}

output "head_id" {
  value = osac_bare_metal_instance.head.id
}

output "worker_id" {
  value = osac_bare_metal_instance.worker.id
}
