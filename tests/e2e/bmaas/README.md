# BMaaS E2E tests

These suites exercise BareMetalInstance creation through Fulfillment and the
deployed bare-metal operator. They require a deployed BMaaS stack and inventory
with available hosts.

## Required type configuration

When `OSAC_BMI_INSTANCE_TYPE` is unset, fixtures use the selected shared
template's `instance_type` reference if present. Otherwise they create and
clean up a shared type matching the CI virtual hosts labeled
`osac.openshift.io/host-type=default`, with a fabric port named `data-0`.
Set `OSAC_BMI_INSTANCE_TYPE` to select an existing type whose host selector
and network port names match a different inventory. The sanity and serial
suites create a temporary CatalogItem with the resolved type as its editable
default; the reference and networking suites pass it explicitly. The networking
regression requires an attachable `eth9` port, so use the override if the
template type does not provide it.

`OSAC_BMI_TEMPLATE` selects the shared template (default:
`bm-host-provisioning`), and `OSAC_BMH_NAMESPACE` selects the BareMetalHost
namespace (default: `host-inventory`). The networking regression also requires
`OSAC_BMI_CATALOG_ITEM`, `OSAC_BMI_AUTO_EIP_CATALOG_ITEM`, the external-IP pool
settings, and `OSAC_BMH_SSH_HOSTS`, a JSON object mapping each BareMetalHost
name to its SSH host, for example `{"virtual-bmh-1":"198.51.100.10"}`. Those
CatalogItems must allow the supplied `instance_type` value (leave the field
editable or unset; do not lock it).

## Inventory backend selection

`OSAC_INVENTORY_BACKEND` selects the backend the suites run against:
`metal3` (default) or `netbox`.

- Selection is env-var-only — the cluster's configured inventory `type` is
  not consulted for test routing. The operator allocates only from its
  configured `type`, so `OSAC_INVENTORY_BACKEND` must match it on the
  cluster under test.
- The abstraction covers the inventory layer only (free-host counts, claim,
  release).
- Once a host is allocated, the operator hands over to BMH/metal3 in every
  mode: the BMI's externalHostID names the BMH (on netbox:
  `netbox-<device-id>`, created on the fly).
- All provisioning assertions (provisioned state, image URL, consumerRef,
  online, power on/off, inspected NICs) run against that BMH exactly as on a
  metal3-only cluster.
- The networking regression is metal3-specific and does not route.

### NetBox connection settings

Primary source on `netbox`: the BMF chart's Secrets in the OSAC namespace
(`osac-inventory-config`, `osac-netbox-token`, the optional
`osac-netbox-ca`, and `osac-management-config`). Env vars are
debug/external-NetBox overrides that win when set:

| Var | Purpose | Default |
|---|---|---|
| `OSAC_INVENTORY_BACKEND` | `metal3` or `netbox` — the only backend selector | `metal3` |
| `OSAC_NETBOX_URL` / `OSAC_NETBOX_TOKEN` | NetBox URL and API token overrides | from BMF Secrets |
| `OSAC_NETBOX_CA_FILE` / `OSAC_NETBOX_INSECURE` | CA PEM file / disable TLS certificate verification — the only verification switch; the deployed `allowInsecureHTTP` merely permits an `http://` URL and never disables verification | CA from the `osac-netbox-ca` Secret / verification on |

Secret pairing rules:

- URL Secret: read only when `OSAC_NETBOX_URL` is unset.
- Token Secret: read only when the URL comes from the deployed config — an
  override URL never receives the deployed token; set `OSAC_NETBOX_TOKEN`
  explicitly for an external NetBox.
- CA Secret: applies only when the URL comes from the deployed config — an
  override URL uses system roots unless `OSAC_NETBOX_CA_FILE` is set, so the
  deployed NetBox's private CA never leaks into an external connection.
- `osac-management-config` BMH-namespace cross-check: cluster-scoped,
  best-effort, every mode.

### Pre-staged inventory

Both backends stage their inventory before the suite starts — never the
tests:

- metal3: BMHs enrolled into the BMH namespace by the lab/CI infrastructure
  (the netris Ansible backend in osac-test-infra) or the platform admin on a
  real deployment.
- netbox: devices staged by the provider reconcile
  (`reconcile-netbox-devices.py`, driven by
  `setup-virtual-netbox-devices.sh` in CI).
- The `inventory_hosts` session fixture only asserts that at least one host
  is available.

### Host selector

The auto-created instance type's host selector is backend-dependent:

- metal3: the `osac.openshift.io/host-type` BMH label.
- netbox: the `host_type` device custom field, stamped on every staged
  device by the provider reconcile (the netbox equivalent of the infra
  backend labelling BMHs).

### Release

On deprovision the operator deletes the per-device BMH and restores the
device to `staged` — release checks wait for both.

## Running

From the repository root, collect the affected suites before running them:

```bash
uv run pytest --collect-only tests/e2e/bmaas tests/e2e/references/test_cluster_baremetal_references.py
uv run pytest -n 0 tests/e2e/bmaas/sanity/
uv run pytest -n 0 tests/e2e/bmaas/regression/test_cpu_architecture_compatibility.py
uv run pytest -n 0 tests/e2e/bmaas/serial/test_baremetal_instance_inventory_exhausted.py
```

The networking regression has additional provider, SSH, and ExternalIP
prerequisites; run it only in a matching environment.

## Architecture compatibility

- **Sanity:** Provision a BareMetalInstance through CLI/catalog with a matching
  multi-architecture DiskImage. Verify that a catalog protects an image
  architecture needed by its InstanceType.
- **Regression:** Provision a compatible direct-template instance through the
  public API, then reject mismatched image/type selections through CLI and
  public/private APIs without creating an instance or allocating a host.

Architecture cases require an amd64 instance type and matching inventory.
The shared image declares AMD64 and ARM64; rejection cases use an ARM64-only
image. Run regression serially against isolated Metal3 inventory.
