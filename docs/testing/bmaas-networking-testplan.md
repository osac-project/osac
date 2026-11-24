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
| [OSAC-7598](https://redhat.atlassian.net/browse/OSAC-7598) | `keeps BMF and OSAC cleanup finalizers while a Fabric delete fails`; `removes network resources before leaving the feedback finalizer last`. | Kind BMF; Kubernetes/controllers are real, provider failures controlled. Existing cases; CI execution pending. |
| [OSAC-7601](https://redhat.atlassian.net/browse/OSAC-7601) | Envtest creates one attachment only for allocated EIP after Ready/Always status, waits for ready Always endpoint with primary address, and skips after deletion starts. | From `osac-operator/` after `make envtest`: `KUBEBUILDER_ASSETS="$(pwd)/bin/k8s/1.31.0-linux-amd64" go test ./internal/controller -count=1 -ginkgo.focus='BareMetalInstance cleanup automatic external access'`. Passed; clients are doubles, so deployed API-to-EIPA feedback remains uncovered. |
| [OSAC-7599](https://redhat.atlassian.net/browse/OSAC-7599) | Mark a FabricDomain host before release, exclude it from selection, then allow selection after clearing the mark. | Gap: implementation and qualifying test are absent. |
| [OSAC-7598](https://redhat.atlassian.net/browse/OSAC-7598), [OSAC-7601](https://redhat.atlassian.net/browse/OSAC-7601) | BMF runs provisioning without network tasks and deprovisions without power-off; Fulfillment creates/deletes without an automatic ExternalIP request when networking is disabled. | Kind BMF and Fulfillment; AAP captured. Compile passed; Kind CI pending. This PR follows [OSAC-7601](https://redhat.atlassian.net/browse/OSAC-7601); alignment with the [accepted OSAC-1437 design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1437-bmaas-networking/design.md) remains open. |
| [OSAC-7602](https://redhat.atlassian.net/browse/OSAC-7602) | [`tests/contract/network-task`](../../tests/contract/network-task) binds physical apply/delete/query payloads across producer and consumer. | Contract: `make test` in `osac-operator/` and `osac-aap/`. No live Fabric Manager or hardware; CI execution pending. |
