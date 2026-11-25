// Command query-auth-gate authenticates the admin query Routes before either
// OAuth proxy sees the request. A bearer client gets a plain 401 or 403
// instead of a login redirect, and only an authorized request reaches the
// allowlist sidecar.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/osac-project/osac/osac-observability/internal/authgate"
)

const defaultExemptPaths = "/oauth2/,/healthz,/ping"

// inClusterConfig is a seam for the tests: the OpenShift mode is the only one
// that talks to the API server.
var inClusterConfig = rest.InClusterConfig

type config struct {
	listenAddr    string
	mode          string
	proxyURL      string
	allowlistURL  string
	cookieName    string
	exempt        []string
	authenticator authgate.Authenticator
}

func main() {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		log.Fatalf("query-auth-gate: %v", err)
	}
	gate, err := authgate.New(authgate.Config{
		ProxyURL:          cfg.proxyURL,
		AllowlistURL:      cfg.allowlistURL,
		SessionCookieName: cfg.cookieName,
		Authenticator:     cfg.authenticator,
		ExemptPaths:       cfg.exempt,
	})
	if err != nil {
		log.Fatalf("query-auth-gate: %v", err)
	}
	server := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           gate,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("query-auth-gate: %v", err)
		}
	}()
	log.Printf("query-auth-gate: listening on %s in %s mode", cfg.listenAddr, cfg.mode)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

// loadConfig reads the gate's environment. Every authentication input is
// required for its mode: a gate that cannot decide must not serve.
func splitList(value string) []string {
	var values []string
	for _, entry := range strings.Split(value, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			values = append(values, entry)
		}
	}
	return values
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		listenAddr:   getenv("LISTEN_ADDR"),
		mode:         strings.ToLower(strings.TrimSpace(getenv("MODE"))),
		proxyURL:     getenv("PROXY_URL"),
		allowlistURL: getenv("ALLOWLIST_URL"),
		cookieName:   getenv("SESSION_COOKIE_NAME"),
	}
	if cfg.listenAddr == "" {
		cfg.listenAddr = ":9092"
	}
	if cfg.proxyURL == "" {
		return config{}, errors.New("PROXY_URL is required")
	}
	if cfg.allowlistURL == "" {
		return config{}, errors.New("ALLOWLIST_URL is required")
	}
	exempt := getenv("EXEMPT_PATHS")
	if exempt == "" {
		exempt = defaultExemptPaths
	}
	cfg.exempt = splitList(exempt)
	switch cfg.mode {
	case "keycloak":
		if cfg.cookieName == "" {
			cfg.cookieName = "_oauth2_proxy"
		}
		authenticator, err := authgate.NewKeycloakAuthenticator(authgate.KeycloakConfig{
			Issuer:    getenv("OIDC_ISSUER_URL"),
			Audiences: splitList(getenv("OIDC_AUDIENCE")),
			AdminRole: getenv("ADMIN_ROLE"),
		})
		if err != nil {
			return config{}, err
		}
		cfg.authenticator = authenticator
	case "openshift":
		if cfg.cookieName == "" {
			cfg.cookieName = "_oauth_proxy"
		}
		group := getenv("OPENSHIFT_GROUP")
		if group == "" {
			return config{}, errors.New("OPENSHIFT_GROUP is required")
		}
		kubeConfig, err := inClusterConfig()
		if err != nil {
			return config{}, fmt.Errorf("in-cluster configuration: %w", err)
		}
		client, err := kubernetes.NewForConfig(kubeConfig)
		if err != nil {
			return config{}, fmt.Errorf("kubernetes client: %w", err)
		}
		dynamicClient, err := dynamic.NewForConfig(kubeConfig)
		if err != nil {
			return config{}, fmt.Errorf("dynamic client: %w", err)
		}
		cfg.authenticator = &authgate.OpenShiftAuthenticator{
			Client:  client,
			Dynamic: dynamicClient,
			Group:   group,
		}
	default:
		return config{}, fmt.Errorf("MODE must be keycloak or openshift (got %q)", getenv("MODE"))
	}
	return cfg, nil
}
