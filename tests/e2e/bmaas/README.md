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
