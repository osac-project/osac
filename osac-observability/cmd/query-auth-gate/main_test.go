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
		"PROXY_URL":       "http://127.0.0.1:4180",
		"ALLOWLIST_URL":   "http://127.0.0.1:9091",
		"OIDC_ISSUER_URL": "https://keycloak.example.com/realms/osac",
		"OIDC_AUDIENCE":   "osac-ui, osac-cli",
		"ADMIN_ROLE":      "cloud-provider-admin",
	}
}

func validOpenShiftEnv() map[string]string {
	return map[string]string{
		"MODE":            "openshift",
		"PROXY_URL":       "http://127.0.0.1:4181",
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
	if cfg.cookieName != "_oauth2_proxy" {
		t.Fatalf("cookieName = %q, want _oauth2_proxy", cfg.cookieName)
	}
	want := []string{"/oauth2/", "/healthz", "/ping"}
	if strings.Join(cfg.exempt, ",") != strings.Join(want, ",") {
		t.Fatalf("exempt = %v, want %v", cfg.exempt, want)
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

func TestLoadConfigUsesTheOpenShiftCookieName(t *testing.T) {
	stubInClusterConfig(t)
	cfg, err := loadConfig(env(validOpenShiftEnv()))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.cookieName != "_oauth_proxy" {
		t.Fatalf("cookieName = %q, want _oauth_proxy", cfg.cookieName)
	}
}

func TestLoadConfigRejectsAnUnknownMode(t *testing.T) {
	values := validKeycloakEnv()
	values["MODE"] = "basic"
	_, err := loadConfig(env(values))
	if err == nil || !strings.Contains(err.Error(), "MODE") {
		t.Fatalf("error = %v, want one naming MODE", err)
	}
}

func TestLoadConfigRejectsAMissingProxyURL(t *testing.T) {
	values := validKeycloakEnv()
	delete(values, "PROXY_URL")
	_, err := loadConfig(env(values))
	if err == nil || !strings.Contains(err.Error(), "PROXY_URL") {
		t.Fatalf("error = %v, want one naming PROXY_URL", err)
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
