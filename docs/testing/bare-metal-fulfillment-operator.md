# bare-metal-fulfillment-operator

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Follow this guide for suite setup and limits; inspect test source for
individual cases. Commands and inline paths are relative to `bare-metal-fulfillment-operator/`
unless explicitly marked as repository-root commands.

Touched-area requirements: [component guide](../../bare-metal-fulfillment-operator/AGENTS.md#integration-testing).

## Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located `*_test.go`; `make test` | Allocation, lifecycle, client, and provider logic in isolation | Kubernetes and external provider APIs are mocked or intercepted. |
| Envtest | Controller tests under `internal/controller/*_envtest_test.go`; run by `make test` | Kubernetes API server, etcd, OSAC CRDs, and static Metal3 CRDs | Metal3 controller, Ironic/BMC, hardware, and some provider clients are faked. |
| Component integration | `test/integration/`; deploy the current operator into a Kind cluster, then run `make integration-tests` | Deployed operator behavior, CRDs, Kubernetes API, pool/instance flows, and status transitions | The suite creates static `BareMetalHost` state and simulates provider transitions; it does not run a real Metal3 operator, Ironic, BMC, or hardware. |
| Component integration (CI) | `make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=bmf` (from repository root) | Thin Kind deployment with the installation API endpoint unset | Same static Metal3/provider boundary as the local suite; installation-setting verification needs a separate deployed test. |
| Contract | No dedicated contract suite; follow [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) for qualifying provider-contract coverage | No real external provider boundary is exercised by the current suite | Static Metal3 CRDs, patched status, and simulated provider transitions do not exercise a real Metal3/Ironic/BMC contract. |
| E2E | Cross-component OSAC E2E suites | Fulfillment-to-operator user journeys where the environment provides them | Real hardware and provider availability remain environment-dependent. |

## Coverage notes

- **Pure inventory, selection, validation, or client logic:** Cover success, no-match, and provider-error paths.
- **Reconciliation, finalizers, allocation, or status transitions:** Use the public reconciler behavior and the appropriate CRD fixtures.
- **Controller deployment, CRDs, pool flows, or Kubernetes wiring:** Envtest alone does not prove the deployed controller path.
- **Metal3, BCM, Ironic, BMC, power, or hardware semantics:** Static CRDs and HTTP test doubles do not satisfy a real-boundary requirement.
- **Generated CRDs or Helm CRDs:** Keep generated artifacts synchronized.

## Networking cleanup ownership (NET-CLEAN-08)

The [cleanup ownership suite](../../bare-metal-fulfillment-operator/test/integration/networking_cleanup_test.go)
exercises the installed BMF manager and Kubernetes persistence. Metal3 hardware
status and OSAC cleanup completion are simulated; it does not exercise a real
provider or the OSAC operator's cleanup implementation.

Run the Component integration (CI) command above with `SUITE=bmf`. Use its own
Kind cluster and ports when running alongside the operator suite, following
[Multi-branch test isolation](../MULTI-BRANCH-ISOLATION.md).

## Coverage gaps

The current Kind suite deliberately stops at static Metal3 resources and
simulated provider status. Work that changes the real Metal3/Ironic/BCM/BMC
boundary must add the qualifying coverage under [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) or its contract-test
follow-up; extending the existing static-fixture suite alone is insufficient.
