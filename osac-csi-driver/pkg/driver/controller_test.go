package driver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/osac-project/osac/osac-csi-driver/pkg/fulfillment"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- mocks ---

type mockVolumeClient struct {
	createVolumeFn func(ctx context.Context, params fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error)
	getVolumeFn    func(ctx context.Context, volumeID string) (*fulfillment.VolumeInfo, error)
	listVolumesFn  func(ctx context.Context, params fulfillment.ListVolumesParams) ([]*fulfillment.VolumeInfo, error)
	deleteVolumeFn func(ctx context.Context, volumeID string) error
}

func (m *mockVolumeClient) CreateVolume(ctx context.Context, params fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error) {
	return m.createVolumeFn(ctx, params)
}

func (m *mockVolumeClient) GetVolume(ctx context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
	return m.getVolumeFn(ctx, volumeID)
}

func (m *mockVolumeClient) ListVolumes(ctx context.Context, params fulfillment.ListVolumesParams) ([]*fulfillment.VolumeInfo, error) {
	return m.listVolumesFn(ctx, params)
}

func (m *mockVolumeClient) DeleteVolume(ctx context.Context, volumeID string) error {
	return m.deleteVolumeFn(ctx, volumeID)
}

type mockControlPlaneClient struct {
	publishVolumeFn   func(ctx context.Context, volumeID, nodeID string) error
	unpublishVolumeFn func(ctx context.Context, volumeID, nodeID string) error
}

func (m *mockControlPlaneClient) PublishVolume(ctx context.Context, volumeID, nodeID string) error {
	return m.publishVolumeFn(ctx, volumeID, nodeID)
}

func (m *mockControlPlaneClient) UnpublishVolume(ctx context.Context, volumeID, nodeID string) error {
	return m.unpublishVolumeFn(ctx, volumeID, nodeID)
}

// --- helpers ---

func newTestController(vc fulfillment.VolumeClient, cpc fulfillment.ControlPlaneClient) *ControllerServer {
	cs := NewControllerServer(vc, cpc, "test-cluster")
	cs.pollInitialInterval = 1 * time.Millisecond
	cs.pollMaxInterval = 5 * time.Millisecond
	return cs
}

func defaultCaps() []*csi.VolumeCapability {
	return []*csi.VolumeCapability{
		{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	}
}

func availableVolume(id, name string) *fulfillment.VolumeInfo {
	return &fulfillment.VolumeInfo{
		ID:             id,
		Name:           name,
		State:          fulfillment.VolumeStateAvailable,
		Backend:        "test-backend",
		VendorVolumeID: "vendor-" + id,
		Protocol:       "nfs",
		CapacityBytes:  1024,
	}
}

func assertCode(t *testing.T, err error, expected codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %v, got nil", expected)
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != expected {
		t.Fatalf("expected code %v, got %v: %s", expected, st.Code(), st.Message())
	}
}

// --- CreateVolume tests ---

func TestCreateVolume_Success(t *testing.T) {
	vol := availableVolume("vol-1", "pvc-123")
	vc := &mockVolumeClient{
		createVolumeFn: func(_ context.Context, params fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error) {
			if params.Tier != "gold" {
				t.Errorf("expected tier 'gold', got %q", params.Tier)
			}
			if params.Tenant != "my-tenant" {
				t.Errorf("expected tenant 'my-tenant', got %q", params.Tenant)
			}
			if params.ClusterID != "test-cluster" {
				t.Errorf("expected clusterID 'test-cluster', got %q", params.ClusterID)
			}
			if params.PVCRef != "pvc-123" {
				t.Errorf("expected pvcRef 'pvc-123', got %q", params.PVCRef)
			}
			if params.SizeBytes != 1024 {
				t.Errorf("expected sizeBytes 1024, got %d", params.SizeBytes)
			}
			return vol, nil
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	resp, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-123",
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold", "tenant": "my-tenant"},
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1024},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Volume.VolumeId != "vol-1" {
		t.Errorf("expected volume ID 'vol-1', got %q", resp.Volume.VolumeId)
	}
	if resp.Volume.VolumeContext["osac.backend"] != "test-backend" {
		t.Errorf("expected backend 'test-backend', got %q", resp.Volume.VolumeContext["osac.backend"])
	}
	if resp.Volume.VolumeContext["osac.volume-id"] != "vendor-vol-1" {
		t.Errorf("expected vendor volume ID 'vendor-vol-1', got %q", resp.Volume.VolumeContext["osac.volume-id"])
	}
	if resp.Volume.VolumeContext["osac.protocol"] != "nfs" {
		t.Errorf("expected protocol 'nfs', got %q", resp.Volume.VolumeContext["osac.protocol"])
	}
}

func TestCreateVolume_DefaultTenant(t *testing.T) {
	vc := &mockVolumeClient{
		createVolumeFn: func(_ context.Context, params fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error) {
			if params.Tenant != "default" {
				t.Errorf("expected default tenant, got %q", params.Tenant)
			}
			return availableVolume("vol-1", "pvc-123"), nil
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	_, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-123",
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateVolume_PollsUntilAvailable(t *testing.T) {
	var calls atomic.Int32
	vc := &mockVolumeClient{
		createVolumeFn: func(_ context.Context, _ fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error) {
			return &fulfillment.VolumeInfo{
				ID:    "vol-1",
				Name:  "pvc-123",
				State: fulfillment.VolumeStateCreating,
			}, nil
		},
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			n := calls.Add(1)
			if n < 3 {
				return &fulfillment.VolumeInfo{
					ID:    volumeID,
					State: fulfillment.VolumeStateCreating,
				}, nil
			}
			return availableVolume(volumeID, "pvc-123"), nil
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	resp, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-123",
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Volume.VolumeId != "vol-1" {
		t.Errorf("expected volume ID 'vol-1', got %q", resp.Volume.VolumeId)
	}
	if got := calls.Load(); got < 3 {
		t.Errorf("expected at least 3 GetVolume calls, got %d", got)
	}
}

func TestCreateVolume_AlreadyExists(t *testing.T) {
	vol := availableVolume("existing-vol", "pvc-123")
	vc := &mockVolumeClient{
		createVolumeFn: func(_ context.Context, _ fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error) {
			return nil, status.Error(codes.AlreadyExists, "already exists")
		},
		listVolumesFn: func(_ context.Context, params fulfillment.ListVolumesParams) ([]*fulfillment.VolumeInfo, error) {
			if params.NameFilter != "pvc-123" {
				t.Errorf("expected name filter 'pvc-123', got %q", params.NameFilter)
			}
			return []*fulfillment.VolumeInfo{vol}, nil
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	resp, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-123",
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Volume.VolumeId != "existing-vol" {
		t.Errorf("expected volume ID 'existing-vol', got %q", resp.Volume.VolumeId)
	}
}

func TestCreateVolume_AlreadyExistsDifferentCapacity(t *testing.T) {
	vc := &mockVolumeClient{
		createVolumeFn: func(_ context.Context, _ fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error) {
			return nil, status.Error(codes.AlreadyExists, "already exists")
		},
		listVolumesFn: func(_ context.Context, _ fulfillment.ListVolumesParams) ([]*fulfillment.VolumeInfo, error) {
			vol := availableVolume("existing-vol", "pvc-123")
			vol.CapacityBytes = 512
			return []*fulfillment.VolumeInfo{vol}, nil
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	_, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-123",
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold"},
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1024},
	})
	assertCode(t, err, codes.AlreadyExists)
}

func TestCreateVolume_AlreadyExistsCompatibleCapacityRange(t *testing.T) {
	vc := &mockVolumeClient{
		createVolumeFn: func(_ context.Context, _ fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error) {
			return nil, status.Error(codes.AlreadyExists, "already exists")
		},
		listVolumesFn: func(_ context.Context, _ fulfillment.ListVolumesParams) ([]*fulfillment.VolumeInfo, error) {
			vol := availableVolume("existing-vol", "pvc-123")
			vol.CapacityBytes = 2048
			return []*fulfillment.VolumeInfo{vol}, nil
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	resp, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-123",
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold"},
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1024, LimitBytes: 4096},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Volume.VolumeId != "existing-vol" {
		t.Errorf("expected volume ID 'existing-vol', got %q", resp.Volume.VolumeId)
	}
}

func TestCreateVolume_AlreadyExistsNotFoundViaList(t *testing.T) {
	vc := &mockVolumeClient{
		createVolumeFn: func(_ context.Context, _ fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error) {
			return nil, status.Error(codes.AlreadyExists, "already exists")
		},
		listVolumesFn: func(_ context.Context, _ fulfillment.ListVolumesParams) ([]*fulfillment.VolumeInfo, error) {
			return nil, nil
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	_, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-123",
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold"},
	})
	assertCode(t, err, codes.Internal)
}

func TestCreateVolume_ErrorState(t *testing.T) {
	vc := &mockVolumeClient{
		createVolumeFn: func(_ context.Context, _ fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error) {
			return &fulfillment.VolumeInfo{
				ID:    "vol-1",
				State: fulfillment.VolumeStateCreating,
			}, nil
		},
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			return &fulfillment.VolumeInfo{
				ID:    volumeID,
				State: fulfillment.VolumeStateError,
			}, nil
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	_, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-123",
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold"},
	})
	assertCode(t, err, codes.Internal)
}

func TestCreateVolume_ContextCancelled(t *testing.T) {
	vc := &mockVolumeClient{
		createVolumeFn: func(_ context.Context, _ fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error) {
			return &fulfillment.VolumeInfo{
				ID:    "vol-1",
				State: fulfillment.VolumeStateCreating,
			}, nil
		},
		getVolumeFn: func(_ context.Context, _ string) (*fulfillment.VolumeInfo, error) {
			return &fulfillment.VolumeInfo{
				ID:    "vol-1",
				State: fulfillment.VolumeStateCreating,
			}, nil
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := cs.CreateVolume(ctx, &csi.CreateVolumeRequest{
		Name:               "pvc-123",
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold"},
	})
	assertCode(t, err, codes.DeadlineExceeded)
}

func TestCreateVolume_MissingName(t *testing.T) {
	cs := newTestController(&mockVolumeClient{}, &mockControlPlaneClient{})

	_, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestCreateVolume_MissingCapabilities(t *testing.T) {
	cs := newTestController(&mockVolumeClient{}, &mockControlPlaneClient{})

	_, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:       "pvc-123",
		Parameters: map[string]string{"tier": "gold"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestCreateVolume_MissingTier(t *testing.T) {
	cs := newTestController(&mockVolumeClient{}, &mockControlPlaneClient{})

	_, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-123",
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestCreateVolume_CreateError(t *testing.T) {
	vc := &mockVolumeClient{
		createVolumeFn: func(_ context.Context, _ fulfillment.CreateVolumeParams) (*fulfillment.VolumeInfo, error) {
			return nil, status.Error(codes.Unavailable, "connection refused")
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	_, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:               "pvc-123",
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold"},
	})
	assertCode(t, err, codes.Unavailable)
}

// --- DeleteVolume tests ---

func TestDeleteVolume_Success(t *testing.T) {
	var deletedID string
	vc := &mockVolumeClient{
		deleteVolumeFn: func(_ context.Context, volumeID string) error {
			deletedID = volumeID
			return nil
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	_, err := cs.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{
		VolumeId: "vol-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deletedID != "vol-1" {
		t.Errorf("expected deleted volume ID 'vol-1', got %q", deletedID)
	}
}

func TestDeleteVolume_MissingVolumeID(t *testing.T) {
	cs := newTestController(&mockVolumeClient{}, &mockControlPlaneClient{})

	_, err := cs.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestDeleteVolume_NotFoundIsSuccess(t *testing.T) {
	vc := &mockVolumeClient{
		deleteVolumeFn: func(_ context.Context, _ string) error {
			return status.Error(codes.NotFound, "volume not found")
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	_, err := cs.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{
		VolumeId: "vol-1",
	})
	if err != nil {
		t.Fatalf("expected success for NotFound, got: %v", err)
	}
}

func TestDeleteVolume_Error(t *testing.T) {
	vc := &mockVolumeClient{
		deleteVolumeFn: func(_ context.Context, _ string) error {
			return status.Error(codes.Unavailable, "service down")
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	_, err := cs.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{
		VolumeId: "vol-1",
	})
	assertCode(t, err, codes.Unavailable)
}

// --- ControllerPublishVolume tests ---

func TestControllerPublishVolume_Success(t *testing.T) {
	var gotVolumeID, gotNodeID string
	cpc := &mockControlPlaneClient{
		publishVolumeFn: func(_ context.Context, volumeID, nodeID string) error {
			gotVolumeID = volumeID
			gotNodeID = nodeID
			return nil
		},
	}
	cs := newTestController(&mockVolumeClient{}, cpc)

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotVolumeID != "vol-1" {
		t.Errorf("expected volume ID 'vol-1', got %q", gotVolumeID)
	}
	if gotNodeID != "node-1" {
		t.Errorf("expected node ID 'node-1', got %q", gotNodeID)
	}
}

func TestControllerPublishVolume_MissingVolumeID(t *testing.T) {
	cs := newTestController(&mockVolumeClient{}, &mockControlPlaneClient{})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "node-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
		},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestControllerPublishVolume_MissingNodeID(t *testing.T) {
	cs := newTestController(&mockVolumeClient{}, &mockControlPlaneClient{})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId: "vol-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
		},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestControllerPublishVolume_MissingCapability(t *testing.T) {
	cs := newTestController(&mockVolumeClient{}, &mockControlPlaneClient{})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestControllerPublishVolume_AlreadyExistsIsSuccess(t *testing.T) {
	cpc := &mockControlPlaneClient{
		publishVolumeFn: func(_ context.Context, _, _ string) error {
			return status.Error(codes.AlreadyExists, "already published")
		},
	}
	cs := newTestController(&mockVolumeClient{}, cpc)

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	if err != nil {
		t.Fatalf("expected success for AlreadyExists, got: %v", err)
	}
}

func TestControllerPublishVolume_Error(t *testing.T) {
	cpc := &mockControlPlaneClient{
		publishVolumeFn: func(_ context.Context, _, _ string) error {
			return status.Error(codes.Unavailable, "service down")
		},
	}
	cs := newTestController(&mockVolumeClient{}, cpc)

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assertCode(t, err, codes.Unavailable)
}

// --- ControllerUnpublishVolume tests ---

func TestControllerUnpublishVolume_Success(t *testing.T) {
	var gotVolumeID, gotNodeID string
	cpc := &mockControlPlaneClient{
		unpublishVolumeFn: func(_ context.Context, volumeID, nodeID string) error {
			gotVolumeID = volumeID
			gotNodeID = nodeID
			return nil
		},
	}
	cs := newTestController(&mockVolumeClient{}, cpc)

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotVolumeID != "vol-1" {
		t.Errorf("expected volume ID 'vol-1', got %q", gotVolumeID)
	}
	if gotNodeID != "node-1" {
		t.Errorf("expected node ID 'node-1', got %q", gotNodeID)
	}
}

func TestControllerUnpublishVolume_MissingVolumeID(t *testing.T) {
	cs := newTestController(&mockVolumeClient{}, &mockControlPlaneClient{})

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		NodeId: "node-1",
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestControllerUnpublishVolume_NotFoundIsSuccess(t *testing.T) {
	cpc := &mockControlPlaneClient{
		unpublishVolumeFn: func(_ context.Context, _, _ string) error {
			return status.Error(codes.NotFound, "not published")
		},
	}
	cs := newTestController(&mockVolumeClient{}, cpc)

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	if err != nil {
		t.Fatalf("expected success for NotFound, got: %v", err)
	}
}

func TestControllerUnpublishVolume_Error(t *testing.T) {
	cpc := &mockControlPlaneClient{
		unpublishVolumeFn: func(_ context.Context, _, _ string) error {
			return status.Error(codes.Unavailable, "service down")
		},
	}
	cs := newTestController(&mockVolumeClient{}, cpc)

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	assertCode(t, err, codes.Unavailable)
}

// --- ValidateVolumeCapabilities tests ---

func TestValidateVolumeCapabilities_Success(t *testing.T) {
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			return availableVolume(volumeID, "pvc-123"), nil
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	resp, err := cs.ValidateVolumeCapabilities(context.Background(), &csi.ValidateVolumeCapabilitiesRequest{
		VolumeId:           "vol-1",
		VolumeCapabilities: defaultCaps(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Confirmed == nil {
		t.Fatal("expected capabilities to be confirmed")
	}
}

func TestValidateVolumeCapabilities_VolumeNotFound(t *testing.T) {
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			return nil, status.Errorf(codes.NotFound, "not found")
		},
	}
	cs := newTestController(vc, &mockControlPlaneClient{})

	_, err := cs.ValidateVolumeCapabilities(context.Background(), &csi.ValidateVolumeCapabilitiesRequest{
		VolumeId:           "vol-1",
		VolumeCapabilities: defaultCaps(),
	})
	assertCode(t, err, codes.NotFound)
}

// --- ControllerGetCapabilities tests ---

func TestControllerGetCapabilities(t *testing.T) {
	cs := newTestController(&mockVolumeClient{}, &mockControlPlaneClient{})

	resp, err := cs.ControllerGetCapabilities(context.Background(), &csi.ControllerGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Capabilities) != 2 {
		t.Fatalf("expected 2 capabilities, got %d", len(resp.Capabilities))
	}

	found := make(map[csi.ControllerServiceCapability_RPC_Type]bool)
	for _, cap := range resp.Capabilities {
		found[cap.GetRpc().GetType()] = true
	}
	if !found[csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME] {
		t.Error("missing CREATE_DELETE_VOLUME capability")
	}
	if !found[csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME] {
		t.Error("missing PUBLISH_UNPUBLISH_VOLUME capability")
	}
}
