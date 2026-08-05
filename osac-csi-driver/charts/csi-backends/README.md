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
    vmsHost: "<vms-management-ip>"
    vipPoolName: "<vip-pool>"
    credentialsSecret: vast-credentials
```

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
