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
| Contract | Real NetBox Community version matrix tracked in [OSAC-7518](https://redhat.atlassian.net/browse/OSAC-7518) | Real NetBox REST semantics | The HTTP fixture shared by Envtest and Kind exercises OSAC behavior. |
| Component integration (Kind) | Installer BMF NetBox test target | Deployed BMF controller, Kubernetes API, Metal3 CRDs, Fulfillment Secret API | NetBox HTTP API is mocked in-cluster; Metal3/Ironic/BMC hardware remains absent. See [OSAC-7519](https://redhat.atlassian.net/browse/OSAC-7519). |
| E2E | Cross-component suites | Fulfillment-to-operator journeys where available | Real hardware and providers remain environment-dependent. |

The NetBox component suite runs from the repository root with
`make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=bmf-netbox`
after provisioning the Kind infrastructure. It uses the OSAC Helm chart and
asserts allocation, release, and use of a rotated token in `test/integration`.
After a local run, the BMF release stays configured against the fixture
Deployment, Service, and synthetic token Secrets in the test namespace.
CI runs both BMF scenarios with shared images and CRDs. The existing NetBox
envtest specs cover selection, allocation, release, and claim recovery with
an in-process reconciler. The Kind case uses the same NetBox WireMock
mappings and checks deployed credential lookup and token reload after a rollout.

The BMF `make test` target builds the WireMock image. Testcontainers starts and
cleans up an isolated instance for each NetBox envtest spec. To run the specs
directly with `go test`, build the image with `make wiremock-image` first.

For Podman, enable its API socket and configure `DOCKER_HOST` as described in
the [Testcontainers Podman setup](https://golang.testcontainers.org/system_requirements/using_podman/).
Use `make test CONTAINER_TOOL=podman` so the image build and tests use the same
engine. For direct test runs, build with `make wiremock-image CONTAINER_TOOL=podman`
and set `TESTCONTAINERS_PROVIDER=podman` when invoking `go test`.

If Ryuk cannot access the container engine socket locally, run
`TESTCONTAINERS_RYUK_DISABLED=true make test`. Each spec cleans up its own container.

The installer builds and loads the same image for Kind. The image in
`bare-metal-fulfillment-operator/test/wiremock` can serve other provider
fixtures with their own mappings. The NetBox mappings exercise conditional
updates and sequential claim/release flows. Parallel envtest specs use separate
WireMock instances.

## Coverage notes

Cover success, no-match and provider-error paths for inventory, selection,
validation and client logic; drive reconciliation, finalizers, allocation and
status through public reconciler behavior and CRD fixtures. Envtest alone does
not prove the deployed path, and static CRDs or HTTP doubles do not satisfy
Metal3/Ironic/BMC/power/hardware boundaries. Keep generated and Helm CRDs
synchronized.

## Selected-port MAC and DHCP discovery

The installer Kind target enables BMF networking for the deployed controller
test. It reads a logical-port MAC from the fake BareMetalHost annotation,
checks that BMF persists the normalized MAC and creates a tenant
NetworkAttachment, and exercises the BareMetalInstance status MAC pattern
through the installed CRD. The test then patches accepted NetworkAttachment
readiness, DHCP lease, and fake Metal3 power state to verify BMF mirrors the
lease and query-job status. This covers the deployed BMF/API wiring; it does not
run the Fabric Manager or AAP DHCP task, and it does not cover reusing a legacy
NetworkAttachment. Unit and Envtest cases cover selection, reuse, diagnostics,
and lease handling in isolation. Real provider and power behavior remains under
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).

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
Real NetBox compatibility remains tracked in [OSAC-7518](https://redhat.atlassian.net/browse/OSAC-7518).
