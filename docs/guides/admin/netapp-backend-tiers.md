# NetApp backend and tier registration

Platform administrators register ONTAP through the private StorageBackends API,
then create StorageTiers referencing the returned backend ID. Registration
checks management access and stores the catalog configuration. Tenant SVM
preparation, onboarding, native Trident setup and FC disk access are separate
steps; a READY backend does not certify those prerequisites.

## Prerequisites

- A cluster management HTTPS endpoint reachable from Fulfillment Service.
  Use its certificate's hostname or a matching IP SAN. An SVM management
  endpoint used for runtime provisioning is a separate connection.
- A discovery account allowed to read cluster version, SVMs, IP and FC
  interfaces, QoS policies, volumes and LUNs through the ONTAP REST API.
- The approved issuing CA chain installed in the Fulfillment process's HTTPS
  trust store. For a private CA, mount an approved PEM bundle and configure
  Go's `SSL_CERT_FILE` or `SSL_CERT_DIR` environment setting. Preserve the
  required existing trust roots. Backend registration has no certificate
  bypass or certificate-pin field.
- The discovery password, either stored in a Fulfillment Secret that the
  registering administrator can read, or supplied inline in the request.

The cluster endpoint/account provides discovery only. It does not assign a
tenant SVM or provide that SVM's runtime credentials.

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

Before saving READY, the service performs these authenticated GETs:

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

Credential updates probe the stored configuration after applying the update
mask. Failed validation/probing leaves the saved object and version unchanged.
Discovery runs before taking the backend update lock. If another request changes
the backend during discovery, the update returns `Aborted`; retry with fresh state.
Description-only and no-op updates do not perform another probe. To switch
from inline password to a reference, explicitly include both
`spec.credentials.password` and `spec.credentials.password_secret` in the
protobuf field mask, clearing the old source. Parent masks, such as
`spec.credentials`, replace that whole subtree. An empty mask changes nothing;
an absent mask supplies a complete replacement object.

| Failure | gRPC code | Administrator action |
|---|---|---|
| Invalid endpoint/credentials or HTTP 401 | InvalidArgument | Correct endpoint or discovery credentials |
| HTTP 403, unsupported discovery path, invalid/oversized response | FailedPrecondition | Check required read permissions and REST API availability |
| TLS/connectivity failure, redirect, server failure | Unavailable | Check routing, approved CA trust, certificate identity/validity and array health |
| Probe deadline | DeadlineExceeded | Check endpoint responsiveness and retry |
| Caller cancellation | Canceled | Retry if still needed |
| Protected Secret read denied | PermissionDenied | Use an authorized administrator and Secret |

Errors identify the discovery path/status where available and omit passwords,
Authorization headers and array response bodies.

## Read-only lab validation

With the candidate Fulfillment service deployed and approved CA trust set,
create an isolated discovery VALUE Secret, register one backend and one tier,
then Get/List them to confirm persistence. Test a rejected credential update
and confirm the original backend/version remains unchanged. Delete temporary
catalog records in dependency order when finished. All ONTAP calls made by
registration are GET-only. This verifies API-to-array discovery; FC fabric,
tenant onboarding and VM disk I/O need their own acceptance tests.
