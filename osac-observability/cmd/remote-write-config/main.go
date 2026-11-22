// Command remote-write-config upserts OSAC's remote-write settings into the
// management cluster's cluster-monitoring-config ConfigMap and removes only
// those settings when observability is disabled.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/osac-project/osac/osac-observability/internal/remotewrite"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	defaultNamespace          = "openshift-monitoring"
	defaultName               = "cluster-monitoring-config"
	defaultScrapeInterval     = "30s"
	defaultReceiveURL         = "https://thanos-receive.osac-observability.svc.cluster.local:19291/api/v1/receive"
	defaultPlatformNamespaces = `["osac","osac-csi","osac-observability"]`
	dataKey                   = "config.yaml"
)

type config struct {
	Enabled            bool
	ScrapeInterval     string
	ReceiveURL         string
	PlatformNamespaces []string
	Namespace          string
	Name               string
}

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
	if err := run(context.Background(), client, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		ScrapeInterval: defaultScrapeInterval,
		ReceiveURL:     defaultReceiveURL,
		Namespace:      valueOr(getenv, "OBS_CONFIGMAP_NAMESPACE", defaultNamespace),
		Name:           valueOr(getenv, "OBS_CONFIGMAP_NAME", defaultName),
	}
	enabled, err := strconv.ParseBool(valueOr(getenv, "OBSERVABILITY_ENABLED", "false"))
	if err != nil {
		return config{}, fmt.Errorf("OBSERVABILITY_ENABLED: %w", err)
	}
	cfg.Enabled = enabled
	if value := getenv("OBS_SCRAPE_INTERVAL"); value != "" {
		cfg.ScrapeInterval = value
	}
	if value := getenv("OBS_RECEIVE_URL"); value != "" {
		cfg.ReceiveURL = value
	}
	namespaces := valueOr(getenv, "OBS_PLATFORM_NAMESPACES", defaultPlatformNamespaces)
	if err := json.Unmarshal([]byte(namespaces), &cfg.PlatformNamespaces); err != nil {
		return config{}, fmt.Errorf("OBS_PLATFORM_NAMESPACES: %w", err)
	}
	if len(cfg.PlatformNamespaces) == 0 {
		return config{}, fmt.Errorf("OBS_PLATFORM_NAMESPACES must list at least one namespace")
	}
	return cfg, nil
}

func valueOr(getenv func(string) string, name, fallback string) string {
	if value := getenv(name); value != "" {
		return value
	}
	return fallback
}

func (cfg config) options() remotewrite.Options {
	return remotewrite.Options{
		Enabled:            cfg.Enabled,
		ScrapeInterval:     cfg.ScrapeInterval,
		ReceiveURL:         cfg.ReceiveURL,
		PlatformNamespaces: cfg.PlatformNamespaces,
	}
}

func run(ctx context.Context, client kubernetes.Interface, cfg config) error {
	configMaps := client.CoreV1().ConfigMaps(cfg.Namespace)
	cm, err := configMaps.Get(ctx, cfg.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if !cfg.Enabled {
			return nil
		}
		merged, err := remotewrite.Merge(nil, cfg.options())
		if err != nil {
			return err
		}
		_, err = configMaps.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: cfg.Namespace},
			Data:       map[string]string{dataKey: string(merged)},
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("create ConfigMap %s/%s: %w", cfg.Namespace, cfg.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read ConfigMap %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}

	merged, err := remotewrite.Merge([]byte(cm.Data[dataKey]), cfg.options())
	if err != nil {
		return err
	}
	if string(merged) == cm.Data[dataKey] {
		return nil
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[dataKey] = string(merged)
	if _, err := configMaps.Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update ConfigMap %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}
	return nil
}
