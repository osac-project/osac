# FabricDomain API integration and lifecycle coverage

`it_fabric_domain_tenant_api_test.go` exercises FabricDomain validation,
platform-admin CRUD, tenant-filtered Get/List, and denial of tenant-user writes
through the deployed Fulfillment API and PostgreSQL database. It is component
integration coverage; it does not claim that the operator, AAP, or Netris
Server Cluster lifecycle works. The Fulfillment integration target deploys the
operator disabled and the installer's NetworkClass of the dev profile doesn't
enable east-west Ethernet, so every FabricDomain create fails validation:
TC-FD-01 checks those rejections (type, servers, VirtualNetwork, and the
`supports_east_west_ethernet` gate) and TC-FD-02 the tenant-user denial. The
CRUD and tenant-filtering specs skip unless the NetworkClass enables east-west
Ethernet; they also need servers that are BareMetalInstance hosts of the
tenant.

The auth unit matrix in
`fulfillment-service/internal/auth/grpc_authz_interceptor_test.go` covers
tenant users, tenant admins, IdP managers, and platform admins. The deployed
integration case creates per-spec `alice` and `carol` service accounts in
Kubernetes namespaces matching its unique OSAC tenants, so the token tenant
claims exercise the same tenant IDs used by the persisted resources.

Run the component suite against its dedicated Kind environment:

```bash
make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment
```

To run only the FabricDomain case after the Fulfillment test environment is
already installed:

```bash
cd fulfillment-service
ginkgo run --focus 'FabricDomain tenant API component integration' it
```

Do not run this API-only case against an environment where networking
provisioning is enabled. Its test hostnames are intentionally synthetic and
must never reach Netris.

## Pending BMaaS lifecycle E2E

The full FabricDomain-to-Netris lifecycle belongs to
[OSAC-4786](https://redhat.atlassian.net/browse/OSAC-4786) in the BMaaS E2E
suite. It requires admin-configured, reserved real Netris hosts and site/region,
the hub's `osac-fabric-domain-inventory` ConfigMap, shared BMIT IDs with matching
`fabric_bindings.ethernet_ew` profile references, a real Netris VirtualNetwork/
VPC, and ownership-verified cleanup. Never discover arbitrary free hosts,
invent hostnames, or repurpose allocated hosts automatically.

The live suite should verify create/Ready, exact Server Cluster members and
template, resize, failure/retry without duplicates, and delete/finalizer cleanup.
Until that fixture and those checks run, API integration success is not evidence
of provider lifecycle coverage.
