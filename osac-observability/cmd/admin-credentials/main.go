// Command admin-credentials provisions the Secrets the admin query proxies
// mount: a copy of the thanos-admin Keycloak client secret and one cookie
// secret per authentication proxy.
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
	defaultCookieSecretNames  = `["thanos-admin-oauth2-proxy","thanos-admin-openshift-oauth"]`
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
	}
	names := valueOr(getenv, "ADMIN_COOKIE_SECRET_NAMES", defaultCookieSecretNames)
	if err := json.Unmarshal([]byte(names), &cfg.CookieSecretNames); err != nil {
		return admincreds.Config{}, fmt.Errorf("ADMIN_COOKIE_SECRET_NAMES: %w", err)
	}
	return cfg, nil
}

func valueOr(getenv func(string) string, name, fallback string) string {
	if value := getenv(name); value != "" {
		return value
	}
	return fallback
}
