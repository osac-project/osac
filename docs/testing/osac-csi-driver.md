# osac-csi-driver

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Inspect test source for individual cases. Paths are relative to
`osac-csi-driver/` unless marked as repository-root paths.

Touched-area requirements: [component guide](../../osac-csi-driver/AGENTS.md#integration-testing).

## Test tiers

| Tier | Suite (by directory/role) | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located Go suites, run through the component `test` target | Driver, node/controller mapping, fulfillment client, and validation logic in-process | Fulfillment service and vendor storage endpoints are mocked. |
| Unit (CSI sanity) | The sanity suite under `test/sanity/`, included by the `test` target | CSI protocol calls over Unix sockets and the meta-driver's routing behavior | The vendor implementation is `fakeVendor`; fulfillment volume operations use a stub. |
| Component integration | No dedicated real-backend suite currently exists | — | No real storage vendor, attach/detach, mount, or fulfillment deployment is exercised by the `test` target. |
| Contract | No dedicated contract suite; track [OSAC-4845](https://redhat.atlassian.net/browse/OSAC-4845) for vendor and fulfillment-boundary coverage | No deployed fulfillment or real vendor endpoint | Fulfillment and vendor calls use stubs and `fakeVendor`. |
| E2E | `../tests/e2e/storage/` when enabled | Tenant/CaaS storage-controller lifecycle and StorageClass setup | No PVC through the OSAC CSI driver: no CSI `CreateVolume`, node publish/mount, or pod I/O; depends on the storage tier and environment gates. |

Run `make help` for the current test targets.

## Coverage notes

- **Mapping, validation, or driver helpers:** cover protocol errors and backend status mapping.
- **CSI routing or protocol behavior:** the sanity suite is required but remains fake-vendor coverage.
- **Fulfillment's private Volume API contract:** a fake generated client does not prove compatibility with the deployed service.
- **Vendor attach, detach, mount, or storage lifecycle:** the fake vendor cannot satisfy a real storage-backend requirement.
- **Helm/deployment changes:** build an image when container/deployment inputs change.

## Coverage gaps

The sanity suite intentionally stops at a fake vendor and a fulfillment stub;
real-backend coverage is tracked by
[OSAC-4845](https://redhat.atlassian.net/browse/OSAC-4845). Do not label
fake-vendor sanity coverage as component integration coverage. The storage E2Es
validate orchestration and StorageClass setup, not the full CSI delivery path
from PVC creation through LVMS/TopoLVM to a mounted workload; that journey
still needs an explicitly owned QE test.
