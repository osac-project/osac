# OSAC CSI Driver

> [!WARNING]
> Be mindful of the content you commit to this repository. Do not commit any
> material containing Red Hat confidential content, including information about
> future product development plans.

The OSAC CSI driver is an aggregating meta-driver that presents a single CSI
identity (`csi.osac.openshift.io`) to Kubernetes and routes storage requests to
vendor-specific CSI drivers (NetApp Trident, VAST, Pure Storage) based on
storage tier resolution from the OSAC fulfillment service.

## Architecture

The driver runs in two modes controlled by Kubernetes deployment topology:

- **Controller plugin** (Deployment): Handles `CreateVolume`, `DeleteVolume`,
  `ControllerPublishVolume`, and `ControllerUnpublishVolume` by resolving the
  storage tier via the fulfillment service and proxying to the appropriate
  vendor CSI controller.

- **Node plugin** (DaemonSet): Handles `NodeStageVolume`, `NodePublishVolume`,
  and related RPCs by routing to vendor node plugins based on the `osac.backend`
  and `osac.protocol` keys in the volume context set at creation time. Each
  volume's route is kept under `--node-state-dir`, so unstage and unpublish
  still reach the vendor after the node plugin restarts.

Vendor socket and controller maps are keyed `<backend>/<protocol>` (for example
`vast/nfs`), with a bare `<backend>` key serving every protocol that has no
protocol-specific entry.

## Build

```bash
make build        # Build the binary
make test         # Run tests
make lint         # Run golangci-lint
make image-build  # Build container image
make image-push   # Push container image
```

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--csi-endpoint` | `unix:///csi/osac/csi.sock` | CSI endpoint this driver listens on |
| `--node-id` | (required) | Process startup node ID; NodeGetInfo uses `NODE_NAME` |
| `--driver-name` | `csi.osac.openshift.io` | CSI driver name |
| `--fulfillment-endpoint` | (empty, uses stub) | gRPC endpoint for the OSAC fulfillment service |
| `--fulfillment-client-id` | (empty) | OAuth2 client ID for fulfillment-service authentication |
| `--fulfillment-client-secret-file` | (empty) | Path to file containing the OAuth2 client secret |
| `--fulfillment-issuer-url` | (empty) | Keycloak issuer URL for `client_credentials` token exchange |
| `--fulfillment-ca-file` | (empty) | PEM CA bundle for verified fulfillment-service and OAuth TLS connections |
| `--grpc-insecure` | `false` | Skip TLS server certificate verification |
| `--vendor-sockets` | (empty) | Comma-separated `backend=socketpath` pairs; keys may be `backend/protocol` |
| `--vendor-controllers` | (empty) | Comma-separated `backend=endpoint` pairs for attach and detach; keys may be `backend/protocol`, and `none` makes attach a no-op |
| `--node-state-dir` | (empty, routes kept in memory) | Directory where the node plugin keeps each volume's route across restarts |
| `--node-only` | `false` | Serve only the Identity and Node services; no fulfillment configuration is needed |

The node DaemonSet supplies `NODE_NAME` from the Kubernetes downward API. The
LVMS socket is routed through the existing vendor socket map and defaults to
`/run/topolvm/csi-topolvm.sock`. An explicit `lvms` entry in `--vendor-sockets`
replaces that default; `OSAC_LVMS_NODE_SOCKET` takes precedence over both.
The chart leaves `node.lvmsNodeSocket` empty by default, so it preserves
`node.vendorSockets`. Set `node.lvmsNodeSocket` to supply an explicit environment
override. For custom paths, also align `node.lvmsNodeSocketDir` and
`node.lvmsNodeSocketHostPath` so the socket is accessible inside the container.

### Disabling fulfillment trust or rolling back

The trust admission webhook uses `failurePolicy: Fail` and rejects updates that
remove the `osac.openshift.io/fulfillment-trust-client` label while the webhook
is active. Before changing `global.fulfillmentTrust.enabled` from `true` to
`false`, or rolling back to a chart version without trust admission, remove the
CSI release's `ValidatingWebhookConfiguration` first. Find it with the CSI
release label and delete the `*-fulfillment-trust` resource:

```bash
CSI_RELEASE=your-csi-release
kubectl get validatingwebhookconfigurations \
  -l "app.kubernetes.io/instance=${CSI_RELEASE}" \
  -o custom-columns=NAME:.metadata.name
TRUST_WEBHOOK_NAME=copy-name-ending-in-fulfillment-trust-from-output
kubectl delete validatingwebhookconfiguration "$TRUST_WEBHOOK_NAME"
```

Delete only the `*-fulfillment-trust` resource shown for that release. Its name
uses the chart fullname plus `-fulfillment-trust`; the fullname defaults to
`<release>-csi-driver` unless the release name already contains `csi-driver` or
`fullnameOverride` is set. After deleting the webhook, upgrade or roll back the
CSI release normally.

## Troubleshooting

### An NFS PVC never mounts

Check the pod's events (`kubectl describe pod`) for the mount error.

- An `Unavailable` error naming a VAST VIP pool ("VAST VIP pool `<fqdn>` is not
  reachable on TCP 2049 from node `<node>`") means the worker node cannot reach
  the VAST VIP on the NFS port. Check the tenant network's routing to the VIP
  pool, and that DNS resolves the pool FQDN. When the volume uses a random FQDN
  prefix, DNS must resolve the wildcard `*.<fqdn>`.
- A vendor `target is busy` error from an older driver version has the same
  cause. The vendor's mount timed out and it could not remove the placeholder
  tmpfs it mounted first. On the worker node, look for a leftover tmpfs at the
  pod's volume path (`findmnt -t tmpfs | grep <pod-uid>`) and fix the network
  or DNS issue above.

## License

Apache License 2.0. See [LICENSE](LICENSE).
