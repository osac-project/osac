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
	"strconv"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	grpcstatus "google.golang.org/grpc/status"
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
	vault.AddBaseFlags(flags)
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
		vaultBase                vault.BaseConfig
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
	// filterable-resource-exempt: static introspection endpoint, no List RPC or CEL filter field
	publicv1.RegisterCapabilitiesServer(grpcServer, capabilitiesServer)
	privateCapabilitiesServer, err := servers.NewPrivateCapabilitiesServer().
		SetLogger(c.logger).
		AddAuthnTrustedTokenIssuers(c.args.trustedTokenIssuers...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create private capabilities server: %w", err)
	}
	// filterable-resource-exempt: static introspection endpoint, no List RPC or CEL filter field
	privatev1.RegisterCapabilitiesServer(grpcServer, privateCapabilitiesServer)

	// Create the runtime scheme for typed OSAC API objects:
	hubScheme, err := hubscheme.NewHub()
	if err != nil {
		return fmt.Errorf("failed to create hub scheme: %w", err)
	}

	// Read the vault flags:
	c.args.vaultBase, err = vault.BaseConfigFromFlags(c.flags)
	if err != nil {
		return fmt.Errorf("failed to read vault flags: %w", err)
	}

	// Set up vault if configured:
	var secretStore vault.SecretStore
	if c.args.vaultBase.Endpoint != "" {
		c.logger.InfoContext(ctx, "Performing vault health check")
		vaultCaPool := caPool
		if c.args.vaultBase.CaCertFile != "" {
			certPEM, readErr := os.ReadFile(c.args.vaultBase.CaCertFile)
			if readErr != nil {
				return fmt.Errorf(
					"failed to read vault CA cert from file '%s': %w",
					c.args.vaultBase.CaCertFile, readErr,
				)
			}
			vaultCaPool = caPool.Clone()
			if !vaultCaPool.AppendCertsFromPEM(certPEM) {
				return fmt.Errorf(
					"vault CA cert file '%s' contains no valid certificates",
					c.args.vaultBase.CaCertFile,
				)
			}
		}
		healthChecker, healthErr := vault.NewHealthChecker().
			SetLogger(c.logger).
			SetAddress(c.args.vaultBase.Endpoint).
			SetCaPool(vaultCaPool).
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

		tenantTokenSource, tokenErr := vault.NewServiceTenantTokenSourceFromConfig(
			c.logger, c.args.vaultBase, vaultCaPool,
		)
		if tokenErr != nil {
			return fmt.Errorf("failed to create service tenant token source: %w", tokenErr)
		}

		secretStore, err = vault.NewVaultSecretStore().
			SetLogger(c.logger).
			SetAddress(c.args.vaultBase.Endpoint).
			SetTokenSource(tenantTokenSource).
			SetParentNamespace(c.args.vaultBase.Namespace).
			SetKVMountPath(c.args.vaultBase.KVMountPath).
			SetCaPool(vaultCaPool).
			Build()
		if err != nil {
			return fmt.Errorf("failed to create vault secret store: %w", err)
		}
	}

	// Create the tier resolver for the volumes server. The resolver looks up a
	// StorageTier by name and returns the first backend association.
	storageTiersDAO, err := dao.NewGenericDAO[*privatev1.StorageTier]().
		SetLogger(c.logger).
		SetTenancyLogic(tenancyLogic).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create storage tiers DAO: %w", err)
	}
	tierResolver := newDAOTierResolver(storageTiersDAO)

	// Register all filterable resources' public and private servers:
	resourceServers, err := RegisterResourceServers(ctx, grpcServer, ResourceServerDeps{
		Logger:                  c.logger,
		Notifier:                notifier,
		PrivateAttributionLogic: privateAttributionLogic,
		PublicAttributionLogic:  publicAttributionLogic,
		TenancyLogic:            tenancyLogic,
		MetricsRegisterer:       metricsRegisterer,
		HubScheme:               hubScheme,
		SecretStore:             secretStore,
		TierResolver:            tierResolver,
		PrivateUsersServer:      privateUsersServer,
	})
	if err != nil {
		return err
	}
	privateHubsServer := resourceServers.PrivateHubsServer
	privateComputeInstancesServer := resourceServers.PrivateComputeInstancesServer
	privateSecretsServer := resourceServers.PrivateSecretsServer

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
	// filterable-resource-exempt: singleton JWKS fetch, no List RPC or CEL filter field
	publicv1.RegisterJsonWebKeySetServer(grpcServer, jsonWebKeySetServer)

	// Build the console target resolver (lookup/policy only):
	hubLookup := servers.NewPrivateServerHubLookup(privateHubsServer, privateSecretsServer)
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
	// filterable-resource-exempt: session/action RPC, no List RPC or CEL filter field; also depends on
	// privateHubsServer/privateComputeInstancesServer from RegisterResourceServers, so it must be built after
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
	// filterable-resource-exempt: streaming Watch RPC, no List RPC or CEL filter field
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
	// filterable-resource-exempt: streaming Watch RPC, no List RPC or CEL filter field
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

func newDAOTierResolver(
	tiersDAO *dao.GenericDAO[*privatev1.StorageTier],
) servers.TierResolverFunc {
	return func(ctx context.Context, tierName string) (*servers.TierResolution, error) {
		filter := fmt.Sprintf("this.metadata.name == %s", strconv.Quote(tierName))
		listResp, err := tiersDAO.List().
			SetFilter(filter).
			SetLimit(1).
			Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to look up storage tier %q: %w", tierName, err)
		}
		items := listResp.GetItems()
		if len(items) == 0 {
			return nil, grpcstatus.Errorf(grpccodes.NotFound, "storage tier %q not found", tierName)
		}
		tier := items[0]

		if tier.GetStatus().GetState() != privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE {
			return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition, "storage tier %q is not active", tierName)
		}

		backends := tier.GetSpec().GetBackends()
		if len(backends) == 0 {
			return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"storage tier %q has no backend associations", tierName)
		}

		selected := backends[0]
		return &servers.TierResolution{
			BackendID: selected.GetBackendId(),
			Protocol:  selected.GetProtocol(),
		}, nil
	}
}

const emergencyServiceAccountsFlagHelp = `
_NAMES_ - Comma-separated list of Kubernetes service account names that are allowed to access the private API with
administrator permissions. These are intended only for emergency situations, for example when the regular authentication
mechanisms are not working. The service accounts are expected to be in the namespace where the service is deployed.
`
