package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/osac-project/osac/osac-csi-driver/pkg/driver"
	"github.com/osac-project/osac/osac-csi-driver/pkg/fulfillment"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/oauth"
	experimentalcredentials "google.golang.org/grpc/experimental/credentials"
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
	fulfillmentTokenFile := flag.String("fulfillment-token-file", "",
		"Path to a file containing the bearer token for fulfillment-service authentication")
	grpcInsecure := flag.Bool("grpc-insecure", false, "Skip TLS server certificate verification")
	vendorSocketsFlag := flag.String("vendor-sockets", "",
		"Comma-separated backend=socketpath pairs (e.g. ontap=/csi/trident/csi.sock)")
	driverName := flag.String("driver-name", "csi.osac.openshift.io", "CSI driver name")

	flag.Parse()

	if *nodeID == "" {
		fmt.Fprintf(os.Stderr, "Error: --node-id is required\n")
		os.Exit(1)
	}

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
		// Establish the gRPC connection to the fulfillment-service.
		// TODO(OSAC-2872): use the connection to create real VolumeClient
		// and ControlPlaneClient once the Volume API is implemented.
		conn, err := dialFulfillment(*fulfillmentEndpoint, *grpcInsecure, *fulfillmentTokenFile)
		if err != nil {
			klog.Fatalf("Failed to connect to fulfillment-service: %v", err)
		}
		defer func() { _ = conn.Close() }()
		klog.Infof("Fulfillment endpoint: %s (connected, using stubs until Volume API is implemented)", *fulfillmentEndpoint)
	} else {
		klog.Infof("No fulfillment endpoint configured, using in-memory stubs")
	}
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

func dialFulfillment(endpoint string, insecureSkipVerify bool, tokenFile string) (*grpc.ClientConn, error) {
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecureSkipVerify, //nolint:gosec // user-controlled flag
	}
	// The OpenShift router does not support ALPN, so we use the
	// experimental credentials package that disables the ALPN check.
	// See https://github.com/grpc/grpc-go/issues/434
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(experimentalcredentials.NewTLSWithALPNDisabled(tlsCfg)),
	}

	if tokenFile != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(
			oauth.TokenSource{TokenSource: &fileTokenSource{path: tokenFile}},
		))
	}

	return grpc.NewClient(endpoint, dialOpts...)
}

type fileTokenSource struct {
	path string
}

func (f *fileTokenSource) Token() (*oauth2.Token, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("reading token file %s: %w", f.path, err)
	}
	return &oauth2.Token{
		AccessToken: strings.TrimSpace(string(data)),
		TokenType:   "Bearer",
	}, nil
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
