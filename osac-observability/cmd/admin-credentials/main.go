// Command admin-credentials provisions what the admin query proxies mount: a
// copy of the thanos-admin Keycloak client secret, one cookie secret per
// authentication proxy, and the trust bundle that verifies the identity
// provider's certificate.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/osac-project/osac/osac-observability/internal/admincreds"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	defaultKeycloakNamespace  = "keycloak"
	defaultKeycloakSecretName = "keycloak-client-secrets"
	defaultKeycloakKey        = "thanos-admin"
	defaultNamespace          = "osac-observability"
	defaultClientSecretName   = "thanos-admin-client-secret"
	defaultClientSecretKey    = "client-secret"
	defaultCookieSecretKey    = "cookie-secret"
	// oauth2-proxy decodes base64url from its environment variable, which cannot
	// carry binary bytes; ose-oauth-proxy decodes standard base64 from the file
	// it mounts.
	defaultCookieSecrets = `[{"name":"thanos-admin-oauth2-proxy"},{"name":"thanos-admin-openshift-oauth","base64":true}]`
	// The proxies verify the Keycloak Route's certificate against the bundle the
	// keycloak chart distributes; it is copied beside them because a volume
	// cannot cross namespaces.
	defaultCABundleSourceNamespace = "keycloak"
	defaultCABundleSourceName      = "ca-bundle"
	defaultCABundleSourceKey       = "bundle.pem"
	defaultCABundleName            = "keycloak-ca-bundle"
	defaultCABundleKey             = "bundle.pem"
)

func main() {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "in-cluster configuration: %v\n", err)
		os.Exit(1)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubernetes client: %v\n", err)
		os.Exit(1)
	}
	if err := admincreds.Reconcile(context.Background(), client, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func loadConfig(getenv func(string) string) (admincreds.Config, error) {
	cfg := admincreds.Config{
		KeycloakNamespace:  valueOr(getenv, "KEYCLOAK_NAMESPACE", defaultKeycloakNamespace),
		KeycloakSecretName: valueOr(getenv, "KEYCLOAK_SECRET_NAME", defaultKeycloakSecretName),
		KeycloakKey:        valueOr(getenv, "KEYCLOAK_SECRET_KEY", defaultKeycloakKey),
		Namespace:          valueOr(getenv, "OBS_NAMESPACE", defaultNamespace),
		ClientSecretName:   valueOr(getenv, "ADMIN_CLIENT_SECRET_NAME", defaultClientSecretName),
		ClientSecretKey:    valueOr(getenv, "ADMIN_CLIENT_SECRET_KEY", defaultClientSecretKey),
		CookieSecretKey:    valueOr(getenv, "ADMIN_COOKIE_SECRET_KEY", defaultCookieSecretKey),
		// The proxies always verify the Keycloak Route through this bundle, and
		// the chart mounts the copy at a fixed path, so the names are not
		// configurable.
		CABundle: admincreds.CABundle{
			SourceNamespace: defaultCABundleSourceNamespace,
			SourceName:      defaultCABundleSourceName,
			SourceKey:       defaultCABundleSourceKey,
			Name:            defaultCABundleName,
			Key:             defaultCABundleKey,
		},
	}
	secrets := valueOr(getenv, "ADMIN_COOKIE_SECRETS", defaultCookieSecrets)
	if err := json.Unmarshal([]byte(secrets), &cfg.CookieSecrets); err != nil {
		return admincreds.Config{}, fmt.Errorf("ADMIN_COOKIE_SECRETS: %w", err)
	}
	return cfg, nil
}

func valueOr(getenv func(string) string, name, fallback string) string {
	if value := getenv(name); value != "" {
		return value
	}
	return fallback
}
