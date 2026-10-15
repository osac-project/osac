# NetApp backend and tier registration

Platform administrators register ONTAP through the private StorageBackends API,
then create StorageTiers referencing the returned backend ID. Registration
checks management access and stores the catalog configuration. Tenant SVM
preparation, onboarding, native Trident setup and FC disk access are separate
steps; a READY backend does not certify those prerequisites.

## Prerequisites

- A cluster management HTTPS endpoint reachable from Fulfillment Service.
  Use its certificate's hostname or a matching IP SAN. The address used by
  Trident must also match its runtime account's scope. The working MVP profile
  reuses this address with a separate account supplied per tenant/backend;
  see the onboarding guide.
- A discovery account allowed to read cluster version, SVMs, IP and FC
  interfaces, QoS policies, volumes and LUNs through the ONTAP REST API.
- The approved issuing CA chain in Fulfillment's configured CA bundle. A CA
  bundle is a PEM file containing certificates the service trusts. The service
  chart's `certs.caBundle.configMap` selects the ConfigMap mounted through
  `--ca-file`; the umbrella chart exposes this as
  `service.certs.caBundle.configMap`. Include the NetApp CA while
  retaining existing certificates, using the installation's supported bundle
  source. Do not edit a trust-manager-generated ConfigMap directly. Restart the
  service after a trust change. Backend registration has no certificate bypass
  or certificate-pin field. AAP and Trident need their own trust configuration;
  mounting a bundle in Fulfillment does not configure either of them.
- The discovery password, either stored in a Fulfillment Secret that the
  registering administrator can read, or supplied inline in the request.

The registered account is used for discovery. The same endpoint is reused for
Trident in the working MVP profile, with separate credentials from the tenant
preparation Secret. Backend registration does not assign a tenant SVM.

For a staged administrator walkthrough, including the hub preparation Secret
and separate native onboarding qualification, see
[manual NetApp validation](netapp-manual-validation.md).

## Register a backend

### 1. Prepare the password

For the Secret reference used below, first create a Secret through the
Fulfillment Secrets API with type `SECRET_TYPE_VALUE`. This type stores a
single value: put the discovery password in its `data` map under the key
`value`. The password must be non-empty. In ProtoJSON, this binary value is
base64-encoded; base64 is an encoding, not encryption.

Take the returned Fulfillment Secret ID and use it as `passwordSecret.id`
in the backend request. The administrator registering the backend must be
allowed to read that Secret; reading shared/system Secrets requires the
Secrets API's platform administrator permissions.

A Kubernetes Secret is a different resource. Creating a Kubernetes Secret
for a lab connection test does not create a Fulfillment Secret, and its
Kubernetes name cannot be used as `passwordSecret.id`.

```text
Discovery password -> Fulfillment VALUE Secret -> returned Secret ID
                                               -> credentials.passwordSecret.id
```

Alternatively, use `"password": "<discovery-password>"` in `credentials`
instead of `passwordSecret`. Supply exactly one password source.

The existing CLI can read the password from a protected file or stdin, keeping
it out of command arguments. After logging in to the private API, for example:

```bash
osac --tenant shared create secret --name netapp-discovery --type value \
  --from-file=value=- < /path/to/protected/discovery-password
```

The input must contain the exact password bytes; avoid an unintended trailing
newline. Record the returned Fulfillment Secret ID.

### 2. Submit the backend request

Example ProtoJSON request for `osac.private.v1.StorageBackends/Create`:

```json
{
  "object": {
    "metadata": {"name": "netapp-primary"},
    "spec": {
      "provider": "ontap",
      "endpoint": "https://cluster-mgmt.example.com",
      "credentials": {
        "username": "osac-discovery",
        "passwordSecret": {"id": "discovery-value-secret-id"}
      }
    }
  }
}
```

Replace `discovery-value-secret-id` with the Fulfillment Secret ID from step 1.

For `osac create -f`, use the downloadable
[backend YAML example](examples/netapp-backend.yaml). It contains the object's
`@type` and fields directly; omit the RPC request's `object` wrapper:

```bash
osac create -f netapp-backend.yaml
osac get storagebackend netapp-primary -o json
```

`password` and `passwordSecret` are mutually exclusive. Secret references are
resolved to canonical ID/name; the resolved password is not copied into the
backend record.

The service performs these authenticated GETs and saves the backend as READY
on success, or ERROR with a diagnostic message on a failed connection check:

| Resource | REST path |
|---|---|
| Cluster version | `/api/cluster?fields=version` |
| SVMs | `/api/svm/svms` |
| IP/management LIFs | `/api/network/ip/interfaces` |
| FC LIFs | `/api/network/fc/interfaces` |
| QoS policies | `/api/storage/qos/policies` |
| Volumes | `/api/storage/volumes` |
| LUNs | `/api/storage/luns` |

Collection queries request UUID only, at most one record and a two-second
server return timeout. Empty collections are valid. The complete probe has
a ten-second budget and a 1 MiB response limit per request; a shorter caller
deadline takes precedence. Redirects are rejected. No array writes occur.
See the [ONTAP cluster API](https://docs.netapp.com/us-en/ontap-restapi-9171/get-cluster.html)
and [LUN API](https://docs.netapp.com/us-en/ontap-restapi-9171/get-storage-luns.html).

## Create a tier

Example request for `osac.private.v1.StorageTiers/Create`, using the backend ID
returned above:

```json
{
  "object": {
    "metadata": {"name": "netapp-block-5000"},
    "spec": {
      "description": "NetApp block tier with a per-volume IOPS ceiling",
      "protocol": "STORAGE_PROTOCOL_BLOCK",
      "backends": [{
        "backendId": "registered-backend-id",
        "encryptionEnabled": true,
        "ontap": {"maxIops": "5000"}
      }]
    }
  }
}
```

ONTAP tiers require one backend and BLOCK protocol. Generic read/write
bandwidth limits must be zero; ONTAP uses the typed `ontap.maxIops` field.
The private proto's `provider_qos` oneof selects this field; it does not add
a `providerQos` wrapper to JSON. ProtoJSON represents the int64 ceiling as
a decimal string. The accepted range is 0–2147483647; absent/zero adds no
OSAC tier IOPS cap and promises no minimum performance. An ONTAP QoS branch
on a different provider is rejected.

The cap and encryption boolean describe the requested tier configuration.
Registration does not create or validate native QoS/encryption resources;
onboarding must validate the prepared SVM's configuration before exposing it.
The public tenant tier API omits backend associations and provider details.

The [tier YAML example](examples/netapp-tier.yaml) uses the same CLI input format:

```bash
osac create -f netapp-tier.yaml
osac get osac.private.v1.StorageTier netapp-block-5000 -o json
```

The fully qualified type selects the generic private API command, which shows
the association settings. The dedicated `osac get storagetier` command displays
the public tier view and does not accept `-o json`.

See the [onboarding interface contract](../developer/netapp-onboarding-contract.md)
for the operator/AAP payload and the separate prepared-SVM credential handoff.

## Updates and failures

Descriptions remain editable. Provider is immutable; an ONTAP backend endpoint
change requires a replacement backend. ONTAP tier backend/protocol/QoS/encryption
changes require a replacement tier. Other providers retain their existing
registration behavior and do not receive the ONTAP probe requirement.

Every ONTAP Update checks the effective configuration after applying the update
mask, including description-only and unchanged updates. A failed connection
check saves the submitted configuration with ERROR and `status.message`; a
successful check sets READY and clears the old message. The service does not
retry in the background.

After fixing a network, certificate or array issue, submit an Update to the
same backend. No field needs to change and no delete/recreate is required:

```bash
osac edit storagebackend netapp-primary
# Save and close the editor, keeping the existing fields.
osac get storagebackend netapp-primary -o json
```

Inspect `status.state` and `status.message` in the response. Saving a referenced
password again rereads the protected Secret, so a password changed there is
checked without changing the reference. The endpoint is always the backend's
`spec.endpoint`, not an address taken from tenant credentials.

Malformed requests, invalid Secret references and unauthorized Secret reads
still fail the API request without saving it. Discovery runs before taking the
backend update lock. If another request changes the backend during discovery,
the update returns `Aborted`; retry with fresh state. To switch
from inline password to a reference, explicitly include both
`spec.credentials.password` and `spec.credentials.password_secret` in the
protobuf field mask, clearing the old source. Parent masks, such as
`spec.credentials`, replace that whole subtree. An empty mask preserves the
specification and reruns the check; an absent mask supplies a complete replacement
object.

| Failure | Result | Administrator action |
|---|---|---|
| Malformed endpoint, missing credential fields or invalid password Secret | API error; not saved | Correct the request or Secret |
| HTTP 401 | Saved as ERROR | Correct discovery credentials and save again |
| HTTP 403, unsupported discovery path, invalid/oversized response | Saved as ERROR | Check read permissions and REST API availability, then save again |
| TLS/connectivity failure, redirect, server failure | Saved as ERROR | Fix routing, approved CA trust, certificate identity/validity or array health; save again |
| Probe deadline | Saved as ERROR | Check responsiveness and save again |
| Caller cancellation/deadline | Canceled/DeadlineExceeded; not saved | Retry if still needed |
| Protected Secret read denied | PermissionDenied; not saved | Use an authorized administrator and Secret |

Errors identify the discovery path/status where available and omit passwords,
Authorization headers and array response bodies.

## Read-only lab validation

With the candidate Fulfillment service deployed and approved CA trust set,
create an isolated discovery VALUE Secret, register one backend and one tier,
then Get/List them to confirm persistence. Test a bad discovery password and
confirm the backend remains saved with ERROR. Restore the password and save
again; verify READY and a cleared message. Delete temporary
catalog records in dependency order when finished. All ONTAP calls made by
registration are GET-only. This verifies API-to-array discovery; FC fabric,
tenant onboarding and VM disk I/O need their own acceptance tests.
