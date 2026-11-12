# tests/e2e

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Suite commands, environment gates and per-area requirements live in
the [touched-area map](../../tests/e2e/AGENTS.md#touched-area-map); fixtures
and skip conditions live in `tests/e2e/conftest.py` and the suite directories.
Read both before extending or claiming coverage. Unit tests under `tests/unit/`
validate helpers and selectors; pytest collection proves import/selection
readiness only.

## Suite boundaries

| Suite (by directory/role) | Real | Faked or omitted |
|---|---|---|
| CaaS sanity (`caas/sanity`) | Fulfillment/database, Kubernetes, operators, AAP, HyperShift/CAPI/CAP-Agent, Assisted Service, Kafka/metering | Virtual BMHs prove no physical hardware, guest LVMS, CSI mount or application I/O |
| VMaaS/BMaaS/storage/reference | Deployed orchestration through Fulfillment, operators, AAP, providers | Need compatible templates, images, providers, credentials; storage E2Es prove orchestration/StorageClass setup, not a CSI PVC-to-mounted-workload path |
| Service-account regression (`regression`, [QE] [OSAC-6557](https://redhat.atlassian.net/browse/OSAC-6557)) | Deployed Fulfillment and Keycloak: service-account journeys through the real APIs, rotation included | Requires the `full-ci` profile and Keycloak admin credentials; no provider-backed workloads are created |
| Deployed disk identity (storage regression, [QE] [OSAC-6614](https://redhat.atlassian.net/browse/OSAC-6614)) | Deployed provisioning through Fulfillment, operator, AAP, and CDI/KubeVirt, and eventual absence of the saved disk records after deletion | Needs compatible storage, tenant networking and private API auth. Does not establish AAP-pending record retention, an exact per-ID Delete request/response order, or pre-feature compatibility; those criteria remain gaps |
| Metering (VMaaS sanity billing) | Deployed event-to-heartbeat path via API/Kafka/metering and the echo adapter (`METERING_ADAPTER_URL`) | ExternalIP billing lives in the networking billing suite |
| Netris VMaaS (`networking/vmaas` connectivity) | Fulfillment, operator, AAP, KubeVirt, CUDN EVPN, Netris | Needs Netris CI values, VM kubeconfig, `kubectl`, pullable SSH helper image, routed external pool (see fixtures). Serial numbered tests; not sliceable |
| Networking billing (`networking/vmaas` billing) | ExternalIP/reattach/NATGateway billing through deployed metering and the echo adapter | Ordered cases inside the connectivity module, before the final cases: under `-x` an earlier failure hides billing and a billing failure hides them. Needs provider networking, a routed pool and the adapter; the module also needs the SSH helper, and cases that skip for a missing adapter carry no signal |
| Netris BMaaS (`networking/bmaas`) | BMI networking on the deployed fabric | Needs BMH SSH and the BMI network interface. PR job omits slow cross-tenant cases; Netris `full` excludes the directory (`cudn_evpn` rejects its ICMP rules) |
| Stretched (`networking/stretched`) | BMI/VM connectivity on one Subnet | Needs the stretched deploy suite, BMaaS SSH, VM template/helper, EVPN underlay; not in standard runs |

- Networking references ([OSAC-6744](https://github.com/osac-project/osac/pull/2323)) distinguish disabled-mode rejection without storage/allocation/accounting changes from enabled-provider allocation and traffic. The disabled fixture requires private pool management and Kubernetes observation; enabled ID/name selectors run serially in the full ordered VMaaS module, retaining the final allocation for detach, automatic allocation, billing and teardown. Registration/default rendering and disabled component tests do not establish live Netris allocation, connectivity or release; missing deployed evidence remains unresolved.

## Coverage gaps

The disabled reference fixture owns its shared pool; private API auth must allow
pool create/read/delete and child ExternalIP List/delete, with Kubernetes
observation for readiness and natural cleanup. No pre-existing pool is needed.
Read failures remain failures; tenant-only credentials are insufficient.
Its admin identity must also read private `InstallationSettings/Get`: the guard
reads the stored mode with explicit JSON defaults before pool creation, skips
only boolean enabled mode, and fails setup for missing/nonboolean mode or RPC
errors. Disabled mode retains the full rejection contract; this guard does not
apply to enabled-provider suites. Hermetic diagnostic tests use synthetic
subprocess failures, not live provider execution or evidence of real exposure.

- Inter-subnet VM-to-VM routing across CUDNs is Phase 2, tracked by
  [OSAC-3667](https://redhat.atlassian.net/browse/OSAC-3667).
- The [OSAC-4291](https://redhat.atlassian.net/browse/OSAC-4291) Netris VMaaS
  suite does not cover the ICMP-capable AAP path or the second-tenant automatic
  ExternalIP ingress plus garbage-collection journey; no owner or ticket
  identified, both unresolved.
- Pool capacity, attachment validation and name-reference cases have
  lower-tier Fulfillment tests; they do not replace provider release or
  cross-component billing evidence. Provider-enabled Fulfillment specs skip in
  the standard provider-disabled deployment — read their skip conditions
  before citing them.
- The AgentlessNet DHCP lab ([OSAC-5530](https://redhat.atlassian.net/browse/OSAC-5530),
  [OSAC-1562](https://redhat.atlassian.net/browse/OSAC-1562)) is separately
  source-pinned; it proves no physical hardware, full BMI
  provisioning/status, NAT or full-install CI.
- Multi-node live hot-plug remains
  [OSAC-5335](https://redhat.atlassian.net/browse/OSAC-5335).

## CaaS CI selection boundary

Full-install callers in osac and osac-test-infra select `caas/sanity` for PRs
and merge queue; `pr-number` dispatches keep that policy regardless of tier
labels or suite/marker overrides. Periodics select all of `caas` (sanity +
regression) plus reference tests marked `requires_caas` or `reference_common`.
Manual dispatches default to sanity and keep overrides. The reusable runner
never adds references to sanity; an empty requested tier fails rather than
falling back. [DEV] selection checks under `tests/unit/` evaluate real caller
YAML (companion checks skip without `OSAC_TEST_INFRA_DIR`) and prove selection
only, not [QE] deployed CaaS success.

## Focused CaaS natural-teardown boundary

Sanity uses a published ClusterCatalogItem; the two-node-set regression needs
BMHs for both sets, and BMIT sharing across sets stays unit coverage. Natural
teardown verifies worker ownership, waits for the exact BMIs, then
ClusterOrder, then independent InfraEnv absence — API errors, terminating
objects and empty status are not absence. No lifecycle hooks or finalizers are
removed; legacy cleanup-enabled waits do not prove natural teardown. Retry
sleep budgets are not production SLAs. Preserve JUnit, lifecycle and cleanup
evidence before teardown.

`tests/e2e/conftest.py` fixtures supply hub, API, CLI, image/template,
pull-secret/SSH, virtual BMH and Kafka/metering inputs. Pin compatible
OSAC/operator/installer/AAP revisions and image digests; AAP must resolve the
tested revision, not mutable `main`/`latest`. Guest LVMS disk prerequisites
are an unresolved infrastructure follow-up (no revision, owner or Jira URL).
Do not wipe/reuse the guest OS disk.
