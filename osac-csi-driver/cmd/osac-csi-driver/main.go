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
		"Comma-separated backend=socketpath pairs for vendor node CSI sockets (e.g. ontap=/csi/trident/csi.sock)")
	vendorControllersFlag := flag.String("vendor-controllers", "",
		"Comma-separated backend=endpoint pairs for vendor CSI controllers, keyed "+
			"by StorageBackend name (e.g. ontap=trident-csi-controller.osac-csi-backends.svc:50051). "+
			"Use the value 'none' for node-local backends that need no attach (e.g. local=none)")
	driverName := flag.String("driver-name", "csi.osac.openshift.io", "CSI driver name")

	flag.Parse()

	if *nodeID == "" {
		fmt.Fprintf(os.Stderr, "Error: --node-id is required\n")
		os.Exit(1)
	}

	vendorSockets, err := parseBackendMap(*vendorSocketsFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing --vendor-sockets: %v\n", err)
		os.Exit(1)
	}

	vendorControllers, err := parseBackendMap(*vendorControllersFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing --vendor-controllers: %v\n", err)
		os.Exit(1)
	}

	klog.Infof("Starting OSAC CSI driver %s version %s (commit %s)", *driverName, version, gitCommit)
	klog.Infof("CSI endpoint: %s", *csiEndpoint)
	klog.Infof("Node ID: %s", *nodeID)
	klog.Infof("Vendor sockets: %v", vendorSockets)
	klog.Infof("Vendor controllers: %v", vendorControllers)

	var volumeClient fulfillment.VolumeClient

	if *fulfillmentEndpoint != "" {
		// Establish the gRPC connection to the fulfillment-service and back the
		// real VolumeClient with it. The connection carries transport
		// credentials and the per-RPC bearer token (see dialFulfillment).
		conn, err := dialFulfillment(*fulfillmentEndpoint, *grpcInsecure, *fulfillmentTokenFile)
		if err != nil {
			klog.Fatalf("Failed to connect to fulfillment-service: %v", err)
		}
		defer func() {
			if cerr := conn.Close(); cerr != nil {
				klog.Warningf("error closing fulfillment-service connection: %v", cerr)
			}
		}()
		klog.Infof("Fulfillment endpoint: %s (connected)", *fulfillmentEndpoint)
		volumeClient = fulfillment.NewVolumeClient(conn)
	} else {
		klog.Infof("No fulfillment endpoint configured, using in-memory volume stub")
		volumeClient = fulfillment.NewVolumeStub("default-backend", "nfs")
	}

	d, err := driver.NewDriver(
		*driverName, version, *csiEndpoint, *nodeID, *clusterID,
		volumeClient, vendorSockets, vendorControllers,
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

// parseBackendMap parses a comma-separated list of backend=value pairs into a
// map. It is used for both --vendor-sockets and --vendor-controllers.
func parseBackendMap(s string) (map[string]string, error) {
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
			return nil, fmt.Errorf("invalid pair %q: expected format backend=value", pair)
		}

		backend := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if backend == "" || value == "" {
			return nil, fmt.Errorf("invalid pair %q: backend and value must not be empty", pair)
		}

		result[backend] = value
	}

	return result, nil
}
