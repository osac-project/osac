package driver

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/osac-project/osac/osac-csi-driver/pkg/fulfillment"
	"github.com/osac-project/osac/osac-csi-driver/pkg/proxy"
	"google.golang.org/grpc"
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

// fakeVendorController is an in-process CSI controller used to exercise the
// controller-side vendor proxy. It records the requests it receives and can be
// configured to return a specific response or error.
type fakeVendorController struct {
	csi.UnimplementedControllerServer

	mu            sync.Mutex
	publishReqs   []*csi.ControllerPublishVolumeRequest
	unpublishReqs []*csi.ControllerUnpublishVolumeRequest

	publishResp  *csi.ControllerPublishVolumeResponse
	publishErr   error
	unpublishErr error
}

func (f *fakeVendorController) ControllerPublishVolume(_ context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	f.mu.Lock()
	f.publishReqs = append(f.publishReqs, req)
	resp, err := f.publishResp, f.publishErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if resp != nil {
		return resp, nil
	}
	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (f *fakeVendorController) ControllerUnpublishVolume(_ context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	f.mu.Lock()
	f.unpublishReqs = append(f.unpublishReqs, req)
	err := f.unpublishErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (f *fakeVendorController) lastPublish() *csi.ControllerPublishVolumeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.publishReqs) == 0 {
		return nil
	}
	return f.publishReqs[len(f.publishReqs)-1]
}

func (f *fakeVendorController) lastUnpublish() *csi.ControllerUnpublishVolumeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.unpublishReqs) == 0 {
		return nil
	}
	return f.unpublishReqs[len(f.unpublishReqs)-1]
}

// startFakeVendorController serves the given controller on a unix socket and
// returns the socket path. The server is stopped when the test finishes.
func startFakeVendorController(t *testing.T, vendor csi.ControllerServer) string {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "vendor.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listening on vendor socket: %v", err)
	}
	srv := grpc.NewServer()
	csi.RegisterControllerServer(srv, vendor)
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)
	return socketPath
}

// --- helpers ---

func newTestController(vc fulfillment.VolumeClient) *ControllerServer {
	return newTestControllerWithVendor(vc, nil)
}

func newTestControllerWithVendor(vc fulfillment.VolumeClient, vendorControllers map[string]string) *ControllerServer {
	cs := NewControllerServer(vc, proxy.NewManager(nil), vendorControllers, "test-cluster")
	cs.pollInitialInterval = 1 * time.Millisecond
	cs.pollMaxInterval = 5 * time.Millisecond
	return cs
}

func singleCap() *csi.VolumeCapability {
	return defaultCaps()[0]
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
	cs := newTestController(vc)

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
	cs := newTestController(vc)

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
	cs := newTestController(vc)

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
	cs := newTestController(vc)

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
	cs := newTestController(vc)

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
	cs := newTestController(vc)

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
	cs := newTestController(vc)

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
	cs := newTestController(vc)

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
	cs := newTestController(vc)

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
	cs := newTestController(&mockVolumeClient{})

	_, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		VolumeCapabilities: defaultCaps(),
		Parameters:         map[string]string{"tier": "gold"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestCreateVolume_MissingCapabilities(t *testing.T) {
	cs := newTestController(&mockVolumeClient{})

	_, err := cs.CreateVolume(context.Background(), &csi.CreateVolumeRequest{
		Name:       "pvc-123",
		Parameters: map[string]string{"tier": "gold"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestCreateVolume_MissingTier(t *testing.T) {
	cs := newTestController(&mockVolumeClient{})

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
	cs := newTestController(vc)

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
	cs := newTestController(vc)

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
	cs := newTestController(&mockVolumeClient{})

	_, err := cs.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestDeleteVolume_NotFoundIsSuccess(t *testing.T) {
	vc := &mockVolumeClient{
		deleteVolumeFn: func(_ context.Context, _ string) error {
			return status.Error(codes.NotFound, "volume not found")
		},
	}
	cs := newTestController(vc)

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
	cs := newTestController(vc)

	_, err := cs.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{
		VolumeId: "vol-1",
	})
	assertCode(t, err, codes.Unavailable)
}

// --- ControllerPublishVolume tests ---

func TestControllerPublishVolume_Success(t *testing.T) {
	vendor := &fakeVendorController{
		publishResp: &csi.ControllerPublishVolumeResponse{
			PublishContext: map[string]string{"vendor.device": "/dev/sdx"},
		},
	}
	socket := startFakeVendorController(t, vendor)
	cs := newTestControllerWithVendor(&mockVolumeClient{}, map[string]string{"test-backend": socket})

	resp, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId:         "vol-1",
		NodeId:           "node-1",
		VolumeCapability: singleCap(),
		VolumeContext: map[string]string{
			"osac.backend":   "test-backend",
			"osac.volume-id": "vendor-vol-1",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The vendor's publish context must be forwarded back to the CO.
	if resp.GetPublishContext()["vendor.device"] != "/dev/sdx" {
		t.Errorf("expected vendor publish context to be forwarded, got %v", resp.GetPublishContext())
	}
	last := vendor.lastPublish()
	if last == nil {
		t.Fatal("vendor did not receive a publish request")
	}
	// The fulfillment volume id must be translated to the vendor volume id.
	if last.GetVolumeId() != "vendor-vol-1" {
		t.Errorf("expected vendor volume ID 'vendor-vol-1', got %q", last.GetVolumeId())
	}
	if last.GetNodeId() != "node-1" {
		t.Errorf("expected node ID 'node-1', got %q", last.GetNodeId())
	}
}

// When the request carries no volume context (or partial context), the backend
// and vendor volume id are resolved from the fulfillment service.
func TestControllerPublishVolume_ResolvesViaVolumeClient(t *testing.T) {
	vendor := &fakeVendorController{}
	socket := startFakeVendorController(t, vendor)
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			return availableVolume(volumeID, "pvc-123"), nil
		},
	}
	cs := newTestControllerWithVendor(vc, map[string]string{"test-backend": socket})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId:         "vol-1",
		NodeId:           "node-1",
		VolumeCapability: singleCap(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last := vendor.lastPublish()
	if last == nil || last.GetVolumeId() != "vendor-vol-1" {
		t.Fatalf("expected vendor volume ID 'vendor-vol-1' resolved via GetVolume, got %v", last)
	}
}

func TestControllerPublishVolume_MissingVolumeID(t *testing.T) {
	cs := newTestController(&mockVolumeClient{})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId:           "node-1",
		VolumeCapability: singleCap(),
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestControllerPublishVolume_MissingNodeID(t *testing.T) {
	cs := newTestController(&mockVolumeClient{})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId:         "vol-1",
		VolumeCapability: singleCap(),
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestControllerPublishVolume_MissingCapability(t *testing.T) {
	cs := newTestController(&mockVolumeClient{})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	assertCode(t, err, codes.InvalidArgument)
}

// An unknown backend has no configured vendor controller to route to.
func TestControllerPublishVolume_UnknownBackend(t *testing.T) {
	cs := newTestControllerWithVendor(&mockVolumeClient{}, map[string]string{})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId:         "vol-1",
		NodeId:           "node-1",
		VolumeCapability: singleCap(),
		VolumeContext: map[string]string{
			"osac.backend":   "test-backend",
			"osac.volume-id": "vendor-vol-1",
		},
	})
	assertCode(t, err, codes.NotFound)
}

// A nonexistent volume with no context cannot be resolved for publish.
func TestControllerPublishVolume_VolumeNotFound(t *testing.T) {
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, _ string) (*fulfillment.VolumeInfo, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	cs := newTestControllerWithVendor(vc, map[string]string{"test-backend": "unused"})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId:         "vol-1",
		NodeId:           "node-1",
		VolumeCapability: singleCap(),
	})
	assertCode(t, err, codes.NotFound)
}

func TestControllerPublishVolume_AlreadyExistsIsSuccess(t *testing.T) {
	vendor := &fakeVendorController{
		publishErr: status.Error(codes.AlreadyExists, "already published"),
	}
	socket := startFakeVendorController(t, vendor)
	cs := newTestControllerWithVendor(&mockVolumeClient{}, map[string]string{"test-backend": socket})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId:         "vol-1",
		NodeId:           "node-1",
		VolumeCapability: singleCap(),
		VolumeContext: map[string]string{
			"osac.backend":   "test-backend",
			"osac.volume-id": "vendor-vol-1",
		},
	})
	if err != nil {
		t.Fatalf("expected success for AlreadyExists, got: %v", err)
	}
}

// NFS-style vendors that do not implement controller attach are treated as a no-op.
func TestControllerPublishVolume_UnimplementedIsSuccess(t *testing.T) {
	vendor := &fakeVendorController{
		publishErr: status.Error(codes.Unimplemented, "not implemented"),
	}
	socket := startFakeVendorController(t, vendor)
	cs := newTestControllerWithVendor(&mockVolumeClient{}, map[string]string{"test-backend": socket})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId:         "vol-1",
		NodeId:           "node-1",
		VolumeCapability: singleCap(),
		VolumeContext: map[string]string{
			"osac.backend":   "test-backend",
			"osac.volume-id": "vendor-vol-1",
		},
	})
	if err != nil {
		t.Fatalf("expected success for Unimplemented, got: %v", err)
	}
}

func TestControllerPublishVolume_Error(t *testing.T) {
	vendor := &fakeVendorController{
		publishErr: status.Error(codes.Unavailable, "service down"),
	}
	socket := startFakeVendorController(t, vendor)
	cs := newTestControllerWithVendor(&mockVolumeClient{}, map[string]string{"test-backend": socket})

	_, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId:         "vol-1",
		NodeId:           "node-1",
		VolumeCapability: singleCap(),
		VolumeContext: map[string]string{
			"osac.backend":   "test-backend",
			"osac.volume-id": "vendor-vol-1",
		},
	})
	assertCode(t, err, codes.Unavailable)
}

// A backend mapped to the "none" sentinel (node-local storage such as lvms)
// needs no controller-side attach, so publish is a no-op and no vendor
// controller is dialed.
func TestControllerPublishVolume_NoAttachBackendIsNoop(t *testing.T) {
	cs := newTestControllerWithVendor(&mockVolumeClient{}, map[string]string{"local": noAttachEndpoint})

	resp, err := cs.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
		VolumeId:         "vol-1",
		NodeId:           "node-1",
		VolumeCapability: singleCap(),
		VolumeContext: map[string]string{
			"osac.backend":   "local",
			"osac.volume-id": "vendor-vol-1",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.GetPublishContext()) != 0 {
		t.Errorf("expected empty publish context for no-attach backend, got %v", resp.GetPublishContext())
	}
}

// --- ControllerUnpublishVolume tests ---

func TestControllerUnpublishVolume_Success(t *testing.T) {
	vendor := &fakeVendorController{}
	socket := startFakeVendorController(t, vendor)
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			return availableVolume(volumeID, "pvc-123"), nil
		},
	}
	cs := newTestControllerWithVendor(vc, map[string]string{"test-backend": socket})

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last := vendor.lastUnpublish()
	if last == nil {
		t.Fatal("vendor did not receive an unpublish request")
	}
	// The backend and vendor volume id are resolved from the fulfillment service
	// because the unpublish request carries no volume context.
	if last.GetVolumeId() != "vendor-vol-1" {
		t.Errorf("expected vendor volume ID 'vendor-vol-1', got %q", last.GetVolumeId())
	}
	if last.GetNodeId() != "node-1" {
		t.Errorf("expected node ID 'node-1', got %q", last.GetNodeId())
	}
}

func TestControllerUnpublishVolume_MissingVolumeID(t *testing.T) {
	cs := newTestController(&mockVolumeClient{})

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		NodeId: "node-1",
	})
	assertCode(t, err, codes.InvalidArgument)
}

// A volume the fulfillment service no longer knows about is already detached.
func TestControllerUnpublishVolume_VolumeNotFoundIsSuccess(t *testing.T) {
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, _ string) (*fulfillment.VolumeInfo, error) {
			return nil, status.Error(codes.NotFound, "volume not found")
		},
	}
	cs := newTestControllerWithVendor(vc, nil)

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	if err != nil {
		t.Fatalf("expected success when volume not found, got: %v", err)
	}
}

func TestControllerUnpublishVolume_VendorNotFoundIsSuccess(t *testing.T) {
	vendor := &fakeVendorController{
		unpublishErr: status.Error(codes.NotFound, "not published"),
	}
	socket := startFakeVendorController(t, vendor)
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			return availableVolume(volumeID, "pvc-123"), nil
		},
	}
	cs := newTestControllerWithVendor(vc, map[string]string{"test-backend": socket})

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	if err != nil {
		t.Fatalf("expected success for NotFound, got: %v", err)
	}
}

func TestControllerUnpublishVolume_UnimplementedIsSuccess(t *testing.T) {
	vendor := &fakeVendorController{
		unpublishErr: status.Error(codes.Unimplemented, "not implemented"),
	}
	socket := startFakeVendorController(t, vendor)
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			return availableVolume(volumeID, "pvc-123"), nil
		},
	}
	cs := newTestControllerWithVendor(vc, map[string]string{"test-backend": socket})

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	if err != nil {
		t.Fatalf("expected success for Unimplemented, got: %v", err)
	}
}

func TestControllerUnpublishVolume_UnknownBackend(t *testing.T) {
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			return availableVolume(volumeID, "pvc-123"), nil
		},
	}
	cs := newTestControllerWithVendor(vc, map[string]string{})

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	assertCode(t, err, codes.NotFound)
}

func TestControllerUnpublishVolume_Error(t *testing.T) {
	vendor := &fakeVendorController{
		unpublishErr: status.Error(codes.Unavailable, "service down"),
	}
	socket := startFakeVendorController(t, vendor)
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			return availableVolume(volumeID, "pvc-123"), nil
		},
	}
	cs := newTestControllerWithVendor(vc, map[string]string{"test-backend": socket})

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	assertCode(t, err, codes.Unavailable)
}

// A backend mapped to the "none" sentinel needs no controller-side detach, so
// unpublish is a no-op and no vendor controller is dialed.
func TestControllerUnpublishVolume_NoAttachBackendIsNoop(t *testing.T) {
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			vol := availableVolume(volumeID, "pvc-123")
			vol.Backend = "local"
			return vol, nil
		},
	}
	cs := newTestControllerWithVendor(vc, map[string]string{"local": noAttachEndpoint})

	_, err := cs.ControllerUnpublishVolume(context.Background(), &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "vol-1",
		NodeId:   "node-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- ValidateVolumeCapabilities tests ---

func TestValidateVolumeCapabilities_Success(t *testing.T) {
	vc := &mockVolumeClient{
		getVolumeFn: func(_ context.Context, volumeID string) (*fulfillment.VolumeInfo, error) {
			return availableVolume(volumeID, "pvc-123"), nil
		},
	}
	cs := newTestController(vc)

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
	cs := newTestController(vc)

	_, err := cs.ValidateVolumeCapabilities(context.Background(), &csi.ValidateVolumeCapabilitiesRequest{
		VolumeId:           "vol-1",
		VolumeCapabilities: defaultCaps(),
	})
	assertCode(t, err, codes.NotFound)
}

// --- ControllerGetCapabilities tests ---

func TestControllerGetCapabilities(t *testing.T) {
	cs := newTestController(&mockVolumeClient{})

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
