/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// bytesPerGiB converts the Volume spec's GiB size to the CSI capacity in bytes.
const bytesPerGiB = 1024 * 1024 * 1024

// Hub Secret keys (Secret "vast-tenant-config-<tenant>"), populated during VAST
// tenant onboarding. Kept in sync with the reader in osac-aap's vast_storage
// role (read_tenant_credentials.yaml).
const (
	secretKeyManagerUsername = "tenant_manager_username"
	secretKeyManagerPassword = "tenant_manager_password"
	secretKeyVastEndpoint    = "vast_endpoint"
	secretKeyVipPoolName     = "vip_pool_name"
	secretKeyVipPoolFQDN     = "vip_pool_fqdn"
	secretKeyTenantUIDHash   = "tenant_uid_hash"
)

// vendorCSIClient is the subset of the CSI controller service the provisioner
// uses. csi.ControllerClient (over a real gRPC connection) satisfies it, and
// tests substitute an in-memory fake.
type vendorCSIClient interface {
	CreateVolume(ctx context.Context, in *csi.CreateVolumeRequest, opts ...grpc.CallOption) (*csi.CreateVolumeResponse, error)
	DeleteVolume(ctx context.Context, in *csi.DeleteVolumeRequest, opts ...grpc.CallOption) (*csi.DeleteVolumeResponse, error)
}

// vendorDialer opens a connection to a vendor CSI controller endpoint and
// returns a client plus a close function. It is a seam for testing: production
// uses dialVendorController; tests inject a fake.
type vendorDialer func(ctx context.Context, endpoint string) (vendorCSIClient, func() error, error)

// VastVendorProvisioner is the production VendorProvisioner. It provisions
// volumes by calling a vendor CSI controller (deployed per backend, e.g. the
// VAST CSI controller) directly over gRPC, passing per-tenant credentials in
// the CSI secrets field. The per-tenant credentials and VAST-side resource
// references are read from the hub Secret "vast-tenant-config-<tenant>" created
// during tenant onboarding.
//
// Only block volumes are implemented today. The exact CSI CreateVolume contract
// is verified against the upstream VAST CSI driver at the tag this deployment
// pins (docker.io/vastdataorg/csi:v2.6.5):
//
//	https://github.com/vast-data/vast-csi/blob/v2.6.5/vast_csi/builders/block.py
//
// The block builder requires the "subsystem" parameter (the VAST view, created
// during onboarding and named "view-<tenant>-<uid_hash>-<tier>"), a VIP pool,
// and username/password/endpoint in secrets. NFS uses a different parameter set
// (root_export + view_policy) and is not implemented here yet.
type VastVendorProvisioner struct {
	// reader reads the per-tenant hub Secret. The operator already has
	// cluster-wide secret read via its manager ClusterRole, so no extra RBAC is
	// required.
	reader client.Reader
	// configNamespace is where the hub "vast-tenant-config-<tenant>" Secrets
	// live (OSAC_STORAGE_CONFIG_NAMESPACE).
	configNamespace string
	// endpoints maps a StorageBackend name to its vendor CSI controller gRPC
	// endpoint (e.g. "vast" -> "vast-csi-controller.osac-csi-backends.svc:50051").
	endpoints map[string]string
	dial      vendorDialer
}

// NewVastVendorProvisioner constructs a VastVendorProvisioner and fails fast on
// invalid configuration: at least one backend->endpoint mapping is required so
// the operator errors loudly at startup rather than running degraded and
// leaving volumes stuck in Progressing.
func NewVastVendorProvisioner(reader client.Reader, configNamespace string, endpoints map[string]string) (*VastVendorProvisioner, error) {
	if reader == nil {
		return nil, fmt.Errorf("client reader is required")
	}
	if configNamespace == "" {
		return nil, fmt.Errorf("storage config namespace is required")
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("at least one vendor controller endpoint (backend=endpoint) must be configured")
	}
	return &VastVendorProvisioner{
		reader:          reader,
		configNamespace: configNamespace,
		endpoints:       endpoints,
		dial:            dialVendorController,
	}, nil
}

// tenantCreds holds the per-tenant values read from the hub Secret.
type tenantCreds struct {
	username     string
	password     string
	vastEndpoint string
	vipPoolName  string
	vipPoolFQDN  string
	uidHash      string
}

// CreateVolume provisions a block volume on the vendor array. It reads the
// per-tenant credentials/config from the hub Secret, resolves the vendor
// controller endpoint from the backend name, and issues a CSI CreateVolume with
// the credentials in the secrets field. CSI CreateVolume is idempotent, so a
// retried call for an existing volume returns that volume rather than erroring.
func (p *VastVendorProvisioner) CreateVolume(ctx context.Context, req VendorCreateVolumeRequest) (VendorCreateVolumeResponse, error) {
	if req.Protocol != v1alpha1.VolumeProtocolBlock {
		return VendorCreateVolumeResponse{}, fmt.Errorf(
			"vendor provisioner supports only block volumes; protocol %q is not yet implemented", req.Protocol)
	}

	endpoint, err := p.endpointFor(req.Backend)
	if err != nil {
		return VendorCreateVolumeResponse{}, err
	}
	creds, err := p.readTenantCreds(ctx, req.Tenant)
	if err != nil {
		return VendorCreateVolumeResponse{}, err
	}

	cli, closeConn, err := p.dial(ctx, endpoint)
	if err != nil {
		return VendorCreateVolumeResponse{}, fmt.Errorf("dial vendor controller %q: %w", endpoint, err)
	}
	defer func() { _ = closeConn() }()

	// The VAST view (NVMe subsystem) is created during onboarding and named
	// "view-<tenant>-<uid_hash>-<tier>" (see osac-aap vast_storage
	// ensure_storage_class). The operator references it by name; it does not
	// create it.
	subsystem := fmt.Sprintf("view-%s-%s-%s", req.Tenant, creds.uidHash, req.Tier)

	csiReq := &csi.CreateVolumeRequest{
		Name:               req.Name,
		CapacityRange:      &csi.CapacityRange{RequiredBytes: req.SizeGiB * bytesPerGiB},
		VolumeCapabilities: []*csi.VolumeCapability{blockVolumeCapability(req.AccessMode)},
		Parameters: map[string]string{
			"subsystem":   subsystem,
			"tenant_name": req.Tenant,
		},
		Secrets: map[string]string{
			"username": creds.username,
			"password": creds.password,
			"endpoint": creds.vastEndpoint,
			"tenant":   req.Tenant,
		},
	}
	creds.applyVipPool(csiReq.Parameters)

	resp, err := cli.CreateVolume(ctx, csiReq)
	if err != nil {
		return VendorCreateVolumeResponse{}, fmt.Errorf("vendor CreateVolume for %q: %w", req.Name, err)
	}
	vol := resp.GetVolume()
	if vol == nil || vol.GetVolumeId() == "" {
		return VendorCreateVolumeResponse{}, fmt.Errorf("vendor CreateVolume for %q returned no volume id", req.Name)
	}

	return VendorCreateVolumeResponse{
		VendorVolumeID: vol.GetVolumeId(),
		Backend:        req.Backend,
		Protocol:       string(v1alpha1.VolumeProtocolBlock),
	}, nil
}

// DeleteVolume deprovisions the vendor volume. CSI DeleteVolume is idempotent:
// a NotFound response is treated as success so a retried delete of an
// already-removed volume does not block finalizer removal.
func (p *VastVendorProvisioner) DeleteVolume(ctx context.Context, req VendorDeleteVolumeRequest) error {
	endpoint, err := p.endpointFor(req.Backend)
	if err != nil {
		return err
	}
	creds, err := p.readTenantCreds(ctx, req.Tenant)
	if err != nil {
		return err
	}

	cli, closeConn, err := p.dial(ctx, endpoint)
	if err != nil {
		return fmt.Errorf("dial vendor controller %q: %w", endpoint, err)
	}
	defer func() { _ = closeConn() }()

	_, err = cli.DeleteVolume(ctx, &csi.DeleteVolumeRequest{
		VolumeId: req.VendorVolumeID,
		Secrets: map[string]string{
			"username": creds.username,
			"password": creds.password,
			"endpoint": creds.vastEndpoint,
			"tenant":   req.Tenant,
		},
	})
	if err != nil {
		if grpcstatus.Code(err) == codes.NotFound {
			return nil
		}
		return fmt.Errorf("vendor DeleteVolume for %q: %w", req.VendorVolumeID, err)
	}
	return nil
}

// endpointFor resolves the vendor controller endpoint for a backend name.
func (p *VastVendorProvisioner) endpointFor(backend string) (string, error) {
	if backend == "" {
		return "", fmt.Errorf("volume has no resolved backend; cannot select a vendor controller")
	}
	endpoint, ok := p.endpoints[backend]
	if !ok {
		return "", fmt.Errorf("no vendor controller endpoint configured for backend %q", backend)
	}
	return endpoint, nil
}

// readTenantCreds reads and validates the per-tenant hub Secret.
func (p *VastVendorProvisioner) readTenantCreds(ctx context.Context, tenant string) (tenantCreds, error) {
	if tenant == "" {
		return tenantCreds{}, fmt.Errorf("volume has no tenant annotation; cannot select vendor credentials")
	}
	name := fmt.Sprintf("vast-tenant-config-%s", tenant)
	secret := &corev1.Secret{}
	if err := p.reader.Get(ctx, client.ObjectKey{Namespace: p.configNamespace, Name: name}, secret); err != nil {
		return tenantCreds{}, fmt.Errorf("read hub secret %s/%s: %w", p.configNamespace, name, err)
	}

	creds := tenantCreds{
		username:     string(secret.Data[secretKeyManagerUsername]),
		password:     string(secret.Data[secretKeyManagerPassword]),
		vastEndpoint: string(secret.Data[secretKeyVastEndpoint]),
		vipPoolName:  string(secret.Data[secretKeyVipPoolName]),
		vipPoolFQDN:  string(secret.Data[secretKeyVipPoolFQDN]),
		uidHash:      string(secret.Data[secretKeyTenantUIDHash]),
	}
	if creds.username == "" || creds.password == "" {
		return tenantCreds{}, fmt.Errorf("hub secret %s/%s missing manager credentials", p.configNamespace, name)
	}
	if creds.vastEndpoint == "" {
		return tenantCreds{}, fmt.Errorf("hub secret %s/%s missing %s", p.configNamespace, name, secretKeyVastEndpoint)
	}
	if creds.vipPoolName == "" && creds.vipPoolFQDN == "" {
		return tenantCreds{}, fmt.Errorf("hub secret %s/%s missing a VIP pool (%s or %s)",
			p.configNamespace, name, secretKeyVipPoolName, secretKeyVipPoolFQDN)
	}
	if creds.uidHash == "" {
		return tenantCreds{}, fmt.Errorf("hub secret %s/%s missing %s", p.configNamespace, name, secretKeyTenantUIDHash)
	}
	return creds, nil
}

// applyVipPool sets the VIP pool CSI parameter, preferring the FQDN form when
// present (matching the StorageClass parameters osac-aap emits today).
func (c tenantCreds) applyVipPool(params map[string]string) {
	if c.vipPoolFQDN != "" {
		params["vip_pool_fqdn"] = c.vipPoolFQDN
		return
	}
	params["vip_pool_name"] = c.vipPoolName
}

// blockVolumeCapability builds a block CSI volume capability from the CRD access
// mode.
func blockVolumeCapability(mode v1alpha1.VolumeAccessMode) *csi.VolumeCapability {
	return &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: toCSIAccessMode(mode)},
	}
}

// toCSIAccessMode maps the CRD access mode to the CSI access mode enum.
func toCSIAccessMode(mode v1alpha1.VolumeAccessMode) csi.VolumeCapability_AccessMode_Mode {
	switch mode {
	case v1alpha1.VolumeAccessModeReadWriteOnce:
		return csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	case v1alpha1.VolumeAccessModeReadWriteOncePod:
		return csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER
	case v1alpha1.VolumeAccessModeReadOnlyMany:
		return csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY
	case v1alpha1.VolumeAccessModeReadWriteMany:
		return csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER
	default:
		return csi.VolumeCapability_AccessMode_UNKNOWN
	}
}

// dialVendorController is the production vendorDialer. The vendor CSI controllers
// are reached over plaintext in-cluster gRPC (same trust model as the CSI
// driver's node-plugin proxy).
func dialVendorController(_ context.Context, endpoint string) (vendorCSIClient, func() error, error) {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return csi.NewControllerClient(conn), conn.Close, nil
}
