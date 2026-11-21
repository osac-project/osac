terraform {
  required_version = ">= 1.6"
  required_providers {
    osac = {
      source = "registry.opentofu.org/osac-project/osac"
    }
  }
}

# Run `osac login` first; the provider reuses that session. If it can access
# multiple tenants, either select one with `osac tenant use` or set
# `metadata.tenant` on the resource.

data "osac_bare_metal_instance_catalog_item" "standard" {
  name = "standard-bare-metal"
}

data "osac_disk_image" "rhel" {
  name = "rhel-9-4-amd64"
}

# A Secret of type USER_DATA, created for example with:
#   osac create secret --name nginx-userdata --type user-data \
#     --from-file userdata=nginx-userdata.yaml
data "osac_secret" "userdata" {
  name = "nginx-userdata"
}

resource "osac_bare_metal_instance" "worker" {
  metadata = {
    name   = "worker-0"
    tenant = "tenant1"
  }
  spec = {
    catalog_item     = { name = data.osac_bare_metal_instance_catalog_item.standard.metadata.name }
    disk_image       = { name = data.osac_disk_image.rhel.metadata.name }
    instance_type    = { name = "standard-bare-metal-type" }
    user_data_source = { secret = { name = data.osac_secret.userdata.name } }
    ssh_public_key   = file("~/.ssh/id_ed25519.pub")
    run_strategy     = "BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS"

    # Optional explicit network attachment; an empty or omitted list requests
    # the tenant default Subnet and SecurityGroup.
    network_attachments = [
      {
        subnet          = { name = "my-subnet" }
        security_groups = [{ name = "my-security-group" }]
      },
    ]
  }
}

output "instance_id" {
  value = osac_bare_metal_instance.worker.id
}
