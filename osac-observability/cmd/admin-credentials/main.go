// Command admin-credentials copies the trust bundle the Keycloak chart
// distributes into the observability namespace, so the admin query gate can
// verify the identity provider's certificate when it fetches the OIDC
// discovery document and the JWK set. It runs as an install/upgrade hook and is
// a no-op when the copy already matches.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/osac-project/osac/osac-observability/internal/admincreds"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	defaultNamespace = "osac-observability"
	// The gate verifies the Keycloak Route's certificate against the bundle the
	// keycloak chart distributes; it is copied here because a volume cannot
	// cross namespaces.
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
	return admincreds.Config{
		Namespace: valueOr(getenv, "OBS_NAMESPACE", defaultNamespace),
		// The chart mounts the copy at a fixed path, so the names are not
		// configurable.
		CABundle: admincreds.CABundle{
			SourceNamespace: defaultCABundleSourceNamespace,
			SourceName:      defaultCABundleSourceName,
			SourceKey:       defaultCABundleSourceKey,
			Name:            defaultCABundleName,
			Key:             defaultCABundleKey,
		},
	}, nil
}

func valueOr(getenv func(string) string, name, fallback string) string {
	if value := getenv(name); value != "" {
		return value
	}
	return fallback
}
