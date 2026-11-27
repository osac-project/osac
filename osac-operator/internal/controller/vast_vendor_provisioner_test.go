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
	"errors"
	"maps"
	"strings"
	"testing"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// fakeCSIClient records the last request and returns canned responses/errors.
type fakeCSIClient struct {
	createReq  *csi.CreateVolumeRequest
	createResp *csi.CreateVolumeResponse
	createErr  error

	deleteReq *csi.DeleteVolumeRequest
	deleteErr error

	// dialedEndpoint and dials record what the provisioner dialled.
	dialedEndpoint string
	dials          int
}

func (f *fakeCSIClient) CreateVolume(_ context.Context, in *csi.CreateVolumeRequest, _ ...grpc.CallOption) (*csi.CreateVolumeResponse, error) {
	f.createReq = in
	return f.createResp, f.createErr
}

func (f *fakeCSIClient) DeleteVolume(_ context.Context, in *csi.DeleteVolumeRequest, _ ...grpc.CallOption) (*csi.DeleteVolumeResponse, error) {
	f.deleteReq = in
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &csi.DeleteVolumeResponse{}, nil
}

// newProvisioner builds a provisioner wired to the given fake CSI client and a
// fake secret store containing the provided secret (if non-nil).
func newProvisioner(t *testing.T, csiCli *fakeCSIClient, secret *corev1.Secret) (*VastVendorProvisioner, *fakeCSIClient) {
	t.Helper()
	return newProvisionerWithEndpoints(t, csiCli, secret,
		map[string]string{"vast": "vast-csi-controller.osac-csi-backends.svc:50051"})
}

// perProtocolEndpoints configures one vendor controller per protocol.
func perProtocolEndpoints() map[string]string {
	return map[string]string{"vast/block": "block.svc:50051", "vast/nfs": "nfs.svc:50051"}
}

// newProvisionerWithEndpoints is newProvisioner with explicit vendor endpoints.
func newProvisionerWithEndpoints(t *testing.T, csiCli *fakeCSIClient, secret *corev1.Secret, endpoints map[string]string) (*VastVendorProvisioner, *fakeCSIClient) {
	t.Helper()
	builder := fake.NewClientBuilder()
	if secret != nil {
		builder = builder.WithObjects(secret)
	}
	dialed := csiCli
	return &VastVendorProvisioner{
		reader:          builder.Build(),
		configNamespace: "osac-system",
		endpoints:       endpoints,
		dial: func(_ context.Context, endpoint string) (vendorCSIClient, func() error, error) {
			dialed.dialedEndpoint = endpoint
			dialed.dials++
			return dialed, func() error { return nil }, nil
		},
	}, dialed
}

// nfsCreateReq is an NFS create request with every NFS input set.
func nfsCreateReq() VendorCreateVolumeRequest {
	return VendorCreateVolumeRequest{
		Name:       "pvc-n",
		Provider:   "vast",
		Tenant:     "acme",
		Tier:       "silver",
		SizeGiB:    5,
		AccessMode: v1alpha1.VolumeAccessModeReadWriteMany,
		Protocol:   v1alpha1.VolumeProtocolNFS,
		NFS: &VendorNFSParams{
			RootExport:  "/osac/acme/silver",
			ViewPolicy:  "acme-nfs",
			VipPoolFQDN: "pool.acme.example",
		},
	}
}

// nfsVendorResponse mirrors the vendor's NFS create response
// (vast_csi/builders/nfs.py volume_context): vip_pool_fqdn is present, so
// vip_pool_name is not.
func nfsVendorResponse() *csi.CreateVolumeResponse {
	return &csi.CreateVolumeResponse{Volume: &csi.Volume{
		VolumeId: "pvc-n",
		VolumeContext: map[string]string{
			"root_export":                 "/osac/acme/silver",
			"mount_options":               "",
			"view_policy":                 "acme-nfs",
			"protocol":                    "NFS",
			"vip_pool_fqdn":               "pool.acme.example",
			"vip_pool_fqdn_random_prefix": "true",
			"volume_name":                 "pvc-n",
			"quota_id":                    "11",
			"view_id":                     "22",
			"tenant_id":                   "33",
		},
	}}
}

// nfsOnlySecret has the credentials NFS needs and none of the block-only keys.
func nfsOnlySecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "vast-tenant-config-acme", Namespace: "osac-system"},
		Data: map[string][]byte{
			secretKeyManagerUsername: []byte("osac-acme"),
			secretKeyManagerPassword: []byte("s3cret"),
			secretKeyVastEndpoint:    []byte("vms.example.com"),
		},
	}
}

// fullSecret returns a hub Secret with all required keys populated.
func fullSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "vast-tenant-config-acme", Namespace: "osac-system"},
		Data: map[string][]byte{
			secretKeyManagerUsername: []byte("osac-acme"),
			secretKeyManagerPassword: []byte("s3cret"),
			secretKeyVastEndpoint:    []byte("vms.example.com"),
			secretKeyVipPoolName:     []byte("osac-shared"),
			secretKeyTenantUIDHash:   []byte("abc123"),
		},
	}
}

func blockCreateReq() VendorCreateVolumeRequest {
	return VendorCreateVolumeRequest{
		Name:       "pvc-1",
		Provider:   "vast",
		Tenant:     "acme",
		Tier:       "gold",
		SizeGiB:    10,
		AccessMode: v1alpha1.VolumeAccessModeReadWriteOnce,
		Protocol:   v1alpha1.VolumeProtocolBlock,
	}
}

func TestCreateVolumeBlockSuccess(t *testing.T) {
	csiResp := &csi.CreateVolumeResponse{Volume: &csi.Volume{VolumeId: "vendor-vol-1"}}
	p, cli := newProvisioner(t, &fakeCSIClient{createResp: csiResp}, fullSecret())

	resp, err := p.CreateVolume(context.Background(), blockCreateReq())
	if err != nil {
		t.Fatalf("CreateVolume error: %v", err)
	}
	if resp.VendorVolumeID != "vendor-vol-1" {
		t.Errorf("VendorVolumeID = %q, want vendor-vol-1", resp.VendorVolumeID)
	}
	if resp.Protocol != "Block" {
		t.Errorf("resp protocol = %q, want Block", resp.Protocol)
	}

	req := cli.createReq
	if got := req.GetParameters()["subsystem"]; got != "view-acme-abc123-gold" {
		t.Errorf("subsystem = %q, want view-acme-abc123-gold", got)
	}
	if got := req.GetParameters()["vip_pool_name"]; got != "osac-shared" {
		t.Errorf("vip_pool_name = %q, want osac-shared", got)
	}
	if got := req.GetSecrets()["username"]; got != "osac-acme" {
		t.Errorf("secrets.username = %q, want osac-acme", got)
	}
	if got := req.GetSecrets()["endpoint"]; got != "vms.example.com" {
		t.Errorf("secrets.endpoint = %q, want vms.example.com", got)
	}
	if got := req.GetSecrets()["tenant"]; got != "acme" {
		t.Errorf("secrets.tenant = %q, want acme", got)
	}
	if got := req.GetCapacityRange().GetRequiredBytes(); got != 10*bytesPerGiB {
		t.Errorf("required_bytes = %d, want %d", got, int64(10)*bytesPerGiB)
	}
	cap := req.GetVolumeCapabilities()[0]
	if cap.GetBlock() == nil {
		t.Errorf("expected a block volume capability")
	}
	if cap.GetAccessMode().GetMode() != csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER {
		t.Errorf("access mode = %v, want SINGLE_NODE_WRITER", cap.GetAccessMode().GetMode())
	}
}

func TestCreateVolumeVipPoolFQDNPreferred(t *testing.T) {
	secret := fullSecret()
	secret.Data[secretKeyVipPoolFQDN] = []byte("pool.example.com")
	p, cli := newProvisioner(t, &fakeCSIClient{createResp: &csi.CreateVolumeResponse{Volume: &csi.Volume{VolumeId: "v"}}}, secret)

	if _, err := p.CreateVolume(context.Background(), blockCreateReq()); err != nil {
		t.Fatalf("CreateVolume error: %v", err)
	}
	if got := cli.createReq.GetParameters()["vip_pool_fqdn"]; got != "pool.example.com" {
		t.Errorf("vip_pool_fqdn = %q, want pool.example.com", got)
	}
	if _, ok := cli.createReq.GetParameters()["vip_pool_name"]; ok {
		t.Errorf("vip_pool_name should be omitted when fqdn is set")
	}
}

func TestCreateVolumeRejectsUnknownProtocol(t *testing.T) {
	for _, protocol := range []v1alpha1.VolumeProtocol{"", "iSCSI"} {
		p, cli := newProvisioner(t, &fakeCSIClient{}, fullSecret())
		req := blockCreateReq()
		req.Protocol = protocol
		_, err := p.CreateVolume(context.Background(), req)
		if err == nil {
			t.Fatalf("protocol %q: expected error, got nil", protocol)
		}
		if !strings.Contains(err.Error(), "unsupported volume protocol") || strings.Contains(err.Error(), "not yet implemented") {
			t.Errorf("protocol %q: error = %q, want an \"unsupported volume protocol\" error", protocol, err)
		}
		if cli.dials != 0 {
			t.Errorf("protocol %q: dialled %d times, want 0", protocol, cli.dials)
		}
	}
}

func TestCreateVolumeNFSMapsRequest(t *testing.T) {
	p, cli := newProvisionerWithEndpoints(t, &fakeCSIClient{createResp: nfsVendorResponse()}, fullSecret(), perProtocolEndpoints())

	if _, err := p.CreateVolume(context.Background(), nfsCreateReq()); err != nil {
		t.Fatalf("CreateVolume error: %v", err)
	}
	if cli.dialedEndpoint != "nfs.svc:50051" {
		t.Errorf("dialed %q, want nfs.svc:50051", cli.dialedEndpoint)
	}
	wantParams := map[string]string{
		"root_export":   "/osac/acme/silver",
		"view_policy":   "acme-nfs",
		"vip_pool_fqdn": "pool.acme.example",
	}
	if got := cli.createReq.GetParameters(); !maps.Equal(got, wantParams) {
		t.Errorf("parameters = %v, want %v", got, wantParams)
	}
	fixture := fullSecret().Data
	wantSecrets := map[string]string{
		"username": string(fixture[secretKeyManagerUsername]),
		"password": string(fixture[secretKeyManagerPassword]),
		"endpoint": string(fixture[secretKeyVastEndpoint]),
		"tenant":   "acme",
	}
	if got := cli.createReq.GetSecrets(); !maps.Equal(got, wantSecrets) {
		t.Errorf("secrets = %v, want %v", got, wantSecrets)
	}
	caps := cli.createReq.GetVolumeCapabilities()
	if len(caps) != 1 || caps[0].GetMount() == nil || caps[0].GetBlock() != nil {
		t.Fatalf("capabilities = %v, want one Mount capability", caps)
	}
	if caps[0].GetAccessMode().GetMode() != csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER {
		t.Errorf("access mode = %v, want MULTI_NODE_MULTI_WRITER", caps[0].GetAccessMode().GetMode())
	}
	if got := cli.createReq.GetCapacityRange().GetRequiredBytes(); got != 5*bytesPerGiB {
		t.Errorf("required_bytes = %d, want %d", got, int64(5)*bytesPerGiB)
	}

	req := nfsCreateReq()
	req.NFS.QoSPolicy = "silver-qos"
	if _, err := p.CreateVolume(context.Background(), req); err != nil {
		t.Fatalf("CreateVolume with QoS error: %v", err)
	}
	if got := cli.createReq.GetParameters()["qos_policy"]; got != "silver-qos" {
		t.Errorf("qos_policy = %q, want silver-qos", got)
	}
}

func TestCreateVolumeNFSVendorContextAllowList(t *testing.T) {
	p, _ := newProvisionerWithEndpoints(t, &fakeCSIClient{createResp: nfsVendorResponse()}, fullSecret(), perProtocolEndpoints())

	resp, err := p.CreateVolume(context.Background(), nfsCreateReq())
	if err != nil {
		t.Fatalf("CreateVolume error: %v", err)
	}
	if resp.Protocol != string(v1alpha1.VolumeProtocolNFS) {
		t.Errorf("protocol = %q, want NFS", resp.Protocol)
	}
	if resp.VendorVolumeID != "pvc-n" {
		t.Errorf("VendorVolumeID = %q, want pvc-n", resp.VendorVolumeID)
	}
	want := map[string]string{
		"root_export":                 "/osac/acme/silver",
		"vip_pool_fqdn":               "pool.acme.example",
		"vip_pool_fqdn_random_prefix": "true",
		"mount_options":               "",
	}
	if !maps.Equal(resp.VendorContext, want) {
		t.Errorf("VendorContext = %v, want %v", resp.VendorContext, want)
	}

	vendor := nfsVendorResponse()
	vendor.Volume.VolumeContext["snapshot_base_path"] = ".snapshot/s1"
	vendor.Volume.VolumeContext["vip_pool_name"] = "pool-a"
	p, _ = newProvisionerWithEndpoints(t, &fakeCSIClient{createResp: vendor}, fullSecret(), perProtocolEndpoints())
	resp, err = p.CreateVolume(context.Background(), nfsCreateReq())
	if err != nil {
		t.Fatalf("CreateVolume error: %v", err)
	}
	if got := resp.VendorContext["snapshot_base_path"]; got != ".snapshot/s1" {
		t.Errorf("snapshot_base_path = %q, want it kept", got)
	}
	if _, ok := resp.VendorContext["vip_pool_name"]; ok {
		t.Errorf("vip_pool_name must not reach VendorContext")
	}
}

func TestCreateVolumeNFSMissingInputs(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*VendorCreateVolumeRequest)
		want   string
	}{
		{"no NFS parameters", func(r *VendorCreateVolumeRequest) { r.NFS = nil }, "NFS parameters"},
		{"no root export", func(r *VendorCreateVolumeRequest) { r.NFS.RootExport = "" }, "root_export"},
		{"no view policy", func(r *VendorCreateVolumeRequest) { r.NFS.ViewPolicy = "" }, "view_policy"},
		{"no VIP pool FQDN", func(r *VendorCreateVolumeRequest) { r.NFS.VipPoolFQDN = "" }, "vip_pool_fqdn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, cli := newProvisionerWithEndpoints(t, &fakeCSIClient{createResp: nfsVendorResponse()}, fullSecret(), perProtocolEndpoints())
			req := nfsCreateReq()
			tc.mutate(&req)
			_, err := p.CreateVolume(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
			if cli.dials != 0 {
				t.Errorf("dialled %d times, want 0", cli.dials)
			}
		})
	}
}

func TestEndpointPerProtocol(t *testing.T) {
	blockResp := &csi.CreateVolumeResponse{Volume: &csi.Volume{VolumeId: "b"}}

	p, cli := newProvisionerWithEndpoints(t, &fakeCSIClient{createResp: blockResp}, fullSecret(), perProtocolEndpoints())
	if _, err := p.CreateVolume(context.Background(), blockCreateReq()); err != nil {
		t.Fatalf("block CreateVolume error: %v", err)
	}
	if cli.dialedEndpoint != "block.svc:50051" {
		t.Errorf("block dialed %q, want block.svc:50051", cli.dialedEndpoint)
	}
	if got := cli.createReq.GetParameters()["subsystem"]; got != "view-acme-abc123-gold" {
		t.Errorf("subsystem = %q, want view-acme-abc123-gold", got)
	}
	if got := cli.createReq.GetParameters()["tenant_name"]; got != "acme" {
		t.Errorf("tenant_name = %q, want acme", got)
	}

	legacy := map[string]string{"vast": "legacy.svc:50051"}
	p, cli = newProvisionerWithEndpoints(t, &fakeCSIClient{createResp: blockResp}, fullSecret(), legacy)
	if _, err := p.CreateVolume(context.Background(), blockCreateReq()); err != nil {
		t.Fatalf("legacy block CreateVolume error: %v", err)
	}
	if cli.dialedEndpoint != "legacy.svc:50051" {
		t.Errorf("legacy block dialed %q, want legacy.svc:50051", cli.dialedEndpoint)
	}

	p, cli = newProvisionerWithEndpoints(t, &fakeCSIClient{createResp: nfsVendorResponse()}, fullSecret(), legacy)
	_, err := p.CreateVolume(context.Background(), nfsCreateReq())
	if err == nil || !strings.Contains(err.Error(), "vast/nfs") {
		t.Fatalf("NFS with only a bare key: error = %v, want one naming vast/nfs", err)
	}
	if cli.dials != 0 {
		t.Errorf("NFS with only a bare key dialled %d times, want 0", cli.dials)
	}
}

func TestDeleteVolumeNFSRoutesByProtocol(t *testing.T) {
	nfsDelete := VendorDeleteVolumeRequest{
		VendorVolumeID: "pvc-n", Provider: "vast", Tenant: "acme", Protocol: v1alpha1.VolumeProtocolNFS,
	}

	p, cli := newProvisionerWithEndpoints(t, &fakeCSIClient{}, fullSecret(), perProtocolEndpoints())
	if err := p.DeleteVolume(context.Background(), nfsDelete); err != nil {
		t.Fatalf("DeleteVolume error: %v", err)
	}
	if cli.dialedEndpoint != "nfs.svc:50051" {
		t.Errorf("dialed %q, want nfs.svc:50051", cli.dialedEndpoint)
	}
	fixture := fullSecret().Data
	if got := cli.deleteReq.GetSecrets(); got["username"] != string(fixture[secretKeyManagerUsername]) ||
		got["tenant"] != "acme" || got["password"] != string(fixture[secretKeyManagerPassword]) ||
		got["endpoint"] != string(fixture[secretKeyVastEndpoint]) {
		t.Errorf("delete secrets = %v", got)
	}

	p, _ = newProvisionerWithEndpoints(t, &fakeCSIClient{deleteErr: grpcstatus.Error(codes.NotFound, "gone")}, fullSecret(), perProtocolEndpoints())
	if err := p.DeleteVolume(context.Background(), nfsDelete); err != nil {
		t.Errorf("NotFound should be success, got %v", err)
	}

	trashOff := grpcstatus.Error(codes.Unknown,
		"[DeleteVolume]: Ensure that deletionViewPolicy is properly configured in your Helm configuration vendor-secret-marker")
	p, _ = newProvisionerWithEndpoints(t, &fakeCSIClient{deleteErr: trashOff}, fullSecret(), perProtocolEndpoints())
	err := p.DeleteVolume(context.Background(), nfsDelete)
	if !errors.Is(err, ErrVendorNFSTrashDisabled) {
		t.Fatalf("error = %v, want ErrVendorNFSTrashDisabled", err)
	}
	if err.Error() != vastNFSTrashDisabledMessage {
		t.Errorf("error text = %q, want the fixed message", err.Error())
	}

	p, _ = newProvisionerWithEndpoints(t, &fakeCSIClient{deleteErr: grpcstatus.Error(codes.Internal, "boom")}, fullSecret(), perProtocolEndpoints())
	err = p.DeleteVolume(context.Background(), nfsDelete)
	if err == nil || errors.Is(err, ErrVendorNFSTrashDisabled) {
		t.Errorf("other vendor error = %v, want a non-sentinel error", err)
	}

	p, cli = newProvisionerWithEndpoints(t, &fakeCSIClient{}, fullSecret(), map[string]string{"vast": "legacy.svc:50051"})
	if err := p.DeleteVolume(context.Background(), VendorDeleteVolumeRequest{
		VendorVolumeID: "vendor-vol-1", Provider: "vast", Tenant: "acme",
	}); err != nil {
		t.Fatalf("empty-protocol delete error: %v", err)
	}
	if cli.dialedEndpoint != "legacy.svc:50051" {
		t.Errorf("empty-protocol delete dialed %q, want legacy.svc:50051", cli.dialedEndpoint)
	}

	p, _ = newProvisionerWithEndpoints(t, &fakeCSIClient{deleteErr: trashOff}, fullSecret(), perProtocolEndpoints())
	err = p.DeleteVolume(context.Background(), VendorDeleteVolumeRequest{
		VendorVolumeID: "vendor-vol-1", Provider: "vast", Tenant: "acme", Protocol: v1alpha1.VolumeProtocolBlock,
	})
	if err == nil || errors.Is(err, ErrVendorNFSTrashDisabled) {
		t.Errorf("block delete error = %v, want a non-sentinel error", err)
	}
}

func TestNFSSecretWithoutBlockKeys(t *testing.T) {
	p, cli := newProvisionerWithEndpoints(t, &fakeCSIClient{createResp: nfsVendorResponse()}, nfsOnlySecret(), perProtocolEndpoints())
	if _, err := p.CreateVolume(context.Background(), nfsCreateReq()); err != nil {
		t.Fatalf("NFS create with NFS-only secret: %v", err)
	}
	if got := cli.createReq.GetSecrets()["tenant"]; got != "acme" {
		t.Errorf("secrets.tenant = %q, want acme", got)
	}
	if err := p.DeleteVolume(context.Background(), VendorDeleteVolumeRequest{
		VendorVolumeID: "pvc-n", Provider: "vast", Tenant: "acme", Protocol: v1alpha1.VolumeProtocolNFS,
	}); err != nil {
		t.Fatalf("NFS delete with NFS-only secret: %v", err)
	}

	p, _ = newProvisionerWithEndpoints(t, &fakeCSIClient{}, nfsOnlySecret(), perProtocolEndpoints())
	if _, err := p.CreateVolume(context.Background(), blockCreateReq()); err == nil {
		t.Errorf("block create with NFS-only secret: expected missing-key error, got nil")
	}
}

func TestCreateVolumeMissingSecret(t *testing.T) {
	p, _ := newProvisioner(t, &fakeCSIClient{}, nil)
	if _, err := p.CreateVolume(context.Background(), blockCreateReq()); err == nil {
		t.Fatalf("expected error for missing secret, got nil")
	}
}

func TestCreateVolumeMissingVipPool(t *testing.T) {
	secret := fullSecret()
	delete(secret.Data, secretKeyVipPoolName)
	p, _ := newProvisioner(t, &fakeCSIClient{}, secret)
	if _, err := p.CreateVolume(context.Background(), blockCreateReq()); err == nil {
		t.Fatalf("expected error for missing vip pool, got nil")
	}
}

func TestCreateVolumeUnknownProvider(t *testing.T) {
	p, _ := newProvisioner(t, &fakeCSIClient{}, fullSecret())
	req := blockCreateReq()
	req.Provider = "nope"
	if _, err := p.CreateVolume(context.Background(), req); err == nil {
		t.Fatalf("expected error for unknown provider, got nil")
	}
}

func TestCreateVolumeVendorError(t *testing.T) {
	p, _ := newProvisioner(t, &fakeCSIClient{createErr: errors.New("boom")}, fullSecret())
	if _, err := p.CreateVolume(context.Background(), blockCreateReq()); err == nil {
		t.Fatalf("expected vendor error to propagate, got nil")
	}
}

func TestDeleteVolumeSuccess(t *testing.T) {
	p, cli := newProvisioner(t, &fakeCSIClient{}, fullSecret())
	err := p.DeleteVolume(context.Background(), VendorDeleteVolumeRequest{
		VendorVolumeID: "vendor-vol-1", Provider: "vast", Tenant: "acme",
	})
	if err != nil {
		t.Fatalf("DeleteVolume error: %v", err)
	}
	if cli.deleteReq.GetVolumeId() != "vendor-vol-1" {
		t.Errorf("delete volume id = %q, want vendor-vol-1", cli.deleteReq.GetVolumeId())
	}
	if cli.deleteReq.GetSecrets()["username"] != "osac-acme" {
		t.Errorf("delete secrets.username = %q", cli.deleteReq.GetSecrets()["username"])
	}
	if got := cli.deleteReq.GetSecrets()["tenant"]; got != "acme" {
		t.Errorf("delete secrets.tenant = %q, want acme", got)
	}
}

func TestDeleteVolumeNotFoundIsSuccess(t *testing.T) {
	p, _ := newProvisioner(t, &fakeCSIClient{deleteErr: grpcstatus.Error(codes.NotFound, "gone")}, fullSecret())
	if err := p.DeleteVolume(context.Background(), VendorDeleteVolumeRequest{
		VendorVolumeID: "vendor-vol-1", Provider: "vast", Tenant: "acme",
	}); err != nil {
		t.Fatalf("NotFound should be treated as success, got %v", err)
	}
}

func TestDeleteVolumeVendorError(t *testing.T) {
	p, _ := newProvisioner(t, &fakeCSIClient{deleteErr: grpcstatus.Error(codes.Internal, "boom")}, fullSecret())
	if err := p.DeleteVolume(context.Background(), VendorDeleteVolumeRequest{
		VendorVolumeID: "vendor-vol-1", Provider: "vast", Tenant: "acme",
	}); err == nil {
		t.Fatalf("expected vendor error to propagate, got nil")
	}
}

func TestNewVastVendorProvisionerFailFast(t *testing.T) {
	reader := fake.NewClientBuilder().Build()
	if _, err := NewVastVendorProvisioner(reader, "osac-system", nil); err == nil {
		t.Errorf("expected error for empty endpoints")
	}
	if _, err := NewVastVendorProvisioner(reader, "", map[string]string{"vast": "x:50051"}); err == nil {
		t.Errorf("expected error for empty config namespace")
	}
	if _, err := NewVastVendorProvisioner(nil, "osac-system", map[string]string{"vast": "x:50051"}); err == nil {
		t.Errorf("expected error for nil reader")
	}
	if _, err := NewVastVendorProvisioner(reader, "osac-system", map[string]string{"vast": "x:50051"}); err != nil {
		t.Errorf("unexpected error for valid config: %v", err)
	}
}

func TestToCSIAccessMode(t *testing.T) {
	cases := map[v1alpha1.VolumeAccessMode]csi.VolumeCapability_AccessMode_Mode{
		v1alpha1.VolumeAccessModeReadWriteOnce:    csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		v1alpha1.VolumeAccessModeReadWriteOncePod: csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
		v1alpha1.VolumeAccessModeReadOnlyMany:     csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		v1alpha1.VolumeAccessModeReadWriteMany:    csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
	}
	for mode, want := range cases {
		if got := toCSIAccessMode(mode); got != want {
			t.Errorf("toCSIAccessMode(%q) = %v, want %v", mode, got, want)
		}
	}
}
