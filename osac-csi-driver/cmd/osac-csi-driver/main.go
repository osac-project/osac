package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/osac-project/osac/osac-csi-driver/pkg/driver"
	"github.com/osac-project/osac/osac-csi-driver/pkg/fulfillment"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
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
	fulfillmentClientID := flag.String("fulfillment-client-id", "",
		"OAuth2 client ID for fulfillment-service authentication")
	fulfillmentClientSecretFile := flag.String("fulfillment-client-secret-file", "",
		"Path to a file containing the OAuth2 client secret for fulfillment-service authentication")
	fulfillmentIssuerURL := flag.String("fulfillment-issuer-url", "",
		"Keycloak issuer URL for client_credentials token exchange (e.g. https://keycloak.example.com/realms/myrealm)")
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

	if err := validateFulfillmentFlags(
		*fulfillmentEndpoint,
		*fulfillmentClientID, *fulfillmentClientSecretFile, *fulfillmentIssuerURL,
	); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var volumeClient fulfillment.VolumeClient

	if *fulfillmentEndpoint != "" {
		// Establish the gRPC connection to the fulfillment-service and back the
		// real VolumeClient with it. The connection carries transport
		// credentials and the per-RPC OAuth2 token (see dialFulfillment).
		conn, err := dialFulfillment(*fulfillmentEndpoint, *grpcInsecure,
			*fulfillmentClientID, *fulfillmentClientSecretFile, *fulfillmentIssuerURL)
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
		volumeClient = fulfillment.NewVolumeStub("local", "nfs")
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

// validateFulfillmentFlags ensures the three credential flags are either all
// set or all empty, and that --fulfillment-endpoint is not set without
// credentials. Partial configuration is a user error.
func validateFulfillmentFlags(
	endpoint, clientID, clientSecretFile, issuerURL string,
) error {
	set := 0
	if clientID != "" {
		set++
	}
	if clientSecretFile != "" {
		set++
	}
	if issuerURL != "" {
		set++
	}
	if set != 0 && set != 3 {
		return fmt.Errorf(
			"--fulfillment-client-id, --fulfillment-client-secret-file, " +
				"and --fulfillment-issuer-url must all be set or all be empty",
		)
	}
	if endpoint != "" && set == 0 {
		return fmt.Errorf(
			"--fulfillment-endpoint requires --fulfillment-client-id, " +
				"--fulfillment-client-secret-file, and --fulfillment-issuer-url",
		)
	}
	return nil
}

func dialFulfillment(
	endpoint string, insecureSkipVerify bool,
	clientID, clientSecretFile, issuerURL string,
) (*grpc.ClientConn, error) {
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

	if clientID != "" && clientSecretFile != "" && issuerURL != "" {
		ts, err := newClientCredentialsTokenSource(context.Background(), clientID, clientSecretFile, issuerURL)
		if err != nil {
			return nil, fmt.Errorf("setting up client credentials: %w", err)
		}
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(
			oauth.TokenSource{TokenSource: ts},
		))
	}

	return grpc.NewClient(endpoint, dialOpts...)
}

// newClientCredentialsTokenSource reads the client secret from a file and
// returns an oauth2.TokenSource that uses the OAuth2 client_credentials grant
// to obtain access tokens from the issuer's token endpoint.
func newClientCredentialsTokenSource(
	ctx context.Context,
	clientID, clientSecretFile, issuerURL string,
) (oauth2.TokenSource, error) {
	data, err := os.ReadFile(clientSecretFile)
	if err != nil {
		return nil, fmt.Errorf("reading client secret file %s: %w", clientSecretFile, err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return nil, fmt.Errorf("client secret file %s is empty", clientSecretFile)
	}

	tokenURL := buildTokenURL(issuerURL)

	cfg := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: secret,
		TokenURL:     tokenURL,
	}
	return cfg.TokenSource(ctx), nil
}

// buildTokenURL constructs the Keycloak token endpoint URL from the issuer URL.
// It normalizes a trailing slash so callers don't have to.
func buildTokenURL(issuerURL string) string {
	return strings.TrimRight(issuerURL, "/") + "/protocol/openid-connect/token"
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
