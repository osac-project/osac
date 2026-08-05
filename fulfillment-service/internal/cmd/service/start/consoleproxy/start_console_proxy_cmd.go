/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package consoleproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"syscall"
	"time"

	"errors"

	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/httprc/v3/errsink"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"

	"github.com/osac-project/fulfillment-service/internal/auth/jwe"
	"github.com/osac-project/fulfillment-service/internal/console"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/metrics"
	"github.com/osac-project/fulfillment-service/internal/network"
	"github.com/osac-project/fulfillment-service/internal/recovery"
	"github.com/osac-project/fulfillment-service/internal/servers"
	shtdwn "github.com/osac-project/fulfillment-service/internal/shutdown"
)

// Cmd creates and returns the `start console-proxy` command.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	command := &cobra.Command{
		Use:                   "console-proxy [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := command.Flags()
	network.AddListenerFlags(flags, network.GrpcListenerName, network.DefaultGrpcAddress)
	network.AddListenerFlags(flags, consoleListenerName, defaultConsoleAddress)
	network.AddListenerFlags(flags, network.MetricsListenerName, network.DefaultMetricsAddress)
	network.AddCorsFlags(flags, consoleListenerName)
	console.AddPingFlags(flags, "client")
	console.AddPingFlags(flags, "backend")
	console.AddSessionTimeoutFlag(flags)
	network.AddGrpcKeepaliveFlags(flags)
	flags.StringSliceVar(
		&runner.args.caFiles,
		"ca-file",
		[]string{},
		caFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.tokenIssuer,
		"token-issuer",
		"",
		tokenIssuerFlagHelp,
	)
	flags.StringVar(
		&runner.args.tokenDecryptionKey,
		"token-decryption-key",
		"",
		tokenDecryptionKeyFlagHelp,
	)
	return command
}

// runnerContext contains the data and logic needed to run the `start console-proxy` command.
type runnerContext struct {
	logger *slog.Logger
	flags  *pflag.FlagSet
	args   struct {
		caFiles            []string
		tokenIssuer        string
		tokenDecryptionKey string
	}
}

// run runs the `start console-proxy` command.
func (c *runnerContext) run(cmd *cobra.Command, argv []string) error {
	// Get the context and create a cancellable version:
	ctx, cancel := context.WithCancel(cmd.Context())

	// Get the dependencies from the context:
	c.logger = logging.LoggerFromContext(ctx)

	// Save the flags:
	c.flags = cmd.Flags()

	// Prepare the metrics registerer:
	metricsRegisterer := prometheus.DefaultRegisterer

	// Create the shutdown sequence triggered by typical stop signals:
	c.logger.InfoContext(ctx, "Creating shutdown sequence")
	shutdown, err := shtdwn.NewSequence().
		SetLogger(c.logger).
		AddSignals(syscall.SIGTERM, syscall.SIGINT).
		AddContext("context", 0, cancel).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create shutdown sequence: %w", err)
	}

	// Load the trusted CA certificates:
	caPool, err := network.NewCertPool().
		SetLogger(c.logger).
		AddSystemFiles(true).
		AddKubernetesFiles(true).
		AddFiles(c.args.caFiles...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to load trusted CA certificates: %w", err)
	}

	// Validate required token flags:
	if c.args.tokenIssuer == "" {
		return fmt.Errorf("flag '--token-issuer' is required")
	}
	if c.args.tokenDecryptionKey == "" {
		return fmt.Errorf("flag '--token-decryption-key' is required")
	}

	// Create the JWKS cache for background key refresh:
	jwksURL := c.args.tokenIssuer + "/.well-known/jwks.json"
	jwksCache, err := c.createJWKSCache(ctx, jwksURL, caPool)
	if err != nil {
		return err
	}

	// Create the token opener (decrypt JWE + verify JWS):
	c.logger.InfoContext(ctx, "Creating token opener",
		slog.String("issuer", c.args.tokenIssuer),
		slog.String("decryption_key", c.args.tokenDecryptionKey),
	)
	tokenOpener, err := jwe.NewOpener().
		SetContext(ctx).
		SetLogger(c.logger).
		SetDecryptionKeyFile(c.args.tokenDecryptionKey).
		SetJWKSURL(jwksURL).
		SetIssuer(c.args.tokenIssuer).
		SetAudience(console.TicketAudience).
		SetCache(jwksCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create token opener: %w", err)
	}

	// Wrap the token opener for console-specific ticket claim extraction:
	opener := console.NewTicketOpener(tokenOpener)

	// Read console configuration from flags:
	clientPingConfig, err := console.PingConfigFromFlags(c.flags, "client")
	if err != nil {
		return fmt.Errorf("failed to read client ping configuration: %w", err)
	}
	backendPingConfig, err := console.PingConfigFromFlags(c.flags, "backend")
	if err != nil {
		return fmt.Errorf("failed to read backend ping configuration: %w", err)
	}
	sessionTimeout, err := console.SessionTimeoutFromFlags(c.flags)
	if err != nil {
		return fmt.Errorf("failed to read session timeout: %w", err)
	}
	keepaliveConfig, err := network.GrpcKeepaliveConfigFromFlags(c.flags)
	if err != nil {
		return fmt.Errorf("failed to read gRPC keepalive configuration: %w", err)
	}

	// Create the KubeVirt backend. The proxy dials pre-computed URIs from the
	// encrypted ticket using the CA pool for TLS.
	kvBackend, err := console.NewKubeVirtBackend().
		SetLogger(c.logger).
		SetCAPool(caPool).
		SetPingConfig(backendPingConfig).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create kubevirt backend: %w", err)
	}

	// Create the console manager:
	consoleManager, err := console.NewManager().
		SetLogger(c.logger).
		SetSessionTimeout(sessionTimeout).
		AddBackend(console.ResourceTypeComputeInstance, kvBackend).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create console manager: %w", err)
	}

	// Create the shared console proxy core (ticket verification + relay logic):
	core, err := servers.NewConsoleProxyCore().
		SetLogger(c.logger).
		SetOpener(opener).
		SetManager(consoleManager).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create console proxy core: %w", err)
	}

	// Prepare the logging interceptor:
	c.logger.InfoContext(ctx, "Creating logging interceptor")
	loggingInterceptor, err := logging.NewInterceptor().
		SetLogger(c.logger).
		SetFlags(c.flags).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create logging interceptor: %w", err)
	}

	// Prepare the panic interceptor:
	c.logger.InfoContext(ctx, "Creating panic interceptor")
	panicInterceptor, err := recovery.NewGrpcPanicInterceptor().
		SetLogger(c.logger).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create panic interceptor: %w", err)
	}

	// Prepare the metrics interceptor:
	c.logger.InfoContext(ctx, "Creating metrics interceptor")
	metricsInterceptor, err := metrics.NewGrpcInterceptor().
		SetSubsystem("inbound").
		SetRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create metrics interceptor: %w", err)
	}

	// Create the gRPC server (no auth interceptor, no tx interceptor):
	c.logger.InfoContext(ctx, "Creating gRPC server")
	grpcServer := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    keepaliveConfig.Time,
			Timeout: keepaliveConfig.Timeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             keepaliveConfig.MinTime,
			PermitWithoutStream: true,
		}),
		grpc.ChainUnaryInterceptor(
			panicInterceptor.UnaryServer,
			metricsInterceptor.UnaryServer,
			loggingInterceptor.UnaryServer,
		),
		grpc.ChainStreamInterceptor(
			panicInterceptor.StreamServer,
			metricsInterceptor.StreamServer,
			loggingInterceptor.StreamServer,
		),
	)
	shutdown.AddGrpcServer(network.GrpcListenerName, 0, grpcServer)

	// Register the reflection and health servers. Start as NOT_SERVING so
	// that the readiness probe fails until the JWKS cache is warm.
	c.logger.InfoContext(ctx, "Registering gRPC reflection server")
	reflection.RegisterV1(grpcServer)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_NOT_SERVING)
	healthv1.RegisterHealthServer(grpcServer, healthServer)

	// Register the ConsoleProxy gRPC server:
	c.logger.InfoContext(ctx, "Registering console proxy gRPC server")
	consoleProxyServer := servers.NewConsoleProxyServer(core)
	publicv1.RegisterConsoleProxyServer(grpcServer, consoleProxyServer)

	// Create and start the gRPC listener:
	grpcListener, err := network.NewListener().
		SetLogger(c.logger).
		SetFlags(c.flags, network.GrpcListenerName).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create gRPC listener: %w", err)
	}
	c.logger.InfoContext(ctx, "Starting gRPC server",
		slog.String("address", grpcListener.Addr().String()),
	)
	serveErrors := make(chan error, 3)
	go func() {
		err := grpcServer.Serve(grpcListener)
		if err != nil {
			c.logger.ErrorContext(ctx, "gRPC server failed",
				slog.Any("error", err),
			)
			serveErrors <- err
		}
	}()

	// Read and validate the allowed origins for WebSocket Origin validation:
	if !c.flags.Changed("console-cors-allowed-origins") {
		return fmt.Errorf(
			"flag '--console-cors-allowed-origins' is required for WebSocket Origin validation",
		)
	}
	allowedOrigins, err := c.flags.GetStringSlice("console-cors-allowed-origins")
	if err != nil {
		return fmt.Errorf("failed to get console CORS allowed origins: %w", err)
	}
	c.logger.InfoContext(ctx, "Console WebSocket Origin validation",
		slog.Any("allowed_origins", allowedOrigins),
	)

	// Create the WebSocket console handler:
	c.logger.InfoContext(ctx, "Creating console WebSocket handler")
	wsHandler := servers.NewConsoleProxyWSHandler(core, allowedOrigins, clientPingConfig)

	// Create the console HTTP mux and middleware chain:
	consoleMux := http.NewServeMux()
	consoleMux.Handle(
		"GET /api/fulfillment/v1/console_sessions/connect",
		servers.ConsoleMetrics(metricsRegisterer,
			servers.ConsoleLogging(c.logger, wsHandler)),
	)
	consoleHandler := servers.ConsolePanicRecovery(c.logger, consoleMux)

	// Create and start the console HTTP listener:
	c.logger.InfoContext(ctx, "Creating console listener")
	consoleListener, err := network.NewListener().
		SetLogger(c.logger).
		SetFlags(c.flags, consoleListenerName).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create console listener: %w", err)
	}
	c.logger.InfoContext(ctx, "Starting console server",
		slog.String("address", consoleListener.Addr().String()),
	)
	consoleHTTPServer := &http.Server{
		Handler:           consoleHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		err := consoleHTTPServer.Serve(consoleListener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.logger.ErrorContext(ctx, "Console server failed",
				slog.Any("error", err),
			)
			serveErrors <- err
		}
	}()
	shutdown.AddHttpServer(consoleListenerName, 0, consoleHTTPServer)

	// Create the metrics listener:
	c.logger.InfoContext(ctx, "Creating metrics listener")
	metricsListener, err := network.NewListener().
		SetLogger(c.logger).
		SetFlags(c.flags, network.MetricsListenerName).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create metrics listener: %w", err)
	}

	// Start the metrics server:
	c.logger.InfoContext(ctx, "Starting metrics server",
		slog.String("address", metricsListener.Addr().String()),
	)
	metricsServer := &http.Server{
		Addr:              metricsListener.Addr().String(),
		Handler:           promhttp.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdown.AddHttpServer(network.MetricsListenerName, 0, metricsServer)
	go func() {
		err := metricsServer.Serve(metricsListener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.logger.ErrorContext(ctx, "Metrics server failed",
				slog.Any("error", err),
			)
			serveErrors <- err
		}
	}()

	// Wait for JWKS keys in the background, then mark the service as ready:
	go c.waitForJWKSReady(ctx, jwksCache, jwksURL, healthServer)

	// Keep running till the shutdown sequence finishes or a server fails:
	c.logger.InfoContext(ctx, "Waiting for shutdown sequence to complete")
	shutdownDone := make(chan struct{})
	go func() {
		if err := shutdown.Wait(); err != nil {
			c.logger.ErrorContext(ctx, "Shutdown sequence failed", slog.Any("error", err))
		}
		close(shutdownDone)
	}()
	select {
	case err := <-serveErrors:
		c.logger.ErrorContext(ctx, "Server failed, triggering shutdown",
			slog.Any("error", err),
		)
		shutdown.Start(1)
		return err
	case <-shutdownDone:
		return nil
	}
}

// createJWKSCache creates a JWKS cache and registers the given URL without
// blocking on the first fetch. The cache starts fetching in the background;
// callers should poll cache.Lookup to detect when keys are available.
func (c *runnerContext) createJWKSCache(ctx context.Context, jwksURL string, caPool *x509.CertPool) (*jwk.Cache, error) {
	c.logger.InfoContext(ctx, "Creating JWKS cache",
		slog.String("jwks_url", jwksURL),
	)
	cache, err := jwk.NewCache(ctx, httprc.NewClient(
		httprc.WithErrorSink(errsink.NewSlog(c.logger)),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create JWKS cache: %w", err)
	}

	registerOpts := []jwk.RegisterOption{
		jwk.WithWaitReady(false),
		jwk.WithHttprcResourceOption(httprc.WithMinInterval(time.Minute)),
	}
	if caPool != nil {
		registerOpts = append(registerOpts, jwk.WithHTTPClient(
			jwk.WrapHTTPClientDefaults(&http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						RootCAs:    caPool,
						MinVersion: tls.VersionTLS13,
					},
				},
			}),
		))
	}
	if err := cache.Register(ctx, jwksURL, registerOpts...); err != nil {
		return nil, fmt.Errorf("failed to register JWKS URL: %w", err)
	}
	return cache, nil
}

// waitForJWKSReady polls the JWKS cache until keys are available, then sets
// the health server to SERVING. Exits if ctx is cancelled.
func (c *runnerContext) waitForJWKSReady(ctx context.Context, cache *jwk.Cache, jwksURL string, healthServer *health.Server) {
	for {
		if _, err := cache.Lookup(ctx, jwksURL); err == nil {
			c.logger.InfoContext(ctx, "JWKS keys available, setting health to SERVING")
			healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

const shortHelp = `Starts the console proxy`

const longHelp = `
Starts the console proxy. The proxy handles WebSocket and gRPC
console connections, verifying JWT tokens issued by the gRPC server
and proxying bytes to backend KubeVirt VNC/serial consoles.

The --console-cors-allowed-origins flag is required and must list the
origin(s) of the web console UI (e.g. https://fulfillment-api.example.com).
`

const caFileFlagHelp = `
_FILE_ - Files or directories containing trusted CA certificates in PEM
format. Used for JWKS endpoint TLS and backend WebSocket TLS.
`

const tokenIssuerFlagHelp = `
_URL_ - Issuer URL of the fulfillment-service. Used to derive the JWKS
endpoint (<issuer>/.well-known/jwks.json) and to validate the iss claim.
`

const tokenDecryptionKeyFlagHelp = `
_FILE_ - Path to the PEM-encoded private key for decrypting JWE tokens.
This is the proxy's encryption private key.
`

// consoleListenerName is the name used for the console HTTP listener flags.
const consoleListenerName = "console"

// defaultConsoleAddress is the default address for the console HTTP listener.
const defaultConsoleAddress = ":8090"
