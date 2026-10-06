package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

var (
	// BridgeAddr is the TCP address for the server to listen on
	BridgeAddr = getEnvVar("HOST", "0.0.0.0") + ":" + getEnvVar("PORT", "8080")
	// FulfillmentApiUrl where the Fulfillment requests will be proxied to
	FulfillmentApiUrl = getEnvUrlVar("FULFILLMENT_API_URL", "")
	// FulfillmentTlsCaFile CA file for Fulfillment service
	FulfillmentTlsCaFile = getEnvVar("FULFILLMENT_TLS_CA_FILE", "")
	// FulfillmentTlsInsecure disables TLS certificate verification for Fulfillment service
	FulfillmentTlsInsecure = getEnvVar("FULFILLMENT_TLS_INSECURE", "") == "1"
	// OIDCClientID is the client_id registered in the IdP for this UI application.
	OIDCClientID = getEnvVar("OIDC_CLIENT_ID", "osac-ui")
	// OIDCIssuerURL is the OIDC issuer URL (e.g. https://keycloak.example.com/realms/osac).
	// Required when OIDC_SERVICE_CLIENT_ID / OIDC_SERVICE_CLIENT_SECRET are set.
	OIDCIssuerURL = getEnvVar("OIDC_ISSUER_URL", "")
	// OIDCServiceClientID is the Keycloak client_id for the proxy's own service account,
	// used to authenticate backend calls such as gRPC reflection at startup.
	OIDCServiceClientID = getEnvVar("OIDC_SERVICE_CLIENT_ID", "")
	// OIDCServiceClientSecret is the Keycloak client_secret for the proxy's service account.
	OIDCServiceClientSecret = getEnvVar("OIDC_SERVICE_CLIENT_SECRET", "")
	// OIDCTlsCaFile CA file for the OIDC IdP (discovery, token exchange, refresh).
	OIDCTlsCaFile = getEnvVar("OIDC_TLS_CA_FILE", "")
	// OIDCTlsInsecure disables TLS certificate verification when contacting the OIDC IdP
	// (discovery, token exchange, refresh). For development only.
	OIDCTlsInsecure = getEnvVar("OIDC_TLS_INSECURE", "") == "1"
	// BaseUIURL is the public base URL of the UI used to compute the /callback redirect URI.
	// If empty, the proxy derives it from the SPA's redirect_base query parameter.
	BaseUIURL = getEnvUrlVar("BASE_UI_URL", "")
)

// FulfillmentGrpcTarget returns the bare host:port for the gRPC upstream,
// derived from FULFILLMENT_API_URL by stripping the scheme.
func FulfillmentGrpcTarget() (string, error) {
	if FulfillmentApiUrl == "" {
		return "", nil
	}
	u, err := url.Parse(FulfillmentApiUrl)
	if err != nil {
		return "", fmt.Errorf("invalid FULFILLMENT_API_URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid FULFILLMENT_API_URL: unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid FULFILLMENT_API_URL: empty host")
	}
	return u.Host, nil
}

func getEnvUrlVar(key, defaultValue string) string {
	return strings.TrimSuffix(getEnvVar(key, defaultValue), "/")
}

func getEnvVar(key, defaultValue string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultValue
}
