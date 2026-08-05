/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"k8s.io/klog/v2"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/controllers"
	"github.com/osac-project/fulfillment-service/internal/controllers/baremetalinstance"
	"github.com/osac-project/fulfillment-service/internal/controllers/cluster"
	"github.com/osac-project/fulfillment-service/internal/controllers/computeinstance"
	"github.com/osac-project/fulfillment-service/internal/controllers/externalip"
	"github.com/osac-project/fulfillment-service/internal/controllers/externalipattachment"
	"github.com/osac-project/fulfillment-service/internal/controllers/externalippool"
	"github.com/osac-project/fulfillment-service/internal/controllers/identityprovider"
	"github.com/osac-project/fulfillment-service/internal/controllers/natgateway"
	"github.com/osac-project/fulfillment-service/internal/controllers/onboarding"
	"github.com/osac-project/fulfillment-service/internal/controllers/project"
	"github.com/osac-project/fulfillment-service/internal/controllers/projectmembership"
	"github.com/osac-project/fulfillment-service/internal/controllers/role"
	"github.com/osac-project/fulfillment-service/internal/controllers/rolebinding"
	"github.com/osac-project/fulfillment-service/internal/controllers/securitygroup"
	"github.com/osac-project/fulfillment-service/internal/controllers/subnet"
	"github.com/osac-project/fulfillment-service/internal/controllers/tenant"
	"github.com/osac-project/fulfillment-service/internal/controllers/user"
	"github.com/osac-project/fulfillment-service/internal/controllers/virtualnetwork"
	internalhealth "github.com/osac-project/fulfillment-service/internal/health"
	"github.com/osac-project/fulfillment-service/internal/idp"
	hubscheme "github.com/osac-project/fulfillment-service/internal/kubernetes/scheme"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/network"
	"github.com/osac-project/fulfillment-service/internal/oauth"
	shtdwn "github.com/osac-project/fulfillment-service/internal/shutdown"
	"github.com/osac-project/fulfillment-service/internal/version"
)

// Cmd creates and returns the `start controllers` command.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	command := &cobra.Command{
		Use:                   "controller [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := command.Flags()
	flags.StringArrayVar(
		&runner.args.caFiles,
		"ca-file",
		[]string{},
		caFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.authIssuerUrl,
		"auth-issuer-url",
		"",
		authIssuerUrlFlagHelp,
	)
	flags.StringVar(
		&runner.args.authIssuerUrlFile,
		"auth-issuer-url-file",
		"",
		authIssuerUrlFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.authClientId,
		"auth-client-id",
		"",
		authClientIdFlagHelp,
	)
	flags.StringVar(
		&runner.args.authClientIdFile,
		"auth-client-id-file",
		"",
		authClientIdFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.authClientSecret,
		"auth-client-secret",
		"",
		authClientSecretFlagHelp,
	)
	flags.StringVar(
		&runner.args.authClientSecretFile,
		"auth-client-secret-file",
		"",
		authClientSecretFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.idpProvider,
		"idp-provider",
		"",
		idpProviderFlagHelp,
	)
	_ = flags.MarkDeprecated("idp-provider", "This flag is deprecated and ignored. Only Keycloak is supported as the identity provider.")
	flags.StringVar(
		&runner.args.idpURL,
		"idp-url",
		"",
		idpUrlFlagHelp,
	)
	flags.StringVar(
		&runner.args.idpClientIdFile,
		"idp-client-id-file",
		"",
		idpClientIdFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.idpClientId,
		"idp-client-id",
		"",
		idpClientIdFlagHelp,
	)
	flags.StringVar(
		&runner.args.idpClientSecretFile,
		"idp-client-secret-file",
		"",
		idpClientSecretFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.idpClientSecret,
		"idp-client-secret",
		"",
		idpClientSecretFlagHelp,
	)
	network.AddGrpcClientFlags(flags, network.GrpcClientName, network.DefaultGrpcAddress)
	network.AddListenerFlags(flags, network.GrpcListenerName, network.DefaultGrpcAddress)
	network.AddListenerFlags(flags, network.MetricsListenerName, network.DefaultMetricsAddress)
	return command
}

// runnerContext contains the data and logic needed to run the `start controllers` command.
type runnerContext struct {
	logger *slog.Logger
	flags  *pflag.FlagSet
	args   struct {
		caFiles              []string
		authIssuerUrl        string
		authIssuerUrlFile    string
		authClientId         string
		authClientIdFile     string
		authClientSecret     string
		authClientSecretFile string
		idpProvider          string
		idpURL               string
		idpClientId          string
		idpClientIdFile      string
		idpClientSecret      string
		idpClientSecretFile  string
	}
	client *grpc.ClientConn
}

// run runs the `start controllers` command.
func (r *runnerContext) run(cmd *cobra.Command, argv []string) error { //nolint:gocyclo
	var err error

	// Get the context:
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// Get the dependencies from the context:
	r.logger = logging.LoggerFromContext(ctx)

	// Configure the Kubernetes libraries to use the logger:
	logrLogger := logr.FromSlogHandler(r.logger.Handler())
	crlog.SetLogger(logrLogger)
	klog.SetLogger(logrLogger)

	// Save the flags:
	r.flags = cmd.Flags()

	// Check the flags:
	if r.args.authIssuerUrl != "" && r.args.authIssuerUrlFile != "" {
		return fmt.Errorf("flags '--auth-issuer-url' and '--auth-issuer-url-file' are mutually exclusive")
	}
	if r.args.authClientId != "" && r.args.authClientIdFile != "" {
		return fmt.Errorf("flags '--auth-client-id' and '--auth-client-id-file' are mutually exclusive")
	}
	if r.args.authClientSecret != "" && r.args.authClientSecretFile != "" {
		return fmt.Errorf("flags '--auth-client-secret' and '--auth-client-secret-file' are mutually exclusive")
	}
	if r.args.idpClientId != "" && r.args.idpClientIdFile != "" {
		return fmt.Errorf("flags '--idp-client-id' and '--idp-client-id-file' are mutually exclusive")
	}
	if r.args.idpClientSecret != "" && r.args.idpClientSecretFile != "" {
		return fmt.Errorf("flags '--idp-client-secret' and '--idp-client-secret-file' are mutually exclusive")
	}
	if r.args.authIssuerUrl == "" && r.args.authIssuerUrlFile == "" {
		return fmt.Errorf("flag '--auth-issuer-url' or '--auth-issuer-url-file' is required")
	}
	if r.args.authClientId == "" && r.args.authClientIdFile == "" {
		return fmt.Errorf("flag '--auth-client-id' or '--auth-client-id-file' is required")
	}
	if r.args.authClientSecret == "" && r.args.authClientSecretFile == "" {
		return fmt.Errorf("flag '--auth-client-secret' or '--auth-client-secret-file' is required")
	}

	// Prepare the metrics registerer:
	metricsRegisterer := prometheus.DefaultRegisterer

	// Create the shutdown sequence:
	r.logger.InfoContext(ctx, "Creating shutdown sequence")
	shutdown, err := shtdwn.NewSequence().
		SetLogger(r.logger).
		AddSignals(syscall.SIGTERM, syscall.SIGINT).
		AddContext("context", 0, cancel).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create shutdown sequence: %w", err)
	}

	// Load the trusted CA certificates:
	r.logger.InfoContext(ctx, "Loading trusted CA certificates")
	caPool, err := network.NewCertPool().
		SetLogger(r.logger).
		AddFiles(r.args.caFiles...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to load trusted CA certificates: %w", err)
	}

	// Create the token source:
	r.logger.InfoContext(ctx, "Creating token source")
	tokenSource, err := r.createTokenSource(ctx, caPool)
	if err != nil {
		return err
	}

	// Calculate the user agent:
	r.logger.InfoContext(ctx, "Calculating user agent")
	userAgent := fmt.Sprintf("%s/%s", controllerUserAgent, version.Get())

	// Create the gRPC client:
	r.logger.InfoContext(ctx, "Creating gRPC client")
	r.client, err = network.NewGrpcClient().
		SetLogger(r.logger).
		SetFlags(r.flags, network.GrpcClientName).
		SetCaPool(caPool).
		SetTokenSource(tokenSource).
		SetUserAgent(userAgent).
		SetMetricsSubsystem("outbound").
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create gRPC client: %w", err)
	}

	// Create the gRPC server:
	r.logger.InfoContext(ctx, "Creating gRPC listener")
	grpcListener, err := network.NewListener().
		SetLogger(r.logger).
		SetFlags(r.flags, network.GrpcListenerName).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	grpcServer := grpc.NewServer()
	shutdown.AddGrpcServer(network.GrpcListenerName, 0, grpcServer)

	// Register the reflection server:
	r.logger.InfoContext(ctx, "Registering gRPC reflection server")
	reflection.RegisterV1(grpcServer)

	// Register the health server:
	r.logger.InfoContext(ctx, "Registering gRPC health server")
	healthServer := health.NewServer()
	healthv1.RegisterHealthServer(grpcServer, healthServer)

	// Create the health aggregator:
	r.logger.InfoContext(ctx, "Creating health aggregator")
	healthAggregator, err := internalhealth.NewAggregator().
		SetLogger(r.logger).
		SetServer(healthServer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create health aggregator: %w", err)
	}

	// Start the gRPC server:
	r.logger.InfoContext(
		ctx,
		"Starting gRPC server",
		slog.String("address", grpcListener.Addr().String()),
	)
	go func() {
		err := grpcServer.Serve(grpcListener)
		if err != nil {
			r.logger.ErrorContext(
				ctx,
				"gRPC server failed",
				slog.Any("error", err),
			)
		}
	}()

	// Wait for the server to be ready:
	r.logger.InfoContext(ctx, "Waiting for server to be ready")
	err = r.waitForServer(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for server to be ready: %w", err)
	}

	// Create scheme for typed OSAC CRD access on hub clusters:
	hubScheme, err := hubscheme.NewHub()
	if err != nil {
		return fmt.Errorf("failed to create hub scheme: %w", err)
	}

	// Create the hub cache:
	r.logger.InfoContext(ctx, "Creating hub cache")
	hubCache, err := controllers.NewHubCache().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetScheme(hubScheme).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create hub cache: %w", err)
	}

	// Create the IDP client:
	idpClient, err := r.createIDPClient(ctx, caPool)
	if err != nil {
		return err
	}

	// Create the IDP tenant manager:
	r.logger.InfoContext(ctx, "Creating IDP tenant manager")
	idpManager, err := idp.NewTenantManager().
		SetLogger(r.logger).
		SetClient(idpClient).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create IDP tenant manager: %w", err)
	}

	// Create the IDP project group manager:
	r.logger.InfoContext(ctx, "Creating IDP project group manager")
	projectGroupManager, err := idp.NewProjectGroupManager().
		SetLogger(r.logger).
		SetClient(idpClient).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create IDP project group manager: %w", err)
	}

	// Create the cluster reconciler:
	r.logger.InfoContext(ctx, "Creating cluster reconciler")
	clusterReconcilerFunction, err := cluster.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetHubCache(hubCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create cluster reconciler function: %w", err)
	}
	clusterReconciler, err := controllers.NewReconciler[*privatev1.Cluster]().
		SetLogger(r.logger).
		SetName("cluster").
		SetClient(r.client).
		SetFunction(clusterReconcilerFunction).
		SetEventFilter("has(event.cluster) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create cluster reconciler: %w", err)
	}

	// Start the cluster reconciler:
	r.logger.InfoContext(ctx, "Starting cluster reconciler")
	go func() {
		err := clusterReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Cluster reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Cluster reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the compute instance reconciler:
	r.logger.InfoContext(ctx, "Creating compute instance reconciler")
	computeInstanceReconcilerFunction, err := computeinstance.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetHubCache(hubCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create compute instance reconciler function: %w", err)
	}
	computeInstanceReconciler, err := controllers.NewReconciler[*privatev1.ComputeInstance]().
		SetLogger(r.logger).
		SetName("compute_instance").
		SetClient(r.client).
		SetFunction(computeInstanceReconcilerFunction).
		SetEventFilter("has(event.compute_instance) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create compute instance reconciler: %w", err)
	}

	// Start the compute instance reconciler:
	r.logger.InfoContext(ctx, "Starting compute instance reconciler")
	go func() {
		err := computeInstanceReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Compute instance reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Compute instance reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the bare metal instance reconciler:
	r.logger.InfoContext(ctx, "Creating bare metal instance reconciler")
	bareMetalInstanceReconcilerFunction, err := baremetalinstance.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetHubCache(hubCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create bare metal instance reconciler function: %w", err)
	}
	bareMetalInstanceReconciler, err := controllers.NewReconciler[*privatev1.BareMetalInstance]().
		SetLogger(r.logger).
		SetName("bare_metal_instance").
		SetClient(r.client).
		SetFunction(bareMetalInstanceReconcilerFunction).
		SetEventFilter("has(event.bare_metal_instance) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create bare metal instance reconciler: %w", err)
	}

	// Start the bare metal instance reconciler:
	r.logger.InfoContext(ctx, "Starting bare metal instance reconciler")
	go func() {
		err := bareMetalInstanceReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Bare metal instance reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Bare metal instance reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the subnet reconciler:
	r.logger.InfoContext(ctx, "Creating subnet reconciler")
	subnetReconcilerFunction, err := subnet.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetHubCache(hubCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create subnet reconciler function: %w", err)
	}
	subnetReconciler, err := controllers.NewReconciler[*privatev1.Subnet]().
		SetLogger(r.logger).
		SetName("subnet").
		SetClient(r.client).
		SetFunction(subnetReconcilerFunction).
		SetEventFilter("has(event.subnet) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create subnet reconciler: %w", err)
	}

	// Start the subnet reconciler:
	r.logger.InfoContext(ctx, "Starting subnet reconciler")
	go func() {
		err := subnetReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Subnet reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Subnet reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the virtual network reconciler:
	r.logger.InfoContext(ctx, "Creating virtual network reconciler")
	virtualNetworkReconcilerFunction, err := virtualnetwork.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetHubCache(hubCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create virtual network reconciler function: %w", err)
	}
	virtualNetworkReconciler, err := controllers.NewReconciler[*privatev1.VirtualNetwork]().
		SetLogger(r.logger).
		SetName("virtual_network").
		SetClient(r.client).
		SetFunction(virtualNetworkReconcilerFunction).
		SetEventFilter("has(event.virtual_network) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create virtual network reconciler: %w", err)
	}

	// Start the virtual network reconciler:
	r.logger.InfoContext(ctx, "Starting virtual network reconciler")
	go func() {
		err := virtualNetworkReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Virtual network reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Virtual network reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the security group reconciler:
	r.logger.InfoContext(ctx, "Creating security group reconciler")
	securityGroupReconcilerFunction, err := securitygroup.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetHubCache(hubCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create security group reconciler function: %w", err)
	}
	securityGroupReconciler, err := controllers.NewReconciler[*privatev1.SecurityGroup]().
		SetLogger(r.logger).
		SetName("security_group").
		SetClient(r.client).
		SetFunction(securityGroupReconcilerFunction).
		SetEventFilter("has(event.security_group) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create security group reconciler: %w", err)
	}

	// Start the security group reconciler:
	r.logger.InfoContext(ctx, "Starting security group reconciler")
	go func() {
		err := securityGroupReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Security group reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Security group reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the external IP pool reconciler:
	r.logger.InfoContext(ctx, "Creating external IP pool reconciler")
	externalIPPoolReconcilerFunction, err := externalippool.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetHubCache(hubCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create external IP pool reconciler function: %w", err)
	}
	externalIPPoolReconciler, err := controllers.NewReconciler[*privatev1.ExternalIPPool]().
		SetLogger(r.logger).
		SetName("external_ip_pool").
		SetClient(r.client).
		SetFunction(externalIPPoolReconcilerFunction).
		SetEventFilter("has(event.external_ip_pool) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create external IP pool reconciler: %w", err)
	}

	// Start the external IP pool reconciler:
	r.logger.InfoContext(ctx, "Starting external IP pool reconciler")
	go func() {
		err := externalIPPoolReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "External IP pool reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"External IP pool reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the external IP reconciler:
	r.logger.InfoContext(ctx, "Creating external IP reconciler")
	externalIPReconcilerFunction, err := externalip.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetHubCache(hubCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create external IP reconciler function: %w", err)
	}
	externalIPReconciler, err := controllers.NewReconciler[*privatev1.ExternalIP]().
		SetLogger(r.logger).
		SetName("external_ip").
		SetClient(r.client).
		SetFunction(externalIPReconcilerFunction).
		SetEventFilter("has(event.external_ip) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create external IP reconciler: %w", err)
	}

	// Start the external IP reconciler:
	r.logger.InfoContext(ctx, "Starting external IP reconciler")
	go func() {
		err := externalIPReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "External IP reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"External IP reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the external IP attachment reconciler:
	r.logger.InfoContext(ctx, "Creating external IP attachment reconciler")
	externalIPAttachmentReconcilerFunction, err := externalipattachment.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetHubCache(hubCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create external IP attachment reconciler function: %w", err)
	}
	externalIPAttachmentReconciler, err := controllers.NewReconciler[*privatev1.ExternalIPAttachment]().
		SetLogger(r.logger).
		SetName("external_ip_attachment").
		SetClient(r.client).
		SetFunction(externalIPAttachmentReconcilerFunction).
		SetEventFilter("has(event.external_ip_attachment) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create external IP attachment reconciler: %w", err)
	}

	// Start the external IP attachment reconciler:
	r.logger.InfoContext(ctx, "Starting external IP attachment reconciler")
	go func() {
		err := externalIPAttachmentReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "External IP attachment reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"External IP attachment reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the NAT gateway reconciler:
	r.logger.InfoContext(ctx, "Creating NAT gateway reconciler")
	natGatewayReconcilerFunction, err := natgateway.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetHubCache(hubCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create NAT gateway reconciler function: %w", err)
	}
	natGatewayReconciler, err := controllers.NewReconciler[*privatev1.NATGateway]().
		SetLogger(r.logger).
		SetName("nat_gateway").
		SetClient(r.client).
		SetFunction(natGatewayReconcilerFunction).
		SetEventFilter("has(event.nat_gateway) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create NAT gateway reconciler: %w", err)
	}

	// Start the NAT gateway reconciler:
	r.logger.InfoContext(ctx, "Starting NAT gateway reconciler")
	go func() {
		err := natGatewayReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "NAT gateway reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"NAT gateway reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the role reconciler:
	r.logger.InfoContext(ctx, "Creating role reconciler")
	roleReconcilerFunction, err := role.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create role reconciler function: %w", err)
	}
	roleReconciler, err := controllers.NewReconciler[*privatev1.Role]().
		SetLogger(r.logger).
		SetName("role").
		SetClient(r.client).
		SetFunction(roleReconcilerFunction.Run).
		SetEventFilter("has(event.role)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create role reconciler: %w", err)
	}

	// Start the role reconciler:
	r.logger.InfoContext(ctx, "Starting role reconciler")
	go func() {
		err := roleReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Role reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Role reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the role binding reconciler:
	r.logger.InfoContext(ctx, "Creating role binding reconciler")
	roleBindingReconcilerFunction, err := rolebinding.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetIdpClient(idpClient).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create role binding reconciler function: %w", err)
	}
	roleBindingReconciler, err := controllers.NewReconciler[*privatev1.RoleBinding]().
		SetLogger(r.logger).
		SetName("role_binding").
		SetClient(r.client).
		SetFunction(roleBindingReconcilerFunction.Run).
		SetEventFilter("has(event.role_binding)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create role binding reconciler: %w", err)
	}

	// Start the role binding reconciler:
	r.logger.InfoContext(ctx, "Starting role binding reconciler")
	go func() {
		err := roleBindingReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Role binding reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Role binding reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the tenant reconciler:
	r.logger.InfoContext(ctx, "Creating tenant reconciler")
	tenantReconcilerFunction, err := tenant.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetIdpManager(idpManager).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create tenant reconciler function: %w", err)
	}
	tenantEventFilter := "has(event.tenant)"
	for _, resource := range []string{"virtual_network", "subnet", "security_group", "nat_gateway"} {
		tenantEventFilter += fmt.Sprintf(
			" || (has(event.%s) && event.%s.metadata.labels['osac.openshift.io/default'] == 'true')",
			resource, resource,
		)
	}
	tenantReconciler, err := controllers.NewReconciler[*privatev1.Tenant]().
		SetLogger(r.logger).
		SetName("tenant").
		SetClient(r.client).
		SetFunction(tenantReconcilerFunction.Run).
		SetEventFilter(tenantEventFilter).
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create tenant reconciler: %w", err)
	}

	// Start the tenant reconciler:
	r.logger.InfoContext(ctx, "Starting tenant reconciler")
	go func() {
		err := tenantReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Tenant reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Tenant reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the user reconciler:
	r.logger.InfoContext(ctx, "Creating user reconciler")
	userReconcilerFunction, err := user.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetIdpClient(idpClient).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create user reconciler function: %w", err)
	}
	userReconciler, err := controllers.NewReconciler[*privatev1.User]().
		SetLogger(r.logger).
		SetName("user").
		SetClient(r.client).
		SetFunction(userReconcilerFunction.Run).
		SetEventFilter("has(event.user)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create user reconciler: %w", err)
	}

	// Start the user reconciler:
	r.logger.InfoContext(ctx, "Starting user reconciler")
	go func() {
		err := userReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "User reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"User reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the onboarding reconciler:
	r.logger.InfoContext(ctx, "Creating onboarding reconciler")
	onboardingReconcilerFunction, err := onboarding.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetHubCache(hubCache).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create onboarding reconciler function: %w", err)
	}
	onboardingReconciler, err := controllers.NewReconciler[*privatev1.Tenant]().
		SetLogger(r.logger).
		SetName("onboarding").
		SetClient(r.client).
		SetFunction(onboardingReconcilerFunction).
		SetEventFilter("has(event.tenant) || (has(event.hub) && event.type == EVENT_TYPE_OBJECT_CREATED)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create onboarding reconciler: %w", err)
	}

	// Start the onboarding reconciler:
	r.logger.InfoContext(ctx, "Starting onboarding reconciler")
	go func() {
		err := onboardingReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Onboarding reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Onboarding reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the project reconciler:
	r.logger.InfoContext(ctx, "Creating project reconciler")
	projectReconcilerFunction, err := project.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetProjectGroupManager(projectGroupManager).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create project reconciler function: %w", err)
	}
	projectReconciler, err := controllers.NewReconciler[*privatev1.Project]().
		SetLogger(r.logger).
		SetName("project").
		SetClient(r.client).
		SetFunction(projectReconcilerFunction.Run).
		SetEventFilter("has(event.project)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create project reconciler: %w", err)
	}

	// Start the project reconciler:
	r.logger.InfoContext(ctx, "Starting project reconciler")
	go func() {
		err := projectReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Project reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Project reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the project membership reconciler:
	r.logger.InfoContext(ctx, "Creating project membership reconciler")
	projectMembershipReconcilerFunction, err := projectmembership.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetIdpClient(idpClient).
		Build()
	if err != nil {
		return fmt.Errorf("failed to build project membership reconciler function: %w", err)
	}
	projectMembershipReconciler, err := controllers.NewReconciler[*privatev1.ProjectMembership]().
		SetLogger(r.logger).
		SetName("project_membership").
		SetClient(r.client).
		SetFunction(projectMembershipReconcilerFunction.Run).
		SetEventFilter("has(event.project_membership)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to build project membership reconciler: %w", err)
	}

	// Start the project membership reconciler:
	r.logger.InfoContext(ctx, "Starting project membership reconciler")
	go func() {
		err := projectMembershipReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Project membership reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Project membership reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the identity provider reconciler:
	r.logger.InfoContext(ctx, "Creating identity provider reconciler")
	identityProviderReconcilerFunction, err := identityprovider.NewFunction().
		SetLogger(r.logger).
		SetConnection(r.client).
		SetIdpClient(idpClient).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create identity provider reconciler function: %w", err)
	}
	identityProviderReconciler, err := controllers.NewReconciler[*privatev1.IdentityProvider]().
		SetLogger(r.logger).
		SetName("identity_provider").
		SetClient(r.client).
		SetFunction(identityProviderReconcilerFunction.Run).
		SetEventFilter("has(event.identity_provider)").
		SetHealthReporter(healthAggregator).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create identity provider reconciler: %w", err)
	}

	// Start the identity provider reconciler:
	r.logger.InfoContext(ctx, "Starting identity provider reconciler")
	go func() {
		err := identityProviderReconciler.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			r.logger.InfoContext(ctx, "Identity provider reconciler finished")
		} else {
			r.logger.InfoContext(
				ctx,
				"Identity provider reconciler failed",
				slog.Any("error", err),
			)
		}
	}()

	// Create the metrics listener:
	r.logger.InfoContext(ctx, "Creating metrics listener")
	metricsListener, err := network.NewListener().
		SetLogger(r.logger).
		SetFlags(r.flags, network.MetricsListenerName).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create metrics listener: %w", err)
	}

	// Start the metrics server:
	r.logger.InfoContext(
		ctx,
		"Starting metrics server",
		slog.String("address", metricsListener.Addr().String()),
	)
	metricsServer := &http.Server{
		Handler:           promhttp.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		err := metricsServer.Serve(metricsListener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.logger.ErrorContext(
				ctx,
				"Metrics server failed",
				slog.Any("error", err),
			)
		}
	}()

	// Wait for the shutdown sequence to complete:
	r.logger.InfoContext(ctx, "Waiting for shutdown sequence to complete")
	return shutdown.Wait()
}

// waitForServer waits for the server to be ready using the health service.
func (r *runnerContext) waitForServer(ctx context.Context) error {
	client := healthv1.NewHealthClient(r.client)
	request := &healthv1.HealthCheckRequest{}
	const max = time.Minute
	const interval = time.Second
	start := time.Now()
	for {
		response, err := client.Check(ctx, request)
		if err == nil && response.Status == healthv1.HealthCheckResponse_SERVING {
			r.logger.InfoContext(ctx, "Server is ready")
			return nil
		}
		if time.Since(start) >= max {
			return fmt.Errorf("server did not become ready after waiting for %s: %w", max, err)
		}
		r.logger.InfoContext(
			ctx,
			"Server not yet ready",
			slog.Duration("elapsed", time.Since(start)),
			slog.String("error", err.Error()),
		)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// createTokenSource creates the token source used to authenticate the controller when it acts as a client of other
// services.
func (r *runnerContext) createTokenSource(ctx context.Context, caPool *x509.CertPool) (result auth.TokenSource,
	err error) {
	// Get the values of the flags:
	issuerUrl := r.args.authIssuerUrl
	if issuerUrl == "" && r.args.authIssuerUrlFile != "" {
		issuerUrl, err = r.readTrimmedFile(r.args.authIssuerUrlFile)
		if err != nil {
			err = fmt.Errorf(
				"failed to read issuer URL from file '%s': %w", r.args.authIssuerUrlFile, err,
			)
			return
		}
	}
	clientId := r.args.authClientId
	if clientId == "" && r.args.authClientIdFile != "" {
		clientId, err = r.readTrimmedFile(r.args.authClientIdFile)
		if err != nil {
			err = fmt.Errorf(
				"failed to read client identifier from file '%s': %w",
				r.args.authClientIdFile, err,
			)
			return
		}
	}
	clientSecret := r.args.authClientSecret
	if clientSecret == "" && r.args.authClientSecretFile != "" {
		clientSecret, err = r.readTrimmedFile(r.args.authClientSecretFile)
		if err != nil {
			err = fmt.Errorf(
				"failed to read client secret from file '%s': %w",
				r.args.authClientSecretFile, err,
			)
			return
		}
	}
	r.logger.DebugContext(
		ctx,
		"Credentials from flags",
		slog.String("issuer_url", issuerUrl),
		slog.String("!client_id", clientId),
		slog.String("!client_secret", clientSecret),
	)

	// Create a token store that saves the token in memory:
	tokenStore, err := auth.NewMemoryTokenStore().
		SetLogger(r.logger).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create token store: %w", err)
	}

	// Create an token source:
	tokenSource, err := oauth.NewTokenSource().
		SetLogger(r.logger).
		SetStore(tokenStore).
		SetCaPool(caPool).
		SetIssuer(issuerUrl).
		SetFlow(oauth.CredentialsFlow).
		SetClientId(clientId).
		SetClientSecret(clientSecret).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create token source: %w", err)
	}

	// Return the token source:
	result = tokenSource
	return
}

// createIDPClient creates the IDP client. The IDP URL and credentials are mandatory.
func (r *runnerContext) createIDPClient(ctx context.Context, caPool *x509.CertPool) (*idp.Client, error) {
	if r.args.idpURL == "" {
		return nil, fmt.Errorf("flag '--idp-url' is required")
	}
	if r.args.idpClientId == "" && r.args.idpClientIdFile == "" {
		return nil, fmt.Errorf("flag '--idp-client-id' or '--idp-client-id-file' is required")
	}
	if r.args.idpClientSecret == "" && r.args.idpClientSecretFile == "" {
		return nil, fmt.Errorf("flag '--idp-client-secret' or '--idp-client-secret-file' is required")
	}

	// Get the client ID
	idpClientId := r.args.idpClientId
	if idpClientId == "" && r.args.idpClientIdFile != "" {
		var err error
		idpClientId, err = r.readTrimmedFile(r.args.idpClientIdFile)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to read IDP client identifier from file '%s': %w",
				r.args.idpClientIdFile, err,
			)
		}
	}

	// Get the client secret
	idpClientSecret := r.args.idpClientSecret
	if idpClientSecret == "" && r.args.idpClientSecretFile != "" {
		var err error
		idpClientSecret, err = r.readTrimmedFile(r.args.idpClientSecretFile)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to read IDP client secret from file '%s': %w",
				r.args.idpClientSecretFile, err,
			)
		}
	}

	// Get the issuer URL (same as auth issuer URL)
	issuerUrl := r.args.authIssuerUrl
	if issuerUrl == "" && r.args.authIssuerUrlFile != "" {
		var err error
		issuerUrl, err = r.readTrimmedFile(r.args.authIssuerUrlFile)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to read issuer URL from file '%s': %w",
				r.args.authIssuerUrlFile, err,
			)
		}
	}

	r.logger.DebugContext(
		ctx,
		"IDP credentials from flags",
		slog.String("issuer_url", issuerUrl),
		slog.String("!client_id", idpClientId),
		slog.String("!client_secret", idpClientSecret),
	)

	// Create a token store that saves the token in memory
	idpTokenStore, err := auth.NewMemoryTokenStore().
		SetLogger(r.logger).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create IDP token store: %w", err)
	}

	// Create OAuth token source for IDP authentication
	r.logger.InfoContext(ctx, "Creating IDP token source")
	idpTokenSource, err := oauth.NewTokenSource().
		SetLogger(r.logger).
		SetStore(idpTokenStore).
		SetCaPool(caPool).
		SetIssuer(issuerUrl).
		SetFlow(oauth.CredentialsFlow).
		SetClientId(idpClientId).
		SetClientSecret(idpClientSecret).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create IDP token source: %w", err)
	}

	// Create Keycloak IDP client:
	r.logger.InfoContext(ctx, "Creating Keycloak IDP client")

	idpClient, err := idp.NewClient().
		SetLogger(r.logger).
		SetBaseURL(r.args.idpURL).
		SetTokenSource(idpTokenSource).
		SetCaPool(caPool).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create Keycloak client: %w", err)
	}

	r.logger.InfoContext(ctx, "Keycloak IDP client created successfully")
	return idpClient, nil
}

// readTrimmedFile reads the content of the given file and returns it with all leading and trailing whitespace removed.
func (r *runnerContext) readTrimmedFile(file string) (result string, err error) {
	data, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		return
	}
	result = strings.TrimSpace(string(data))
	return
}

// controllerUserAgent is the user agent string for the controller.
const controllerUserAgent = "fulfillment-controller"

const shortHelp = `Starts the controller`

const longHelp = `
Starts the controller.
`

const caFileFlagHelp = `
_FILE|DIRECTORY_ - File or directory containing trusted CA certificates.
`

const authIssuerUrlFlagHelp = `
_URL_ - Issuer URL for OAuth token acquisition. Required when using
{{ bt }}--auth-client-id{{ bt }} and {{ bt }}--auth-client-secret{{ bt }}.
Mutually exclusive with {{ bt }}--auth-issuer-url-file{{ bt }}.
`

const authIssuerUrlFileFlagHelp = `
_FILE_ - File containing the issuer URL for OAuth token
acquisition. Mutually exclusive with {{ bt }}--auth-issuer-url{{ bt }}.
`

const authClientIdFlagHelp = `
_ID_ - OAuth client identifier for authentication with the API.
Mutually exclusive with {{ bt }}--auth-client-id-file{{ bt }}.
`

const authClientIdFileFlagHelp = `
_FILE_ - File containing the OAuth client identifier for
authentication with the API. Mutually exclusive with
{{ bt }}--auth-client-id{{ bt }}.
`

const authClientSecretFlagHelp = `
_SECRET_ - OAuth client secret for authentication with the API.
Mutually exclusive with {{ bt }}--auth-client-secret-file{{ bt }}.
`

const authClientSecretFileFlagHelp = `
_FILE_ - File containing the OAuth client secret for
authentication with the API. Mutually exclusive with
{{ bt }}--auth-client-secret{{ bt }}.
`

const idpProviderFlagHelp = `
_DEPRECATED_ - This flag is deprecated and no longer has any effect.
Only Keycloak is supported as the identity provider.
`

const idpUrlFlagHelp = `
_URL_ - Base URL of the identity provider.
`

const idpClientIdFileFlagHelp = `
_FILE_ - File containing the OAuth client identifier for IDP
authentication. Mutually exclusive with {{ bt }}--idp-client-id{{ bt }}.
`

const idpClientIdFlagHelp = `
_ID_ - OAuth client identifier for IDP authentication. Mutually
exclusive with {{ bt }}--idp-client-id-file{{ bt }}.
`

const idpClientSecretFileFlagHelp = `
_FILE_ - File containing the OAuth client secret for IDP
authentication. Mutually exclusive with {{ bt }}--idp-client-secret{{ bt }}.
`

const idpClientSecretFlagHelp = `
_SECRET_ - OAuth client secret for IDP authentication. Mutually
exclusive with {{ bt }}--idp-client-secret-file{{ bt }}.
`
