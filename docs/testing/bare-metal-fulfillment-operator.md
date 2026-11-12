# bare-metal-fulfillment-operator

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers and evidence;
inspect test source for individual cases. Paths are relative to
`bare-metal-fulfillment-operator/`; touched-area requirements are in the
[guide](../../bare-metal-fulfillment-operator/AGENTS.md#integration-testing).

Discover targets with `make help`; `make test` and `make integration-tests` are
the public entry points. The CI job runs the installer's Kind target with the BMF
suite (repository-root `make -C osac-installer help`, `.github/workflows/`).

## Test tiers

| Tier | Suite | Real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located Go unit tests | Allocation, lifecycle, client and provider logic in isolation | Kubernetes and provider APIs mocked. |
| Envtest | `internal/controller/` | Kubernetes API, etcd, OSAC and static Metal3 CRDs | Metal3 controller, Ironic/BMC, hardware and some provider clients faked. |
| Component integration | Deployed `test/integration/` | Deployed operator, CRDs, API, pool/instance flows, status | Static `BareMetalHost` state and simulated transitions; no real Metal3 operator, Ironic, BMC or hardware. |
| Component integration (CI) | Installer Kind target, BMF suite | Thin Kind install, installation API endpoint unset | Same static Metal3/provider boundary; installation settings need separate deployed coverage. |
| Contract | None dedicated | No real provider boundary | Static Metal3 CRDs, patched status and simulated transitions do not exercise a real contract; follow [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) for qualifying coverage. |
| E2E | Cross-component suites | Fulfillment-to-operator journeys where available | Real hardware and providers remain environment-dependent. |

## Coverage notes

Cover success, no-match and provider-error paths for inventory, selection,
validation and client logic; drive reconciliation, finalizers, allocation and
status through public reconciler behavior and CRD fixtures. Envtest alone does
not prove the deployed path, and static CRDs or HTTP doubles do not satisfy
Metal3/Ironic/BMC/power/hardware boundaries. Keep generated and Helm CRDs
synchronized.

## Networking cleanup ownership (NET-CLEAN-08)

The deployed `test/integration/` suite exercises the installed BMF manager and
Kubernetes persistence. Metal3 status and OSAC cleanup completion are simulated;
it does not exercise a real provider or the OSAC operator's cleanup. Run it via
the installer's Kind target with the BMF suite on its own cluster and ports
([Multi-branch test isolation](../MULTI-BRANCH-ISOLATION.md)).

## Coverage gaps

The Kind suite stops at static Metal3 resources and simulated provider status.
Changes to the real Metal3/Ironic/BMC/provider boundary must add qualifying
coverage under [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) or its
contract-test follow-up; extending the static-fixture suite alone is insufficient.
