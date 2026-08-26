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
  key in the volume context set at creation time.

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
| `--node-id` | (required) | Node ID for NodeGetInfo |
| `--driver-name` | `csi.osac.openshift.io` | CSI driver name |
| `--fulfillment-endpoint` | (empty, uses stub) | gRPC endpoint for the OSAC fulfillment service |
| `--fulfillment-client-id` | (empty) | OAuth2 client ID for fulfillment-service authentication |
| `--fulfillment-client-secret-file` | (empty) | Path to file containing the OAuth2 client secret |
| `--fulfillment-issuer-url` | (empty) | Keycloak issuer URL for `client_credentials` token exchange |
| `--grpc-insecure` | `false` | Skip TLS server certificate verification |
| `--vendor-sockets` | (empty) | Comma-separated `backend=socketpath` pairs |

## License

Apache License 2.0. See [LICENSE](LICENSE).
