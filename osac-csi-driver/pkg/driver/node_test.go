package driver

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/osac-csi-driver/pkg/proxy"
)

type fakeNodePlugin struct {
	csi.UnimplementedNodeServer
	stageCalled       bool
	unstageCalled     bool
	publishCalled     bool
	unpublishCalled   bool
	statsCalled       bool
	stageVolumeID     string
	unstageVolumeID   string
	publishVolumeID   string
	unpublishVolumeID string
	statsVolumeID     string
	unstageCalls      int
	publishCalls      int
	unpublishCalls    int

	stageErr     error
	unstageErr   error
	publishErr   error
	unpublishErr error
	statsResp    *csi.NodeGetVolumeStatsResponse
	statsErr     error
}

func (f *fakeNodePlugin) NodeStageVolume(_ context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	f.stageCalled = true
	f.stageVolumeID = req.GetVolumeId()
	if f.stageErr != nil {
		return nil, f.stageErr
	}
	return &csi.NodeStageVolumeResponse{}, nil
}

func (f *fakeNodePlugin) NodeUnstageVolume(_ context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	f.unstageCalled = true
	f.unstageCalls++
	f.unstageVolumeID = req.GetVolumeId()
	if f.unstageErr != nil {
		return nil, f.unstageErr
	}
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (f *fakeNodePlugin) NodePublishVolume(_ context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	f.publishCalled = true
	f.publishCalls++
	f.publishVolumeID = req.GetVolumeId()
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

func (f *fakeNodePlugin) NodeUnpublishVolume(_ context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	f.unpublishCalled = true
	f.unpublishCalls++
	f.unpublishVolumeID = req.GetVolumeId()
	if f.unpublishErr != nil {
		return nil, f.unpublishErr
	}
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (f *fakeNodePlugin) NodeGetVolumeStats(_ context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	f.statsCalled = true
	f.statsVolumeID = req.GetVolumeId()
	if f.statsErr != nil {
		return nil, f.statsErr
	}
	if f.statsResp != nil {
		return f.statsResp, nil
	}
	return &csi.NodeGetVolumeStatsResponse{}, nil
}

func (f *fakeNodePlugin) anyCalled() bool {
	return f.stageCalled || f.unstageCalled || f.publishCalled || f.unpublishCalled || f.statsCalled
}

func startFakeNodePlugin(t *testing.T) (socketPath string, plugin *fakeNodePlugin, cleanup func()) {
	t.Helper()
	// Unix socket paths are length-limited, so keep the directory short.
	dir, err := os.MkdirTemp("", "vs")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath = filepath.Join(dir, "vendor.sock")
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

func newTestNodeServer(t *testing.T, backend string, socketPath string, opts ...NodeOption) *NodeServer {
	t.Helper()
	return newNodeServerWithSockets(t, map[string]string{backend: socketPath}, opts...)
}

func newNodeServerWithSockets(t *testing.T, vendorSockets map[string]string, opts ...NodeOption) *NodeServer {
	t.Helper()
	return NewNodeServer("test-node", proxy.NewManager(vendorSockets), vendorSockets, opts...)
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

func mustStage(t *testing.T, ns *NodeServer, volumeID string, volumeContext map[string]string) {
	t.Helper()
	_, err := ns.NodeStageVolume(context.Background(), &csi.NodeStageVolumeRequest{
		VolumeId:          volumeID,
		StagingTargetPath: "/staging/" + volumeID,
		VolumeCapability:  blockCap(),
		VolumeContext:     volumeContext,
	})
	if err != nil {
		t.Fatalf("stage %s: %v", volumeID, err)
	}
}

func mustPublish(t *testing.T, ns *NodeServer, volumeID, targetPath string, volumeContext map[string]string) {
	t.Helper()
	_, err := ns.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId:         volumeID,
		TargetPath:       targetPath,
		VolumeCapability: blockCap(),
		VolumeContext:    volumeContext,
	})
	if err != nil {
		t.Fatalf("publish %s: %v", volumeID, err)
	}
}

func unstage(ns *NodeServer, volumeID string) error {
	_, err := ns.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{
		VolumeId:          volumeID,
		StagingTargetPath: "/staging/" + volumeID,
	})
	return err
}

func unpublish(ns *NodeServer, volumeID, targetPath string) error {
	_, err := ns.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{
		VolumeId:   volumeID,
		TargetPath: targetPath,
	})
	return err
}

func volumeStats(ns *NodeServer, volumeID string) error {
	_, err := ns.NodeGetVolumeStats(context.Background(), &csi.NodeGetVolumeStatsRequest{
		VolumeId:   volumeID,
		VolumePath: "/target/" + volumeID,
	})
	return err
}

// --- route store lifecycle ---

func TestRouteStoreLifecycle(t *testing.T) {
	newServer := func(t *testing.T) (*NodeServer, *fakeNodePlugin) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		t.Cleanup(cleanup)
		return newTestNodeServer(t, "netapp", sock, WithRouteStore(newMemoryRouteStore())), plugin
	}
	netappCtx := map[string]string{"osac.backend": "netapp", "osac.volume-id": "netapp-vol"}

	t.Run("stage records a route that unstage uses", func(t *testing.T) {
		ns, plugin := newServer(t)
		mustStage(t, ns, "vol-1", netappCtx)
		if err := unstage(ns, "vol-1"); err != nil {
			t.Fatalf("unstage: %v", err)
		}
		if !plugin.unstageCalled || plugin.unstageVolumeID != "netapp-vol" {
			t.Errorf("unstage did not reach the vendor with the vendor volume ID: %+v", plugin)
		}
	})

	t.Run("unstage forgets the route", func(t *testing.T) {
		ns, plugin := newServer(t)
		mustStage(t, ns, "vol-2", netappCtx)
		if err := unstage(ns, "vol-2"); err != nil {
			t.Fatalf("unstage: %v", err)
		}
		plugin.unstageCalled = false
		if err := unstage(ns, "vol-2"); err != nil {
			t.Fatalf("second unstage: %v", err)
		}
		if plugin.unstageCalled {
			t.Error("second unstage reached the vendor after the route was forgotten")
		}
	})

	t.Run("unknown volume", func(t *testing.T) {
		ns, plugin := newServer(t)
		if err := unstage(ns, "vol-never-seen"); err != nil {
			t.Fatalf("unstage of unknown volume: %v", err)
		}
		assertGRPCCode(t, volumeStats(ns, "vol-never-seen"), codes.FailedPrecondition)
		if plugin.anyCalled() {
			t.Error("vendor was called for an unknown volume")
		}
	})

	for name, volumeContext := range map[string]map[string]string{
		"nil context records nothing":   nil,
		"empty backend records nothing": {"osac.backend": "", "osac.volume-id": "netapp-vol"},
	} {
		t.Run(name, func(t *testing.T) {
			ns, plugin := newServer(t)
			_, err := ns.NodeStageVolume(context.Background(), &csi.NodeStageVolumeRequest{
				VolumeId:          "vol-x",
				StagingTargetPath: "/staging/vol-x",
				VolumeCapability:  blockCap(),
				VolumeContext:     volumeContext,
			})
			assertGRPCCode(t, err, codes.InvalidArgument)
			if err := unstage(ns, "vol-x"); err != nil {
				t.Fatalf("unstage: %v", err)
			}
			if plugin.anyCalled() {
				t.Error("vendor was called for a volume with no route")
			}
		})
	}
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
			VolumeContext:     map[string]string{"osac.backend": "vendor-a", "osac.volume-id": "vendor-vol-1"},
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
		if err := unstage(ns, "vol-1"); err != nil || !plugin.unstageCalled {
			t.Errorf("route not recorded after stage: err=%v unstageCalled=%v", err, plugin.unstageCalled)
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
			VolumeContext:     map[string]string{"osac.backend": "vendor-b", "osac.volume-id": "vendor-vol-u"},
		})
		if err != nil {
			t.Fatalf("expected no error for unimplemented, got: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		if err := unstage(ns, "vol-u"); err != nil || !plugin.unstageCalled {
			t.Errorf("route not recorded after unimplemented stage: err=%v unstageCalled=%v",
				err, plugin.unstageCalled)
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
			VolumeContext:     map[string]string{"osac.backend": "vendor-c", "osac.volume-id": "vendor-vol-err"},
		})
		assertGRPCCode(t, err, codes.Internal)
	})
}

// --- NodeUnstageVolume ---

func TestNodeUnstageVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("routes via recorded route and forgets on success", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		ns := newTestNodeServer(t, "vendor-a", sock)
		mustStage(t, ns, "vol-1", map[string]string{"osac.backend": "vendor-a", "osac.volume-id": "vendor-vol-1"})

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
		plugin.unstageCalled = false
		if err := unstage(ns, "vol-1"); err != nil || plugin.unstageCalled {
			t.Errorf("route not forgotten after unstage: err=%v unstageCalled=%v", err, plugin.unstageCalled)
		}
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
		ns := newTestNodeServer(t, "vendor-b", sock)
		mustStage(t, ns, "vol-u", map[string]string{"osac.backend": "vendor-b", "osac.volume-id": "vendor-vol-u"})
		plugin.unstageErr = status.Error(codes.Unimplemented, "not supported")

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
		plugin.unstageErr = nil
		plugin.unstageCalled = false
		if err := unstage(ns, "vol-u"); err != nil || plugin.unstageCalled {
			t.Errorf("route not forgotten after unimplemented unstage: err=%v unstageCalled=%v",
				err, plugin.unstageCalled)
		}
	})

	t.Run("vendor error propagated, backend not forgotten", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		ns := newTestNodeServer(t, "vendor-c", sock)
		mustStage(t, ns, "vol-err", map[string]string{"osac.backend": "vendor-c", "osac.volume-id": "vendor-vol-err"})
		plugin.unstageErr = status.Error(codes.Internal, "disk error")

		_, err := ns.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{
			VolumeId:          "vol-err",
			StagingTargetPath: "/staging/vol-err",
		})
		assertGRPCCode(t, err, codes.Internal)

		plugin.unstageErr = nil
		plugin.unstageCalled = false
		if err := unstage(ns, "vol-err"); err != nil || !plugin.unstageCalled {
			t.Errorf("route should be kept after a vendor error: err=%v unstageCalled=%v",
				err, plugin.unstageCalled)
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
			VolumeContext:    map[string]string{"osac.backend": "vendor-a", "osac.volume-id": "vendor-vol-1"},
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
		if err := unpublish(ns, "vol-1", "/target/vol-1"); err != nil || !plugin.unpublishCalled {
			t.Errorf("route not recorded after publish: err=%v unpublishCalled=%v", err, plugin.unpublishCalled)
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
			VolumeContext:    map[string]string{"osac.backend": "vendor-a", "osac.volume-id": "vendor-vol-err"},
		})
		assertGRPCCode(t, err, codes.ResourceExhausted)
	})
}

// --- NodeUnpublishVolume ---

func TestNodeUnpublishVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("routes via recorded route", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		ns := newTestNodeServer(t, "vendor-a", sock)
		mustPublish(t, ns, "vol-1", "/target/vol-1",
			map[string]string{"osac.backend": "vendor-a", "osac.volume-id": "vendor-vol-1"})

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

	t.Run("proxies to vendor via recorded route", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		ns := newTestNodeServer(t, "vendor-a", sock)
		mustStage(t, ns, "vol-1", map[string]string{"osac.backend": "vendor-a", "osac.volume-id": "vendor-vol-1"})

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

func TestNodeServerUsesVendorVolumeID(t *testing.T) {
	ctx := context.Background()
	sock, plugin, cleanup := startFakeNodePlugin(t)
	defer cleanup()
	ns := newTestNodeServer(t, "vendor-a", sock)
	volumeContext := map[string]string{
		"osac.backend":            "vendor-a",
		"osac.volume-id":          "generic-vendor-id",
		topolvmVolumeIDContextKey: "topolvm-vendor-id",
	}

	_, err := ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
		VolumeId:          "fulfillment-volume-id",
		StagingTargetPath: "/staging/volume",
		VolumeCapability:  blockCap(),
		VolumeContext:     volumeContext,
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}

	_, err = ns.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{
		VolumeId:         "fulfillment-volume-id",
		TargetPath:       "/target/volume",
		VolumeCapability: blockCap(),
		VolumeContext:    volumeContext,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	_, err = ns.NodeGetVolumeStats(ctx, &csi.NodeGetVolumeStatsRequest{
		VolumeId:   "fulfillment-volume-id",
		VolumePath: "/target/volume",
	})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	_, err = ns.NodeUnpublishVolume(ctx, &csi.NodeUnpublishVolumeRequest{
		VolumeId:   "fulfillment-volume-id",
		TargetPath: "/target/volume",
	})
	if err != nil {
		t.Fatalf("unpublish: %v", err)
	}

	_, err = ns.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{
		VolumeId:          "fulfillment-volume-id",
		StagingTargetPath: "/staging/volume",
	})
	if err != nil {
		t.Fatalf("unstage: %v", err)
	}

	for operation, got := range map[string]string{
		"stage":     plugin.stageVolumeID,
		"publish":   plugin.publishVolumeID,
		"stats":     plugin.statsVolumeID,
		"unpublish": plugin.unpublishVolumeID,
		"unstage":   plugin.unstageVolumeID,
	} {
		if got != "topolvm-vendor-id" {
			t.Errorf("%s forwarded volume ID = %q, want topolvm-vendor-id", operation, got)
		}
	}
}

// --- NodeGetInfo / NodeGetCapabilities ---

func TestNodeGetInfo(t *testing.T) {
	t.Setenv(nodeNameEnv, "my-node-42")
	ns := NewNodeServer("constructor-node-must-not-be-used", proxy.NewManager(nil), nil)
	resp, err := ns.NodeGetInfo(context.Background(), &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GetNodeId() != "my-node-42" {
		t.Errorf("expected nodeId my-node-42, got %s", resp.GetNodeId())
	}
	if got := resp.GetAccessibleTopology().GetSegments()[volumeNodeTopologyKey]; got != "my-node-42" {
		t.Errorf("topology node = %q, want my-node-42", got)
	}
}

func TestNodeGetInfoMissingNodeName(t *testing.T) {
	t.Setenv(nodeNameEnv, "")
	ns := NewNodeServer("constructor-node-must-not-be-used", proxy.NewManager(nil), nil)

	_, err := ns.NodeGetInfo(context.Background(), &csi.NodeGetInfoRequest{})
	assertGRPCCode(t, err, codes.FailedPrecondition)
}

func TestNewNodeServerResolvesLVMSSocket(t *testing.T) {
	t.Run("environment override", func(t *testing.T) {
		t.Setenv(lvmsNodeSocketEnv, "/run/custom/topolvm.sock")
		ns := NewNodeServer("node", proxy.NewManager(nil), map[string]string{lvmsProvider: "/run/other/topolvm.sock"})

		got, err := ns.resolveVendorSocket(map[string]string{"osac.backend": lvmsProvider})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "/run/custom/topolvm.sock" {
			t.Errorf("LVMS socket = %q, want /run/custom/topolvm.sock", got)
		}
	})

	t.Run("configured vendor socket without environment override", func(t *testing.T) {
		t.Setenv(lvmsNodeSocketEnv, "")
		socketPath, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		ns := newTestNodeServer(t, lvmsProvider, socketPath)
		_, err := ns.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
			VolumeId: "osac-volume", TargetPath: "/target",
			VolumeCapability: blockCap(),
			VolumeContext:    map[string]string{"osac.backend": lvmsProvider, topolvmVolumeIDContextKey: "vendor-volume"},
		})
		if err != nil {
			t.Fatalf("custom LVMS socket was not used: %v", err)
		}
		if !plugin.publishCalled || plugin.publishVolumeID != "vendor-volume" {
			t.Fatalf("publish did not reach the configured LVMS socket: %+v", plugin)
		}
	})

	t.Run("default", func(t *testing.T) {
		t.Setenv(lvmsNodeSocketEnv, "")
		ns := NewNodeServer("node", proxy.NewManager(nil), nil)

		got, err := ns.resolveVendorSocket(map[string]string{"osac.backend": lvmsProvider})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != defaultLVMSNodeSocket {
			t.Errorf("LVMS socket = %q, want %q", got, defaultLVMSNodeSocket)
		}
	})
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
	sock, plugin, cleanup := startFakeNodePlugin(t)
	defer cleanup()
	ns := newTestNodeServer(t, "vendor-a", sock)

	_, err := ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
		VolumeId:          "vol-lc",
		StagingTargetPath: "/staging/vol-lc",
		VolumeCapability:  blockCap(),
		VolumeContext:     map[string]string{"osac.backend": "vendor-a", "osac.volume-id": "vendor-vol-lc"},
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}

	// The route is recorded after stage.
	if err := volumeStats(ns, "vol-lc"); err != nil || !plugin.statsCalled {
		t.Fatalf("route should be recorded after stage: err=%v statsCalled=%v", err, plugin.statsCalled)
	}

	_, err = ns.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{
		VolumeId:          "vol-lc",
		StagingTargetPath: "/staging/vol-lc",
	})
	if err != nil {
		t.Fatalf("unstage: %v", err)
	}

	// The route is forgotten after unstage.
	assertGRPCCode(t, volumeStats(ns, "vol-lc"), codes.FailedPrecondition)
}

// --- Routing on backend and protocol ---

func TestNodeRoutesByProtocol(t *testing.T) {
	for _, protocol := range []string{"nfs", "block"} {
		t.Run(protocol, func(t *testing.T) {
			nfsSock, nfsPlugin, nfsCleanup := startFakeNodePlugin(t)
			defer nfsCleanup()
			blockSock, blockPlugin, blockCleanup := startFakeNodePlugin(t)
			defer blockCleanup()
			ns := newNodeServerWithSockets(t, map[string]string{"vast/nfs": nfsSock, "vast/block": blockSock})
			volumeContext := map[string]string{
				"osac.backend": "vast", "osac.protocol": protocol, "osac.volume-id": "pvc-n",
			}

			mustStage(t, ns, "vol-n", volumeContext)
			mustPublish(t, ns, "vol-n", "/target/vol-n", volumeContext)

			want, other := nfsPlugin, blockPlugin
			if protocol == "block" {
				want, other = blockPlugin, nfsPlugin
			}
			if !want.stageCalled || want.stageVolumeID != "pvc-n" {
				t.Errorf("stage did not reach the %s plugin with pvc-n: %+v", protocol, want)
			}
			if !want.publishCalled || want.publishVolumeID != "pvc-n" {
				t.Errorf("publish did not reach the %s plugin with pvc-n: %+v", protocol, want)
			}
			if other.anyCalled() {
				t.Errorf("the other protocol's plugin was called")
			}
		})
	}
}

func TestNodeWithoutVendorVolumeIDFallsBack(t *testing.T) {
	sock, plugin, cleanup := startFakeNodePlugin(t)
	defer cleanup()
	ns := newNodeServerWithSockets(t, map[string]string{"netapp": sock})

	mustPublish(t, ns, "osac-volume", "/target/osac-volume",
		map[string]string{"osac.backend": "netapp", "osac.protocol": "nfs"})
	if !plugin.publishCalled || plugin.publishVolumeID != "osac-volume" {
		t.Errorf("publish without a vendor volume ID did not fall back to the CSI volume ID: %+v", plugin)
	}
}

func TestNodeLVMSWithoutVolumeIDFallsBack(t *testing.T) {
	t.Setenv(lvmsNodeSocketEnv, "")
	sock, plugin, cleanup := startFakeNodePlugin(t)
	defer cleanup()
	ns := newTestNodeServer(t, lvmsProvider, sock)

	mustPublish(t, ns, "osac-volume", "/target/osac-volume",
		map[string]string{"osac.backend": lvmsProvider, "osac.protocol": "block"})
	if !plugin.publishCalled || plugin.publishVolumeID != "osac-volume" {
		t.Errorf("lvms publish without a vendor volume ID did not fall back: %+v", plugin)
	}
}

func TestBareVendorKeysRouteAsBefore(t *testing.T) {
	for _, backend := range []string{"netapp", "pure"} {
		for _, protocol := range []string{"nfs", "block"} {
			t.Run(backend+"/"+protocol, func(t *testing.T) {
				netappSock, netappPlugin, netappCleanup := startFakeNodePlugin(t)
				defer netappCleanup()
				pureSock, purePlugin, pureCleanup := startFakeNodePlugin(t)
				defer pureCleanup()
				ns := newNodeServerWithSockets(t, map[string]string{"netapp": netappSock, "pure": pureSock})
				volumeContext := map[string]string{
					"osac.backend": backend, "osac.protocol": protocol, "osac.volume-id": backend + "-vol",
				}

				mustStage(t, ns, "vol-b", volumeContext)
				mustPublish(t, ns, "vol-b", "/target/vol-b", volumeContext)
				if err := unpublish(ns, "vol-b", "/target/vol-b"); err != nil {
					t.Fatalf("unpublish: %v", err)
				}
				if err := unstage(ns, "vol-b"); err != nil {
					t.Fatalf("unstage: %v", err)
				}

				want, other := netappPlugin, purePlugin
				if backend == "pure" {
					want, other = purePlugin, netappPlugin
				}
				for op, got := range map[string]string{
					"stage":     want.stageVolumeID,
					"publish":   want.publishVolumeID,
					"unpublish": want.unpublishVolumeID,
					"unstage":   want.unstageVolumeID,
				} {
					if got != backend+"-vol" {
						t.Errorf("%s reached %s with volume ID %q, want %s-vol", op, backend, got, backend)
					}
				}
				if other.anyCalled() {
					t.Errorf("the other backend's plugin was called")
				}
			})
		}
	}
}

// --- Routes kept across a node plugin restart ---

func TestNodeRoutesSurviveRestart(t *testing.T) {
	nfsContext := map[string]string{"osac.backend": "vast", "osac.protocol": "nfs", "osac.volume-id": "pvc-n"}

	setup := func(t *testing.T) (dir string, sockets map[string]string, nfsPlugin *fakeNodePlugin) {
		t.Helper()
		nfsSock, nfsPlugin, nfsCleanup := startFakeNodePlugin(t)
		t.Cleanup(nfsCleanup)
		blockSock, _, blockCleanup := startFakeNodePlugin(t)
		t.Cleanup(blockCleanup)
		return t.TempDir(), map[string]string{"vast/nfs": nfsSock, "vast/block": blockSock}, nfsPlugin
	}
	// Each call builds a server as a restarted pod would: a fresh connection
	// manager and a new file store over the same directory.
	server := func(t *testing.T, sockets map[string]string, dir string) *NodeServer {
		t.Helper()
		return newNodeServerWithSockets(t, sockets, WithRouteStore(newFileRouteStore(dir)))
	}

	t.Run("stats, unpublish and unstage route after restart", func(t *testing.T) {
		dir, sockets, nfs := setup(t)
		a := server(t, sockets, dir)
		mustStage(t, a, "vol-n", nfsContext)
		mustPublish(t, a, "vol-n", "/target/vol-n", nfsContext)

		b := server(t, sockets, dir)
		if err := volumeStats(b, "vol-n"); err != nil {
			t.Fatalf("stats after restart: %v", err)
		}
		if err := unpublish(b, "vol-n", "/target/vol-n"); err != nil {
			t.Fatalf("unpublish after restart: %v", err)
		}
		if err := unstage(b, "vol-n"); err != nil {
			t.Fatalf("unstage after restart: %v", err)
		}
		for op, got := range map[string]string{
			"stats": nfs.statsVolumeID, "unpublish": nfs.unpublishVolumeID, "unstage": nfs.unstageVolumeID,
		} {
			if got != "pvc-n" {
				t.Errorf("%s after restart reached the NFS plugin with %q, want pvc-n", op, got)
			}
		}
		if _, ok, err := newFileRouteStore(dir).Load("vol-n"); err != nil || ok {
			t.Errorf("route after unstage: ok=%v err=%v, want not found", ok, err)
		}
	})

	t.Run("two publishes, two unpublishes, one unstage", func(t *testing.T) {
		dir, sockets, nfs := setup(t)
		a := server(t, sockets, dir)
		mustStage(t, a, "vol-n", nfsContext)
		mustPublish(t, a, "vol-n", "/target/one", nfsContext)
		mustPublish(t, a, "vol-n", "/target/two", nfsContext)

		b := server(t, sockets, dir)
		for _, target := range []string{"/target/one", "/target/two"} {
			if err := unpublish(b, "vol-n", target); err != nil {
				t.Fatalf("unpublish %s: %v", target, err)
			}
		}
		if err := unstage(b, "vol-n"); err != nil {
			t.Fatalf("unstage: %v", err)
		}
		if nfs.publishCalls != 2 || nfs.unpublishCalls != 2 || nfs.unstageCalls != 1 {
			t.Errorf("calls: publish=%d unpublish=%d unstage=%d, want 2, 2, 1",
				nfs.publishCalls, nfs.unpublishCalls, nfs.unstageCalls)
		}
		if nfs.unpublishVolumeID != "pvc-n" || nfs.unstageVolumeID != "pvc-n" {
			t.Errorf("vendor volume IDs: unpublish=%q unstage=%q, want pvc-n",
				nfs.unpublishVolumeID, nfs.unstageVolumeID)
		}
	})

	t.Run("failed unstage keeps the route for the retry", func(t *testing.T) {
		dir, sockets, nfs := setup(t)
		mustStage(t, server(t, sockets, dir), "vol-n", nfsContext)

		nfs.unstageErr = status.Error(codes.Internal, "device busy")
		assertGRPCCode(t, unstage(server(t, sockets, dir), "vol-n"), codes.Internal)

		nfs.unstageErr = nil
		if err := unstage(server(t, sockets, dir), "vol-n"); err != nil {
			t.Fatalf("retried unstage: %v", err)
		}
		if nfs.unstageCalls != 2 || nfs.unstageVolumeID != "pvc-n" {
			t.Errorf("unstage calls=%d volume=%q, want 2 calls with pvc-n", nfs.unstageCalls, nfs.unstageVolumeID)
		}
	})

	t.Run("unknown volume keeps today's results", func(t *testing.T) {
		dir, sockets, nfs := setup(t)
		b := server(t, sockets, dir)
		if err := unpublish(b, "vol-unknown", "/target/vol-unknown"); err != nil {
			t.Errorf("unpublish of unknown volume: %v", err)
		}
		if err := unstage(b, "vol-unknown"); err != nil {
			t.Errorf("unstage of unknown volume: %v", err)
		}
		assertGRPCCode(t, volumeStats(b, "vol-unknown"), codes.FailedPrecondition)
		if nfs.anyCalled() {
			t.Error("vendor was called for an unknown volume")
		}
	})

	t.Run("corrupt route file is an error", func(t *testing.T) {
		dir, sockets, nfs := setup(t)
		if err := os.WriteFile(filepath.Join(dir, routeFileName("vol-n")), []byte("{garbage"), 0o600); err != nil {
			t.Fatal(err)
		}
		b := server(t, sockets, dir)
		assertGRPCCode(t, unpublish(b, "vol-n", "/target/vol-n"), codes.Internal)
		assertGRPCCode(t, unstage(b, "vol-n"), codes.Internal)
		if nfs.anyCalled() {
			t.Error("vendor was called for a corrupt route")
		}
	})

	t.Run("route for a backend no longer configured", func(t *testing.T) {
		dir := t.TempDir()
		sock, _, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		mustStage(t, server(t, map[string]string{"gone-backend": sock}, dir), "vol-orphan",
			map[string]string{"osac.backend": "gone-backend", "osac.volume-id": "orphan"})

		assertGRPCCode(t, unstage(server(t, map[string]string{}, dir), "vol-orphan"), codes.NotFound)
	})
}

// --- VIP reachability and placeholder cleanup ---

type fakeChecker struct {
	mu      sync.Mutex
	checked []string
	err     error
}

func (f *fakeChecker) check(_ context.Context, hostPort string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checked = append(f.checked, hostPort)
	return f.err
}

type fakeMounter struct {
	mu         sync.Mutex
	tmpfs      bool
	unmountErr error
	unmounted  []string
}

func (f *fakeMounter) IsTmpfsMount(string) (bool, error) {
	return f.tmpfs, nil
}

func (f *fakeMounter) Unmount(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unmounted = append(f.unmounted, path)
	return f.unmountErr
}

func TestNodePublishChecksVIPReachability(t *testing.T) {
	nfsContext := func(extra map[string]string) map[string]string {
		m := map[string]string{"osac.backend": "vast", "osac.protocol": "nfs", "osac.volume-id": "pvc-n"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	publish := func(ns *NodeServer, volumeContext map[string]string) error {
		_, err := ns.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
			VolumeId: "vol-n", TargetPath: "/target/vol-n",
			VolumeCapability: blockCap(), VolumeContext: volumeContext,
		})
		return err
	}

	t.Run("unreachable VIP fails before the vendor call", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		checker := &fakeChecker{err: errors.New("i/o timeout")}
		ns := newTestNodeServer(t, "vast", sock, withReachabilityChecker(checker.check))

		err := publish(ns, nfsContext(map[string]string{"vip_pool_fqdn": "pool.example"}))
		assertGRPCCode(t, err, codes.Unavailable)
		msg := status.Convert(err).Message()
		if !strings.Contains(msg, "pool.example") || !strings.Contains(msg, "2049") {
			t.Errorf("error %q does not name the pool and port 2049", msg)
		}
		if len(checker.checked) != 1 || checker.checked[0] != "pool.example:2049" {
			t.Errorf("checked %v, want [pool.example:2049]", checker.checked)
		}
		if plugin.publishCalled {
			t.Error("vendor was called although the VIP is unreachable")
		}
	})

	t.Run("random prefix checks a label under the pool", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		checker := &fakeChecker{}
		ns := newTestNodeServer(t, "vast", sock, withReachabilityChecker(checker.check))

		err := publish(ns, nfsContext(map[string]string{
			"vip_pool_fqdn": "pool.example", "vip_pool_fqdn_random_prefix": "true",
		}))
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
		pattern := regexp.MustCompile(`^[a-z]{4}\.pool\.example:2049$`)
		if len(checker.checked) != 1 || !pattern.MatchString(checker.checked[0]) {
			t.Errorf("checked %v, want one host matching %s", checker.checked, pattern)
		}
		if !plugin.publishCalled {
			t.Error("vendor was not called")
		}
	})

	t.Run("reachable VIP calls the vendor", func(t *testing.T) {
		sock, plugin, cleanup := startFakeNodePlugin(t)
		defer cleanup()
		checker := &fakeChecker{}
		ns := newTestNodeServer(t, "vast", sock, withReachabilityChecker(checker.check))

		if err := publish(ns, nfsContext(map[string]string{"vip_pool_fqdn": "pool.example"})); err != nil {
			t.Fatalf("publish: %v", err)
		}
		if len(checker.checked) != 1 || checker.checked[0] != "pool.example:2049" {
			t.Errorf("checked %v, want [pool.example:2049]", checker.checked)
		}
		if !plugin.publishCalled {
			t.Error("vendor was not called")
		}
	})

	for name, volumeContext := range map[string]map[string]string{
		"block skips the check": {
			"osac.backend": "vast", "osac.protocol": "block", "osac.volume-id": "pvc-b",
			"vip_pool_fqdn": "pool.example",
		},
		"nfs without vip_pool_fqdn skips the check": nfsContext(nil),
	} {
		t.Run(name, func(t *testing.T) {
			sock, plugin, cleanup := startFakeNodePlugin(t)
			defer cleanup()
			checker := &fakeChecker{err: errors.New("must not be called")}
			ns := newTestNodeServer(t, "vast", sock, withReachabilityChecker(checker.check))

			if err := publish(ns, volumeContext); err != nil {
				t.Fatalf("publish: %v", err)
			}
			if len(checker.checked) != 0 {
				t.Errorf("checked %v, want no check", checker.checked)
			}
			if !plugin.publishCalled {
				t.Error("vendor was not called")
			}
		})
	}
}

func TestNodePublishCleansUpPlaceholderMount(t *testing.T) {
	volumeContext := map[string]string{"osac.backend": "vast", "osac.protocol": "nfs", "osac.volume-id": "pvc-n"}
	vendorErr := status.Error(codes.DeadlineExceeded, "mount timed out")

	tests := []struct {
		name          string
		mounter       *fakeMounter
		wantUnmounted int
	}{
		{name: "tmpfs at the target is unmounted", mounter: &fakeMounter{tmpfs: true}, wantUnmounted: 1},
		{name: "no tmpfs at the target", mounter: &fakeMounter{}, wantUnmounted: 0},
		{name: "unmount failure is not returned",
			mounter: &fakeMounter{tmpfs: true, unmountErr: errors.New("target is busy")}, wantUnmounted: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sock, plugin, cleanup := startFakeNodePlugin(t)
			defer cleanup()
			plugin.publishErr = vendorErr
			ns := newTestNodeServer(t, "vast", sock, withMounter(tt.mounter))

			_, err := ns.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
				VolumeId: "vol-n", TargetPath: "/target/vol-n",
				VolumeCapability: blockCap(), VolumeContext: volumeContext,
			})
			assertGRPCCode(t, err, codes.DeadlineExceeded)
			if msg := status.Convert(err).Message(); msg != "mount timed out" {
				t.Errorf("error message = %q, want the vendor's", msg)
			}
			if len(tt.mounter.unmounted) != tt.wantUnmounted {
				t.Fatalf("unmounted %v, want %d call(s)", tt.mounter.unmounted, tt.wantUnmounted)
			}
			for _, path := range tt.mounter.unmounted {
				if path != "/target/vol-n" {
					t.Errorf("unmounted %q, want /target/vol-n", path)
				}
			}
		})
	}
}
