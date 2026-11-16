# Model tenant VM runbook

This runbook creates a Fedora VM in the `model` tenant of the current
`osac-demo` lab deployment. It uses the local `osac` CLI and the
mirror-backed Fedora container disk.

## UI equivalents

Most steps can also be done in the OSAC UI at `$OSAC_UI`. The table is based
on the `osac-ui` source and its Playwright specs under
`osac-ui/apps/e2e-tests/src/vmaas/`; it was not verified against the lab
deployment.

| Step | CLI command | UI equivalent | Notes |
|---|---|---|---|
| Inspect resources | `osac get storagetiers`, `instancetypes`, `subnets`, `secrets` | Storage Tiers, Instance types, Network and Secrets pages | Admin pages are visible to admin users only. |
| Create instance type | `osac create instancetype` | Administration > Infrastructure > Instance types > Create | The UI also accepts optional GPU fields. |
| Create SSH key Secret | `osac create secret --type ssh-public-key` | Secrets > Create secret, type SSH public key | No e2e coverage. |
| 1. Log in, select tenant | `osac login`, `osac tenant` | Browser OIDC login | The tenant comes from the signed-in user. |
| 2. Create DiskImage | `osac create diskimage` | Infrastructure > Disk images > Create | The UI also sets scope. |
| 3. Create tenant user | `osac create -f - ... User` | None | CLI only (private API). |
| 4. Inspect the chart-seeded template | `osac get computeinstancetemplate ocp-virt-vm` | None | CLI only. The template is seeded by the chart's publish-templates hook; its id is the AAP role path, so do not create another one. |
| 5. Create catalog item | `osac create -f - ... ComputeInstanceCatalogItem` | Catalog > Create catalog item, then Publish | Each field has an Editable/Locked switch. The e2e spec does not set the storage tier or run strategy, or toggle the switches. |
| 6. Log in as `model-user` | `osac logout`, `osac login` | Sign out and sign in again in the browser | Requires the user from step 3. |
| 7. Create ComputeInstance | `osac create computeinstance` | Compute > VMs > Create, or Launch from the catalog item | The explicit subnet and security-group path has no e2e coverage. The run strategy is fixed to Always unless the catalog item locks it. |
| 8. Attach an ExternalIP | `osac create externalip`, `osac create externalipattachment`, `ssh` | None | The VM subnet is an overlay; an ExternalIP provides the reachable address. SSH is currently blocked by an OVN UDN north/south gap (see §8). |
| 9. Verify VM | `osac get computeinstances --watch` | VM list and details pages | Live refresh was not checked. |

## Prerequisites

- The `model` tenant exists and is synced.
- You have an account that can administer that tenant.
- The tenant has a usable storage tier, subnet, and SSH public-key Secret.
- Run the commands from the repository root:

```bash
cd /home/asathe/work/osac-docs-3921
```

Set the current endpoints and resource names:

```bash
export OSAC_API=https://fulfillment-api-osac.apps.osac-demo.pool.se-lab.eng.rdu2.dc.redhat.com
export OSAC_INTERNAL_API=https://fulfillment-internal-api-osac.apps.osac-demo.pool.se-lab.eng.rdu2.dc.redhat.com
export OSAC_UI=https://osac-ui-osac.apps.osac-demo.pool.se-lab.eng.rdu2.dc.redhat.com
export INSTANCE_TYPE=<instance-type-name>
export STORAGE_TIER=<storage-tier-name>
export SUBNET_ID=<subnet-id>
export SSH_KEY='name=<ssh-key-secret-name>'
```

Inspect the available resources before you continue:

```bash
./fulfillment-service/osac get storagetiers
./fulfillment-service/osac get instancetypes
./fulfillment-service/osac get subnets
./fulfillment-service/osac get secrets
```

If the SSH key Secret is in a named project, set `SSH_KEY` to
`name=<secret-name>,project=<project-name>`.

### Create missing prerequisites

If `osac get instancetypes` returns no objects, create one:

```bash
./fulfillment-service/osac create instancetype \
  --name standard-2-8 \
  --vcpus 2 \
  --memory-gib 8 \
  --description 'Standard 2 vCPU / 8 GiB'
```

If `osac get secrets` does not show an SSH public-key Secret, create one:

```bash
./fulfillment-service/osac create secret \
  --name model-user-ssh-key \
  --type ssh-public-key \
  --from-file=public_key="$HOME/.ssh/id_ed25519.pub"
```

If you do not already have an SSH key, generate one first:

```bash
ssh-keygen -t ed25519 -f "$HOME/.ssh/model-user-key" -N ''
```

Then use `$HOME/.ssh/model-user-key.pub` in the `--from-file` value above.

Create a security group that permits SSH from the address that the VM observes
as the connection source. With the site tunnel of this lab (`sshuttle -r
core@10.6.76.15 192.168.100.0/24`), forwarded connections reach the VM from the
tunnel host's address on the `192.168.100.0/24` network, so the example allows
that network; replace `192.168.100.0/24` with the source your site observes:

```bash
./fulfillment-service/osac create securitygroup \
  --name allow-ssh \
  --virtual-network default \
  --ingress protocol=tcp,port-from=22,port-to=22,ipv4-cidr=192.168.100.0/24 \
  --egress protocol=all,ipv4-cidr=0.0.0.0/0
```

NOTE: `--egress protocol=all` alone is rejected by API validation
(`object.spec.egress[0].ipv4_cidr: value is required`) even though the CLI
help shows it as an example — the CIDR is mandatory on every rule.

Wait until the security group is ready:

```bash
./fulfillment-service/osac get securitygroups
```

### Create an ExternalIPPool (provider administrator)

VM subnets are cluster-internal overlays: no host outside the cluster routes
to `10.200.0.0/20`, so the tenant needs an ExternalIP to reach the VM. A
provider administrator creates one pool per routable address range through the
private API. The pool lives in the `shared` tenant and its CIDR must be
reachable from the client network; this lab uses the node's `br-sno` network,
which the site tunnel already routes:

```bash
./fulfillment-service/osac create -f - <<'EOF'
'@type': type.googleapis.com/osac.private.v1.ExternalIPPool
metadata:
  name: br-sno-pool
  tenant: shared
spec:
  cidrs: [192.168.100.224/29]
  ip_family: IP_FAMILY_IPV4
EOF
```

Confirm the pool is ready (read it from the `shared` tenant):

```bash
./fulfillment-service/osac tenant shared
./fulfillment-service/osac get externalippool br-sno-pool
./fulfillment-service/osac tenant model
```

## 1. Log in and select the tenant

Use an account that can administer the `model` tenant. Private-API requests
must be sent to the internal API route, not `$OSAC_API`; the external route
does not serve the private service and returns `Unimplemented` for private
object types:

```bash
./fulfillment-service/osac login --insecure --private --address "$OSAC_INTERNAL_API"
./fulfillment-service/osac tenant model
```

The CLI opens a browser for authentication. If it does not, open the URL
printed by the command.

The `--private` flag enables the private admin API used by the user,
template, and catalog-item manifests below.

## 2. Create the mirror-backed DiskImage

Catalog objects (disk images, instance types, templates, catalog items) live
in the reserved `shared` tenant so every tenant can use them. Two CLI
behaviors matter here:

- `osac create diskimage` (flag form) sends no tenant, and the server places
  the image in `shared` — but with a name-derived id and no manifest control.
- `osac create -f -` **overwrites** `metadata.tenant` with the currently
  selected tenant, so shared-catalog objects require selecting `shared` first.

```bash
./fulfillment-service/osac tenant shared
./fulfillment-service/osac create -f - <<'EOF'
'@type': type.googleapis.com/osac.private.v1.DiskImage
metadata:
  name: fedora-latest
  tenant: shared
spec:
  source_type: SOURCE_TYPE_REGISTRY
  source_ref: registry-vm-svc.aap-portal-lab.svc.cluster.local:8443/containerdisks/fedora:latest
  guest_os_family: GUEST_OS_FAMILY_LINUX
  architecture:
    - ARCHITECTURE_AMD64
EOF
```

Confirm that the image is available, then switch back to the `model`
tenant (unlike instance types, shared disk images are NOT visible from
other tenants' listings):

```bash
./fulfillment-service/osac get diskimage fedora-latest
./fulfillment-service/osac tenant model
```

## 3. Create a regular tenant user

A loginable user needs an account in the Keycloak `osac` realm. The
fulfillment `User` object does not create one: its reconciler only links a
Keycloak user that already exists, and the `credentials` field of the
manifest never reaches Keycloak. Create the Keycloak user first (the same
way `osac-installer/scripts/dev-full/provision-tenant.sh` does), for
example with the Keycloak Admin API, and give it a permanent password.

Three Keycloak behaviors matter:

- The realm's "Verify Profile" default action blocks token grants for users
  without `firstName`/`lastName` — the grant fails with
  `Account is not fully set up`. Always set both when creating the user.
- Write the password file without a trailing newline
  (`printf '%s' "$(openssl rand -base64 18)" > /dev/shm/model-user-pw`):
  `osac login --password-file` strips a trailing newline, so a newline in
  the file silently mismatches the Keycloak password.
- The user must be a member of the Keycloak organization named after the
  tenant (org `model`) — `osac tenant model` fails for users outside the
  organization. `provision-tenant.sh` adds the same membership when it
  creates tenants; add it with the Admin API
  (`POST .../realms/osac/organizations/<org-id>/members` with the user id
  as the JSON body) if you create the user by hand.

Then register the user in the `model` tenant through the private API:

```bash
./fulfillment-service/osac create -f - <<'EOF'
'@type': type.googleapis.com/osac.private.v1.User
metadata:
  name: model-user
  tenant: model
spec:
  username: model-user
  email: model-user@example.invalid
  enabled: true
EOF
```

Confirm that the user is linked to its Keycloak account
(`status.keycloak_user_id` must be set):

```bash
./fulfillment-service/osac get user model-user -o yaml
```

If `status.keycloak_user_id` is empty, the Keycloak-side account does not
exist yet or has a different username; the reconciler links it
automatically once it does. If it instead points at a *deleted* Keycloak
user (for example after the account was recreated), the reconciler never
relinks on its own — it only performs the lookup while the field is
empty. Clear `status.keycloak_user_id` (for example with
`osac edit user model-user`) and the reconciler re-links the current
account within seconds.

Do not delete the `User` to force a relink: deleting it also deletes its
Keycloak account, and when that account is already gone the deletion
hangs in `DELETING` forever (the controller finalizer is only removed
after a successful Keycloak delete).

## 4. Inspect the chart-seeded ComputeInstanceTemplate

> **NOTE:** a template's `id` is not cosmetic — it is the Ansible role
> path the AAP provisioning playbook includes
> (`include_role: name={{ spec.templateID }}`, with `spec.templateID`
> filled from the template's id). A template created without an explicit
> id gets a generated UUID and can never provision: the VM hangs in
> `STARTING` with a failed provisioning job (`the role '<uuid>' was not
> found in /runner/project/osac-aap`). The installer's
> publish-templates hook therefore seeds the platform templates, and
> their ids are primary keys — a second template with the same id
> cannot be created. Reuse the seeded template instead of creating one.

The OpenShift Virtualization VM template (`osac.templates.ocp_virt_vm`)
is seeded by the chart. Inspect it with the `shared` tenant selected,
then switch back to `model`:

```bash
./fulfillment-service/osac tenant shared
./fulfillment-service/osac get computeinstancetemplate ocp-virt-vm
./fulfillment-service/osac tenant model
```

## 5. Create the ComputeInstanceCatalogItem

The catalog item locks the VM to the mirror-backed Fedora image and leaves the
instance type, boot disk, run strategy, subnet, and SSH key selectable. Like
the template it is a shared catalog object; reference the seeded template by
its id (`osac.templates.ocp_virt_vm` — the AAP role path):

```bash
./fulfillment-service/osac tenant shared
./fulfillment-service/osac create -f - <<EOF
'@type': type.googleapis.com/osac.private.v1.ComputeInstanceCatalogItem
metadata:
  name: model-fedora-vm
  tenant: shared
title: Model Fedora VM
description: |
  Fedora VM offering for the model tenant.
template:
  id: osac.templates.ocp_virt_vm
published: true
fields:
  disk_image:
    locked:
      name: fedora-latest
  instance_type:
    editable:
      default_value:
        name: ${INSTANCE_TYPE}
  boot_disk:
    size_gib:
      editable:
        default_value: 20
    storage_tier:
      editable:
        default_value:
          name: ${STORAGE_TIER}
  run_strategy:
    editable:
      default_value: COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS
EOF
```

Confirm that the catalog item is published (still with the `shared`
tenant selected), then switch back to `model`. Regular tenant users see
published shared catalog items in their own listings even though the
admin's tenant-scoped listing does not show them:

```bash
./fulfillment-service/osac get computeinstancecatalogitem model-fedora-vm
./fulfillment-service/osac tenant model
```

## 6. Log in as the regular tenant user

Sign out of the administrative session:

```bash
./fulfillment-service/osac logout
```

Start a new CLI login:

```bash
./fulfillment-service/osac login --insecure --address "$OSAC_API"
```

Open the printed URL, sign in as `model-user` with the password set on its
Keycloak account (a temporary Keycloak password cannot be used with the
CLI's password flow; complete the required change in the browser first, or
set a permanent password when creating the account). Then select the
tenant:

```bash
./fulfillment-service/osac tenant model
```

The UI is also available at `$OSAC_UI`.

## 7. Create the ComputeInstance

Two reference rules matter in the create command:

- Reference the security group by its **ID**, not its name. Name-based
  references are resolved by server-side reference validation in the
  object's tenant+project scope, and the name lookup fails for tenant
  users in this version (OSAC-3921 lab, OSAC 0.0.62); ID-based lookups
  are visibility-based and succeed. Find the ID with
  `./fulfillment-service/osac get securitygroups`.
- Omit `--boot-disk-storage-tier`: the catalog item's editable
  `default_value` for `boot_disk.storage_tier` fills it server-side (the
  storage tier `local` is a shared-tenant object, so a name-based
  reference from the tenant would not resolve).

```bash
./fulfillment-service/osac create computeinstance \
  --name model-vm \
  --catalog-item model-fedora-vm \
  --instance-type "$INSTANCE_TYPE" \
  --boot-disk-size 20 \
  --network-attachment "subnet=${SUBNET_ID},security-groups=${SG_ID}" \
  --ssh-key "$SSH_KEY" \
  --run-strategy Always
```

## 8. Connect to the VM with SSH

The VM address on the tenant subnet (`10.200.0.0/20`) is an overlay address:
it is reachable inside the cluster but no host outside the cluster routes to
it, including the site tunnel host. Use an ExternalIP from the pool created in
the prerequisites: it is advertised on the `192.168.100.0/24` network, which
the site tunnel (`sshuttle -r core@10.6.76.15 192.168.100.0/24`) already
routes.

Allocate an ExternalIP from the pool and wait until its state is `ALLOCATED`
and `status.address` is set:

```bash
./fulfillment-service/osac create externalip --name model-vm-ip --pool br-sno-pool
./fulfillment-service/osac get externalip model-vm-ip
```

Bind it to the ComputeInstance (the attachment exposes SSH, TCP port 22):

```bash
./fulfillment-service/osac create externalipattachment \
  --externalip model-vm-ip \
  --compute-instance model-vm \
  --name model-vm-att
```

Then connect with the private key that matches the public key registered in
the Secret, using the allocated address:

```bash
./fulfillment-service/osac get externalip model-vm-ip -o yaml
ssh -i "$HOME/.ssh/model-user-key" fedora@<external-ip-address>
```

> **Known limitation (this lab, 2026-11-16):** the SSH connect times out.
> The attachment correctly creates the LoadBalancer service (TCP/22) and OVN
> programs the ExternalIP VIP on the tenant user-defined network's load
> balancer, but nothing on the host steers north/south traffic into the UDN,
> so the service is unreachable from outside the cluster — and even
> east-west from the cluster network. This needs an upstream OVN
> multi-network services fix, not a runbook change; full evidence is in the
> demo recording deviations (item 17,
> `demos_and_workflows/model-tenant-vm-demo/`). Until it lands, use the
> serial console below for interactive access.

If the VM already exists and uses the default security group, add the same
ingress rule to that group instead of recreating the VM:

```bash
./fulfillment-service/osac edit securitygroup default
```

Add the rule to `spec.ingress`:

```yaml
- ipv4_cidr: 192.168.100.0/24
  protocol: PROTOCOL_TCP
  port_from: 22
  port_to: 22
```

If the ExternalIP path is unavailable, use the serial console for interactive
access while an ExternalIP is being prepared:

```bash
./fulfillment-service/osac console serial computeinstance model-vm
```

## 9. Verify the VM

Watch the instance until `status.state` is
`COMPUTE_INSTANCE_STATE_RUNNING`:

```bash
./fulfillment-service/osac get computeinstances --watch
```

Inspect the final object and conditions:

```bash
./fulfillment-service/osac get computeinstance model-vm -o yaml
```

## References

- `docs/guides/tenant-identity-and-access-guide.md`
- `docs/guides/developer/computeinstance-guide.md`
- `docs/guides/developer/computeinstance-catalogitem-guide.md`
- `fulfillment-service/docs/CATALOG_ITEMS.md`
- `docs/guides/installation/disconnected/installation.md`
