# Manual NetApp registration and onboarding validation

The preview uses a shared, administrator-managed TridentBackendConfig (TBC).
Run each administrator action yourself and inspect its checkpoint. Examples use
fictional names; use approved values and your authenticated private OSAC session.

## What this walkthrough can prove

| Checkpoint | Requirement / current limit |
|---|---|
| Backend/tier registration | Deploy the matching API and CLI; READY means accepted configuration |
| Operator → AAP payload | Matching operator plus a reviewed capture-only playbook can verify references/selectors without native writes |
| Native TBC observation and tenant classes | Requires Will's matching shared-profile AAP receiver |
| Encrypted FC volume, VM disk I/O and tenant isolation | Requires authorized native qualification on FC-connected hosting workers |

The edge-17 capture run uses current production operator packages, real Kubernetes
and AAP, and explicit catalog fixtures. It verifies the sender boundary, not a
complete administrator-to-native journey. Existing lab deployments are not upgraded
by that harness. Native qualification must be authorized separately.

## 1. Confirm infrastructure preparation

Record API/operator/AAP revisions and the selected VM hosting cluster. The hub
runs OSAC; native Trident, the customer TBC and tenant StorageClasses belong on
the configured hosting target. That target can be the hub or one dedicated cluster.

The storage administrator prepares the SVM, FC access, Trident credentials and
server TLS trust, then creates the shared TBC and its labelled virtual pools.
For example, `trident/customer-fc` offers `performance=extreme` and
`performance=value`. The pool policies and encryption defaults are configured
and qualified by the administrator. OSAC does not perform these array operations.

If native access is authorized, inspect only status using the hosting context:

```bash
oc --context <hosting-context> -n trident get tbc customer-fc \
  -o jsonpath='{.status.phase}{"\t"}{.status.lastOperationStatus}{"\t"}{.status.backendInfo.backendName}{"\n"}'
```

Expected native preparation is Bound/Success. This is not proof of FC disk I/O.
For capture-only testing, an explicit fictional reference is sufficient; do not
create or modify a TBC merely to exercise payload delivery.

## 2. Register backend and tiers

Follow the [registration guide](netapp-backend-tiers.md) to create the OSAC VALUE
password Secret and submit the [backend example](examples/netapp-backend.yaml).
Keep the required compatibility endpoint/credentials; shared onboarding does not
use them. Record the backend ID and READY state.

Submit the [tier example](examples/netapp-tier.yaml) with that ID. Optionally
create a second tier selecting `performance: value`. Both associations use
`profile: shared-fc`; neither sets a numeric IOPS cap. The selected prepared pool
must match each tier's advertised encryption setting.

**Checkpoint:** private Get/List retain the profile's TBC namespace/name and each
tier's selector. No array access, TBC or StorageClass creation occurs during
registration. A valid unavailable HTTPS endpoint is accepted intentionally.

## 3. Create the tenant

Use the normal OSAC Tenant flow, for example:

```yaml
'@type': type.googleapis.com/osac.private.v1.Tenant
metadata:
  name: netapp-manual
spec: {}
```

```bash
osac create -f netapp-test-tenant.yaml
osac get osac.private.v1.Tenant netapp-manual -o json
```

Store any one-time credentials returned by Tenant creation securely. With the
shared profile, **do not create a per-tenant preparation Secret**. The backend's
TBC reference and each tier's pool selector are the storage inputs.

Inspect the Tenant CR on the hub:

```bash
oc --context <hub-context> -n <operator-namespace> \
  get tenants.osac.openshift.io netapp-manual \
  -o jsonpath='{.status.phase}{"\n"}{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

**Checkpoint:** Tenant identity onboarding reaches Ready independently. Storage
conditions remain false until the current backend and class stages succeed.

## 4. Verify delivery, then stop or qualify native behavior

For a data-passing test, route all four storage actions to a reviewed capture-only
playbook before enabling reconciliation. The playbook must assert:

- One ID-keyed connection contains `provider_config.profiles` and the correct
  TBC namespace/name, with no endpoint, username, password or tenant preparation.
- Each tier carries its `provider_config.profile` and literal `pool_selector`.
- No ONTAP request, native Secret, TBC or StorageClass is created.

An intentional failure after successful capture keeps storage not ready. Record
sanitized evidence and pause further retries. Stop here if native changes are
not authorized.

With a compatible receiver and native qualification authorized, AAP observes the
existing TBC through the hosting Kubernetes API, then creates only tenant classes.
Successful current-configuration jobs plus observed classes make storage ready.
Verify the classes select the bound native backend and intended pools. Only then
exercise shared VM provisioning, read/write persistence and isolation, including
encryption readback through the administrator's approved procedure.

## 5. Verify guarded offboarding separately

Remove workload/data dependencies through the shared lifecycle first. Offboarding
must remove only that tenant's OSAC-owned classes/configuration and retain its
finalizer on cleanup failure. The shared TBC, native credentials, SVM, virtual
pools and other tenants' classes/disks must remain. Capture-only testing does not
prove native cleanup or disk isolation.

Legacy ONTAP backends without profiles retain the earlier preparation-Secret
workflow; that is a separate compatibility path, not this shared preview procedure.
