/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package restgateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/encoding/protojson"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/network"
	"github.com/osac-project/fulfillment-service/internal/servers"
	shtdwn "github.com/osac-project/fulfillment-service/internal/shutdown"
	"github.com/osac-project/fulfillment-service/internal/version"
)

// Cmd creates and returns the `start rest-gateway` command.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	command := &cobra.Command{
		Use:                   "rest-gateway [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := command.Flags()
	network.AddListenerFlags(flags, network.HttpListenerName, network.DefaultHttpAddress)
	network.AddListenerFlags(flags, network.MetricsListenerName, network.DefaultMetricsAddress)
	network.AddCorsFlags(flags, network.HttpListenerName)
	network.AddGrpcClientFlags(flags, network.GrpcClientName, network.DefaultGrpcAddress)
	flags.StringArrayVar(
		&runner.args.caFiles,
		"ca-file",
		[]string{},
		caFileFlagHelp,
	)
	return command
}

// runnerContext contains the data and logic needed to run the `start rest-gateway` command.
type runnerContext struct {
	logger       *slog.Logger
	flags        *pflag.FlagSet
	grpcClient   *grpc.ClientConn
	healthClient healthv1.HealthClient
	args         struct {
		caFiles   []string
		tokenFile string
	}
}

// run runs the `start rest-gateway` command.
func (c *runnerContext) run(cmd *cobra.Command, argv []string) error {
	// Get the context:
	ctx, cancel := context.WithCancel(cmd.Context())

	// Get the dependencies from the context:
	c.logger = logging.LoggerFromContext(ctx)

	// Save the flags:
	c.flags = cmd.Flags()

	// prepare the metrics registerer:
	metricsRegisterer := prometheus.DefaultRegisterer

	// Create the shutdown sequence:
	shutdown, err := shtdwn.NewSequence().
		SetLogger(c.logger).
		AddSignals(syscall.SIGTERM, syscall.SIGINT).
		AddContext("context", 0, cancel).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create shutdown sequence: %w", err)
	}

	// Create the network listener:
	c.logger.InfoContext(ctx, "Creating REST gateway listener")
	gwListener, err := network.NewListener().
		SetLogger(c.logger).
		SetFlags(c.flags, network.HttpListenerName).
		AddTLSProtocol("h2").
		AddTLSProtocol("http/1.1").
		Build()
	if err != nil {
		return err
	}

	// Load the trusted CA certificates:
	c.logger.InfoContext(ctx, "Loading trusted CA certificates")
	caPool, err := network.NewCertPool().
		SetLogger(c.logger).
		AddSystemFiles(true).
		AddKubernetesFiles(true).
		AddFiles(c.args.caFiles...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to load trusted CA certificates: %w", err)
	}

	// Calculate the user agent:
	c.logger.InfoContext(ctx, "Calculating user agent")
	userAgent := fmt.Sprintf("%s/%s", userAgent, version.Get())

	// Create the gRPC client:
	c.logger.InfoContext(ctx, "Creating gRPC client")
	c.grpcClient, err = network.NewGrpcClient().
		SetLogger(c.logger).
		SetFlags(c.flags, network.GrpcClientName).
		SetCaPool(caPool).
		SetUserAgent(userAgent).
		SetMetricsSubsystem("outbound").
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return err
	}

	// Create the gateway multiplexer:
	c.logger.InfoContext(ctx, "Creating REST gateway server")
	gatewayMarshaller := &runtime.JSONPb{
		MarshalOptions: protojson.MarshalOptions{
			UseProtoNames: true,
		},
	}
	gatewayMux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.HTTPBodyMarshaler{
			Marshaler: gatewayMarshaller,
		}),
		runtime.WithIncomingHeaderMatcher(func(key string) (string, bool) {
			if strings.EqualFold(key, servers.DryRunHTTPHeader) {
				return servers.DryRunMetadataKey, true
			}
			return runtime.DefaultHeaderMatcher(key)
		}),
	)

	// Register the public and private API service handlers:
	err = c.registerHandlers(ctx, gatewayMux)
	if err != nil {
		return err
	}

	// Add the health endpoint:
	c.logger.InfoContext(ctx, "Adding health endpoint")
	c.healthClient = healthv1.NewHealthClient(c.grpcClient)
	err = gatewayMux.HandlePath(http.MethodGet, "/healthz", c.handleHealth)
	if err != nil {
		return fmt.Errorf("failed to register health endpoint: %w", err)
	}

	// Add the CORS support:
	corsMiddleware, err := network.NewCorsMiddleware().
		SetLogger(c.logger).
		SetFlags(c.flags, network.HttpListenerName).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create CORS middleware: %w", err)
	}
	handler := corsMiddleware(gatewayMux)

	// Create the metrics server:
	c.logger.InfoContext(ctx, "Creating metrics listener")
	metricsListener, err := network.NewListener().
		SetLogger(c.logger).
		SetFlags(c.flags, network.MetricsListenerName).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create metrics listener: %w", err)
	}

	// Start the metrics server:
	c.logger.InfoContext(
		ctx,
		"Starting metrics server",
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
	go func() {
		err := metricsServer.Serve(metricsListener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.logger.ErrorContext(
				ctx,
				"Metrics server failed",
				slog.Any("error", err),
			)
		}
	}()
	shutdown.AddHttpServer(network.MetricsListenerName, 0, metricsServer)

	// Start serving:
	c.logger.InfoContext(
		ctx,
		"Start serving",
		slog.String("address", gwListener.Addr().String()),
	)
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)
	http1Server := &http.Server{
		Addr:              gwListener.Addr().String(),
		Handler:           handler,
		Protocols:         &protocols,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		err := http1Server.Serve(gwListener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.logger.ErrorContext(
				ctx,
				"REST gateway server failed",
				slog.Any("error", err),
			)
		}
	}()
	shutdown.AddHttpServer(network.HttpListenerName, 0, http1Server)

	// Keep running till the shutdown sequence cancels the context:
	c.logger.InfoContext(ctx, "Waiting for shutdown to sequence to complete")
	return shutdown.Wait()
}

func (c *runnerContext) handleHealth(
	w http.ResponseWriter, r *http.Request, p map[string]string) {
	response, err := c.healthClient.Check(r.Context(), &healthv1.HealthCheckRequest{})
	if err != nil {
		c.logger.ErrorContext(
			r.Context(),
			"Health check failed",
			slog.Any("error", err),
		)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if response.Status != healthv1.HealthCheckResponse_SERVING {
		c.logger.WarnContext(
			r.Context(),
			"Server is not serving",
			slog.String("status", response.Status.String()),
		)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handlerRegistrar is the common signature shared by all generated grpc-gateway Register*Handler functions.
type handlerRegistrar func(context.Context, *runtime.ServeMux, *grpc.ClientConn) error

// registerHandlers registers all public and private API service handlers on the gateway mux.
func (c *runnerContext) registerHandlers(ctx context.Context, mux *runtime.ServeMux) error {
	handlers := []handlerRegistrar{
		// Public API:
		publicv1.RegisterCapabilitiesHandler,
		publicv1.RegisterClusterTemplatesHandler,
		publicv1.RegisterClusterCatalogItemsHandler,
		publicv1.RegisterClustersHandler,
		publicv1.RegisterClusterVersionsHandler,
		publicv1.RegisterHostTypesHandler,
		publicv1.RegisterComputeInstanceTemplatesHandler,
		publicv1.RegisterComputeInstanceCatalogItemsHandler,
		publicv1.RegisterComputeInstancesHandler,
		publicv1.RegisterBareMetalInstanceTemplatesHandler,
		publicv1.RegisterBareMetalInstanceCatalogItemsHandler,
		publicv1.RegisterBareMetalInstancesHandler,
		publicv1.RegisterNetworkClassesHandler,
		publicv1.RegisterVirtualNetworksHandler,
		publicv1.RegisterSubnetsHandler,
		publicv1.RegisterSecurityGroupsHandler,
		publicv1.RegisterNATGatewaysHandler,
		publicv1.RegisterExternalIPPoolsHandler,
		publicv1.RegisterExternalIPsHandler,
		publicv1.RegisterExternalIPAttachmentsHandler,
		publicv1.RegisterRolesHandler,
		publicv1.RegisterRoleBindingsHandler,
		publicv1.RegisterConsoleSessionsHandler,
		publicv1.RegisterJsonWebKeySetHandler,
		publicv1.RegisterInstanceTypesHandler,
		publicv1.RegisterBareMetalInstanceTypesHandler,

		// Private API:
		privatev1.RegisterCapabilitiesHandler,
		privatev1.RegisterClusterTemplatesHandler,
		privatev1.RegisterClusterCatalogItemsHandler,
		privatev1.RegisterClustersHandler,
		privatev1.RegisterClusterVersionsHandler,
		privatev1.RegisterEventsHandler,
		privatev1.RegisterHostTypesHandler,
		privatev1.RegisterHubsHandler,
		privatev1.RegisterComputeInstanceTemplatesHandler,
		privatev1.RegisterComputeInstanceCatalogItemsHandler,
		privatev1.RegisterComputeInstancesHandler,
		privatev1.RegisterBareMetalInstanceTemplatesHandler,
		privatev1.RegisterBareMetalInstanceCatalogItemsHandler,
		privatev1.RegisterBareMetalInstancesHandler,
		privatev1.RegisterNetworkClassesHandler,
		privatev1.RegisterSecretsHandler,
		privatev1.RegisterStorageBackendsHandler,
		privatev1.RegisterStorageTiersHandler,
		privatev1.RegisterVirtualNetworksHandler,
		privatev1.RegisterSubnetsHandler,
		privatev1.RegisterSecurityGroupsHandler,
		privatev1.RegisterNATGatewaysHandler,
		privatev1.RegisterExternalIPPoolsHandler,
		privatev1.RegisterExternalIPsHandler,
		privatev1.RegisterExternalIPAttachmentsHandler,
		privatev1.RegisterRolesHandler,
		privatev1.RegisterRoleBindingsHandler,
		privatev1.RegisterInstanceTypesHandler,
		privatev1.RegisterBareMetalInstanceTypesHandler,
	}
	for _, register := range handlers {
		if err := register(ctx, mux, c.grpcClient); err != nil {
			return err
		}
	}
	return nil
}

// userAgent is the user agent string for the REST gateway.
const userAgent = "fulfillment-rest-gateway"

const shortHelp = `Starts the REST gateway`

const longHelp = `
Starts the REST gateway.
`

const caFileFlagHelp = `
_FILE|DIRECTORY_ - File or directory containing trusted CA certificates.
`
