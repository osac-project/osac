package driver

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/osac-project/osac/osac-csi-driver/pkg/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeNodePlugin struct {
	csi.UnimplementedNodeServer
	stageCalled     bool
	unstageCalled   bool
	publishCalled   bool
	unpublishCalled bool
	statsCalled     bool

	stageErr     error
	unstageErr   error
	publishErr   error
	unpublishErr error
	statsResp    *csi.NodeGetVolumeStatsResponse
	statsErr     error
}

func (f *fakeNodePlugin) NodeStageVolume(_ context.Context, _ *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	f.stageCalled = true
	if f.stageErr != nil {
		return nil, f.stageErr
	}
	return &csi.NodeStageVolumeResponse{}, nil
}

func (f *fakeNodePlugin) NodeUnstageVolume(_ context.Context, _ *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	f.unstageCalled = true
	if f.unstageErr != nil {
		return nil, f.unstageErr
	}
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (f *fakeNodePlugin) NodePublishVolume(_ context.Context, _ *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	f.publishCalled = true
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

func (f *fakeNodePlugin) NodeUnpublishVolume(_ context.Context, _ *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	f.unpublishCalled = true
	if f.unpublishErr != nil {
		return nil, f.unpublishErr
	}
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (f *fakeNodePlugin) NodeGetVolumeStats(_ context.Context, _ *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	f.statsCalled = true
	if f.statsErr != nil {
		return nil, f.statsErr
	}
	if f.statsResp != nil {
		return f.statsResp, nil
	}
	return &csi.NodeGetVolumeStatsResponse{}, nil
}

func startFakeNodePlugin(t *testing.T) (socketPath string, plugin *fakeNodePlugin, cleanup func()) {
	t.Helper()
	socketPath = filepath.Join(t.TempDir(), "vendor.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	plugin = &fakeNodePlugin{}
	srv := grpc.NewServer()
	csi.RegisterNodeServer(srv, plugin)

	go func() { _ = srv.Serve(listener) }()

	return socketPath, plugin, func() { srv.GracefulStop() }
}

func newTestNodeServer(t *testing.T, backend string, socketPath string) *NodeServer {
	t.Helper()
	vendorSockets := map[string]string{backend: socketPath}
	proxyMgr := proxy.NewManager(vendorSockets)
	return NewNodeServer("test-node", proxyMgr, vendorSockets)
}

func assertGRPCCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != want {
		t.Errorf("expected code %s, got %s: %s", want, st.Code(), st.Message())
	}
}

func blockCap() *csi.VolumeCapability {
	return &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}
}

// --- resolveVendorSocket ---

func TestResolveVendorSocket(t *testing.T) {
	ns := &NodeServer{vendorSockets: map[string]string{"netapp": "/sockets/netapp.sock"}}

	t.Run("valid backend", func(t *testing.T) {
		sock, err := ns.resolveVendorSocket(map[string]string{"osac.backend": "netapp"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sock != "/sockets/netapp.sock" {
			t.Errorf("expected /sockets/netapp.sock, got %s", sock)
		}
	})

	t.Run("missing osac.backend key", func(t *testing.T) {
		_, err := ns.resolveVendorSocket(map[string]string{"other": "value"})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("nil volume context", func(t *testing.T) {
		_, err := ns.resolveVendorSocket(nil)
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("empty backend value", func(t *testing.T) {
		_, err := ns.resolveVendorSocket(map[string]string{"osac.backend": ""})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("unknown backend", func(t *testing.T) {
		_, err := ns.resolveVendorSocket(map[string]string{"osac.backend": "unknown"})
		assertGRPCCode(t, err, codes.NotFound)
	})
}

// --- volumeBackends map ---

func TestBackendMapLifecycle(t *testing.T) {
	ns := NewNodeServer("node", proxy.NewManager(nil), map[string]string{"ceph": "/sockets/ceph.sock"})

	t.Run("record and lookup", func(t *testing.T) {
		ns.recordBackend("vol-1", map[string]string{"osac.backend": "ceph"})
		sock, err := ns.lookupBackendSocket("vol-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sock != "/sockets/ceph.sock" {
			t.Errorf("expected /sockets/ceph.sock, got %s", sock)
		}
	})

	t.Run("forget removes entry", func(t *testing.T) {
		ns.recordBackend("vol-2", map[string]string{"osac.backend": "ceph"})
		ns.forgetBackend("vol-2")
		_, err := ns.lookupBackendSocket("vol-2")
		assertGRPCCode(t, err, codes.FailedPrecondition)
	})

	t.Run("lookup unknown volume", func(t *testing.T) {
		_, err := ns.lookupBackendSocket("vol-never-seen")
		assertGRPCCode(t, err, codes.FailedPrecondition)
	})

	t.Run("record with nil context is no-op", func(t *testing.T) {
		ns.recordBackend("vol-nil", nil)
		_, err := ns.lookupBackendSocket("vol-nil")
		assertGRPCCode(t, err, codes.FailedPrecondition)
	})

	t.Run("record with empty backend is no-op", func(t *testing.T) {
		ns.recordBackend("vol-empty", map[string]string{"osac.backend": ""})
		_, err := ns.lookupBackendSocket("vol-empty")
		assertGRPCCode(t, err, codes.FailedPrecondition)
	})

	t.Run("lookup succeeds but socket missing from vendorSockets", func(t *testing.T) {
		ns.mu.Lock()
		ns.volumeBackends["vol-orphan"] = "gone-backend"
		ns.mu.Unlock()
		_, err := ns.lookupBackendSocket("vol-orphan")
		assertGRPCCode(t, err, codes.NotFound)
	})
}

// --- NodeStageVolume ---

func TestNodeStageVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("routes to vendor and records backend", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		ns := newTestNodeServer(t, "vendor-a", sock)

		resp, err := ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
			VolumeId:          "vol-1",
			StagingTargetPath: "/staging/vol-1",
			VolumeCapability:  blockCap(),
			VolumeContext:     map[string]string{"osac.backend": "vendor-a"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		if !plugin.stageCalled {
			t.Error("vendor NodeStageVolume was not called")
		}
		// Backend should be recorded
		_, err = ns.lookupBackendSocket("vol-1")
		if err != nil {
			t.Errorf("backend not recorded after stage: %v", err)
		}
	})

	t.Run("missing volume ID", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
			StagingTargetPath: "/staging",
			VolumeCapability:  blockCap(),
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("missing staging path", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
			VolumeId:         "vol-1",
			VolumeCapability: blockCap(),
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("missing volume capability", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
			VolumeId:          "vol-1",
			StagingTargetPath: "/staging",
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("missing osac.backend returns error", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
			VolumeId:          "vol-1",
			StagingTargetPath: "/staging",
			VolumeCapability:  blockCap(),
			VolumeContext:     map[string]string{},
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("vendor unimplemented treated as no-op and records backend", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		plugin.stageErr = status.Error(codes.Unimplemented, "not supported")
		ns := newTestNodeServer(t, "vendor-b", sock)

		resp, err := ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
			VolumeId:          "vol-u",
			StagingTargetPath: "/staging/vol-u",
			VolumeCapability:  blockCap(),
			VolumeContext:     map[string]string{"osac.backend": "vendor-b"},
		})
		if err != nil {
			t.Fatalf("expected no error for unimplemented, got: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		_, err = ns.lookupBackendSocket("vol-u")
		if err != nil {
			t.Errorf("backend not recorded after unimplemented stage: %v", err)
		}
	})

	t.Run("vendor error propagated", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		plugin.stageErr = status.Error(codes.Internal, "disk error")
		ns := newTestNodeServer(t, "vendor-c", sock)

		_, err := ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
			VolumeId:          "vol-err",
			StagingTargetPath: "/staging/vol-err",
			VolumeCapability:  blockCap(),
			VolumeContext:     map[string]string{"osac.backend": "vendor-c"},
		})
		assertGRPCCode(t, err, codes.Internal)
	})
}

// --- NodeUnstageVolume ---

func TestNodeUnstageVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("routes via backend map and forgets on success", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		ns := newTestNodeServer(t, "vendor-a", sock)
		ns.recordBackend("vol-1", map[string]string{"osac.backend": "vendor-a"})

		resp, err := ns.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{
			VolumeId:          "vol-1",
			StagingTargetPath: "/staging/vol-1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		if !plugin.unstageCalled {
			t.Error("vendor NodeUnstageVolume was not called")
		}
		// Backend should be forgotten
		_, err = ns.lookupBackendSocket("vol-1")
		assertGRPCCode(t, err, codes.FailedPrecondition)
	})

	t.Run("no backend recorded is no-op", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		resp, err := ns.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{
			VolumeId:          "vol-unknown",
			StagingTargetPath: "/staging/vol-unknown",
		})
		if err != nil {
			t.Fatalf("expected no-op, got error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
	})

	t.Run("missing volume ID", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{
			StagingTargetPath: "/staging",
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("missing staging path", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{
			VolumeId: "vol-1",
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("vendor unimplemented treated as no-op and forgets backend", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		plugin.unstageErr = status.Error(codes.Unimplemented, "not supported")
		ns := newTestNodeServer(t, "vendor-b", sock)
		ns.recordBackend("vol-u", map[string]string{"osac.backend": "vendor-b"})

		resp, err := ns.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{
			VolumeId:          "vol-u",
			StagingTargetPath: "/staging/vol-u",
		})
		if err != nil {
			t.Fatalf("expected no error for unimplemented, got: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		_, err = ns.lookupBackendSocket("vol-u")
		assertGRPCCode(t, err, codes.FailedPrecondition)
	})

	t.Run("vendor error propagated, backend not forgotten", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		plugin.unstageErr = status.Error(codes.Internal, "disk error")
		ns := newTestNodeServer(t, "vendor-c", sock)
		ns.recordBackend("vol-err", map[string]string{"osac.backend": "vendor-c"})

		_, err := ns.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{
			VolumeId:          "vol-err",
			StagingTargetPath: "/staging/vol-err",
		})
		assertGRPCCode(t, err, codes.Internal)
		// Backend should still be recorded
		_, err = ns.lookupBackendSocket("vol-err")
		if err != nil {
			t.Error("backend should not be forgotten on vendor error")
		}
	})
}

// --- NodePublishVolume ---

func TestNodePublishVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("routes to vendor and records backend", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		ns := newTestNodeServer(t, "vendor-a", sock)

		resp, err := ns.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{
			VolumeId:         "vol-1",
			TargetPath:       "/target/vol-1",
			VolumeCapability: blockCap(),
			VolumeContext:    map[string]string{"osac.backend": "vendor-a"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		if !plugin.publishCalled {
			t.Error("vendor NodePublishVolume was not called")
		}
		_, err = ns.lookupBackendSocket("vol-1")
		if err != nil {
			t.Errorf("backend not recorded after publish: %v", err)
		}
	})

	t.Run("missing volume ID", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{
			TargetPath:       "/target",
			VolumeCapability: blockCap(),
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("missing target path", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{
			VolumeId:         "vol-1",
			VolumeCapability: blockCap(),
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("missing volume capability", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{
			VolumeId:   "vol-1",
			TargetPath: "/target",
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("vendor error propagated", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		plugin.publishErr = status.Error(codes.ResourceExhausted, "no space")
		ns := newTestNodeServer(t, "vendor-a", sock)

		_, err := ns.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{
			VolumeId:         "vol-err",
			TargetPath:       "/target/vol-err",
			VolumeCapability: blockCap(),
			VolumeContext:    map[string]string{"osac.backend": "vendor-a"},
		})
		assertGRPCCode(t, err, codes.ResourceExhausted)
	})
}

// --- NodeUnpublishVolume ---

func TestNodeUnpublishVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("routes via backend map", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		ns := newTestNodeServer(t, "vendor-a", sock)
		ns.recordBackend("vol-1", map[string]string{"osac.backend": "vendor-a"})

		resp, err := ns.NodeUnpublishVolume(ctx, &csi.NodeUnpublishVolumeRequest{
			VolumeId:   "vol-1",
			TargetPath: "/target/vol-1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		if !plugin.unpublishCalled {
			t.Error("vendor NodeUnpublishVolume was not called")
		}
	})

	t.Run("no backend recorded is no-op", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		resp, err := ns.NodeUnpublishVolume(ctx, &csi.NodeUnpublishVolumeRequest{
			VolumeId:   "vol-unknown",
			TargetPath: "/target/vol-unknown",
		})
		if err != nil {
			t.Fatalf("expected no-op, got error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
	})

	t.Run("missing volume ID", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodeUnpublishVolume(ctx, &csi.NodeUnpublishVolumeRequest{
			TargetPath: "/target",
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("missing target path", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodeUnpublishVolume(ctx, &csi.NodeUnpublishVolumeRequest{
			VolumeId: "vol-1",
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})
}

// --- NodeGetVolumeStats ---

func TestNodeGetVolumeStats(t *testing.T) {
	ctx := context.Background()

	t.Run("proxies to vendor via backend map", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		ns := newTestNodeServer(t, "vendor-a", sock)
		ns.recordBackend("vol-1", map[string]string{"osac.backend": "vendor-a"})

		_, err := ns.NodeGetVolumeStats(ctx, &csi.NodeGetVolumeStatsRequest{
			VolumeId:   "vol-1",
			VolumePath: "/path/vol-1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !plugin.statsCalled {
			t.Error("vendor NodeGetVolumeStats was not called")
		}
	})

	t.Run("no backend recorded returns error", func(t *testing.T) {
		ns := NewNodeServer("n", proxy.NewManager(nil), nil)
		_, err := ns.NodeGetVolumeStats(ctx, &csi.NodeGetVolumeStatsRequest{
			VolumeId:   "vol-unknown",
			VolumePath: "/path",
		})
		assertGRPCCode(t, err, codes.FailedPrecondition)
	})
}

// --- NodeGetInfo / NodeGetCapabilities ---

func TestNodeGetInfo(t *testing.T) {
	ns := NewNodeServer("my-node-42", proxy.NewManager(nil), nil)
	resp, err := ns.NodeGetInfo(context.Background(), &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GetNodeId() != "my-node-42" {
		t.Errorf("expected nodeId my-node-42, got %s", resp.GetNodeId())
	}
}

func TestNodeGetCapabilities(t *testing.T) {
	ns := NewNodeServer("n", proxy.NewManager(nil), nil)
	resp, err := ns.NodeGetCapabilities(context.Background(), &csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.GetCapabilities()) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(resp.GetCapabilities()))
	}
	rpc := resp.GetCapabilities()[0].GetRpc()
	if rpc == nil || rpc.GetType() != csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME {
		t.Errorf("expected STAGE_UNSTAGE_VOLUME capability")
	}
}

// --- Stage then Unstage lifecycle ---

func TestStageUnstageLifecycle(t *testing.T) {
	ctx := context.Background()
	sock, _, cleanup := startFakeNodePlugin(t)
	defer cleanup()
	ns := newTestNodeServer(t, "vendor-a", sock)

	_, err := ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
		VolumeId:          "vol-lc",
		StagingTargetPath: "/staging/vol-lc",
		VolumeCapability:  blockCap(),
		VolumeContext:     map[string]string{"osac.backend": "vendor-a"},
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}

	// Backend is recorded after stage
	_, err = ns.lookupBackendSocket("vol-lc")
	if err != nil {
		t.Fatalf("backend should be recorded after stage: %v", err)
	}

	_, err = ns.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{
		VolumeId:          "vol-lc",
		StagingTargetPath: "/staging/vol-lc",
	})
	if err != nil {
		t.Fatalf("unstage: %v", err)
	}

	// Backend is forgotten after unstage
	_, err = ns.lookupBackendSocket("vol-lc")
	assertGRPCCode(t, err, codes.FailedPrecondition)
}
