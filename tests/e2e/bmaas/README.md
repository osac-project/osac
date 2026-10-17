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
uv run pytest -n 0 tests/e2e/bmaas/sanity/test_baremetal_instance_lifecycle.py
uv run pytest -n 0 tests/e2e/bmaas/serial/test_baremetal_instance_inventory_exhausted.py
```

The networking regression has additional provider, SSH, and ExternalIP
prerequisites; run it only in a matching environment.

The Netris networking regression exercises OSAC-4923 against the deployed
Fulfillment/operator/AAP/Netris path. It replays same-spec creates for the
ExternalIPPool, VirtualNetwork, both Subnets, SecurityGroup, NATGateway,
ExternalIP, and ExternalIPAttachment, then checks that each replay finishes
with a fresh successful AAP job while preserving the Kubernetes resource and
desired state. It replays one BMaaS tenant-network attachment move and verifies
the attachment status and handoff conditions remain ready. Teardown checks the
successful provider deprovision job and controller-finalizer completion for
each resource, and verifies BMaaS port moves back to the provisioning network
during instance deletion. NATGateway and ExternalIPAttachment transition
timestamps may advance during replay because their controllers timestamp
Progressing/Ready phase changes. Run the suite without pytest's `-x` option to
continue through later tests after an individual failure; dependent cases may
skip when a failed create left their prerequisites unavailable. The
`netris_subnet_delete` and `netris_parent_delete`
component-integration targets cover provider-absence handling for their
documented callers; default-resource guards are component-integration
coverage. The deployed E2E does not force a second reconcile after deletion
begins.

Run that suite with:

```bash
uv run pytest -n 0 tests/e2e/bmaas/regression/networking/test_bmaas_networking.py
```

It requires `OSAC_BMI_CATALOG_ITEM`, `OSAC_BMI_AUTO_EIP_CATALOG_ITEM`, the
ExternalIPPool name/CIDR settings, and `OSAC_BMH_SSH_HOSTS` as a JSON mapping
of BareMetalHost names to reachable SSH hosts. The selected instance type must
provide an attachable `eth9` port. This deployed journey does not inject AAP or
Netris failures, create immutable-field drift, exercise default-resource
delete guards, or test deferred SecurityGroup stale-ACL pruning.
