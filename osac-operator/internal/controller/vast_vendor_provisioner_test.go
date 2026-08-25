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
	builder := fake.NewClientBuilder()
	if secret != nil {
		builder = builder.WithObjects(secret)
	}
	dialed := csiCli
	return &VastVendorProvisioner{
		reader:          builder.Build(),
		configNamespace: "osac-system",
		endpoints:       map[string]string{"vast-backend": "vast-csi-controller.osac-csi-backends.svc:50051"},
		dial: func(_ context.Context, _ string) (vendorCSIClient, func() error, error) {
			return dialed, func() error { return nil }, nil
		},
	}, dialed
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
		Backend:    "vast-backend",
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
	if resp.Backend != "vast-backend" || resp.Protocol != "Block" {
		t.Errorf("resp backend/protocol = %q/%q", resp.Backend, resp.Protocol)
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

func TestCreateVolumeRejectsNonBlock(t *testing.T) {
	p, _ := newProvisioner(t, &fakeCSIClient{}, fullSecret())
	req := blockCreateReq()
	req.Protocol = v1alpha1.VolumeProtocolNFS
	if _, err := p.CreateVolume(context.Background(), req); err == nil {
		t.Fatalf("expected error for NFS protocol, got nil")
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

func TestCreateVolumeUnknownBackend(t *testing.T) {
	p, _ := newProvisioner(t, &fakeCSIClient{}, fullSecret())
	req := blockCreateReq()
	req.Backend = "nope"
	if _, err := p.CreateVolume(context.Background(), req); err == nil {
		t.Fatalf("expected error for unknown backend, got nil")
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
		VendorVolumeID: "vendor-vol-1", Backend: "vast-backend", Tenant: "acme",
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
		VendorVolumeID: "vendor-vol-1", Backend: "vast-backend", Tenant: "acme",
	}); err != nil {
		t.Fatalf("NotFound should be treated as success, got %v", err)
	}
}

func TestDeleteVolumeVendorError(t *testing.T) {
	p, _ := newProvisioner(t, &fakeCSIClient{deleteErr: grpcstatus.Error(codes.Internal, "boom")}, fullSecret())
	if err := p.DeleteVolume(context.Background(), VendorDeleteVolumeRequest{
		VendorVolumeID: "vendor-vol-1", Backend: "vast-backend", Tenant: "acme",
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
