package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/osac-project/osac-operator/internal/consoleproxy"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	envPort                   = "OSAC_CONSOLE_PROXY_PORT"
	envHealthProbeBindAddress = "OSAC_CONSOLE_PROXY_HEALTH_BIND_ADDRESS"
	envTLSCertFile            = "OSAC_CONSOLE_PROXY_TLS_CERT_FILE"
	envTLSKeyFile             = "OSAC_CONSOLE_PROXY_TLS_KEY_FILE"
	envVMClusterMode          = "OSAC_CONSOLE_PROXY_VM_CLUSTER_MODE"
)

func main() {
	var (
		port                   int
		healthProbeBindAddress string
		tlsCertFile            string
		tlsKeyFile             string
		vmClusterMode          string
	)
	flag.IntVar(&port, "port", 8443,
		"Port for the HTTPS API server that handles console WebSocket connections")
	flag.StringVar(&healthProbeBindAddress, "health-probe-bind-address",
		envOrDefault(envHealthProbeBindAddress, ":8081"),
		"host:port for the health probe server (/healthz, /readyz)")
	flag.StringVar(&tlsCertFile, "tls-cert-file",
		envOrDefault(envTLSCertFile, ""),
		"Path to the TLS certificate for the API server")
	flag.StringVar(&tlsKeyFile, "tls-key-file",
		envOrDefault(envTLSKeyFile, ""),
		"Path to the TLS private key for the API server")
	flag.StringVar(&vmClusterMode, "vm-cluster-mode",
		envOrDefault(envVMClusterMode, consoleproxy.VMClusterModeAuto),
		`How to obtain the kubeconfig for the cluster where KubeVirt VMs run:
  "remote" - look up a kubeconfig from a Secret labeled osac.openshift.io/remote-cluster-kubeconfig
             in the ComputeInstance's namespace (dedicated VM cluster)
  "local"  - use the proxy's own in-cluster config (VMs on the same cluster)
  "auto"   - try remote first, fall back to local if no Secret is found (default)`)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if !isFlagSet("port") {
		if v := os.Getenv(envPort); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				logger.Warn("Invalid value for env var, using default",
					"env", envPort, "value", v, "default", port, "error", err)
			} else {
				port = n
			}
		}
	}

	hubConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.Error("Failed to build in-cluster config", "error", err)
		os.Exit(1)
	}

	hubClient, err := consoleproxy.NewHubClient(hubConfig)
	if err != nil {
		logger.Error("Failed to create hub client", "error", err)
		os.Exit(1)
	}

	resolver, err := buildConfigResolver(vmClusterMode, hubClient, hubConfig, logger)
	if err != nil {
		logger.Error("Failed to build config resolver", "error", err, "vmClusterMode", vmClusterMode)
		os.Exit(1)
	}

	logger.Info("Console proxy starting",
		slog.String("vmClusterMode", vmClusterMode),
		slog.Int("port", port),
	)

	server, err := consoleproxy.NewServer(consoleproxy.Config{
		Logger:                 logger,
		HubConfig:              hubConfig,
		HubClient:              hubClient,
		ConfigResolver:         resolver,
		Port:                   port,
		HealthProbeBindAddress: healthProbeBindAddress,
		TLSCertFile:            tlsCertFile,
		TLSKeyFile:             tlsKeyFile,
	})
	if err != nil {
		logger.Error("Failed to create server", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := server.Run(ctx); err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func buildConfigResolver(
	mode string, hubClient client.Client, hubConfig *rest.Config, logger *slog.Logger,
) (consoleproxy.ConfigResolver, error) {
	switch mode {
	case consoleproxy.VMClusterModeRemote:
		return consoleproxy.NewRemoteConfigResolver(hubClient, logger), nil

	case consoleproxy.VMClusterModeLocal:
		return consoleproxy.NewLocalConfigResolver(hubConfig, logger), nil

	case consoleproxy.VMClusterModeAuto, "":
		remote := consoleproxy.NewRemoteConfigResolver(hubClient, logger)
		local := consoleproxy.NewLocalConfigResolver(hubConfig, logger)
		return consoleproxy.NewAutoConfigResolver(remote, local, logger), nil

	default:
		return nil, fmt.Errorf(
			"invalid vm-cluster-mode %q: must be %s, %s, or %s",
			mode,
			consoleproxy.VMClusterModeRemote,
			consoleproxy.VMClusterModeLocal,
			consoleproxy.VMClusterModeAuto,
		)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func isFlagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
