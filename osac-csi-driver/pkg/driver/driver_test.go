package driver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
)

func TestNewDriverNodeOnlyAcceptsNilVolumeClient(t *testing.T) {
	d, err := NewDriver("csi.osac.openshift.io", "test", "unix:///unused.sock", "node-1", "",
		nil, nil, nil, WithNodeOnly())
	if err != nil {
		t.Fatalf("NewDriver in node-only mode: %v", err)
	}
	if d == nil {
		t.Fatal("expected a driver")
	}
}

func TestNewDriverRequiresVolumeClientWithoutNodeOnly(t *testing.T) {
	if _, err := NewDriver("csi.osac.openshift.io", "test", "unix:///unused.sock", "node-1", "",
		nil, nil, nil); err == nil {
		t.Fatal("expected an error for a nil volume client")
	}
}

func TestNodeOnlyDriverServesNoController(t *testing.T) {
	// Unix socket paths are length-limited, so keep the directory short.
	dir, err := os.MkdirTemp("", "osac-csi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "csi.sock")

	d, err := NewDriver("csi.osac.openshift.io", "test", "unix://"+socketPath, "node-1", "",
		nil, nil, nil, WithNodeOnly())
	if err != nil {
		t.Fatalf("NewDriver in node-only mode: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.RunWithContext(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("driver socket did not appear")
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_, err = csi.NewControllerClient(conn).ControllerGetCapabilities(ctx, &csi.ControllerGetCapabilitiesRequest{})
	assertGRPCCode(t, err, codes.Unimplemented)

	if _, err := csi.NewNodeClient(conn).NodeGetCapabilities(ctx, &csi.NodeGetCapabilitiesRequest{}); err != nil {
		t.Errorf("node service not served: %v", err)
	}

	resp, err := csi.NewIdentityClient(conn).GetPluginCapabilities(ctx, &csi.GetPluginCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetPluginCapabilities: %v", err)
	}
	for _, capability := range resp.GetCapabilities() {
		if capability.GetService().GetType() == csi.PluginCapability_Service_CONTROLLER_SERVICE {
			t.Error("node-only driver advertises CONTROLLER_SERVICE")
		}
	}
}
