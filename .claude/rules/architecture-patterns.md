# Architecture Patterns

## Multi-tenancy

Tenant-scoped resources include tenant isolation metadata:
- `metadata.annotations["osac.openshift.io/tenant"]` for tenant scoping
- `metadata.annotations["osac.openshift.io/owner-reference"]` for resource hierarchy
- OPA policies enforce isolation at runtime
- Never skip tenant isolation metadata in new tenant-scoped resources
- Use annotations for owner references, not separate fields
- Provider-defined resources (NetworkClass, ExternalIPPool) are exempt — tenants do not interact with them directly

## Resource Hierarchy

```text
Cluster Resources:
  ClusterOrder → provisions OpenShift clusters via Hosted Control Planes

Compute Resources:
  ComputeInstance → KubeVirt VM, attached to Subnets + SecurityGroups
  DiskImage → disk image source (registry, URL)

Networking Resources:
  NetworkClass (platform-defined, internal)
  └── VirtualNetwork (tenant L2 network with CIDR)
        ├── Subnet (CIDR range within VirtualNetwork)
        ├── SecurityGroup (firewall rules scoped to VirtualNetwork)
        └── NATGateway (SNATs egress traffic through an ExternalIP)

External IP Resources:
  ExternalIPPool (platform-defined, external IP ranges)
  ├── ExternalIP (allocated from pool)
  └── ExternalIPAttachment (binds ExternalIP to ComputeInstance)

Tenant Resources:
  Tenant → namespace and resource isolation
```

Parent-child relationships use owner reference annotations (`osac.openshift.io/owner-reference`).

## Service Stack (fulfillment-service)

- PostgreSQL for persistent storage
- gRPC with grpc-gateway for REST/JSON support
- Controller-runtime for Kubernetes integration
- OPA for authorization policies
- Prometheus for metrics

## Integration Testing (fulfillment-service)

- Runs against a Kind cluster (named "osac-dev"), created via
  `make -C osac-installer install-infra PLATFORM=kind PROFILE=dev NS=osac`
- TLS with SNI routing via Envoy Gateway
- Keycloak for authentication
- Requires `/etc/hosts` entries:
  - `127.0.0.1 keycloak.keycloak.svc.cluster.local`
  - `127.0.0.1 fulfillment-api.osac.svc.cluster.local`
  - `127.0.0.1 fulfillment-internal-api.osac.svc.cluster.local`
- Clean up with: `make -C osac-installer uninstall PLATFORM=kind PROFILE=dev NS=osac`
