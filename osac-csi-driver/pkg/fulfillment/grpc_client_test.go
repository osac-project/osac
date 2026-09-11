package fulfillment

import (
	"context"
	"math"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// fakeVolumesClient is a test double for the generated privatev1.VolumesClient.
// It records the last request of each type and returns canned responses/errors.
type fakeVolumesClient struct {
	createReq  *privatev1.VolumesCreateRequest
	createResp *privatev1.VolumesCreateResponse
	createErr  error

	getReq  *privatev1.VolumesGetRequest
	getResp *privatev1.VolumesGetResponse
	getErr  error

	listReq  *privatev1.VolumesListRequest
	listResp *privatev1.VolumesListResponse
	listErr  error

	deleteReq *privatev1.VolumesDeleteRequest
	deleteErr error
}

func (f *fakeVolumesClient) Create(_ context.Context, in *privatev1.VolumesCreateRequest, _ ...grpc.CallOption) (*privatev1.VolumesCreateResponse, error) {
	f.createReq = in
	return f.createResp, f.createErr
}

func (f *fakeVolumesClient) Get(_ context.Context, in *privatev1.VolumesGetRequest, _ ...grpc.CallOption) (*privatev1.VolumesGetResponse, error) {
	f.getReq = in
	return f.getResp, f.getErr
}

func (f *fakeVolumesClient) List(_ context.Context, in *privatev1.VolumesListRequest, _ ...grpc.CallOption) (*privatev1.VolumesListResponse, error) {
	f.listReq = in
	return f.listResp, f.listErr
}

func (f *fakeVolumesClient) Delete(_ context.Context, in *privatev1.VolumesDeleteRequest, _ ...grpc.CallOption) (*privatev1.VolumesDeleteResponse, error) {
	f.deleteReq = in
	return &privatev1.VolumesDeleteResponse{}, f.deleteErr
}

func (f *fakeVolumesClient) Update(_ context.Context, _ *privatev1.VolumesUpdateRequest, _ ...grpc.CallOption) (*privatev1.VolumesUpdateResponse, error) {
	return nil, nil
}

func (f *fakeVolumesClient) Signal(_ context.Context, _ *privatev1.VolumesSignalRequest, _ ...grpc.CallOption) (*privatev1.VolumesSignalResponse, error) {
	return nil, nil
}

// newTestVolume builds a proto Volume with the given fields for response fixtures.
func newTestVolume(id, name string, state privatev1.VolumeState, backend, vendorID string, proto privatev1.StorageProtocol, sizeGiB int64) *privatev1.Volume {
	md := &privatev1.Metadata{}
	md.SetName(name)
	spec := &privatev1.VolumeSpec{}
	spec.SetSizeGib(sizeGiB)
	st := &privatev1.VolumeStatus{}
	st.SetState(state)
	st.SetBackend(backend)
	st.SetVendorVolumeId(vendorID)
	st.SetProtocol(proto)
	v := &privatev1.Volume{}
	v.SetId(id)
	v.SetMetadata(md)
	v.SetSpec(spec)
	v.SetStatus(st)
	return v
}

func TestCreateVolumeMapsRequestAndResponse(t *testing.T) {
	resp := &privatev1.VolumesCreateResponse{}
	resp.SetObject(newTestVolume("vol-1", "pvc-abc", privatev1.VolumeState_VOLUME_STATE_CREATING,
		"vast-backend", "", privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS, 5))
	fake := &fakeVolumesClient{createResp: resp}
	c := &grpcVolumeClient{client: fake}

	info, err := c.CreateVolume(context.Background(), CreateVolumeParams{
		Tenant:     "tenant-a",
		Tier:       "gold",
		SizeBytes:  5 * bytesPerGiB,
		AccessMode: "SINGLE_NODE_WRITER",
		PVCRef:     "pvc-abc",
	})
	if err != nil {
		t.Fatalf("CreateVolume returned error: %v", err)
	}

	// Request mapping.
	gotObj := fake.createReq.GetObject()
	if got := gotObj.GetMetadata().GetName(); got != "pvc-abc" {
		t.Errorf("metadata.name = %q, want %q", got, "pvc-abc")
	}
	if got := gotObj.GetMetadata().GetTenant(); got != "tenant-a" {
		t.Errorf("metadata.tenant = %q, want %q", got, "tenant-a")
	}
	if got := gotObj.GetSpec().GetStorageTier(); got != "gold" {
		t.Errorf("spec.storage_tier = %q, want %q", got, "gold")
	}
	if got := gotObj.GetSpec().GetSizeGib(); got != 5 {
		t.Errorf("spec.size_gib = %d, want 5", got)
	}
	if got := gotObj.GetSpec().GetAccessMode(); got != privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE {
		t.Errorf("spec.access_mode = %v, want READ_WRITE_ONCE", got)
	}

	// Response mapping.
	if info.ID != "vol-1" || info.Name != "pvc-abc" {
		t.Errorf("info id/name = %q/%q, want vol-1/pvc-abc", info.ID, info.Name)
	}
	if info.State != VolumeStateCreating {
		t.Errorf("info.State = %q, want CREATING", info.State)
	}
	if info.Backend != "vast-backend" {
		t.Errorf("info.Backend = %q, want vast-backend", info.Backend)
	}
	if info.CapacityBytes != 5*bytesPerGiB {
		t.Errorf("info.CapacityBytes = %d, want %d", info.CapacityBytes, 5*bytesPerGiB)
	}
}

func TestCreateVolumePropagatesAlreadyExists(t *testing.T) {
	fake := &fakeVolumesClient{createErr: status.Error(codes.AlreadyExists, "dup")}
	c := &grpcVolumeClient{client: fake}

	_, err := c.CreateVolume(context.Background(), CreateVolumeParams{Tenant: "t", Tier: "gold", SizeBytes: bytesPerGiB, AccessMode: "SINGLE_NODE_WRITER", PVCRef: "pvc-x"})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists, got %v", err)
	}
}

func TestListVolumesBuildsNameFilter(t *testing.T) {
	resp := &privatev1.VolumesListResponse{}
	resp.SetItems([]*privatev1.Volume{
		newTestVolume("vol-1", "pvc-abc", privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
			"vast-backend", "vendor-9", privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK, 2),
	})
	fake := &fakeVolumesClient{listResp: resp}
	c := &grpcVolumeClient{client: fake}

	infos, err := c.ListVolumes(context.Background(), ListVolumesParams{NameFilter: "pvc-abc"})
	if err != nil {
		t.Fatalf("ListVolumes error: %v", err)
	}
	if got, want := fake.listReq.GetFilter(), `this.metadata.name == "pvc-abc"`; got != want {
		t.Errorf("filter = %q, want %q", got, want)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(infos))
	}
	if infos[0].VendorVolumeID != "vendor-9" {
		t.Errorf("VendorVolumeID = %q, want vendor-9", infos[0].VendorVolumeID)
	}
	if infos[0].Protocol != "block" {
		t.Errorf("Protocol = %q, want block", infos[0].Protocol)
	}
	if infos[0].State != VolumeStateAvailable {
		t.Errorf("State = %q, want AVAILABLE", infos[0].State)
	}
}

func TestListVolumesNoFilterOmitsFilter(t *testing.T) {
	fake := &fakeVolumesClient{listResp: &privatev1.VolumesListResponse{}}
	c := &grpcVolumeClient{client: fake}
	if _, err := c.ListVolumes(context.Background(), ListVolumesParams{}); err != nil {
		t.Fatalf("ListVolumes error: %v", err)
	}
	if got := fake.listReq.GetFilter(); got != "" {
		t.Errorf("expected empty filter, got %q", got)
	}
}

func TestGetAndDeleteVolumeMapIDs(t *testing.T) {
	getResp := &privatev1.VolumesGetResponse{}
	getResp.SetObject(newTestVolume("vol-7", "pvc-z", privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
		"b", "vendor-7", privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS, 1))
	fake := &fakeVolumesClient{getResp: getResp}
	c := &grpcVolumeClient{client: fake}

	info, err := c.GetVolume(context.Background(), "vol-7")
	if err != nil {
		t.Fatalf("GetVolume error: %v", err)
	}
	if fake.getReq.GetId() != "vol-7" {
		t.Errorf("get id = %q, want vol-7", fake.getReq.GetId())
	}
	if info.ID != "vol-7" {
		t.Errorf("info.ID = %q, want vol-7", info.ID)
	}

	if err := c.DeleteVolume(context.Background(), "vol-7"); err != nil {
		t.Fatalf("DeleteVolume error: %v", err)
	}
	if fake.deleteReq.GetId() != "vol-7" {
		t.Errorf("delete id = %q, want vol-7", fake.deleteReq.GetId())
	}
}

func TestGetVolumePropagatesNotFound(t *testing.T) {
	fake := &fakeVolumesClient{getErr: status.Error(codes.NotFound, "missing")}
	c := &grpcVolumeClient{client: fake}
	if _, err := c.GetVolume(context.Background(), "nope"); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestBytesToGiB(t *testing.T) {
	cases := []struct {
		in   int64
		want int64
	}{
		{0, 0},
		{-1, 0},
		{1, 1},
		{bytesPerGiB, 1},
		{bytesPerGiB + 1, 2},
		{5 * bytesPerGiB, 5},
		{5*bytesPerGiB - 1, 5},
		{1 << 62, 1 << 32}, // exact: 2^62 bytes / 2^30 = 2^32 GiB
	}
	for _, tc := range cases {
		if got := bytesToGiB(tc.in); got != tc.want {
			t.Errorf("bytesToGiB(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
	// Near-max input must not overflow to a negative/zero result.
	if got := bytesToGiB(math.MaxInt64); got <= 0 {
		t.Errorf("bytesToGiB(MaxInt64) = %d, want a positive value (no overflow)", got)
	}
}

func TestGibToBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want int64
	}{
		{0, 0},
		{-1, 0},
		{1, bytesPerGiB},
		{5, 5 * bytesPerGiB},
		{math.MaxInt64, math.MaxInt64}, // clamped, no overflow
		{math.MaxInt64 / bytesPerGiB, (math.MaxInt64 / bytesPerGiB) * bytesPerGiB},
	}
	for _, tc := range cases {
		if got := gibToBytes(tc.in); got != tc.want {
			t.Errorf("gibToBytes(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestToProtoAccessMode(t *testing.T) {
	cases := map[string]privatev1.VolumeAccessMode{
		"SINGLE_NODE_WRITER":        privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
		"SINGLE_NODE_MULTI_WRITER":  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
		"SINGLE_NODE_SINGLE_WRITER": privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE_POD,
		"SINGLE_NODE_READER_ONLY":   privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_ONLY_MANY,
		"MULTI_NODE_READER_ONLY":    privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_ONLY_MANY,
		"MULTI_NODE_SINGLE_WRITER":  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_MANY,
		"MULTI_NODE_MULTI_WRITER":   privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_MANY,
		"UNKNOWN":                   privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_UNSPECIFIED,
		"":                          privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := toProtoAccessMode(in); got != want {
			t.Errorf("toProtoAccessMode(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFromProtoStateAndProtocol(t *testing.T) {
	states := map[privatev1.VolumeState]VolumeState{
		privatev1.VolumeState_VOLUME_STATE_CREATING:    VolumeStateCreating,
		privatev1.VolumeState_VOLUME_STATE_AVAILABLE:   VolumeStateAvailable,
		privatev1.VolumeState_VOLUME_STATE_DELETING:    VolumeStateDeleting,
		privatev1.VolumeState_VOLUME_STATE_DELETED:     VolumeStateDeleting,
		privatev1.VolumeState_VOLUME_STATE_FAILED:      VolumeStateError,
		privatev1.VolumeState_VOLUME_STATE_UNSPECIFIED: VolumeState(""),
	}
	for in, want := range states {
		if got := fromProtoState(in); got != want {
			t.Errorf("fromProtoState(%v) = %q, want %q", in, got, want)
		}
	}

	protocols := map[privatev1.StorageProtocol]string{
		privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS:         "nfs",
		privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK:       "block",
		privatev1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED: "",
	}
	for in, want := range protocols {
		if got := fromProtoProtocol(in); got != want {
			t.Errorf("fromProtoProtocol(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestCreateVolumeNilObjectErrors(t *testing.T) {
	// Server returns a response with no object; the client must surface an error
	// rather than (nil, nil), which the controller's poll loop would panic on.
	fake := &fakeVolumesClient{createResp: &privatev1.VolumesCreateResponse{}}
	c := &grpcVolumeClient{client: fake}
	if _, err := c.CreateVolume(context.Background(), CreateVolumeParams{
		Tenant: "t", Tier: "gold", SizeBytes: bytesPerGiB, AccessMode: "SINGLE_NODE_WRITER", PVCRef: "pvc-x",
	}); status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal error for nil object, got %v", err)
	}
}

func TestGetVolumeNilObjectErrors(t *testing.T) {
	fake := &fakeVolumesClient{getResp: &privatev1.VolumesGetResponse{}}
	c := &grpcVolumeClient{client: fake}
	if _, err := c.GetVolume(context.Background(), "vol-1"); status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal error for nil object, got %v", err)
	}
}
