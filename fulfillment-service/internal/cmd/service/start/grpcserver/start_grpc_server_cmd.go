/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
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
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"k8s.io/klog/v2"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/auth/jwe"
	"github.com/osac-project/osac/fulfillment-service/internal/console"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	hubscheme "github.com/osac-project/osac/fulfillment-service/internal/kubernetes/scheme"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/metrics"
	"github.com/osac-project/osac/fulfillment-service/internal/network"
	"github.com/osac-project/osac/fulfillment-service/internal/provisioners"
	"github.com/osac-project/osac/fulfillment-service/internal/recovery"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	"github.com/osac-project/osac/fulfillment-service/internal/servers"
	shtdwn "github.com/osac-project/osac/fulfillment-service/internal/shutdown"
	"github.com/osac-project/osac/fulfillment-service/internal/validation"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
)

// userIDResolver implements auth.UserIDResolver by querying the users DAO.
type userIDResolver struct {
	usersDAO *dao.GenericDAO[*privatev1.User]
}

func (r *userIDResolver) GetID(ctx context.Context, username string) (string, error) {
	filter := fmt.Sprintf("this.spec.username==%q", username)
	listResponse, err := r.usersDAO.List().
		SetFilter(filter).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get user ID: %w", err)
	}
	if listResponse.GetSize() == 0 {
		return "", nil
	}
	user := listResponse.GetItems()[0]
	return user.GetId(), nil
}

// Cmd creates and returns the `start grpc-server` command.
func Cmd() *cobra.Command {
	var err error
	runner := &runnerContext{}
	command := &cobra.Command{
		Use:                   "grpc-server [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := command.Flags()
	network.AddListenerFlags(flags, network.GrpcListenerName, network.DefaultGrpcAddress)
	network.AddListenerFlags(flags, network.MetricsListenerName, network.DefaultMetricsAddress)
	database.AddFlags(flags)
	flags.StringVar(
		&runner.args.authType,
		"grpc-authn-type",
		"guest",
		grpcAuthnTypeFlagHelp,
	)
	err = flags.MarkDeprecated(
		"grpc-authn-type",
		"this flag is ignored, authentication is now always enabled",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to mark deprecated flag 'grpc-authn-type': %v\n", err)
		return command
	}
	flags.StringVar(
		&runner.args.externalAuthAddress,
		"grpc-authn-external-address",
		"",
		grpcAuthnExternalAddressFlagHelp,
	)
	err = flags.MarkDeprecated(
		"grpc-authn-external-address",
		"this flag is ignored, external authentication via Authorino is no longer used",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to mark deprecated flag 'grpc-authn-external-address': %v\n", err)
		return command
	}
	flags.StringSliceVar(
		&runner.args.caFiles,
		"ca-file",
		[]string{},
		caFileFlagHelp,
	)
	flags.StringSliceVar(
		&runner.args.trustedTokenIssuers,
		"grpc-authn-trusted-token-issuers",
		[]string{},
		grpcAuthnTrustedTokenIssuersFlagHelp,
	)
	flags.StringVar(
		&runner.args.tenancyLogic,
		"tenancy-logic",
		"default",
		tenancyLogicFlagHelp,
	)
	err = flags.MarkDeprecated(
		"tenancy-logic",
		"this flag is ignored, tenancy logic is now always the default",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to mark deprecated flag 'tenancy-logic': %v\n", err)
		return command
	}
	flags.StringVar(
		&runner.args.tokenSignerCrt,
		"token-signer-crt",
		"",
		tokenSignerCrtFlagHelp,
	)
	flags.StringVar(
		&runner.args.tokenSignerKey,
		"token-signer-key",
		"",
		tokenSignerKeyFlagHelp,
	)
	flags.StringVar(
		&runner.args.tokenEncryptionCrt,
		"token-encryption-crt",
		"",
		tokenEncryptionCrtFlagHelp,
	)
	flags.StringVar(
		&runner.args.tokenIssuer,
		"token-issuer",
		"",
		tokenIssuerFlagHelp,
	)
	flags.StringSliceVar(
		&runner.args.emergencyServiceAccounts,
		"emergency-service-accounts",
		[]string{
			"admin",
			"osac-operator",
			"osac-operator-controller-manager",
			"template-publisher",
		},
		emergencyServiceAccountsFlagHelp,
	)
	flags.StringVar(
		&runner.args.vaultEndpoint,
		"vault-endpoint",
		"",
		vaultEndpointFlagHelp,
	)
	flags.StringVar(
		&runner.args.vaultNamespace,
		"vault-namespace",
		"osac",
		vaultNamespaceFlagHelp,
	)
	flags.StringVar(
		&runner.args.vaultKVMountPath,
		"vault-kv-mount-path",
		"secret",
		vaultKVMountPathFlagHelp,
	)
	network.AddGrpcKeepaliveFlags(flags)
	return command
}

// runnerContext contains the data and logic needed to run the `start grpc-server` command.
type runnerContext struct {
	logger *slog.Logger
	flags  *pflag.FlagSet
	args   struct {
		caFiles                  []string
		authType                 string
		externalAuthAddress      string
		trustedTokenIssuers      []string
		tenancyLogic             string
		tokenSignerCrt           string
		tokenSignerKey           string
		tokenEncryptionCrt       string
		tokenIssuer              string
		emergencyServiceAccounts []string
		vaultEndpoint            string
		vaultNamespace           string
		vaultKVMountPath         string
	}
}

// run runs the `start grpc-server` command.
func (c *runnerContext) run(cmd *cobra.Command, argv []string) error { //nolint:gocyclo
	// Get the context and create a cancellable version:
	ctx, cancel := context.WithCancel(cmd.Context())

	// Get the dependencies from the context:
	c.logger = logging.LoggerFromContext(ctx)

	// Configure the Kubernetes libraries to use the logger:
	logrLogger := logr.FromSlogHandler(c.logger.Handler())
	crlog.SetLogger(logrLogger)
	klog.SetLogger(logrLogger)

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

	// Wait till the database is available:
	dbTool, err := database.NewTool().
		SetLogger(c.logger).
		SetFlags(c.flags).
		Build()
	if err != nil {
		return err
	}
	c.logger.InfoContext(ctx, "Waiting for database")
	err = dbTool.Wait(ctx)
	if err != nil {
		return err
	}

	// Run the migrations:
	c.logger.InfoContext(ctx, "Running database migrations")
	err = dbTool.Migrate(ctx, math.MaxUint)
	if err != nil {
		return err
	}

	// Create the database connection pool:
	c.logger.InfoContext(ctx, "Creating database connection pool")
	dbPool, err := dbTool.Pool(ctx)
	if err != nil {
		return err
	}
	shutdown.AddDatabasePool("database", 0, dbPool)

	// Create the network listener:
	listener, err := network.NewListener().
		SetLogger(c.logger).
		SetFlags(c.flags, network.GrpcListenerName).
		Build()
	if err != nil {
		return err
	}

	// Prepare the logging interceptor:
	c.logger.InfoContext(ctx, "Creating logging interceptor")
	loggingInterceptor, err := logging.NewInterceptor().
		SetLogger(c.logger).
		SetFlags(c.flags).
		Build()
	if err != nil {
		return err
	}

	// Prepare the validation interceptor:
	c.logger.InfoContext(ctx, "Creating validation interceptor")
	validationInterceptor, err := validation.NewProtovalidateInterceptor().
		SetLogger(c.logger).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create validation interceptor: %w", err)
	}

	// Create metadata fetchers for project and project membership authorization.
	metadataFetcher, err := dao.NewMetadataFetcher().
		SetLogger(c.logger).
		SetTable("projects").
		Build()
	if err != nil {
		return fmt.Errorf("failed to create metadata fetcher: %w", err)
	}
	pmMetadataFetcher, err := dao.NewMetadataFetcher().
		SetLogger(c.logger).
		SetTable("project_memberships").
		Build()
	if err != nil {
		return fmt.Errorf("failed to create project membership metadata fetcher: %w", err)
	}

	// Prepare the authentication interceptor:
	c.logger.InfoContext(ctx, "Creating JWKS cache")
	jwksCache, err := auth.NewJwksCache().
		SetLogger(c.logger).
		SetCaPool(caPool).
		AddIssuers(c.args.trustedTokenIssuers...).
		AddKubernetesIssuer(true).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create JWKS cache: %w", err)
	}
	c.logger.InfoContext(ctx, "Creating JWT validator")
	jwtValidator, err := auth.NewJwtValidator().
		SetLogger(c.logger).
		SetJwksCache(jwksCache).
		SetExpirationLeeway(5 * time.Second).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create JWT validator: %w", err)
	}
	c.logger.InfoContext(ctx, "Creating authentication interceptor")
	authnInterceptor, err := auth.NewGrpcAuthnInterceptor().
		SetLogger(c.logger).
		SetJwtValidator(jwtValidator).
		AddAnonymousMethodRegex(anonymousMethodsRegex).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create authentication interceptor: %w", err)
	}

	// Create the tenancy logic (needed by both authz interceptor and DAO):
	c.logger.InfoContext(
		ctx,
		"Creating tenancy logic",
		slog.String("type", c.args.tenancyLogic),
	)
	var tenancyLogic auth.TenancyLogic
	tenancyLogic, err = auth.NewDefaultTenancyLogic().
		SetLogger(c.logger).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create default tenancy logic: %w", err)
	}

	// Prepare the authorization interceptor:
	c.logger.InfoContext(ctx, "Creating Rego authorization interceptor")
	authzInterceptor, err := auth.NewGrpcAuthzInterceptor().
		SetLogger(c.logger).
		AddAnonymousMethodRegex(anonymousMethodsRegex).
		SetMetadataFetcher(metadataFetcher).
		SetProjectMembershipMetadataFetcher(pmMetadataFetcher).
		AddEmergencyServiceAccounts(c.args.emergencyServiceAccounts...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create Rego authorization interceptor: %w", err)
	}

	// Create the notifier:
	c.logger.InfoContext(ctx, "Creating notifier")
	notifier, err := database.NewNotifier().
		SetLogger(c.logger).
		SetChannel("events").
		SetPool(dbPool).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create notifier: %w", err)
	}
	err = notifier.Start(ctx)
	if err != nil {
		return fmt.Errorf("failed to start notifier: %w", err)
	}

	// Create the private attribution logic:
	c.logger.InfoContext(ctx, "Creating private attribution logic")
	privateAttributionLogic, err := auth.NewSystemAttributionLogic().
		SetLogger(c.logger).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create system attribution logic: %w", err)
	}

	// Create the private users server:
	c.logger.InfoContext(ctx, "Creating private users server")
	privateUsersServer, err := servers.NewPrivateUsersServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private users server: %w", err)
	}

	// Create the user provisioner:
	c.logger.InfoContext(ctx, "Creating user provisioner for JIT provisioning")
	userProvisioner, err := provisioners.NewUserProvisioner().
		SetLogger(c.logger).
		SetUsersServer(privateUsersServer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create user provisioner: %w", err)
	}

	c.logger.InfoContext(ctx, "Creating JIT provisioning interceptor")
	jitProvisioningInterceptor, err := auth.NewGrpcJitProvisioningInterceptor().
		SetLogger(c.logger).
		SetProvisioner(userProvisioner).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create JIT provisioning interceptor: %w", err)
	}

	// Prepare the reference validation interceptor:
	c.logger.InfoContext(ctx, "Creating reference validation interceptor")
	referenceValidator, err := references.NewReferenceValidator().
		SetLogger(c.logger).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create reference validation interceptor: %w", err)
	}

	// Register reference lookup functions for all resource types:
	c.logger.InfoContext(ctx, "Registering reference lookup functions")
	err = registerReferenceLookups(referenceValidator, c.logger, tenancyLogic, metricsRegisterer)
	if err != nil {
		return fmt.Errorf("failed to register reference lookups: %w", err)
	}

	// Prepare the transactions manager:
	c.logger.InfoContext(ctx, "Creating transactions manager")
	txManager, err := database.NewTxManager().
		SetLogger(c.logger).
		SetPool(dbPool).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create transactions manager: %w", err)
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

	// Prepare the transactions interceptor:
	c.logger.InfoContext(ctx, "Creating transactions interceptor")
	txInterceptor, err := database.NewTxInterceptor().
		SetLogger(c.logger).
		SetManager(txManager).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create transactions interceptor: %w", err)
	}

	// Read gRPC keepalive configuration:
	keepaliveConfig, err := network.GrpcKeepaliveConfigFromFlags(c.flags)
	if err != nil {
		return fmt.Errorf("failed to read gRPC keepalive configuration: %w", err)
	}

	// Create the gRPC server:
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
			validationInterceptor.UnaryServer,
			txInterceptor.UnaryServer,
			authnInterceptor.UnaryServer,
			authzInterceptor.UnaryServer,
			jitProvisioningInterceptor.UnaryServer,
			referenceValidator.UnaryServer,
		),
		grpc.ChainStreamInterceptor(
			panicInterceptor.StreamServer,
			metricsInterceptor.StreamServer,
			loggingInterceptor.StreamServer,
			validationInterceptor.StreamServer,
			authnInterceptor.StreamServer,
			authzInterceptor.StreamServer,
			jitProvisioningInterceptor.StreamServer,
			referenceValidator.StreamServer,
		),
	)
	shutdown.AddGrpcServer(network.GrpcListenerName, 0, grpcServer)

	// Register the reflection server:
	c.logger.InfoContext(ctx, "Registering gRPC reflection server")
	reflection.RegisterV1(grpcServer)

	// Register the health server:
	c.logger.InfoContext(ctx, "Registering gRPC health server")
	healthServer := health.NewServer()
	healthv1.RegisterHealthServer(grpcServer, healthServer)

	// Create the users DAO for user ID resolution in attribution:
	c.logger.InfoContext(ctx, "Creating users DAO for attribution")
	usersDAO, err2 := dao.NewGenericDAO[*privatev1.User]().
		SetLogger(c.logger).
		SetTableName("users").
		SetTenancyLogic(tenancyLogic).
		Build()
	if err2 != nil {
		return fmt.Errorf("failed to create users DAO: %w", err2)
	}

	// Create user ID resolver implementation:
	userIDResolver := &userIDResolver{usersDAO: usersDAO}

	// Create the public attribution logic:
	c.logger.InfoContext(ctx, "Creating public attribution logic")
	publicAttributionLogic, err := auth.NewDefaultAttributionLogic().
		SetLogger(c.logger).
		SetUserIDResolver(userIDResolver).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create public attribution logic: %w", err)
	}

	// Create the capabilities servers:
	c.logger.InfoContext(ctx, "Creating capabilities servers")
	capabilitiesServer, err := servers.NewCapabilitiesServer().
		SetLogger(c.logger).
		AddAutnTrustedTokenIssuers(c.args.trustedTokenIssuers...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create public capabilities server: %w", err)
	}
	publicv1.RegisterCapabilitiesServer(grpcServer, capabilitiesServer)
	privateCapabilitiesServer, err := servers.NewPrivateCapabilitiesServer().
		SetLogger(c.logger).
		AddAuthnTrustedTokenIssuers(c.args.trustedTokenIssuers...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private capabilities server: %w", err)
	}
	privatev1.RegisterCapabilitiesServer(grpcServer, privateCapabilitiesServer)

	// Create the cluster templates server:
	c.logger.InfoContext(ctx, "Creating cluster templates server")
	clusterTemplatesServer, err := servers.NewClusterTemplatesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create cluster templates server: %w", err)
	}
	publicv1.RegisterClusterTemplatesServer(grpcServer, clusterTemplatesServer)

	// Create the cluster catalog items server:
	c.logger.InfoContext(ctx, "Creating cluster catalog items server")
	clusterCatalogItemsServer, err := servers.NewClusterCatalogItemsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create cluster catalog items server: %w", err)
	}
	publicv1.RegisterClusterCatalogItemsServer(grpcServer, clusterCatalogItemsServer)

	// Create the compute instance catalog items server:
	c.logger.InfoContext(ctx, "Creating compute instance catalog items server")
	computeInstanceCatalogItemsServer, err := servers.NewComputeInstanceCatalogItemsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create compute instance catalog items server: %w", err)
	}
	publicv1.RegisterComputeInstanceCatalogItemsServer(grpcServer, computeInstanceCatalogItemsServer)

	// Create the private cluster templates server:
	c.logger.InfoContext(ctx, "Creating private cluster templates server")
	privateClusterTemplatesServer, err := servers.NewPrivateClusterTemplatesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private cluster templates server: %w", err)
	}
	privatev1.RegisterClusterTemplatesServer(grpcServer, privateClusterTemplatesServer)

	// Create the private cluster catalog items server:
	c.logger.InfoContext(ctx, "Creating private cluster catalog items server")
	privateClusterCatalogItemsServer, err := servers.NewPrivateClusterCatalogItemsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private cluster catalog items server: %w", err)
	}
	privatev1.RegisterClusterCatalogItemsServer(grpcServer, privateClusterCatalogItemsServer)

	// Create the private compute instance catalog items server:
	c.logger.InfoContext(ctx, "Creating private compute instance catalog items server")
	privateComputeInstanceCatalogItemsServer, err := servers.NewPrivateComputeInstanceCatalogItemsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private compute instance catalog items server: %w", err)
	}
	privatev1.RegisterComputeInstanceCatalogItemsServer(grpcServer, privateComputeInstanceCatalogItemsServer)

	// Create the runtime scheme for typed OSAC API objects:
	hubScheme, err := hubscheme.NewHub()
	if err != nil {
		return fmt.Errorf("failed to create hub scheme: %w", err)
	}

	// Create the clusters server:
	c.logger.InfoContext(ctx, "Creating clusters server")
	clustersServer, err := servers.NewClustersServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		SetScheme(hubScheme).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create clusters server: %w", err)
	}
	publicv1.RegisterClustersServer(grpcServer, clustersServer)

	// Create the private clusters server:
	c.logger.InfoContext(ctx, "Creating private clusters server")
	privateClustersServer, err := servers.NewPrivateClustersServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private clusters server: %w", err)
	}
	privatev1.RegisterClustersServer(grpcServer, privateClustersServer)

	// Create the host types server:
	c.logger.InfoContext(ctx, "Creating host types server")
	hostTypesServer, err := servers.NewHostTypesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create host types server: %w", err)
	}
	publicv1.RegisterHostTypesServer(grpcServer, hostTypesServer)

	// Create the private host types server:
	c.logger.InfoContext(ctx, "Creating private host types server")
	privateHostTypesServer, err := servers.NewPrivateHostTypesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private host types server: %w", err)
	}
	privatev1.RegisterHostTypesServer(grpcServer, privateHostTypesServer)

	// Create the compute instance templates server:
	c.logger.InfoContext(ctx, "Creating compute instance templates server")
	computeInstanceTemplatesServer, err := servers.NewComputeInstanceTemplatesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create compute instance templates server: %w", err)
	}
	publicv1.RegisterComputeInstanceTemplatesServer(grpcServer, computeInstanceTemplatesServer)

	// Create the private compute instance templates server:
	c.logger.InfoContext(ctx, "Creating private compute instance templates server")
	privateComputeInstanceTemplatesServer, err := servers.NewPrivateComputeInstanceTemplatesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private compute instance templates server: %w", err)
	}
	privatev1.RegisterComputeInstanceTemplatesServer(grpcServer, privateComputeInstanceTemplatesServer)

	// Create the compute instances server:
	c.logger.InfoContext(ctx, "Creating compute instances server")
	computeInstancesServer, err := servers.NewComputeInstancesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create compute instances server: %w", err)
	}
	publicv1.RegisterComputeInstancesServer(grpcServer, computeInstancesServer)

	// Create the private compute instances server:
	c.logger.InfoContext(ctx, "Creating private compute instances server")
	privateComputeInstancesServer, err := servers.NewPrivateComputeInstancesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private compute instances server: %w", err)
	}
	privatev1.RegisterComputeInstancesServer(grpcServer, privateComputeInstancesServer)

	// Create the bare metal instance templates server:
	c.logger.InfoContext(ctx, "Creating bare metal instance templates server")
	bareMetalInstanceTemplatesServer, err := servers.NewBareMetalInstanceTemplatesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create bare metal instance templates server: %w", err)
	}
	publicv1.RegisterBareMetalInstanceTemplatesServer(grpcServer, bareMetalInstanceTemplatesServer)

	// Create the bare metal instance catalog items server:
	c.logger.InfoContext(ctx, "Creating bare metal instance catalog items server")
	bareMetalInstanceCatalogItemsServer, err := servers.NewBareMetalInstanceCatalogItemsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create bare metal instance catalog items server: %w", err)
	}
	publicv1.RegisterBareMetalInstanceCatalogItemsServer(grpcServer, bareMetalInstanceCatalogItemsServer)

	// Create the bare metal instances server:
	c.logger.InfoContext(ctx, "Creating bare metal instances server")
	bareMetalInstancesServer, err := servers.NewBareMetalInstancesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create bare metal instances server: %w", err)
	}
	publicv1.RegisterBareMetalInstancesServer(grpcServer, bareMetalInstancesServer)

	// Create the private bare metal instance templates server:
	c.logger.InfoContext(ctx, "Creating private bare metal instance templates server")
	privateBareMetalInstanceTemplatesServer, err := servers.NewPrivateBareMetalInstanceTemplatesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private bare metal instance templates server: %w", err)
	}
	privatev1.RegisterBareMetalInstanceTemplatesServer(grpcServer, privateBareMetalInstanceTemplatesServer)

	// Create the private bare metal instance catalog items server:
	c.logger.InfoContext(ctx, "Creating private bare metal instance catalog items server")
	privateBareMetalInstanceCatalogItemsServer, err := servers.NewPrivateBareMetalInstanceCatalogItemsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private bare metal instance catalog items server: %w", err)
	}
	privatev1.RegisterBareMetalInstanceCatalogItemsServer(grpcServer, privateBareMetalInstanceCatalogItemsServer)

	// Create the private bare metal instances server:
	c.logger.InfoContext(ctx, "Creating private bare metal instances server")
	privateBareMetalInstancesServer, err := servers.NewPrivateBareMetalInstancesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private bare metal instances server: %w", err)
	}
	privatev1.RegisterBareMetalInstancesServer(grpcServer, privateBareMetalInstancesServer)

	// Create the private hubs server:
	c.logger.InfoContext(ctx, "Creating hubs server")
	privateHubsServer, err := servers.NewPrivateHubsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create hubs server: %w", err)
	}
	privatev1.RegisterHubsServer(grpcServer, privateHubsServer)

	// Create the virtual networks server:
	c.logger.InfoContext(ctx, "Creating virtual networks server")
	virtualNetworksServer, err := servers.NewVirtualNetworksServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create virtual networks server: %w", err)
	}
	publicv1.RegisterVirtualNetworksServer(grpcServer, virtualNetworksServer)

	// Create the private virtual networks server:
	c.logger.InfoContext(ctx, "Creating private virtual networks server")
	privateVirtualNetworksServer, err := servers.NewPrivateVirtualNetworksServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private virtual networks server: %w", err)
	}
	privatev1.RegisterVirtualNetworksServer(grpcServer, privateVirtualNetworksServer)

	// Create the subnets server:
	c.logger.InfoContext(ctx, "Creating subnets server")
	subnetsServer, err := servers.NewSubnetsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create subnets server: %w", err)
	}
	publicv1.RegisterSubnetsServer(grpcServer, subnetsServer)

	// Create the private subnets server:
	c.logger.InfoContext(ctx, "Creating private subnets server")
	privateSubnetsServer, err := servers.NewPrivateSubnetsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private subnets server: %w", err)
	}
	privatev1.RegisterSubnetsServer(grpcServer, privateSubnetsServer)

	// Create the security groups server:
	c.logger.InfoContext(ctx, "Creating security groups server")
	securityGroupsServer, err := servers.NewSecurityGroupsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create security groups server: %w", err)
	}
	publicv1.RegisterSecurityGroupsServer(grpcServer, securityGroupsServer)

	// Create the private security groups server:
	c.logger.InfoContext(ctx, "Creating private security groups server")
	privateSecurityGroupsServer, err := servers.NewPrivateSecurityGroupsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private security groups server: %w", err)
	}
	privatev1.RegisterSecurityGroupsServer(grpcServer, privateSecurityGroupsServer)

	// Create the network classes server:
	c.logger.InfoContext(ctx, "Creating network classes server")
	networkClassesServer, err := servers.NewNetworkClassesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create network classes server: %w", err)
	}
	publicv1.RegisterNetworkClassesServer(grpcServer, networkClassesServer)

	// Create the private network classes server:
	c.logger.InfoContext(ctx, "Creating private network classes server")
	privateNetworkClassesServer, err := servers.NewPrivateNetworkClassesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private network classes server: %w", err)
	}
	privatev1.RegisterNetworkClassesServer(grpcServer, privateNetworkClassesServer)

	// Create the instance types server:
	c.logger.InfoContext(ctx, "Creating instance types server")
	instanceTypesServer, err := servers.NewInstanceTypesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create instance types server: %w", err)
	}
	publicv1.RegisterInstanceTypesServer(grpcServer, instanceTypesServer)

	// Create the private instance types server:
	c.logger.InfoContext(ctx, "Creating private instance types server")
	privateInstanceTypesServer, err := servers.NewPrivateInstanceTypesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private instance types server: %w", err)
	}
	privatev1.RegisterInstanceTypesServer(grpcServer, privateInstanceTypesServer)

	// Create the bare metal instance types server:
	c.logger.InfoContext(ctx, "Creating bare metal instance types server")
	bareMetalInstanceTypesServer, err := servers.NewBareMetalInstanceTypesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create bare metal instance types server: %w", err)
	}
	publicv1.RegisterBareMetalInstanceTypesServer(grpcServer, bareMetalInstanceTypesServer)

	// Create the private bare metal instance types server:
	c.logger.InfoContext(ctx, "Creating private bare metal instance types server")
	privateBareMetalInstanceTypesServer, err := servers.NewPrivateBareMetalInstanceTypesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private bare metal instance types server: %w", err)
	}
	privatev1.RegisterBareMetalInstanceTypesServer(grpcServer, privateBareMetalInstanceTypesServer)

	// Create the cluster versions server:
	c.logger.InfoContext(ctx, "Creating cluster versions server")
	clusterVersionsServer, err := servers.NewClusterVersionsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create cluster versions server: %w", err)
	}
	publicv1.RegisterClusterVersionsServer(grpcServer, clusterVersionsServer)

	// Create the private cluster versions server:
	c.logger.InfoContext(ctx, "Creating private cluster versions server")
	privateClusterVersionsServer, err := servers.NewPrivateClusterVersionsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private cluster versions server: %w", err)
	}
	privatev1.RegisterClusterVersionsServer(grpcServer, privateClusterVersionsServer)

	// Create the private storage backends server:
	c.logger.InfoContext(ctx, "Creating private storage backends server")
	privateStorageBackendsServer, err := servers.NewPrivateStorageBackendsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private storage backends server: %w", err)
	}
	privatev1.RegisterStorageBackendsServer(grpcServer, privateStorageBackendsServer)

	// Perform vault health check if configured:
	if c.args.vaultEndpoint != "" {
		c.logger.InfoContext(ctx, "Performing vault health check")
		healthChecker, healthErr := vault.NewHealthChecker().
			SetLogger(c.logger).
			SetAddress(c.args.vaultEndpoint).
			SetCaPool(caPool).
			Build()
		if healthErr != nil {
			c.logger.ErrorContext(ctx, "Failed to create Vault health checker",
				slog.String("error", healthErr.Error()),
			)
		} else if healthErr = healthChecker.Check(ctx); healthErr != nil {
			c.logger.ErrorContext(ctx, "Vault health check failed",
				slog.String("error", healthErr.Error()),
			)
		}
	}

	// Create the private secrets server:
	c.logger.InfoContext(ctx, "Creating private secrets server")
	privateSecretsServer, err := servers.NewPrivateSecretsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private secrets server: %w", err)
	}
	privatev1.RegisterSecretsServer(grpcServer, privateSecretsServer)

	// Create the public secrets server:
	c.logger.InfoContext(ctx, "Creating public secrets server")
	publicSecretsServer, err := servers.NewSecretsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create public secrets server: %w", err)
	}
	publicv1.RegisterSecretsServer(grpcServer, publicSecretsServer)

	// Create the storage backends DAO for cross-resource validation in the storage tiers server:
	storageBackendsDAO, err := dao.NewGenericDAO[*privatev1.StorageBackend]().
		SetLogger(c.logger).
		SetTenancyLogic(tenancyLogic).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create storage backends DAO: %w", err)
	}

	// Create the private storage tiers server:
	c.logger.InfoContext(ctx, "Creating private storage tiers server")
	privateStorageTiersServer, err := servers.NewPrivateStorageTiersServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		SetStorageBackendsDAO(storageBackendsDAO).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private storage tiers server: %w", err)
	}
	privatev1.RegisterStorageTiersServer(grpcServer, privateStorageTiersServer)

	// Create the private volumes server:
	c.logger.InfoContext(ctx, "Creating private volumes server")
	privateVolumesServer, err := servers.NewPrivateVolumesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private volumes server: %w", err)
	}
	privatev1.RegisterVolumesServer(grpcServer, privateVolumesServer)

	// Create the roles server:
	c.logger.InfoContext(ctx, "Creating roles server")
	rolesServer, err := servers.NewRolesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create roles server: %w", err)
	}
	publicv1.RegisterRolesServer(grpcServer, rolesServer)

	// Create the private roles server:
	c.logger.InfoContext(ctx, "Creating private roles server")
	privateRolesServer, err := servers.NewPrivateRolesServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private roles server: %w", err)
	}
	privatev1.RegisterRolesServer(grpcServer, privateRolesServer)

	// Create the role bindings server:
	c.logger.InfoContext(ctx, "Creating role bindings server")
	roleBindingsServer, err := servers.NewRoleBindingsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create role bindings server: %w", err)
	}
	publicv1.RegisterRoleBindingsServer(grpcServer, roleBindingsServer)

	// Create the private role bindings server:
	c.logger.InfoContext(ctx, "Creating private role bindings server")
	privateRoleBindingsServer, err := servers.NewPrivateRoleBindingsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private role bindings server: %w", err)
	}
	privatev1.RegisterRoleBindingsServer(grpcServer, privateRoleBindingsServer)

	// Create the project memberships server:
	c.logger.InfoContext(ctx, "Creating project memberships server")
	projectMembershipsServer, err := servers.NewProjectMembershipsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create project memberships server: %w", err)
	}
	publicv1.RegisterProjectMembershipsServer(grpcServer, projectMembershipsServer)

	// Create the private project memberships server:
	c.logger.InfoContext(ctx, "Creating private project memberships server")
	privateProjectMembershipsServer, err := servers.NewPrivateProjectMembershipsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private project memberships server: %w", err)
	}
	privatev1.RegisterProjectMembershipsServer(grpcServer, privateProjectMembershipsServer)

	// Create the external IP pools server (read-only: List + Get):
	c.logger.InfoContext(ctx, "Creating external IP pools server")
	externalIPPoolsServer, err := servers.NewExternalIPPoolsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create external IP pools server: %w", err)
	}
	publicv1.RegisterExternalIPPoolsServer(grpcServer, externalIPPoolsServer)

	// Create the private external IP pools server:
	c.logger.InfoContext(ctx, "Creating private external IP pools server")
	privateExternalIPPoolsServer, err := servers.NewPrivateExternalIPPoolsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private external IP pools server: %w", err)
	}
	privatev1.RegisterExternalIPPoolsServer(grpcServer, privateExternalIPPoolsServer)

	// Create the external IPs server:
	c.logger.InfoContext(ctx, "Creating external IPs server")
	externalIPsServer, err := servers.NewExternalIPsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create external IPs server: %w", err)
	}
	publicv1.RegisterExternalIPsServer(grpcServer, externalIPsServer)

	// Create the private external IPs server:
	c.logger.InfoContext(ctx, "Creating private external IPs server")
	privateExternalIPsServer, err := servers.NewPrivateExternalIPsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private external IPs server: %w", err)
	}
	privatev1.RegisterExternalIPsServer(grpcServer, privateExternalIPsServer)

	// Create the external IP attachments server:
	c.logger.InfoContext(ctx, "Creating external IP attachments server")
	externalIPAttachmentsServer, err := servers.NewExternalIPAttachmentsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create external IP attachments server: %w", err)
	}
	publicv1.RegisterExternalIPAttachmentsServer(grpcServer, externalIPAttachmentsServer)

	// Create the private external IP attachments server:
	c.logger.InfoContext(ctx, "Creating private external IP attachments server")
	privateExternalIPAttachmentsServer, err := servers.NewPrivateExternalIPAttachmentsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private external IP attachments server: %w", err)
	}
	privatev1.RegisterExternalIPAttachmentsServer(grpcServer, privateExternalIPAttachmentsServer)

	// Create the NAT gateways server:
	c.logger.InfoContext(ctx, "Creating NAT gateways server")
	natGatewaysServer, err := servers.NewNATGatewaysServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create NAT gateways server: %w", err)
	}
	publicv1.RegisterNATGatewaysServer(grpcServer, natGatewaysServer)

	// Create the private NAT gateways server:
	c.logger.InfoContext(ctx, "Creating private NAT gateways server")
	privateNATGatewaysServer, err := servers.NewPrivateNATGatewaysServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private NAT gateways server: %w", err)
	}
	privatev1.RegisterNATGatewaysServer(grpcServer, privateNATGatewaysServer)

	// Create the public tenants server:
	c.logger.InfoContext(ctx, "Creating public tenants server")
	publicTenantsServer, err := servers.NewTenantsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create public tenants server: %w", err)
	}
	publicv1.RegisterTenantsServer(grpcServer, publicTenantsServer)

	// Create the default networking provisioner:
	c.logger.InfoContext(ctx, "Creating default networking provisioner")
	defaultNetworkingProvisioner, err := servers.NewDefaultNetworkingProvisioner().
		SetLogger(c.logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create default networking provisioner: %w", err)
	}

	// Create the private tenants server:
	c.logger.InfoContext(ctx, "Creating private tenants server")
	privateTenantsServer, err := servers.NewPrivateTenantsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		SetDefaultNetworkingProvisioner(defaultNetworkingProvisioner).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private tenants server: %w", err)
	}
	privatev1.RegisterTenantsServer(grpcServer, privateTenantsServer)

	// Create the public identity providers server:
	c.logger.InfoContext(ctx, "Creating public identity providers server")
	publicIdentityProvidersServer, err := servers.NewIdentityProvidersServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create public identity providers server: %w", err)
	}
	publicv1.RegisterIdentityProvidersServer(grpcServer, publicIdentityProvidersServer)

	// Create the private identity providers server:
	c.logger.InfoContext(ctx, "Creating private identity providers server")
	privateIdentityProvidersServer, err := servers.NewPrivateIdentityProvidersServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private identity providers server: %w", err)
	}
	privatev1.RegisterIdentityProvidersServer(grpcServer, privateIdentityProvidersServer)

	// Create the public projects server:
	c.logger.InfoContext(ctx, "Creating public projects server")
	publicProjectsServer, err := servers.NewProjectsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create public projects server: %w", err)
	}
	publicv1.RegisterProjectsServer(grpcServer, publicProjectsServer)

	// Create the private projects server:
	c.logger.InfoContext(ctx, "Creating private projects server")
	privateProjectsServer, err := servers.NewPrivateProjectsServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(privateAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private projects server: %w", err)
	}
	privatev1.RegisterProjectsServer(grpcServer, privateProjectsServer)

	// Create the public users server:
	c.logger.InfoContext(ctx, "Creating public users server")
	publicUsersServer, err := servers.NewUsersServer().
		SetLogger(c.logger).
		SetNotifier(notifier).
		SetAttributionLogic(publicAttributionLogic).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create public users server: %w", err)
	}
	publicv1.RegisterUsersServer(grpcServer, publicUsersServer)

	// Register the private users server:
	privatev1.RegisterUsersServer(grpcServer, privateUsersServer)

	// Create the token sealer (sign + encrypt infrastructure):
	c.logger.InfoContext(ctx, "Creating token sealer")
	tokenSealer, err := jwe.NewSealer().
		SetLogger(c.logger).
		SetSigningCertFile(c.args.tokenSignerCrt).
		SetSigningKeyFile(c.args.tokenSignerKey).
		SetEncryptionCertFile(c.args.tokenEncryptionCrt).
		SetIssuer(c.args.tokenIssuer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create token sealer: %w", err)
	}

	// Wrap the token sealer for console-specific ticket claim mapping:
	ticketSealer := console.NewTicketSealer(tokenSealer)

	// Create the JSON Web Key Set server (serves JWKS at /.well-known/jwks.json):
	c.logger.InfoContext(ctx, "Creating JSON Web Key Set server")
	jsonWebKeySetServer, err := servers.NewJsonWebKeySetServer().
		SetLogger(c.logger).
		SetSealer(tokenSealer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create JSON Web Key Set server: %w", err)
	}
	publicv1.RegisterJsonWebKeySetServer(grpcServer, jsonWebKeySetServer)

	// Build the console target resolver (lookup/policy only):
	hubLookup := servers.NewPrivateServerHubLookup(privateHubsServer)
	consoleResolver, err := servers.NewConsoleTargetResolver().
		SetLogger(c.logger).
		SetComputeInstanceLookup(servers.NewPrivateServerCILookup(privateComputeInstancesServer)).
		SetHubLookup(hubLookup).
		SetHubClientFactory(servers.NewDefaultHubClientFactory(hubScheme)).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create console target resolver: %w", err)
	}

	// Build the console session service (orchestration):
	sessionService, err := console.NewSessionService().
		SetLogger(c.logger).
		SetResolver(consoleResolver).
		SetSealer(ticketSealer).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create console session service: %w", err)
	}

	// Create the console sessions server (thin adapter):
	c.logger.InfoContext(ctx, "Creating console server")
	consoleServer, err := servers.NewConsoleServer().
		SetLogger(c.logger).
		SetSessionService(sessionService).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create console server: %w", err)
	}
	publicv1.RegisterConsoleSessionsServer(grpcServer, consoleServer)

	// Create the events server:
	c.logger.InfoContext(ctx, "Creating events server")
	eventsListener, err := database.NewListener().
		SetLogger(c.logger).
		SetUrl(dbTool.URL()).
		SetChannel("events").
		Build()
	if err != nil {
		return fmt.Errorf("failed to create events listener: %w", err)
	}
	eventsServer, err := servers.NewEventsServer().
		SetLogger(c.logger).
		SetListener(eventsListener).
		SetTenancyLogic(tenancyLogic).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create events server: %w", err)
	}
	go func() {
		err := eventsServer.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			c.logger.InfoContext(ctx, "Events server finished")
		} else {
			c.logger.ErrorContext(
				ctx,
				"Events server finished",
				slog.Any("error", err),
			)
		}
	}()
	publicv1.RegisterEventsServer(grpcServer, eventsServer)

	// Create the private events server:
	c.logger.InfoContext(ctx, "Creating private events server")
	privateEventsListener, err := database.NewListener().
		SetLogger(c.logger).
		SetUrl(dbTool.URL()).
		SetChannel("events").
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private events listener: %w", err)
	}
	privateEventsServer, err := servers.NewPrivateEventsServer().
		SetLogger(c.logger).
		SetListener(privateEventsListener).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private events server: %w", err)
	}
	go func() {
		err := privateEventsServer.Start(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			c.logger.InfoContext(ctx, "Private events server finished")
		} else {
			c.logger.ErrorContext(
				ctx,
				"Private events server finished",
				slog.Any("error", err),
			)
		}
	}()
	privatev1.RegisterEventsServer(grpcServer, privateEventsServer)

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
	shutdown.AddHttpServer(network.MetricsListenerName, 0, metricsServer)
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

	// Start serving:
	c.logger.InfoContext(
		ctx,
		"Start serving",
		slog.String("address", listener.Addr().String()),
	)
	go func() {
		err := grpcServer.Serve(listener)
		if err != nil {
			c.logger.ErrorContext(
				ctx,
				"gRPC server failed",
				slog.Any("error", err),
			)
		}
	}()

	// Keep running till the shutdown sequence finishes:
	c.logger.InfoContext(ctx, "Waiting for shutdown to sequence to complete")
	return shutdown.Wait()
}

// anonymousMethodsRegex is regular expression for the methods that are considered public, including the capabilities,
// JWKS, reflection, and health methods. These will skip authentication and authorization.
const anonymousMethodsRegex = `^/(osac\.public\.v1\.(Capabilities/|JsonWebKeySet/)|grpc\.(reflection|health)\.).*$`

const shortHelp = `Starts the gRPC server`

const longHelp = `
Starts the gRPC server.
`

const grpcAuthnTypeFlagHelp = `
_TYPE_ - **Deprecated and ignored.** The service now always uses
the built-in JWKS authentication and Rego authorization. This flag
is accepted for backward compatibility but has no effect.
 auth service.`

const grpcAuthnExternalAddressFlagHelp = `
_ADDRESS_ - **Deprecated and ignored.** External authentication via
Authorino is no longer used. The service now validates JWT tokens
directly using JWKS endpoints discovered from the trusted token
issuers. This flag is accepted for backward compatibility but has
no effect.
`

const caFileFlagHelp = `
_FILE_ - Files or directories containing trusted CA certificates in PEM format. Used for TLS connections to the external
services.
`

const grpcAuthnTrustedTokenIssuersFlagHelp = `
_ISSUERS_ - Comma separated list of token issuers that
are advertised as trusted by the gRPC server.
`

const tenancyLogicFlagHelp = `
_LOGIC_ - **Deprecated and ignored.** The service now always uses the default tenancy logic.
`

const tokenSignerCrtFlagHelp = `
_FILE_ - Path to the PEM-encoded signing certificate used to sign
JWT tokens issued by this server.
`

const tokenSignerKeyFlagHelp = `
_FILE_ - Path to the PEM-encoded private key used to sign
JWT tokens issued by this server.
`

const tokenEncryptionCrtFlagHelp = `
_FILE_ - Path to the PEM-encoded encryption certificate (public key)
of the token recipient. Used to encrypt the JWE envelope of issued tokens.
`

const tokenIssuerFlagHelp = `
_URL_ - Issuer URL for JWT tokens. Used as the iss claim. Token
consumers derive the JWKS endpoint as <issuer>/.well-known/jwks.json.
`

const emergencyServiceAccountsFlagHelp = `
_NAMES_ - Comma-separated list of Kubernetes service account names that are allowed to access the private API with
administrator permissions. These are intended only for emergency situations, for example when the regular authentication
mechanisms are not working. The service accounts are expected to be in the namespace where the service is deployed.
`

const vaultEndpointFlagHelp = `
_URL_ - Vault API endpoint URL.
`

const vaultNamespaceFlagHelp = `
_NAMESPACE_ - Parent namespace path within the Vault-compatible
store. Tenant namespaces are created as children of this namespace.
`

const vaultKVMountPathFlagHelp = `
_PATH_ - KV v2 secret engine mount path within tenant namespaces.
`
