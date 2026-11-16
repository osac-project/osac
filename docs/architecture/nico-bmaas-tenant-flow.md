# NICo-backed BMaaS tenant flow and CLI recording

The [asciinema recording](nico-bmaas-tenant-flow.cast) captures a fresh
`tenant-demo` BMaaS create and delete on the zeus12 Machine-a-Tron lab on
2026-10-29. Replay it with:

```bash
asciinema play docs/architecture/nico-bmaas-tenant-flow.cast
```

The recording is an edited cut of the real terminal output. It omits an
invalid SecurityGroup command and a recording helper that initially treated
the CLI's single-object JSON as an array. No BMaaS request was retried to make
the recording. Passwords, client secrets, and tokens are absent from the cast.

## Two independent tenant systems

Creating an OSAC Tenant does not automatically create a NICo Tenant. OSAC and
NICo have separate IdP organizations, users, resource IDs, and authorization.
An administrator explicitly binds one OSAC tenant and Subnet to the matching
AWX credential set after both sides have been provisioned.

| Layer | Fresh demo resource | Purpose |
| --- | --- | --- |
| OSAC identity and fulfillment | `tenant-demo`, user `tenant_demo_user` | OSAC API scope and BMI ownership |
| OSAC network | Subnet `01a18561-d62a-732f-82c2-dcf65f861d92` in `10.217.100.0/28` | The BMI's `subnetRef` and tenant binding key |
| NICo identity | IdP org `test-org-demo`, user `osac-demo` | Token carries `test-org-demo:TENANT_ADMIN`; it received HTTP 403 for the other org |
| NICo Tenant | `62a06914-fdcf-44e0-8c56-df9d06849a14` | Owns the NICo Instance and network |
| NICo network | VPC `7f8287c9-dc42-4a11-8920-1e61163f0fc1`, Subnet `28fa2c59-e2b6-4396-9397-e5865784be14` | Supplies the matching `10.217.100.0/28` interface |
| AWX | Required Kubernetes credential 3 and tenant-specific NICo credential 6 | Injects NICo IDs and authentication at job launch |

The provider created a NICo Tenant Account for `test-org-demo`, reserved one
simulated Instance Type slot, and allocated a `10.217.100.0/24` IP Block. The
NICo tenant created the VPC and `/28` Subnet from that allocation. The new
Machine-a-Tron host was associated with the pilot Instance Type. The compute
allocation is capacity, not a fixed physical-Machine assignment.

On the OSAC side, the dev-full onboarding script created the fulfillment
Tenant through the private gRPC Tenants API and set up its Keycloak
organization/member and default resources. The tenant user then used the
public CLI to create a matching VirtualNetwork, Subnet, and SecurityGroup:

```bash
TENANT=tenant-demo TENANT_USERS=tenant_demo_user \
  bash osac-installer/scripts/dev-full/provision-tenant.sh osac
osac login --flow password --user tenant_demo_user \
  --password-file /tmp/nico-osac-demo-password --insecure \
  fulfillment-api.osac.localhost:8443
osac create virtualnetwork --name nico-demo-vpc --ipv4-cidr 10.217.100.0/24
osac create subnet --name nico-demo-subnet \
  --virtual-network 01a18561-d316-7a23-916c-5fd8162f42e0 \
  --ipv4-cidr 10.217.100.0/28
osac create securitygroup --name nico-demo-sg \
  --virtual-network 01a18561-d316-7a23-916c-5fd8162f42e0
```

The administrator then added AWX credential 6 and this entry to the
administrator-owned `osac-nico-tenant-bindings` ConfigMap. The ConfigMap
contains IDs only; AWX stores the secret credential values. The operator
reads the projected file before both create and delete, matches the BMI's
`osac.openshift.io/tenant` annotation and `spec.networkAttachments[0].subnetRef`,
and chooses the complete AWX credential list:

```yaml
- osacTenantID: tenant-demo
  osacSubnetID: 01a18561-d62a-732f-82c2-dcf65f861d92
  credentialIDs: [3, 6]
```

Credential 3 is the AWX template's required `kind-cluster` Kubernetes
credential. Credential 6 holds the distinct NICo Tenant, VPC, and Subnet IDs,
the NICo tenant identity, and a provider identity used for Machine cleanup
verification. The AAP role checks the injected OSAC tenant and Subnet IDs
again before making NICo requests. The binding must remain available through
delete; if it is missing, the operator retains the BMI finalizer. See the
[operator contract](../../bare-metal-fulfillment-operator/README.md) for
binding and credential details.

## BMI create and safe delete

The tenant user created one BMI through the public OSAC CLI:

```bash
osac create baremetalinstance --name nico-demo-bmi-20261029020138 \
  --catalog-item nico-mat-pilot \
  --instance-type nico-mat-control-plane --disk-image fedora \
  --network-attachment 'subnet=01a18561-d62a-732f-82c2-dcf65f861d92,interface=data-0,primary,security-groups=01a18562-938c-7351-a188-e30e99b738a4' \
  --ssh-key "$(cat /tmp/nico-osac-demo.pub)"
osac get baremetalinstances nico-demo-bmi-20261029020138
```

OSAC stored the BMI and the BMF operator launched AWX create job 28 with
credential IDs `[3, 6]`. The job created NICo Instance
`06d7bf99-2eef-4ce6-8e57-48888cc22ed7`; NICo placed it on original
Machine `fm100hte4bdne640f6no73t0joo4s3irforbt7cirt2rmjuidhlnpj7kfag`.
The operator saved those provider IDs and IP `10.217.100.2`. The public BMI
reached `RUNNING`, and independent NICo REST reads showed the Instance
`Ready`, on the configured Tenant/VPC/Subnet and Machine. The existing OSAC
`tenant2` identity could not list this BMI.

The tenant then deleted that same BMI:

```bash
osac delete baremetalinstances nico-demo-bmi-20261029020138
osac get baremetalinstances
```

The BMF finalizer held the BMI in `Deleting`. AWX delete job 29 launched with
credential IDs `[3, 6]`; its role checked the saved Instance/Machine IDs and
the BMI UID ownership label before deleting the NICo Instance. It waited for
Instance absence, the original Machine to be unassigned `Ready`, and no
`PreventAllocations` Machine health alert. Job 29 finished `successful` after
about 539 seconds with `nicoCleanupStatus: Released` and the saved IDs. Only
then did OSAC remove the BMI. Independent REST checks returned HTTP 404 for
the Instance and showed the Machine `Ready`, `instanceId: null`, and zero
health reports; the public BMI list was empty.

This proves the simulator control-plane lifecycle and cleanup gate. The pilot
iPXE script exits without installing a guest OS, and the selected OSAC
SecurityGroup is not translated to a NICo policy. The current BMI path replaces
the inline iPXE script with a catalog NICo Operating System and phone-home
readiness, and the binding file is now `version: 2` with an
`operatingSystems` catalog; see the
[approved standalone boot path](nico-bmaas-launchpad-pilot.md#approved-standalone-boot-path). Guest boot and network
traffic isolation remain untested. The lab's NICo provider credential is
shared and broad; a production binding needs a scoped provider identity and
credential rotation procedure.
