# FabricDomain Phase 1 API coverage

`test_fabric_domain_api.py` adapts the OSAC-4782 API regression cases from PR1272.
It exercises the public API, authorization, database tenancy filtering, and
resource cleanup through the deployed stack. Despite its location in the E2E
runner, this is **API/component-integration coverage**, not evidence of a working
Netris ServerCluster lifecycle.

The auth unit matrix in
`fulfillment-service/internal/auth/grpc_authz_interceptor_test.go` is DEV-owned.
It checks public tenant Get/List, denial of public tenant Create/Update/Delete,
private API restrictions, and the existing platform-admin identities. A tenant
admin is not a platform admin. The deployed API scenarios are tracked by
[OSAC-4782](https://redhat.atlassian.net/browse/OSAC-4782).

## Dedicated API-only environment

Use the existing E2E authentication and hub configuration, including
`OSAC_NAMESPACE`, public/private fulfillment endpoints, platform-admin service
account, and tenant1/tenant2 JWT users. The platform-admin token is used against
the **public** endpoint to create domains explicitly assigned to each tenant.
Tenant users and tenant admins must be denied writes even to their own domain;
Get/List expose their own domains and hide the other tenant's objects.

The deployment's single active NetworkClass must be Ready, advertise
`supportsEastWestEthernet`, and use `fabricManager: netris`. There is no
NetworkClass template-ID prerequisite: templates now belong to BMIT bindings
and are resolved during operator provisioning, outside these API-only tests.

Before running, configure a dedicated test deployment with an explicit
`OSAC_ENABLE_NETWORKING_PROVISIONING=false` on its `manager` container and wait
for rollout completion. Keep that configuration stable throughout the run.
The fixture checks the flag and observed/updated/available replicas before
each test. It never patches or disables a shared deployment for the caller.

```bash
export OSAC_FABRIC_DOMAIN_API_ONLY=true
export OSAC_FABRIC_DOMAIN_OPERATOR_DEPLOYMENT=<operator-deployment-name>
uv run pytest -n 0 tests/e2e/vmaas/regression/test_fabric_domain_api.py
```

The operator deployment must be in the configured hub client's `OSAC_NAMESPACE`.
Without the opt-in, these tests report an explicit skip; that means **no deployed
API coverage was obtained**. With opt-in, missing deployment configuration,
enabled provisioning, incomplete rollout, or an unsuitable NetworkClass fails
the tests. The arbitrary `*.example.test` hostnames are only for API persistence;
they must never be provisioned against real Netris.

Local validation without a deployed environment:

```bash
uv run pytest -n 0 tests/unit
uv run pytest -n 0 --collect-only tests/e2e/vmaas/regression/test_fabric_domain_api.py
uv run ruff check tests/e2e/vmaas/regression/test_fabric_domain_api.py
uv run ruff format --check tests/e2e/vmaas/regression/test_fabric_domain_api.py
```

Collection and unit success do not establish deployed API or lifecycle success.

## Pending real-provider lifecycle coverage

The broader [OSAC-1382](https://redhat.atlassian.net/browse/OSAC-1382) QE lifecycle
coverage remains pending an explicit environment-fixture contract. It must take
admin-configured, reserved real Netris hosts and site/region, the hub's
`osac-fabric-domain-inventory` ConfigMap, shared BMIT IDs with compatible
`fabric_bindings.ethernet_ew.netris` NetworkClass/template bindings, and a real
Netris VirtualNetwork/VPC. Never discover arbitrary free hosts, invent hostnames,
or repurpose allocated hosts automatically. The provider checks need an
environment-owned Netris read client and cleanup/ownership verification.

Required checks before claiming lifecycle coverage:

- Create reaches Ready and the expected ServerCluster exists in the pinned
  site/VPC with exactly the requested real hosts and template.
- Explicit resize preserves the pinned backend context and converges membership.
- Unknown inventory hosts, missing BMIT bindings, and incompatible bindings fail
  closed without launching backend work; live rebinding is rejected.
- Restart/retry preserves backend identity and does not create duplicates.
- Delete removes the owned ServerCluster and releases VirtualNetwork protection,
  including when the inventory entry or BMIT disappears after provisioning.
- Tenant isolation and admin-only writes hold through the complete journey.

Do not add a live lifecycle test that silently skips without this fixture and
then report it as covered. Until the environment and assertions exist, these
checks are pending; the API-only cases above do not substitute for them.
