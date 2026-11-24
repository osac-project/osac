# BMaaS networking coverage

The matrix tracks [OSAC-7619](https://redhat.atlassian.net/browse/OSAC-7619)
across implementation issues [OSAC-7595](https://redhat.atlassian.net/browse/OSAC-7595),
[OSAC-7596](https://redhat.atlassian.net/browse/OSAC-7596),
[OSAC-7597](https://redhat.atlassian.net/browse/OSAC-7597),
[OSAC-7598](https://redhat.atlassian.net/browse/OSAC-7598),
[OSAC-7599](https://redhat.atlassian.net/browse/OSAC-7599),
[OSAC-7600](https://redhat.atlassian.net/browse/OSAC-7600),
[OSAC-7601](https://redhat.atlassian.net/browse/OSAC-7601),
[OSAC-7602](https://redhat.atlassian.net/browse/OSAC-7602), and
[OSAC-7603](https://redhat.atlassian.net/browse/OSAC-7603). Provider hardware
and live Fabric Manager behavior belong to
[OSAC-7604](https://redhat.atlassian.net/browse/OSAC-7604). The Kind commands
below run from the repository root; the deployed E2E requires the compatible
BMaaS/Netris environment, BMH SSH, and a network interface.

- Kind Fulfillment: `make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment`
- Kind BMF/OSAC: `make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=bmf`
- Deployed E2E: `uv run pytest -n 0 tests/e2e/networking/bmaas`

| Issue(s) | Coverage and test evidence | Tier / boundary / status |
|---|---|---|
| [OSAC-7595](https://redhat.atlassian.net/browse/OSAC-7595), [OSAC-7596](https://redhat.atlassian.net/browse/OSAC-7596), [OSAC-7597](https://redhat.atlassian.net/browse/OSAC-7597), [OSAC-7600](https://redhat.atlassian.net/browse/OSAC-7600) | Fulfillment `projects network status without exposing diagnostics and reports deletion`; BMF/OSAC `provisions, applies, hands off, discovers the lease, then applies pending run and restart intent once`; E2E `test_05_create_three_bmis` checks public `NETWORK_READY` and primary IP. | Kind and deployed E2E. Component controllers/APIs are real; hub status and AAP are controlled. E2E assertion added; deployed run and Kind CI pending. |
| [OSAC-7600](https://redhat.atlassian.net/browse/OSAC-7600) | Fulfillment rejects non-fabric interfaces and `primary: false`, immutable attachment updates, and a type without a fabric port before persistence. | Kind Fulfillment; public API and PostgreSQL are real. Package compile passed; Kind CI pending. |
| [OSAC-7596](https://redhat.atlassian.net/browse/OSAC-7596) | BMF/OSAC reports final Fabric apply failure and the 15-minute handoff deadline; Fulfillment projects `NetworkHandoffFailed` and the public reason/message. | Kind BMF and Fulfillment; controllers/Kubernetes are real, AAP and power/fabric outcomes controlled. Compile passed; combined failure-to-public-status journey remains uncovered. |
| [OSAC-7597](https://redhat.atlassian.net/browse/OSAC-7597) | Retryable DHCP query stays pending; terminal query reports `IPDiscoveryFailed`; `records LeaseNotFound after ten minutes and keeps DHCP discovery pending`. | Kind BMF; Kubernetes/controllers are real and AAP responses are recorded. Compile passed; Kind CI pending. |
| [OSAC-7598](https://redhat.atlassian.net/browse/OSAC-7598) | `keeps BMF and OSAC cleanup finalizers while a Fabric delete fails` verifies no attachment/Fabric deletion before power-off confirmation, then waits for the reconciled `delete_network_attachment/fabric` retry and provider diagnostic before checking retained ownership; `removes network resources before leaving the feedback finalizer last`. | Kind BMF; Kubernetes/controllers are real, AAP failure is controlled. Execution pending CI. |
| [OSAC-7601](https://redhat.atlassian.net/browse/OSAC-7601) | Envtest exercises the `ALLOCATED` and Ready/Always gates, endpoint readiness recheck, and suppression after deletion starts. The deployed BMaaS E2E exercises successful automatic EIP attachment. Open deployed boundary cases: verify the real request waits for both allocation and endpoint readiness, deletion suppresses the request, and failed allocation leaves BMI readiness unaffected. | Envtest clients are doubles; it does not prove API-to-EIPA behavior. Keep the three deployed cases open until a qualifying real-boundary test runs; do not count the envtest as deployed coverage. |
| [OSAC-7599](https://redhat.atlassian.net/browse/OSAC-7599) | Mark a FabricDomain host before release, exclude it from selection, then allow selection after clearing the mark. | Gap: implementation and qualifying test are absent. |
| [OSAC-7598](https://redhat.atlassian.net/browse/OSAC-7598), [OSAC-7601](https://redhat.atlassian.net/browse/OSAC-7601) | BMF runs provisioning without network tasks and deprovisions without power-off; Fulfillment creates/deletes without an automatic ExternalIP request when networking is disabled. The deployed condition reason/message assertions are blocked on the owning `ProvisioningDisabled` implementation in [OSAC-7598](https://redhat.atlassian.net/browse/OSAC-7598); this case does not claim condition-contract coverage. | Kind BMF and Fulfillment; AAP captured. Disabled-mode resource/task absence is covered; all three BMF condition reasons and operation-specific messages remain open until OSAC-7598 lands. |
| [OSAC-7602](https://redhat.atlassian.net/browse/OSAC-7602) | [`tests/contract/network-task`](../../tests/contract/network-task) binds physical apply/delete/query payloads across producer and consumer. | Contract: `make test` in `osac-operator/` and `osac-aap/`. No live Fabric Manager or hardware; CI execution pending. |
