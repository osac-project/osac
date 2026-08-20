package sanity_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kubernetes-csi/csi-test/v5/pkg/sanity"
	"github.com/osac-project/osac/osac-csi-driver/pkg/driver"
	"github.com/osac-project/osac/osac-csi-driver/pkg/fulfillment"
)

// metaDriverSkips lists CSI sanity tests that don't apply to the OSAC
// controller. Publish/unpublish are proxied to the fake vendor controller, a
// permissive test double that accepts any volume and node id (volume creation
// is decoupled from attach in stub mode), so it does not reproduce the
// volume/node existence failures these tests expect.
var metaDriverSkips = []string{
	"ControllerPublishVolume.*should fail when the volume does not exist",
	"ControllerPublishVolume.*should fail when the node does not exist",
}

func TestSanity(t *testing.T) {
	if f := flag.Lookup("ginkgo.skip"); f != nil {
		existing := f.Value.String()
		skip := strings.Join(metaDriverSkips, "|")
		if existing != "" {
			skip = existing + "|" + skip
		}
		_ = f.Value.Set(skip)
	}

	tmpDir := t.TempDir()

	vendorSocket := filepath.Join(tmpDir, "vendor.sock")
	driverSocket := filepath.Join(tmpDir, "driver.sock")
	nodeID := "test-node-1"
	backendName := "fake"

	vendorSrv, _, err := startFakeVendor(vendorSocket, nodeID)
	if err != nil {
		t.Fatalf("starting fake vendor: %v", err)
	}
	defer vendorSrv.GracefulStop()

	vc := fulfillment.NewVolumeStub(backendName, "nfs")

	vendorSockets := map[string]string{
		backendName: vendorSocket,
	}
	// The controller proxies publish/unpublish to the same fake vendor, which
	// serves the CSI controller service on this socket alongside the node service.
	vendorControllers := map[string]string{
		backendName: vendorSocket,
	}

	d, err := driver.NewDriver(
		"csi.osac.openshift.io",
		"0.0.1-test",
		"unix://"+driverSocket,
		nodeID,
		"test-cluster",
		vc,
		vendorSockets,
		vendorControllers,
	)
	if err != nil {
		t.Fatalf("creating driver: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := d.RunWithContext(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("driver exited with error: %v", err)
		}
	}()

	secretsFile, err := filepath.Abs(filepath.Join("testdata", "secrets.yaml"))
	if err != nil {
		t.Fatalf("resolving secrets path: %v", err)
	}
	if _, err := os.Stat(secretsFile); err != nil {
		t.Fatalf("secrets file not found: %v", err)
	}

	config := sanity.NewTestConfig()
	config.Address = driverSocket
	config.SecretsFile = secretsFile
	config.TestVolumeParameters = map[string]string{
		"tier":   "default",
		"tenant": "test-tenant",
	}
	config.TargetPath = filepath.Join(tmpDir, "target")
	config.StagingPath = filepath.Join(tmpDir, "staging")

	sanity.Test(t, config)
}
