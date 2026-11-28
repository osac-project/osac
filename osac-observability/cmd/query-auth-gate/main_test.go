package main

import (
	"strings"
	"testing"

	"k8s.io/client-go/rest"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func validKeycloakEnv() map[string]string {
	return map[string]string{
		"MODE":            "keycloak",
		"ALLOWLIST_URL":   "http://127.0.0.1:9091",
		"OIDC_ISSUER_URL": "https://keycloak.example.com/realms/osac",
		"OIDC_AUDIENCE":   "osac-ui, osac-cli",
		"ADMIN_ROLE":      "cloud-provider-admin",
	}
}

func validOpenShiftEnv() map[string]string {
	return map[string]string{
		"MODE":            "openshift",
		"ALLOWLIST_URL":   "http://127.0.0.1:9091",
		"OPENSHIFT_GROUP": "osac-cloud-infrastructure-admins",
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := loadConfig(env(validKeycloakEnv()))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.listenAddr != ":9092" {
		t.Fatalf("listenAddr = %q, want :9092", cfg.listenAddr)
	}
	if cfg.authenticator == nil {
		t.Fatalf("no authenticator")
	}
}

func stubInClusterConfig(t *testing.T) {
	t.Helper()
	previous := inClusterConfig
	inClusterConfig = func() (*rest.Config, error) {
		return &rest.Config{Host: "https://127.0.0.1:6443"}, nil
	}
	t.Cleanup(func() { inClusterConfig = previous })
}

func TestLoadConfigRejectsAnUnknownMode(t *testing.T) {
	values := validKeycloakEnv()
	values["MODE"] = "basic"
	_, err := loadConfig(env(values))
	if err == nil || !strings.Contains(err.Error(), "MODE") {
		t.Fatalf("error = %v, want one naming MODE", err)
	}
}

func TestLoadConfigRejectsAMissingAllowlistURL(t *testing.T) {
	values := validKeycloakEnv()
	delete(values, "ALLOWLIST_URL")
	_, err := loadConfig(env(values))
	if err == nil || !strings.Contains(err.Error(), "ALLOWLIST_URL") {
		t.Fatalf("error = %v, want one naming ALLOWLIST_URL", err)
	}
}

func TestLoadConfigRejectsAMissingIssuer(t *testing.T) {
	values := validKeycloakEnv()
	delete(values, "OIDC_ISSUER_URL")
	_, err := loadConfig(env(values))
	if err == nil || !strings.Contains(err.Error(), "OIDC_ISSUER_URL") {
		t.Fatalf("error = %v, want one naming OIDC_ISSUER_URL", err)
	}
}

func TestLoadConfigRejectsAMissingAdminRole(t *testing.T) {
	values := validKeycloakEnv()
	delete(values, "ADMIN_ROLE")
	_, err := loadConfig(env(values))
	if err == nil || !strings.Contains(err.Error(), "ADMIN_ROLE") {
		t.Fatalf("error = %v, want one naming ADMIN_ROLE", err)
	}
}

func TestLoadConfigRejectsAMissingOpenShiftGroup(t *testing.T) {
	values := validOpenShiftEnv()
	delete(values, "OPENSHIFT_GROUP")
	_, err := loadConfig(env(values))
	if err == nil || !strings.Contains(err.Error(), "OPENSHIFT_GROUP") {
		t.Fatalf("error = %v, want one naming OPENSHIFT_GROUP", err)
	}
}
