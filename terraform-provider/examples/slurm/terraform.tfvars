# Copy to terraform.tfvars and adjust. The key file variables are mandatory.

# Tenant where the cluster is created:
tenant = "tenant1"

# Catalog item used by all the nodes (created with
# `osac create -f ../../fulfillment-service/examples/catalog-items/slurm-node.yaml`):
catalog_item = "e2e-bmaas-04183e5f"

# Bare metal instance type used by all the nodes:
instance_type = "e2e-bmi-type-4eceda2c"

# Disk image, by name. Empty uses the catalog item default:
# disk_image = ""

# Subnet and security group the nodes are attached to. The security group must
# allow TCP 6817-6819 and TCP 22 from the subnet:
subnet_name         = "slurm-subnet"
security_group_name = "slurm-sg"

# SSH public key injected into the nodes' default user:
ssh_public_key_file = "~/.ssh/id_garbage.pub"

# Work key pair used by the workers to SSH to the head node:
ssh_work_public_key_file  = "~/.ssh/id_garbage.pub"
ssh_work_private_key_file = "~/.ssh/id_garbage"

# Default user of the nodes' OS image:
# ssh_user = "fedora"
