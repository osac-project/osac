# Manual NetApp registration and onboarding validation

Run the administrator actions yourself from the lab access host. Pause after
each checkpoint and inspect the result before continuing. Examples use fictional
names and addresses; use your approved lab values and CLI login.

## Working profile and current limits

The working preview assumption is one cluster management endpoint with separate
discovery and tenant/backend accounts. `StorageBackend` supplies the endpoint
and discovery account. A labelled Kubernetes preparation Secret supplies the
Trident account and explicit SVM name. Tiers on the same backend share that
preparation. AAP creates the native Trident credentials Secret, backend
configuration and StorageClasses on the VM hosting cluster.

| Stage | Current readiness |
|---|---|
| Register backend and tier; inspect ERROR/READY; save unchanged to retry | Implemented locally; deploy the matching service/CLI and CA configuration before testing |
| Create Tenant through OSAC | Existing API; requires the lab's normal identity, infrastructure and operator setup |
| Wait for preparation, select it by tenant/backend labels, pass it to AAP | Implemented locally; deploy the candidate operator to test waiting/selection |
| Consume explicit prepared SVM and operator-provided credentials | Receiving implementation in PR #1580; deploy a matching revision before native qualification |
| Verify native Trident readiness, trust, FC disk use and cleanup | Joint deployed qualification; registration success does not establish these |

Submitting the Tenant request and creating the preparation Secret do not depend
on the NetApp AAP role. The normal tenant onboarding workflow must still reach
Ready; its existing AAP dependencies remain. With a Ready tenant and the
candidate operator, you can test preparation waiting/selection without a NetApp
provisioning provider; a valid preparation then reports that none is configured.
The NetApp AAP role turns those inputs into native Trident resources and
StorageClasses. Deploy a matching receiver before that final checkpoint.
For a data-passing test, route all four storage actions to a reviewed capture-only
AAP playbook before creating inputs or resuming retries. That playbook must stop
before ONTAP requests or native provisioning. An intentionally failed capture
job leaves storage not ready; it does not establish native readiness.

## 1. Confirm the environment

Record the deployed Fulfillment/operator image revisions and AAP collection
revision. The local status/trust changes must be included. Establish your
authenticated private `osac` CLI session and separate Kubernetes contexts for
the operator/hub and VM hosting cluster. The source preparation Secret belongs in `osac-storage` on the hub. The installer
creates this protected namespace; it is independent of the target `trident`
namespace. Verify that the deployed operator can list Secrets there.

Confirm these inputs without printing passwords:

- Updated cluster discovery login, and a cluster endpoint reachable from
  Fulfillment. The old lab diagnostic Secret is not a tenant preparation Secret
  or an OSAC Secrets Service record.
- Approved NetApp CA chain included in Fulfillment's configured trust bundle.
  AAP and Trident need their own verified trust delivery before onboarding.
- An administrator-prepared SVM and an account with the permissions Trident
  needs, usable through the same cluster endpoint. A different account name
  alone does not prove restriction to that SVM.
- A functioning Trident installation on the VM hosting cluster. FC fabric and
  connected worker prerequisites are needed for disk-I/O qualification.

ONTAP infrastructure preparation belongs to the storage administrator. The
registration checks issue GETs only; native onboarding and volume tests require
the agreed prepared resources and permission to perform their operations.

## 2. Register the backend

Follow [backend registration](netapp-backend-tiers.md#register-a-backend) to
create the discovery password's OSAC VALUE Secret, then submit the
[backend example](examples/netapp-backend.yaml):

```bash
osac create -f netapp-backend.yaml
osac get storagebackend netapp-primary -o json
```

Record the returned backend ID. Expect READY and no failure message when the
seven management checks succeed. ERROR means the object was saved but the
connection check failed; inspect `status.message`.

The API also accepts an inline discovery password. Use protected file/stdin
input if choosing that path; keep passwords out of command arguments. A password
reference requires a working OSAC Secrets Service. A Kubernetes Secret name is
not an OSAC Secret ID.

**Checkpoint:** backend ID, state and sanitized message. Keep credentials private.

## 3. Register tiers

Set the returned backend ID in the
[tier example](examples/netapp-tier.yaml). For the local AAP adapter, remove
`ontap.maxIops` (or set it to `0`) and set `encryptionEnabled: false` before
registering the tier. The adapter rejects caps and encryption requirements
until their receiving mappings are implemented. Registration itself does not
validate native capacity or policies.

```bash
osac create -f netapp-tier.yaml
osac get osac.private.v1.StorageTier netapp-block-5000 -o json
```

Optionally register a second tier on the same backend to test shared preparation.

**Checkpoint:** both private tier definitions retain their backend, IOPS and
encryption settings. No SVM, TBC or StorageClass is created by these requests.

## 4. Create the tenant and observe storage waiting

After the candidate operator is deployed, create `netapp-test-tenant.yaml`:

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

Tenant Create prints one-time break-glass credentials; store them securely.
Use the Tenant resource name (`netapp-manual`) for preparation labels. OSAC
enforces Tenant ID == resource name; the Kubernetes Tenant CR UID is different. Tenant creation starts the normal onboarding workflow;
there is no separate NetApp start command.

Use your hub context and operator namespace to inspect the Tenant CR:

```bash
oc --context <hub-context> -n <operator-namespace> \
  get tenants.osac.openshift.io netapp-manual \
  -o jsonpath='{.status.phase}{"\n"}{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

**Checkpoint:** the normal Tenant workflow reaches Ready. Its storage conditions
remain not ready, explaining missing preparation, and no NetApp provisioning job
starts until the matching preparation is available. Tenant Ready by itself does
not mean storage is ready. This waiting checkpoint is implemented in the candidate operator.

## 5. Create the preparation Secret

The input shape for this profile is:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: netapp-manual-primary-info
  namespace: osac-storage
  labels:
    osac.openshift.io/storage-backend: netapp-primary
    osac.openshift.io/tenant: netapp-manual
type: Opaque
stringData:
  username: trident_netapp_manual
  password: <tenant-account-password>
  svm: prepared_customer_svm
```

There is no `management_endpoint` or per-tenant CA field in this working profile.
The endpoint comes from the backend; trust comes from deployment configuration.
Use the actual prepared SVM name; no computed naming formula is required.
The backend label uses its registered **name**, not its ID. The Secret name is
admin-selected. Only the two illustrated labels are required. Admins can create
it before the Tenant using the intended Tenant resource name; storage still
waits for Tenant Ready. Restrict namespace access to trusted admins/operator.

To create it without putting the password in a file, command argument or shell
history, edit the non-secret placeholders below and run this in your terminal:

```bash
python3 - <<'PY'
import getpass
import json
import subprocess

password = getpass.getpass("Tenant ONTAP account password: ")
if not password:
    raise SystemExit("No password supplied; nothing created")
secret = {
    "apiVersion": "v1",
    "kind": "Secret",
    "metadata": {
        "name": "netapp-manual-primary-info",
        "namespace": "osac-storage",
        "labels": {
            "osac.openshift.io/storage-backend": "netapp-primary",
            "osac.openshift.io/tenant": "netapp-manual",
        },
    },
    "type": "Opaque",
    "stringData": {
        "username": "trident_netapp_manual",
        "password": password,
        "svm": "prepared_customer_svm",
    },
}
subprocess.run(
    ["oc", "--context", "<hub-context>", "create", "-f", "-"],
    input=json.dumps(secret), text=True, check=True,
)
PY
```

**Checkpoint:** the operator finds exactly one preparation for the
tenant/backend names and continues without a manual Tenant status edit. Duplicate,
incomplete or mismatched preparations must keep storage not ready with a useful
reason. These selection/retry cases are implemented in the candidate operator.

## 6. Observe the result

For capture-only testing, check sanitized capture evidence: source namespace,
name/UID, backend ID, tier names and private value-comparison results. Expect zero
ONTAP requests, zero Trident creations and storage conditions still false. Pause
retries after capture. Stop here when native provisioning is not authorized.

For an authorized native test with compatible AAP/Trident setup, inspect conditions using the command from step 4. On the actual VM hosting
cluster, inspect only runtime metadata/status:

```bash
oc --context <hosting-context> -n trident \
  get tridentbackendconfigs.trident.netapp.io \
  -l osac.openshift.io/tenant=netapp-manual \
  -o custom-columns=NAME:.metadata.name,PHASE:.status.phase,RESULT:.status.lastOperationStatus,MESSAGE:.status.message
oc --context <hosting-context> get storageclasses \
  -l osac.openshift.io/tenant=netapp-manual
```

**Checkpoint:** the TBC uses the backend's cluster address, the explicit prepared
SVM, the tenant account, FC mode and verified TLS. It becomes Bound with a
successful operation status; all expected tier classes appear and current
storage conditions become true. Source Secret presence alone is insufficient.

A real volume/VM disk test is a later checkpoint shared with the VM consumption
workstream. Record its hosting cluster, driver version, FC path and result;
metadata readiness is not evidence of working FC I/O or tenant disk isolation.

## 7. Recovery and cleanup

For a backend connection failure, fix the external issue and save the same
backend with `osac edit storagebackend netapp-primary`, retaining its fields.
Expect READY and a cleared message after a successful recheck.

For missing preparation, observe waiting, create the Secret and verify automatic
continuation. The operator uses its existing job retry/backoff for failed NetApp
setup and native configuration jobs. A later retry reads a fresh preparation
snapshot; correcting credentials during recovery differs from rotating a
successfully configured backend. Automatic credential rotation is deferred.

Offboarding qualification must verify owned runtime resources are removed in
dependency order while administrator-prepared SVMs/accounts/source Secrets are
retained. Remove temporary catalog records after their dependencies are cleared.
Keep credentials, Secret data, tokens and complete AAP extra variables out of
shared test evidence; record revisions, IDs, conditions and sanitized errors.
