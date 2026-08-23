# csi-backends

Deploys vendor CSI controller pods for the OSAC CSI meta-driver. Each vendor
runs as a separate Deployment + Service exposing gRPC on port 50051.

## Prerequisites

Each vendor requires secrets and may require CRDs created **before** installing
the chart.

### Trident (NetApp ONTAP)

Trident requires its CRDs (normally installed by the Trident operator) and a
TLS certificate secret for its internal REST API:

```bash
TMPDIR=$(mktemp -d)
openssl genrsa -out "$TMPDIR/caKey" 2048
openssl req -new -x509 -key "$TMPDIR/caKey" -sha256 -days 3650 -subj '/CN=trident-ca' -out "$TMPDIR/caCert"
openssl genrsa -out "$TMPDIR/serverKey" 2048
openssl req -new -key "$TMPDIR/serverKey" -subj '/CN=trident-csi-controller' -out "$TMPDIR/server.csr"
openssl x509 -req -in "$TMPDIR/server.csr" -CA "$TMPDIR/caCert" -CAkey "$TMPDIR/caKey" -CAcreateserial -out "$TMPDIR/serverCert" -days 3650 -sha256
openssl genrsa -out "$TMPDIR/clientKey" 2048
openssl req -new -key "$TMPDIR/clientKey" -subj '/CN=trident-client' -out "$TMPDIR/client.csr"
openssl x509 -req -in "$TMPDIR/client.csr" -CA "$TMPDIR/caCert" -CAkey "$TMPDIR/caKey" -CAcreateserial -out "$TMPDIR/clientCert" -days 3650 -sha256
openssl rand -hex 16 > "$TMPDIR/aesKey"

kubectl -n osac-csi-backends create secret generic trident-csi \
  --from-file=caKey="$TMPDIR/caKey" \
  --from-file=caCert="$TMPDIR/caCert" \
  --from-file=serverKey="$TMPDIR/serverKey" \
  --from-file=serverCert="$TMPDIR/serverCert" \
  --from-file=clientKey="$TMPDIR/clientKey" \
  --from-file=clientCert="$TMPDIR/clientCert" \
  --from-file=aesKey="$TMPDIR/aesKey"
rm -rf "$TMPDIR"
```

Trident also requires its CRDs installed (normally provided by the Trident
operator). Values:

```yaml
trident:
  enabled: true
  config:
    certsSecret: trident-csi
```

### VAST Data

VAST requires a credentials secret with management API username/password:

```bash
kubectl -n osac-csi-backends create secret generic vast-credentials \
  --from-literal=username='<vms-username>' \
  --from-literal=password='<vms-password>'
```

Values:

```yaml
vast:
  enabled: true
  config:
    plugin: block          # block (raw-block) | csi (NFS/filesystem)
    mode: controller       # controller (controller-only) | controller_and_node
    vmsHost: "<vms-management-ip>"
    vipPoolName: "<vip-pool>"
    credentialsSecret: vast-credentials
```

The VAST driver serves a **single plugin per process**, so this controller
handles one protocol at a time. It defaults to `block` (the current priority).
NFS/filesystem needs its own separate deployment — and object storage likewise
when it lands — rather than reconfiguring this one; those follow OSAC-4262. Use
`mode: controller` for the vendor path: the operator issues only controller
RPCs, so node-side NVMe probes (which require the host FS at `/host`) are
skipped, avoiding the block plugin's CrashLoopBackOff under `controller_and_node`.

### Pure Storage

Pure requires its CRDs (`PureVolume`, `PureSnapshot`, `StorageNodeInitiator`)
and a JSON config secret with FlashArray endpoints and API tokens:

```bash
kubectl -n osac-csi-backends create secret generic px-pure-secret \
  --from-literal=pure.json='{
    "FlashArrays": [
      {"MgmtEndPoint": "<fa-mgmt-ip>", "APIToken": "<api-token>"}
    ],
    "FlashBlades": []
  }'
```

Values:

```yaml
pure:
  enabled: true
  config:
    credentialsSecret: px-pure-secret
    clusterUuid: "<unique-cluster-id>"
    nodePluginNamespace: "<namespace-where-node-plugin-runs>"
```

## Provisioning volumes through a vendor controller

The Deployments this chart creates expose each vendor's **raw CSI gRPC API** on
port 50051. Nothing calls them yet: the OSAC CSI meta-driver's controller plugin
never talks to vendor drivers directly (it delegates to the fulfillment-service
Volume API), and volume-level tier/backend resolution — which picks the vendor
for a given request — does not exist yet.

The future caller is the **osac-operator Volume reconciler**. It reconciles
`Volume` CRs by calling `VendorProvisioner.CreateVolume`. That interface has no
production implementation yet: the reconciler is currently constructed with a
`nil` provisioner and skips provisioning (a mock, `MockVendorProvisioner`, is
used only in tests). Wiring the real vendor CSI client is the follow-up. See
`../../../osac-operator/internal/controller/volume_controller.go`
(the `VendorProvisioner` interface and `VendorCreateVolumeRequest`/`Response`
structs). The real implementation will dial the vendor Deployment created here
(`<vendor>-csi-controller:50051`) and issue a CSI `CreateVolume` RPC.

This section documents the wire contract that implementation must emit, verified
against the VAST controller this chart deploys.

Note the CSI-level fields below (`parameters.*`, `secrets.*`, `fs_type`) are
**not** on today's `VendorCreateVolumeRequest`, which carries only `Name`,
`Backend`, `SizeGiB`, and `AccessMode`. Wiring the real client means either
extending that struct or sourcing these values from the resolved `Backend`/tier
config; the mapping notes below point at the closest existing field where one
exists.

### VAST `CreateVolume` contract (verified)

A CSI `CreateVolume` request that successfully provisions on VAST:

| Field | Value | Notes |
|-------|-------|-------|
| `name` | volume name | maps to `VendorCreateVolumeRequest.Name` |
| `capacity_range.required_bytes` | size in bytes | from `SizeGiB` (convert GiB → bytes) |
| `volume_capabilities[0].mount.fs_type` | **empty string** | NFS view — no filesystem is formatted; NFS is the transport. The driver **rejects** `fs_type=nfs` and only accepts `ext4`/`ext3`/`xfs` when `fs_type` is set at all, so leave it empty |
| `volume_capabilities[0].access_mode.mode` | `MULTI_NODE_MULTI_WRITER` | NFS shared access; maps to `AccessMode` |
| `parameters.root_export` | e.g. `/k8s` | export root under which the view path is created |
| `parameters.view_policy` | **a uniquely-named per-tenant view policy** | see multi-tenancy note below |
| `parameters.vip_pool_name` | e.g. `osac-shared` | VIP pool serving the NFS mount |
| `secrets.username` / `secrets.password` | VMS management credentials | the same credentials held in the `vast-credentials` secret above — passed **per-request** as CSI secrets |
| `secrets.endpoint` | VMS host | **required** in secrets whenever credentials are in secrets; the `X_CSI_VMS_HOST` env fallback does **not** apply to per-request credentials |
| `secrets.tenant` | *(omit)* | only for the tenant-scoped-auth model — see below |

`Backend` in `VendorCreateVolumeRequest` selects **which** vendor Deployment to
dial (it is resolved by fulfillment-service tier resolution before the `Volume`
CR is created); the fields above are what the operator sends **to** that vendor.

### Multi-tenancy: one credential, many VAST tenants

Verified: a **single** VMS credential (a cluster admin) provisions isolated
volumes across different VAST tenants by selecting a **uniquely-named per-tenant
view policy** per request. The VAST driver resolves `view_policy` by name, and
each view policy carries the `tenant_id` that scopes where the resulting view and
quota land. A shared policy name (e.g. `default`) is ambiguous across tenants and
fails with `Too many viewpolicys found`.

Verification run — the same `<vms-username>`/`<vms-password>` credential created:

| Volume | VAST tenant | view | quota |
|--------|-------------|------|-------|
| `csi-rgolan-test-1-vol1` | `rgolan-test-1` (tenant_id 118) | 72 | 36 |
| `csi-rgolan-test-2-vol1` | `rgolan-test-2` (tenant_id 119) | 73 | 37 |

**Prerequisite for each tenant:** create a uniquely-named VMS view policy bound
to that tenant's `tenant_id` before provisioning volumes for it. This is the
mapping the operator (or an out-of-band tenant-onboarding step) must establish
so that a per-tenant `view_policy` name exists to pass in `parameters`.

There is an **alternative** VAST model: setting the `tenant` CSI secret sends an
`X-Tenant-Name` header for tenant-scoped authentication. That path requires
**tenant-admin** credentials (a cluster-admin credential is rejected with
`Authentication failure` at `/api/v1/token/`), so it is *not* used by the
cluster-admin + per-tenant-view-policy model above. Pick one model; do not mix.

## Registry authentication

If vendor images require private registry access (e.g. `registry.connect.redhat.com`
for Trident), create a pull secret and reference it:

```bash
kubectl -n osac-csi-backends create secret docker-registry pull-secret \
  --docker-server=registry.connect.redhat.com \
  --docker-username='<token-name>' \
  --docker-password='<token-value>'
```

```yaml
imagePullSecrets:
  - name: pull-secret
```
