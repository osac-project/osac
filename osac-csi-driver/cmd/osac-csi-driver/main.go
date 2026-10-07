package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/oauth"
	experimentalcredentials "google.golang.org/grpc/experimental/credentials"
	"k8s.io/klog/v2"

	"github.com/osac-project/osac/osac-csi-driver/pkg/driver"
	"github.com/osac-project/osac/osac-csi-driver/pkg/fulfillment"
)

var (
	version   = "dev"
	gitCommit = "unknown"
)

const tokenHTTPTimeout = 30 * time.Second

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
	fulfillmentCAFile := flag.String("fulfillment-ca-file", "",
		"Path to a PEM CA bundle for verified fulfillment-service and OAuth TLS connections")
	allowStub := flag.Bool("allow-stub", false, "Allow the in-memory volume stub when fulfillment endpoint is empty")
	grpcInsecure := flag.Bool("grpc-insecure", false, "Skip TLS server certificate verification")
	vendorSocketsFlag := flag.String("vendor-sockets", "",
		"Comma-separated backend=socketpath pairs for vendor node CSI sockets (e.g. ontap=/csi/trident/csi.sock)")
	vendorControllersFlag := flag.String("vendor-controllers", "",
		"Comma-separated backend=endpoint pairs for vendor CSI controllers, keyed "+
			"by StorageBackend provider (e.g. ontap=trident-csi-controller.osac-csi-backends.svc:50051). "+
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
	klog.Infof("Vendor sockets configured for %d backends", len(vendorSockets))
	klog.Infof("Vendor controllers configured for %d backends", len(vendorControllers))

	if err := validateFulfillmentFlags(
		*fulfillmentEndpoint,
		*fulfillmentCAFile, *grpcInsecure,
		*fulfillmentClientID, *fulfillmentClientSecretFile, *fulfillmentIssuerURL,
		*allowStub,
	); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var volumeClient fulfillment.VolumeClient

	if *fulfillmentEndpoint != "" {
		// Establish the gRPC connection to the fulfillment-service and back the
		// real VolumeClient with it. The connection carries transport
		// credentials and the per-RPC OAuth2 token (see dialFulfillment).
		conn, err := dialFulfillment(*fulfillmentEndpoint, *fulfillmentCAFile, *grpcInsecure,
			*fulfillmentClientID, *fulfillmentClientSecretFile, *fulfillmentIssuerURL)
		if err != nil {
			klog.Fatalf("Failed to connect to fulfillment-service: %v", err)
		}
		defer func() {
			if cerr := conn.Close(); cerr != nil {
				klog.Warningf("error closing fulfillment-service connection: %v", cerr)
			}
		}()
		klog.Info("Connected to fulfillment service")
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
	endpoint, caFile string, grpcInsecure bool,
	clientID, clientSecretFile, issuerURL string, allowStub bool,
) error {
	if caFile != "" && endpoint == "" {
		return fmt.Errorf("--fulfillment-ca-file requires --fulfillment-endpoint")
	}
	if caFile != "" && grpcInsecure {
		return fmt.Errorf("--fulfillment-ca-file cannot be combined with --grpc-insecure")
	}

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
	if endpoint == "" && set != 0 {
		return fmt.Errorf("fulfillment credentials require --fulfillment-endpoint")
	}
	if endpoint != "" && set == 0 {
		return fmt.Errorf(
			"--fulfillment-endpoint requires --fulfillment-client-id, " +
				"--fulfillment-client-secret-file, and --fulfillment-issuer-url",
		)
	}
	if endpoint == "" && !allowStub {
		return fmt.Errorf("--fulfillment-endpoint is required unless --allow-stub is set")
	}
	return nil
}

func dialFulfillment(
	endpoint, caFile string, insecureSkipVerify bool,
	clientID, clientSecretFile, issuerURL string,
) (*grpc.ClientConn, error) {
	tlsCfg, err := fulfillmentTLSConfig(caFile, insecureSkipVerify)
	if err != nil {
		return nil, err
	}
	// The OpenShift router does not support ALPN, so we use the
	// experimental credentials package that disables the ALPN check.
	// See https://github.com/grpc/grpc-go/issues/434
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(experimentalcredentials.NewTLSWithALPNDisabled(tlsCfg)),
	}

	if clientID != "" && clientSecretFile != "" && issuerURL != "" {
		ts, err := newClientCredentialsTokenSource(
			context.Background(), clientID, clientSecretFile, issuerURL, tlsCfg,
		)
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
	tlsConfig *tls.Config,
) (oauth2.TokenSource, error) {
	data, err := os.ReadFile(clientSecretFile)
	if err != nil {
		return nil, fmt.Errorf("reading client secret file %s: %w", clientSecretFile, err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return nil, fmt.Errorf("client secret file %s is empty", clientSecretFile)
	}

	tokenURL, err := buildTokenURL(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("building token URL: %w", err)
	}

	httpClient := newTokenHTTPClient(tlsConfig)
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)

	cfg := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: secret,
		TokenURL:     tokenURL,
	}
	return cfg.TokenSource(ctx), nil
}

func newTokenHTTPClient(tlsConfig *tls.Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	transport.TLSClientConfig = tlsConfig.Clone()
	return &http.Client{
		Transport: transport,
		Timeout:   tokenHTTPTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errors.New("refusing token redirect")
		},
	}
}

func fulfillmentTLSConfig(caFile string, insecureSkipVerify bool) (*tls.Config, error) {
	if caFile == "" {
		return newTLSConfig(insecureSkipVerify), nil
	}
	if insecureSkipVerify {
		return nil, fmt.Errorf("--fulfillment-ca-file cannot be combined with --grpc-insecure")
	}

	pemData, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading fulfillment CA file %s: %w", caFile, err)
	}
	if len(strings.TrimSpace(string(pemData))) == 0 {
		return nil, fmt.Errorf("fulfillment CA file %s is empty", caFile)
	}

	rootCAs, err := fulfillmentCACertPool(pemData)
	if err != nil {
		return nil, fmt.Errorf("validating fulfillment CA file %s: %w", caFile, err)
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
	}, nil
}

func fulfillmentCACertPool(pemData []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	certificateCount := 0
	for len(pemData) > 0 {
		block, rest := pem.Decode(pemData)
		if block == nil {
			if len(strings.TrimSpace(string(pemData))) == 0 {
				break
			}
			return nil, fmt.Errorf("contains malformed PEM data")
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("contains a %q PEM block instead of a certificate", block.Type)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("contains an invalid certificate: %w", err)
		}
		if (!certificate.IsCA && certificate.Version != 1) ||
			(certificate.KeyUsage != 0 && certificate.KeyUsage&x509.KeyUsageCertSign == 0) {
			return nil, fmt.Errorf("contains an invalid CA certificate")
		}
		pool.AddCert(certificate)
		certificateCount++
		pemData = rest
	}
	if certificateCount == 0 {
		return nil, fmt.Errorf("does not contain a PEM certificate")
	}
	return pool, nil
}

func newTLSConfig(insecureSkipVerify bool) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecureSkipVerify, //nolint:gosec // user-controlled flag
	}
}

// buildTokenURL constructs the Keycloak token endpoint URL from the issuer URL.
// It normalizes a trailing slash so callers don't have to. Only HTTPS issuer
// URLs with an authority are accepted because this URL is used for credentials.
func buildTokenURL(issuerURL string) (string, error) {
	parsed, err := url.Parse(issuerURL)
	if err != nil {
		return "", fmt.Errorf("invalid issuer URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("issuer URL must be an absolute HTTPS URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("issuer URL must not contain a query or fragment")
	}

	tokenURL, err := url.JoinPath(parsed.String(), "protocol/openid-connect/token")
	if err != nil {
		return "", fmt.Errorf("building token URL path: %w", err)
	}
	return tokenURL, nil
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
