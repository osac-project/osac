// Command metrics-auth is OSAC's metrics authentication helper. The write-proxy
// subcommand serves the managed remote-write endpoint: it terminates mTLS with
// the tenant-cluster certificate, stamps the cluster identity from that
// certificate, applies the drop rule, and forwards the request to Thanos Receive.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/osac-project/osac/osac-observability/internal/writeproxy"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	defaultListen            = ":8443"
	defaultProbeListen       = ":8080"
	defaultTLSCertFile       = "/etc/tls/tls.crt"
	defaultTLSKeyFile        = "/etc/tls/tls.key"
	defaultClientCAFile      = "/etc/write-ca/ca.crt"
	defaultUpstream          = "https://thanos-receive.osac-observability.svc.cluster.local:19291/api/v1/receive"
	defaultUpstreamCAFile    = "/etc/service-ca/service-ca.crt"
	defaultNamespace         = "osac-observability"
	defaultKeepKubePrefix    = "kube_node_"
	defaultDropPrefixes      = "container_,claim,mount,kserve_,vllm_,dcgm_"
	defaultRevocationRetry   = time.Second
	defaultShutdownGraceTime = 10 * time.Second
)

func main() {
	if err := run(os.Args[1:], os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, getenv func(string) string) error {
	if len(args) == 0 {
		return errors.New("usage: metrics-auth write-proxy [flags]")
	}
	switch args[0] {
	case "write-proxy":
		cfg, err := loadWriteProxyConfig(args[1:], getenv)
		if err != nil {
			return err
		}
		if err := runWriteProxy(cfg); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q; usage: metrics-auth write-proxy [flags]", args[0])
	}
}

type writeProxyConfig struct {
	Listen              string
	ProbeListen         string
	TLSCertFile         string
	TLSKeyFile          string
	ClientCAFile        string
	UpstreamCAFile      string
	Upstream            *url.URL
	RevocationNamespace string
	RevocationName      string
	Rule                writeproxy.DropRule
	RevocationRetry     time.Duration
}

func loadWriteProxyConfig(args []string, getenv func(string) string) (writeProxyConfig, error) {
	flags := flag.NewFlagSet("write-proxy", flag.ContinueOnError)
	listen := flags.String("listen", envOr(getenv, "OSAC_WRITE_PROXY_LISTEN", defaultListen), "address to serve mTLS on")
	probeListen := flags.String("probe-listen", envOr(getenv, "OSAC_WRITE_PROXY_PROBE_LISTEN", defaultProbeListen), "plain address serving only the health endpoints")
	tlsCert := flags.String("tls-cert-file", envOr(getenv, "OSAC_WRITE_PROXY_TLS_CERT_FILE", defaultTLSCertFile), "server certificate")
	tlsKey := flags.String("tls-key-file", envOr(getenv, "OSAC_WRITE_PROXY_TLS_KEY_FILE", defaultTLSKeyFile), "server private key")
	clientCA := flags.String("client-ca-file", envOr(getenv, "OSAC_WRITE_PROXY_CLIENT_CA_FILE", defaultClientCAFile), "write CA bundle used to verify clients")
	upstreamCA := flags.String("upstream-ca-file", envOr(getenv, "OSAC_WRITE_PROXY_UPSTREAM_CA_FILE", defaultUpstreamCAFile), "CA bundle used to verify Thanos Receive")
	upstream := flags.String("upstream", envOr(getenv, "OSAC_WRITE_PROXY_UPSTREAM", defaultUpstream), "Thanos Receive remote-write URL")
	namespace := flags.String("revocation-namespace", envOr(getenv, "OSAC_WRITE_PROXY_REVOCATION_NAMESPACE", defaultNamespace), "namespace of the revocation ConfigMap")
	name := flags.String("revocation-name", envOr(getenv, "OSAC_WRITE_PROXY_REVOCATION_NAME", writeproxy.RevocationConfigMapName), "revocation ConfigMap name")
	keepKube := flags.String("keep-kube-prefix", envOr(getenv, "OSAC_WRITE_PROXY_KEEP_KUBE_PREFIX", defaultKeepKubePrefix), "the only kube_ metric family allowed through")
	dropPrefixes := flags.String("drop-prefixes", envOr(getenv, "OSAC_WRITE_PROXY_DROP_PREFIXES", defaultDropPrefixes), "comma-separated denied metric-name prefixes")
	retry := flags.Duration("revocation-retry", defaultRevocationRetry, "delay after a failed revocation read or watch")
	if err := flags.Parse(args); err != nil {
		return writeProxyConfig{}, err
	}
	parsedUpstream, err := url.Parse(*upstream)
	if err != nil {
		return writeProxyConfig{}, fmt.Errorf("invalid upstream URL: %w", err)
	}
	if parsedUpstream.Scheme != "https" && parsedUpstream.Scheme != "http" {
		return writeProxyConfig{}, fmt.Errorf("invalid upstream URL %q: scheme must be http or https", *upstream)
	}
	prefixes := make([]string, 0, len(strings.Split(*dropPrefixes, ",")))
	for _, prefix := range strings.Split(*dropPrefixes, ",") {
		if trimmed := strings.TrimSpace(prefix); trimmed != "" {
			prefixes = append(prefixes, trimmed)
		}
	}
	return writeProxyConfig{
		Listen:              *listen,
		ProbeListen:         *probeListen,
		TLSCertFile:         *tlsCert,
		TLSKeyFile:          *tlsKey,
		ClientCAFile:        *clientCA,
		UpstreamCAFile:      *upstreamCA,
		Upstream:            parsedUpstream,
		RevocationNamespace: *namespace,
		RevocationName:      *name,
		Rule:                writeproxy.DropRule{KeepKubePrefix: *keepKube, DropPrefixes: prefixes},
		RevocationRetry:     *retry,
	}, nil
}

func runWriteProxy(cfg writeProxyConfig) error {
	serverCertificate, err := newReloadingCertificate(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("loading server certificate: %w", err)
	}
	clientRoots, err := newReloadingCertPool(cfg.ClientCAFile)
	if err != nil {
		return fmt.Errorf("loading write CA: %w", err)
	}
	upstreamTransport, err := newUpstreamTransport(cfg.UpstreamCAFile)
	if err != nil {
		return fmt.Errorf("loading upstream CA: %w", err)
	}

	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("in-cluster configuration: %w", err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}

	store := writeproxy.NewRevocationStore()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	syncer := &writeproxy.RevocationSyncer{
		Client:    client,
		Namespace: cfg.RevocationNamespace,
		Name:      cfg.RevocationName,
		Store:     store,
		Retry:     cfg.RevocationRetry,
	}
	go func() {
		if err := syncer.Run(ctx); err != nil {
			slog.Error("watching the revocation ConfigMap", "err", err)
		}
	}()

	handler, err := writeproxy.NewHandler(writeproxy.Config{
		Upstream:    cfg.Upstream,
		RootsSource: clientRoots.Pool,
		Revocation:  store,
		Rule:        cfg.Rule,
		Client:      &http.Client{Timeout: time.Minute, Transport: upstreamTransport},
		Logger:      slog.Default(),
	})
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.Listen, err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         serverTLSConfig(serverCertificate),
	}
	go func() {
		if err := server.Serve(tls.NewListener(listener, server.TLSConfig)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serving the managed remote-write endpoint", "err", err)
		}
	}()

	// The kubelet cannot present a client certificate, so probes use a separate plain
	// listener that serves only /livez and /readyz.
	probeListener, err := net.Listen("tcp", cfg.ProbeListen)
	if err != nil {
		return fmt.Errorf("listening for probes on %s: %w", cfg.ProbeListen, err)
	}
	probeServer := &http.Server{
		Handler:           writeproxy.NewProbeHandler(store),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := probeServer.Serve(probeListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serving the write-proxy probes", "err", err)
		}
	}()
	slog.Info("serving the managed remote-write endpoint",
		"listen", cfg.Listen, "probeListen", cfg.ProbeListen, "upstream", cfg.Upstream)

	<-ctx.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), defaultShutdownGraceTime)
	defer cancel()
	if err := probeServer.Shutdown(shutdownContext); err != nil {
		return err
	}
	return server.Shutdown(shutdownContext)
}

func loadCertPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificates found in %s", path)
	}
	return pool, nil
}

func newUpstreamTransport(caFile string) (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caFile == "" {
		return transport, nil
	}
	roots, err := loadCertPool(caFile)
	if err != nil {
		return nil, err
	}
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return transport, nil
}

func envOr(getenv func(string) string, name, fallback string) string {
	if value := getenv(name); value != "" {
		return value
	}
	return fallback
}
