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

`OSAC_INVENTORY_BACKEND` selects the inventory backend the sanity and serial
suites run against: `metal3` (default) or `netbox`. The cluster's configured
inventory `type` is not consulted for routing — selection is explicit so a
single cluster can be tested against either backend. The abstraction covers
the inventory layer only (free-host counts, fixture devices, the claim and
release of inventory hosts): once the inventory allocates a host, the
operator hands over to BMH/metal3 in every mode — the BMI's externalHostID
names the BMH (on netbox the operator derives `netbox-<device-id>` and
creates it). All provisioning assertions (provisioned state, image URL,
consumerRef, online, power on/off, inspected NICs) therefore run against the
BMH exactly as on a metal3-only cluster. The networking regression is
metal3-specific and does not route.

On `netbox`, NetBox connection settings come from the deployed
bare-metal-fulfillment configuration — the BMF chart's Secrets in the OSAC
namespace (`osac-inventory-config`, `osac-netbox-token`, the optional
`osac-netbox-ca`, and `osac-management-config`). The following env vars are
debug/external-NetBox overrides that win when set, plus the fixture inputs
the backend needs:

| Var | Purpose | Default |
|---|---|---|
| `OSAC_INVENTORY_BACKEND` | `metal3` or `netbox` — the only backend selector | `metal3` |
| `OSAC_NETBOX_URL` / `OSAC_NETBOX_TOKEN` | NetBox URL and API token overrides | from BMF Secrets |
| `OSAC_NETBOX_CA_FILE` / `OSAC_NETBOX_INSECURE` | CA PEM file / disable TLS certificate verification — the only verification switch; the deployed `allowInsecureHTTP` merely permits an `http://` URL and never disables verification | CA from the `osac-netbox-ca` Secret / verification on |
| `OSAC_NETBOX_SITE` | Site slug for fixture devices (required on netbox) | — |
| `OSAC_NETBOX_DEVICE_TYPE` | Device type slug for fixture devices (required on netbox) | — |
| `OSAC_NETBOX_ROLE` | Device role slug for fixture devices | `server` |
| `OSAC_NETBOX_FIXTURE_HOSTS` | JSON list of virtual host specs: `{"bmc_address": ..., "boot_mac": ..., "interface_macs"?: {...}, "custom_fields"?: {...}}` — one per lab host; `custom_fields` carries capability fields matching the instance type's host selector, and `interface_macs` feeds the operator's logical-port MAC mapping (used by DHCP/networking, not by the sanity and serial assertions, which read BMH inspection data) | — |

Unlike metal3 (whose BMHs are enrolled into the BMH namespace by the
lab/CI infrastructure — the netris Ansible backend in osac-test-infra — or
by the platform admin on a real deployment, never by the tests), the netbox
backend provisions its own inventory: the `inventory_hosts` session
fixture creates `osac_managed` devices in the `staged` state from
`OSAC_NETBOX_FIXTURE_HOSTS` (named `<test_run_id>-netbox-<n>`; repeated setup
within one session is idempotent) and deletes them on teardown. Each run
uses a new run id, so delete leftover devices after an aborted run.

The auto-created instance type's host selector is backend-dependent: on
metal3 it matches the `osac.openshift.io/host-type` BMH label; on netbox it
is the `host_type` device custom field, which the fixture stamps on every
device it creates (the netbox equivalent of the infra backend labelling
BMHs). `interface_macs` keys must match the instance type's network port
names (`data-0` by default) — they populate the operator's
`osac_interface_macs` logical-port MAC mapping.

Connection settings come from the BMF chart's `osac-inventory-config`,
`osac-netbox-token`, and `osac-netbox-ca` Secrets. The URL and token Secrets
are read only when their `OSAC_NETBOX_*` overrides are unset, and the CA
Secret applies only when the URL comes from the deployed config — an
override URL uses system roots unless `OSAC_NETBOX_CA_FILE` is set, so the
deployed NetBox's private CA never leaks into an external connection. (The
`osac-management-config` BMH-namespace cross-check is cluster-scoped and read
best-effort in every mode.) On deprovision the operator deletes the
per-device BMH and restores the device to `staged`, so release checks wait
for both.

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
