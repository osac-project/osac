package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/osac-project/osac/osac-csi-driver/pkg/driver"
	"github.com/osac-project/osac/osac-csi-driver/pkg/fulfillment"
	"k8s.io/klog/v2"
)

var (
	version   = "dev"
	gitCommit = "unknown"
)

func main() {
	klog.InitFlags(nil)

	csiEndpoint := flag.String("csi-endpoint", "unix:///csi/osac/csi.sock", "CSI endpoint this driver listens on")
	nodeID := flag.String("node-id", "", "Node ID for NodeGetInfo")
	clusterID := flag.String("cluster-id", "", "Cluster ID for volume creation")
	fulfillmentEndpoint := flag.String("fulfillment-endpoint", "",
		"gRPC endpoint for the OSAC fulfillment service (uses stub if empty)")
	vendorSocketsFlag := flag.String("vendor-sockets", "",
		"Comma-separated backend=socketpath pairs (e.g. ontap=/csi/trident/csi.sock)")
	driverName := flag.String("driver-name", "csi.osac.openshift.io", "CSI driver name")

	flag.Parse()

	if *nodeID == "" {
		fmt.Fprintf(os.Stderr, "Error: --node-id is required\n")
		os.Exit(1)
	}
	// --cluster-id is optional; the fulfillment service may identify the
	// cluster from connection credentials or a mounted ConfigMap.

	vendorSockets, err := parseVendorSockets(*vendorSocketsFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing --vendor-sockets: %v\n", err)
		os.Exit(1)
	}

	klog.Infof("Starting OSAC CSI driver %s version %s (commit %s)", *driverName, version, gitCommit)
	klog.Infof("CSI endpoint: %s", *csiEndpoint)
	klog.Infof("Node ID: %s", *nodeID)
	klog.Infof("Vendor sockets: %v", vendorSockets)

	var volumeClient fulfillment.VolumeClient
	var controlPlaneClient fulfillment.ControlPlaneClient

	if *fulfillmentEndpoint != "" {
		fmt.Fprintf(os.Stderr, "Error: --fulfillment-endpoint is set but the gRPC client is not implemented yet\n")
		os.Exit(1)
	}
	klog.Infof("No fulfillment endpoint configured, using in-memory stubs")
	volumeClient = fulfillment.NewVolumeStub("default-backend", "nfs")
	controlPlaneClient = &fulfillment.ControlPlaneStub{}

	d, err := driver.NewDriver(
		*driverName, version, *csiEndpoint, *nodeID, *clusterID,
		volumeClient, controlPlaneClient, vendorSockets,
	)
	if err != nil {
		klog.Fatalf("Failed to create driver: %v", err)
	}

	if err := d.Run(); err != nil {
		klog.Fatalf("Failed to run driver: %v", err)
	}
}

func parseVendorSockets(s string) (map[string]string, error) {
	result := make(map[string]string)
	if s == "" {
		return result, nil
	}

	pairs := strings.Split(s, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid vendor socket pair %q: expected format backend=socketpath", pair)
		}

		backend := strings.TrimSpace(parts[0])
		socketPath := strings.TrimSpace(parts[1])

		if backend == "" || socketPath == "" {
			return nil, fmt.Errorf("invalid vendor socket pair %q: backend and socketpath must not be empty", pair)
		}

		result[backend] = socketPath
	}

	return result, nil
}
