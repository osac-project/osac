/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// SECURITY: this file is the only regression coverage for the public-filter-oracle boundary
// (dao.FilterTranslator.SetDescriptor, see internal/database/dao/filter_translator.go). OPA authorization policies
// (internal/auth/policies/authz.rego) authorize by gRPC method path and caller identity only — they have no
// visibility into CEL filter expression content — so this test must never be skipped, excluded from CI, or weakened
// to cover only a fixed list of resources. It discovers every public/private message pair and their private-only
// field paths via protoreflect, so a future resource that forgets to wire SetFilterDesc fails here automatically.
// Discovery is by field name only: a future field that exists on both sides under the same name but with a
// different type (e.g. a private enum smuggled through a same-named public field) is not a case this file can
// generate, since a self-comparison on that path compiles fine against the public descriptor too.
package grpcserver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	hubscheme "github.com/osac-project/osac/fulfillment-service/internal/kubernetes/scheme"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/metrics"
	"github.com/osac-project/osac/fulfillment-service/internal/packages"
	"github.com/osac-project/osac/fulfillment-service/internal/provisioners"
	"github.com/osac-project/osac/fulfillment-service/internal/recovery"
	"github.com/osac-project/osac/fulfillment-service/internal/servers"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	"github.com/osac-project/osac/fulfillment-service/internal/services"
	itesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/validation"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestRegisterServers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "gRPC server registration package")
}

var (
	ctx           context.Context
	ctrl          *gomock.Controller
	logger        *slog.Logger
	dbContainer   *database.Container
	attribution   *auth.MockAttributionLogic
	tenancy       *auth.MockTenancyLogic
	conn          *grpc.ClientConn
	hubScheme     *k8sruntime.Scheme
	tierResolver  servers.TierResolverFunc
	txInterceptor *database.TxInterceptor
)

var _ = BeforeSuite(func() {
	var err error

	ctrl = gomock.NewController(GinkgoT())
	DeferCleanup(ctrl.Finish)

	logger, err = logging.NewLogger().
		SetLevel(slog.LevelDebug.String()).
		SetWriter(GinkgoWriter).
		Build()
	Expect(err).ToNot(HaveOccurred())

	attribution = auth.NewMockAttributionLogic(ctrl)
	attribution.EXPECT().DetermineAssignedCreator(gomock.Any()).
		Return("system", nil).
		AnyTimes()

	tenancy = auth.NewMockTenancyLogic(ctrl)
	tenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
		Return(auth.AllTenants, nil).
		AnyTimes()
	tenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
		Return(auth.SystemTenant, nil).
		AnyTimes()
	tenancy.EXPECT().DetermineVisibility(gomock.Any()).
		Return(auth.TotalVisibility(), nil).
		AnyTimes()

	// Start the containerized database:
	startupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dbContainer, err = database.NewContainer().
		SetLogger(logger).
		Build()
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx context.Context) {
		err := dbContainer.Stop(ctx)
		Expect(err).ToNot(HaveOccurred())
	})
	err = dbContainer.Start(startupCtx)
	Expect(err).ToNot(HaveOccurred())

	ctx = context.Background()

	// Prepare the database pool:
	db, err := dbContainer.NewInstance().Build()
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(db.Close)
	pool, err := db.Pool(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(pool.Close)

	// Create the transaction manager and interceptor, exactly as production wires them in
	// start_grpc_server_cmd.go's run(). This is load-bearing, not incidental setup: GenericDAO's ListRequest.Do()
	// calls database.TxFromContext(ctx) and fails with Internal before ever reaching filter translation if no
	// transaction has been injected into the request context. Driving a real grpc.ClientConn means the server
	// must inject its own per-request transaction via this interceptor.
	txManager, err := database.NewTxManager().
		SetLogger(logger).
		SetPool(pool).
		Build()
	Expect(err).ToNot(HaveOccurred())
	txInterceptor, err = database.NewTxInterceptor().
		SetLogger(logger).
		SetManager(txManager).
		Build()
	Expect(err).ToNot(HaveOccurred())

	// Create the panic interceptor, exactly as production wires it in start_grpc_server_cmd.go's run(). Ginkgo's
	// GinkgoRecover in Server.Start() only guards the top-level Serve() goroutine, not the per-request goroutines
	// grpc-go spawns internally, so without this a handler panic in any of the 28+ servers registered below would
	// crash the whole test binary instead of failing one spec.
	panicInterceptor, err := recovery.NewGrpcPanicInterceptor().
		SetLogger(logger).
		Build()
	Expect(err).ToNot(HaveOccurred())

	hubScheme, err = hubscheme.NewHub()
	Expect(err).ToNot(HaveOccurred())
	metricsRegisterer := prometheus.NewRegistry()

	// Create the storage tiers and backends DAOs and the tier resolver, exactly as production wires them in
	// start_grpc_server_cmd.go's run(), for the private volumes server's mandatory SetTierResolver dependency.
	storageTiersDAO, err := dao.NewGenericDAO[*privatev1.StorageTier]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	storageBackendsDAO, err := dao.NewGenericDAO[*privatev1.StorageBackend]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	tierResolver = newDAOTierResolver(storageTiersDAO, storageBackendsDAO)

	// Create the private users server, exactly as production does in start_grpc_server_cmd.go's run(). It's
	// constructed outside RegisterResourceServers there because the JIT provisioning interceptor needs it before
	// the interceptor chain is built — see ResourceServerDeps.PrivateUsersServer's doc comment.
	privateUsersServer, err := servers.NewPrivateUsersServer().
		SetLogger(logger).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	Expect(err).ToNot(HaveOccurred())

	// Start a real gRPC server with the transaction and reference interceptors production uses, and
	// register every filterable resource through the same function production calls:
	referenceValidator, err := newReferenceValidator(logger, tenancy, metricsRegisterer)
	Expect(err).ToNot(HaveOccurred())
	server := itesting.NewServer(grpc.ChainUnaryInterceptor(
		panicInterceptor.UnaryServer,
		txInterceptor.UnaryServer,
		referenceValidator.UnaryServer,
	))
	DeferCleanup(server.Stop)
	_, err = RegisterResourceServers(ctx, server.Registrar(), ResourceServerDeps{
		Logger:                  logger,
		PrivateAttributionLogic: attribution,
		PublicAttributionLogic:  attribution,
		TenancyLogic:            tenancy,
		MetricsRegisterer:       metricsRegisterer,
		HubScheme:               hubScheme,
		SecretStore:             vault.NewMockSecretStore(ctrl),
		TierResolver:            tierResolver,
		PrivateUsersServer:      privateUsersServer,
		Services:                &services.Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true},
	})
	Expect(err).ToNot(HaveOccurred())
	server.Start()

	conn, err = grpc.NewClient(
		server.Address(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(conn.Close)
})

// filterOracleCase describes one private-only field path found on a resource that has a public List RPC.
type filterOracleCase struct {
	resourceName string
	fieldPath    string
	methodPath   string
	requestDesc  protoreflect.MessageDescriptor
	responseDesc protoreflect.MessageDescriptor
	filterField  protoreflect.FieldDescriptor
}

// resourcesExcludedFromDiscovery lists resources deliberately not reachable through RegisterResourceServers, so
// the static discovery below must not generate cases for them. Users is excluded because its private half is
// built early (for JIT provisioning, before RegisterResourceServers is even called) and its public half is built
// afterward in run() — a targeted filter-oracle regression test for Users (covering the private-only
// UserStatus.keycloak_user_id field) is added separately in users_server_test.go as part of this same fix.
// BillingAccount and TenantBillingAssignment are registered separately through registerBillingResourceServers,
// sharing the configured provider between their public/private servers. Both use GenericDAO with public filter
// descriptors; their targeted filter regressions live in the corresponding billing server suites.
var resourcesExcludedFromDiscovery = map[string]bool{
	"User":                    true,
	"BillingAccount":          true,
	"TenantBillingAssignment": true,
}

var _ = Describe("Registered billing scaffolds", func() {
	It("keeps scaffolds unimplemented and denies them through OPA even to administrators before provider access", func(ctx context.Context) {
		fake := billingfake.New(billingfake.Scenario{})
		newClient := func(interceptors ...grpc.UnaryServerInterceptor) *grpc.ClientConn {
			interceptors = append([]grpc.UnaryServerInterceptor{txInterceptor.UnaryServer}, interceptors...)
			server := itesting.NewServer(grpc.ChainUnaryInterceptor(interceptors...))
			DeferCleanup(server.Stop)
			Expect(registerBillingResourceServers(ctx, server.Registrar(), logger, fake.Provider())).To(Succeed())
			server.Start()
			client, err := grpc.NewClient(server.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(client.Close)
			return client
		}
		methods := []struct {
			method   string
			request  proto.Message
			response proto.Message
		}{
			{publicv1.BillingAccounts_Get_FullMethodName, &publicv1.BillingAccountsGetRequest{}, &publicv1.BillingAccountsGetResponse{}},
			{privatev1.BillingAccounts_Get_FullMethodName, &privatev1.BillingAccountsGetRequest{}, &privatev1.BillingAccountsGetResponse{}},
			{publicv1.BillingAccounts_Create_FullMethodName, &publicv1.BillingAccountsCreateRequest{}, &publicv1.BillingAccountsCreateResponse{}},
			{privatev1.BillingAccounts_Create_FullMethodName, &privatev1.BillingAccountsCreateRequest{}, &privatev1.BillingAccountsCreateResponse{}},
			{publicv1.BillingAccounts_Delete_FullMethodName, &publicv1.BillingAccountsDeleteRequest{}, &publicv1.BillingAccountsDeleteResponse{}},
			{privatev1.BillingAccounts_Delete_FullMethodName, &privatev1.BillingAccountsDeleteRequest{}, &privatev1.BillingAccountsDeleteResponse{}},
			{publicv1.TenantBillingAssignments_Delete_FullMethodName, &publicv1.TenantBillingAssignmentsDeleteRequest{}, &publicv1.TenantBillingAssignmentsDeleteResponse{}},
			{privatev1.TenantBillingAssignments_Delete_FullMethodName, &privatev1.TenantBillingAssignmentsDeleteRequest{}, &privatev1.TenantBillingAssignmentsDeleteResponse{}},
		}
		// Without authorization, these registered methods must still be the generated scaffolds, not CRUD handlers.
		raw := newClient()
		for _, method := range methods {
			err := raw.Invoke(ctx, method.method, method.request, method.response)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented), method.method)
		}
		Expect(fake.Calls()).To(BeEmpty())

		evaluator, err := auth.NewEvaluator().Build()
		Expect(err).ToNot(HaveOccurred())
		authz, err := auth.NewGrpcAuthzInterceptor().SetLogger(logger).SetEvaluator(evaluator).Build()
		Expect(err).ToNot(HaveOccurred())
		referenceValidator, err := newReferenceValidator(logger, tenancy, prometheus.NewRegistry())
		Expect(err).ToNot(HaveOccurred())
		for _, persona := range []struct {
			username string
			groups   []any
			admin    bool
		}{
			{"billing-admin", []any{"admins"}, true},
			{"service-account-osac-admin", nil, true},
			{"service-account-osac-controller", nil, true},
			{"tenant-user", nil, false},
		} {
			token := itesting.MakeTokenObject(nil, jwt.MapClaims{
				"username": persona.username, "groups": persona.groups, "organization": []any{"tenant-a"},
			})
			client := newClient(func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				return handler(auth.ContextWithToken(ctx, token), request)
			}, authz.UnaryServer, referenceValidator.UnaryServer)
			for _, method := range methods {
				err := client.Invoke(ctx, method.method, method.request, method.response)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied), persona.username+" "+method.method)
			}
			// Positive control: the reviewed Get path reaches the real handler's required-id check for admins.
			_, err := publicv1.NewTenantBillingAssignmentsClient(client).Get(ctx, &publicv1.TenantBillingAssignmentsGetRequest{})
			if persona.admin {
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(grpcstatus.Convert(err).Message()).To(Equal("id is required"))
			} else {
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))
			}
			Expect(fake.Calls()).To(BeEmpty(), persona.username)
		}
	})
})

var _ = Describe("Production request transaction boundary", func() {
	const tenantID = "request-tenant"
	const foreignTenant = "foreign-tenant"
	var (
		manager     database.TxManager
		observer    *pgx.Conn
		realTenancy auth.TenancyLogic
		newClient   func(*billing.Provider) *grpc.ClientConn
		caller      func(string, bool) context.Context
	)

	BeforeEach(func() {
		db, err := dbContainer.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err := db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)
		// This connection never belongs to the server pool or its request transaction.
		observer, err = db.Connection(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(observer.Close(ctx)).To(Succeed()) })
		for _, tenant := range []string{tenantID, foreignTenant} {
			_, err = observer.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
				values ($1, $1, $1, 'system', '{}')`, tenant)
			Expect(err).ToNot(HaveOccurred())
		}
		manager, err = database.NewTxManager().SetLogger(logger).SetPool(pool).Build()
		Expect(err).ToNot(HaveOccurred())
		realTenancy, err = auth.NewDefaultTenancyLogic().SetLogger(logger).
			SetTenantResolver(database.ResolveLiveTenantNames).Build()
		Expect(err).ToNot(HaveOccurred())
		localCtrl := gomock.NewController(GinkgoT())
		DeferCleanup(localCtrl.Finish)
		jwtValidator := auth.NewMockJwtValidator(localCtrl)
		tokens := map[string]*jwt.Token{}
		// Only JWT crypto/issuer validation is mocked. The real authn interceptor
		// consumes Bearer metadata; OPA constructs the actual trusted subject.
		jwtValidator.EXPECT().Validate(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, raw string) (*jwt.Token, error) {
				token, ok := tokens[raw]
				if !ok {
					return nil, fmt.Errorf("unknown fixture token")
				}
				return token, nil
			}).AnyTimes()
		caller = func(username string, admin bool) context.Context {
			claims := jwt.MapClaims{"username": username, "organization": []any{tenantID},
				"realm_access": map[string]any{"roles": []any{"tenant-admin"}}}
			if admin {
				claims["groups"] = []any{"admins"}
			}
			token := itesting.MakeTokenObject(nil, claims)
			tokens[token.Raw] = token
			return metadata.NewOutgoingContext(ctx, metadata.Pairs(auth.Authorization, "Bearer "+token.Raw))
		}
		newClient = func(provider *billing.Provider) *grpc.ClientConn {
			registry := prometheus.NewRegistry()
			privateAttribution, err := auth.NewSystemAttributionLogic().SetLogger(logger).Build()
			Expect(err).ToNot(HaveOccurred())
			users, err := servers.NewPrivateUsersServer().SetLogger(logger).
				SetAttributionLogic(privateAttribution).SetTenancyLogic(realTenancy).SetMetricsRegisterer(registry).Build()
			Expect(err).ToNot(HaveOccurred())
			usersDAO, err := dao.NewGenericDAO[*privatev1.User]().SetLogger(logger).SetTenancyLogic(realTenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			publicAttribution, err := auth.NewDefaultAttributionLogic().SetLogger(logger).
				SetUserIDResolver(&userIDResolver{usersDAO: usersDAO}).Build()
			Expect(err).ToNot(HaveOccurred())
			provisioner, err := provisioners.NewUserProvisioner().SetLogger(logger).SetUsersServer(users).Build()
			Expect(err).ToNot(HaveOccurred())
			jit, err := auth.NewGrpcJitProvisioningInterceptor().SetLogger(logger).SetProvisioner(provisioner).Build()
			Expect(err).ToNot(HaveOccurred())
			evaluator, err := auth.NewEvaluator().SetManagedServiceAccountResolver(serviceaccounts.ResolveAuthorization).
				SetTenantResolver(database.ResolveLiveTenantNames).Build()
			Expect(err).ToNot(HaveOccurred())
			projects, err := dao.NewMetadataFetcher().SetLogger(logger).SetTable("projects").Build()
			Expect(err).ToNot(HaveOccurred())
			memberships, err := dao.NewMetadataFetcher().SetLogger(logger).SetTable("project_memberships").Build()
			Expect(err).ToNot(HaveOccurred())
			authz, err := auth.NewGrpcAuthzInterceptor().SetLogger(logger).SetEvaluator(evaluator).
				SetMetadataFetcher(projects).SetProjectMembershipMetadataFetcher(memberships).Build()
			Expect(err).ToNot(HaveOccurred())
			authn, err := auth.NewGrpcAuthnInterceptor().SetLogger(logger).SetJwtValidator(jwtValidator).Build()
			Expect(err).ToNot(HaveOccurred())
			refs, err := newReferenceValidator(logger, realTenancy, registry)
			Expect(err).ToNot(HaveOccurred())
			billingAvailability := servers.NewBillingAvailabilityInterceptor(provider,
				billingAssignmentMethods()...)
			tx, err := database.NewTxInterceptor().SetLogger(logger).SetManager(manager).Build()
			Expect(err).ToNot(HaveOccurred())
			panicInterceptor, err := recovery.NewGrpcPanicInterceptor().SetLogger(logger).Build()
			Expect(err).ToNot(HaveOccurred())
			metricsInterceptor, err := metrics.NewGrpcInterceptor().SetSubsystem("inbound").SetRegisterer(registry).Build()
			Expect(err).ToNot(HaveOccurred())
			loggingInterceptor, err := logging.NewInterceptor().SetLogger(logger).Build()
			Expect(err).ToNot(HaveOccurred())
			validationInterceptor, err := validation.NewProtovalidateInterceptor().SetLogger(logger).Build()
			Expect(err).ToNot(HaveOccurred())
			server := itesting.NewServer(grpc.ChainUnaryInterceptor(
				panicInterceptor.UnaryServer, metricsInterceptor.UnaryServer, loggingInterceptor.UnaryServer,
				validationInterceptor.UnaryServer, tx.UnaryServer, authn.UnaryServer,
				authz.UnaryServer, jit.UnaryServer, billingAvailability, refs.UnaryServer,
			))
			DeferCleanup(server.Stop)
			_, err = RegisterResourceServers(ctx, server.Registrar(), ResourceServerDeps{
				Logger: logger, PrivateAttributionLogic: privateAttribution, PublicAttributionLogic: publicAttribution,
				TenancyLogic: realTenancy, MetricsRegisterer: registry, HubScheme: hubScheme,
				SecretStore: vault.NewMockSecretStore(localCtrl), TierResolver: tierResolver,
				PrivateUsersServer: users, Services: &services.Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(registerBillingResourceServers(ctx, server.Registrar(), logger, provider)).To(Succeed())
			server.Start()
			client, err := grpc.NewClient(server.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(client.Close)
			return client
		}
	})

	assertUserEffects := func(username string, expected int) {
		GinkgoHelper()
		var count int
		Expect(observer.QueryRow(ctx, `select count(*) from users where name = $1 and tenant = $2`, username, tenantID).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(expected), "committed JIT User")
		Expect(observer.QueryRow(ctx, `select count(*) from changes where "table" = 'users' and data->>'name' = $1`, username).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(expected), "transactional User changes row")
	}

	It("rolls back real eligible JIT writes after missing-reference and ordinary handler errors", func() {
		client := publicv1.NewRoleBindingsClient(newClient(billing.NewProvider("", nil, nil)))
		for _, failure := range []string{"reference", "filter"} {
			username := "jit-" + failure
			identity := caller(username, false)
			assertUserEffects(username, 0)
			var err error
			if failure == "reference" {
				_, err = client.Create(identity, publicv1.RoleBindingsCreateRequest_builder{Object: publicv1.RoleBinding_builder{
					Metadata: publicv1.Metadata_builder{Name: "missing-role-binding", Tenant: tenantID}.Build(),
					Spec:     publicv1.RoleBindingSpec_builder{Role: publicv1.RoleReference_builder{Name: "missing-role"}.Build()}.Build(),
				}.Build()}.Build())
				Expect(err).To(MatchError(ContainSubstring("object.spec.role")))
			} else {
				_, err = client.List(identity, publicv1.RoleBindingsListRequest_builder{Filter: new("this.spec.role ==")}.Build())
			}
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			assertUserEffects(username, 0)
			// The same identity really is eligible: a successful ordinary RPC commits
			// both the actual provisioner's User and the database trigger's change.
			_, err = client.List(identity, &publicv1.RoleBindingsListRequest{})
			Expect(err).ToNot(HaveOccurred())
			assertUserEffects(username, 1)
		}
	})

	It("keeps denied, unconfigured and provider-failed billing discovery free of local effects", func() {
		for _, private := range []bool{false, true} {
			failed := billingfake.New(billingfake.Scenario{Failures: map[string]billingfake.Failure{
				"ListAccounts": {Reason: billing.ReasonUnavailable, Retryable: true},
			}})
			client := newClient(failed.Provider())
			list := func(identity context.Context, client *grpc.ClientConn) error {
				if private {
					_, err := privatev1.NewBillingAccountsClient(client).List(identity, &privatev1.BillingAccountsListRequest{})
					return err
				}
				_, err := publicv1.NewBillingAccountsClient(client).List(identity, &publicv1.BillingAccountsListRequest{})
				return err
			}
			Expect(grpcstatus.Code(list(caller("denied-billing", false), client))).To(Equal(grpccodes.PermissionDenied))
			Expect(failed.Calls()).To(BeEmpty())
			Expect(grpcstatus.Code(list(caller("billing-admin", true), client))).To(Equal(grpccodes.Unavailable))
			Expect(failed.Count("ListAccounts")).To(Equal(1))
			off := newClient(billing.NewProvider("", nil, nil))
			err := list(caller("billing-admin", true), off)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(fmt.Sprint(grpcstatus.Convert(err).Details())).To(ContainSubstring("BILLING_NOT_CONFIGURED"))
		}
		assertUserEffects("billing-admin", 0)
		assertUserEffects("denied-billing", 0)
		for _, table := range []string{"billing_accounts", "tenant_billing_assignments"} {
			var count int
			Expect(observer.QueryRow(ctx, "select count(*) from "+table).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
		}
	})

	Describe("billing Update prehandler precedence", func() {
		const accountID = "request-account"
		const replacementID = "replacement-account"
		stableName := func(id string) string { return fmt.Sprintf("ba-%x", sha256.Sum256([]byte(id)))[:63] }
		accountName := func() string { return stableName(accountID) }
		var fake *billingfake.Fake
		var client *grpc.ClientConn
		BeforeEach(func() {
			// Commit all setup before transport; it must be visible to its own transaction.
			setup := auth.ContextWithSubject(database.TxManagerIntoContext(ctx, manager), &auth.Subject{User: "fixture", Tenants: auth.AllTenants})
			_, err := database.WithNewTx(setup, func(c context.Context) (struct{}, error) {
				accounts, err := dao.NewGenericDAO[*privatev1.BillingAccount]().SetLogger(logger).SetTenancyLogic(realTenancy).Build()
				if err != nil {
					return struct{}{}, err
				}
				for _, id := range []string{accountID, replacementID} {
					_, err = accounts.Create().SetObject(privatev1.BillingAccount_builder{Id: id,
						Metadata: privatev1.Metadata_builder{Name: stableName(id), Tenant: auth.SystemTenant, Creator: "system"}.Build(),
						Spec:     &privatev1.BillingAccountSpec{}, Status: privatev1.BillingAccountStatus_builder{
							ProviderKey: billingfake.Key, ProviderAccountReference: id,
							State: privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE, StateObservationTimestamp: timestamppb.Now(),
						}.Build(),
					}.Build()).Do(c)
					if err != nil {
						return struct{}{}, err
					}
				}
				assignments, err := dao.NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(logger).SetTenancyLogic(realTenancy).Build()
				if err != nil {
					return struct{}{}, err
				}
				for _, historical := range []bool{true, false} {
					id := "current"
					state := privatev1.TenantBillingAssignmentStatus_builder{
						ConnectionState:        privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
						EffectiveFromTimestamp: timestamppb.New(time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)),
					}.Build()
					if historical {
						id = "historical"
						state.SetEffectiveToTimestamp(timestamppb.New(time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)))
					}
					_, err = assignments.Create().SetObject(privatev1.TenantBillingAssignment_builder{Id: id,
						Metadata: privatev1.Metadata_builder{Name: id, Tenant: foreignTenant, Creator: "fixture",
							Annotations: map[string]string{"osac.openshift.io/tenant": foreignTenant}}.Build(),
						Spec: privatev1.TenantBillingAssignmentSpec_builder{Tenant: privatev1.TenantReference_builder{Id: foreignTenant, Name: foreignTenant}.Build(),
							BillingAccount: privatev1.BillingAccountReference_builder{Id: accountID, Name: accountName()}.Build()}.Build(), Status: state,
					}.Build()).Do(c)
					if err != nil {
						return struct{}{}, err
					}
				}
				return struct{}{}, nil
			})
			Expect(err).ToNot(HaveOccurred())
			fake = billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
				{ID: accountID, ExternalID: "provider-token", State: billing.AccountActive},
				{ID: replacementID, ExternalID: "replacement-token", State: billing.AccountActive},
			}})
			client = newClient(fake.Provider())
		})
		update := func(identity context.Context, private bool, id, tenant, account, name string, mask *fieldmaskpb.FieldMask) error {
			GinkgoHelper()
			if private {
				_, err := privatev1.NewTenantBillingAssignmentsClient(client).Update(identity, privatev1.TenantBillingAssignmentsUpdateRequest_builder{
					Object: privatev1.TenantBillingAssignment_builder{Id: id, Spec: privatev1.TenantBillingAssignmentSpec_builder{
						Tenant: privatev1.TenantReference_builder{Id: tenant}.Build(), BillingAccount: privatev1.BillingAccountReference_builder{Id: account, Name: name}.Build(),
					}.Build()}.Build(), UpdateMask: mask,
				}.Build())
				return err
			}
			_, err := publicv1.NewTenantBillingAssignmentsClient(client).Update(identity, publicv1.TenantBillingAssignmentsUpdateRequest_builder{
				Object: publicv1.TenantBillingAssignment_builder{Id: id, Spec: publicv1.TenantBillingAssignmentSpec_builder{
					Tenant: publicv1.TenantReference_builder{Id: tenant}.Build(), BillingAccount: publicv1.BillingAccountReference_builder{Id: account, Name: name}.Build(),
				}.Build()}.Build(), UpdateMask: mask,
			}.Build())
			return err
		}
		create := func(identity context.Context, private bool, name, tenant, account, accountName string) error {
			GinkgoHelper()
			if private {
				_, err := privatev1.NewTenantBillingAssignmentsClient(client).Create(identity, privatev1.TenantBillingAssignmentsCreateRequest_builder{
					Object: privatev1.TenantBillingAssignment_builder{
						Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
						Spec: privatev1.TenantBillingAssignmentSpec_builder{
							Tenant:         privatev1.TenantReference_builder{Id: tenant}.Build(),
							BillingAccount: privatev1.BillingAccountReference_builder{Id: account, Name: accountName}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				return err
			}
			_, err := publicv1.NewTenantBillingAssignmentsClient(client).Create(identity, publicv1.TenantBillingAssignmentsCreateRequest_builder{
				Object: publicv1.TenantBillingAssignment_builder{
					Metadata: publicv1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
					Spec: publicv1.TenantBillingAssignmentSpec_builder{
						Tenant:         publicv1.TenantReference_builder{Id: tenant}.Build(),
						BillingAccount: publicv1.BillingAccountReference_builder{Id: account, Name: accountName}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			return err
		}
		It("denies non-admins before references and characterizes admin validation before stored-target handling", func() {
			for _, private := range []bool{false, true} {
				for _, id := range []string{"missing", "current", "historical"} {
					err := update(caller("tenant-admin", false), private, id, foreignTenant, "missing-account", "contradictory-name", nil)
					Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))
				}
				admin := caller("billing-admin", true)
				err := update(admin, private, "missing", foreignTenant, accountID, accountName(), nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
				for _, id := range []string{"missing", "current", "historical"} {
					err = update(admin, private, id, foreignTenant, "missing-account", "", nil)
					Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
					Expect(err).To(MatchError(ContainSubstring("object.spec.billing_account")))
					err = update(admin, private, id, foreignTenant, "missing-account", "",
						&fieldmaskpb.FieldMask{Paths: []string{"spec.billing_account"}})
					Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
					Expect(err).To(MatchError(ContainSubstring("object.spec.billing_account")))
					err = update(admin, private, id, foreignTenant, accountID, "contradictory-name", nil)
					Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
					Expect(err).To(MatchError(ContainSubstring("object.spec.billing_account")))
					Expect(grpcstatus.Convert(err).Details()).ToNot(BeEmpty())
				}
				err = update(admin, private, "missing", foreignTenant, accountID, accountName(), &fieldmaskpb.FieldMask{Paths: []string{"spec..billing_account"}})
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("non-canonical")))
				err = update(admin, private, "historical", foreignTenant, accountID, accountName(), nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				err = update(admin, private, "current", tenantID, accountID, accountName(), nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				err = update(admin, private, "current", foreignTenant, accountID, accountName(), &fieldmaskpb.FieldMask{})
				Expect(err).ToNot(HaveOccurred())
				Expect(fake.Calls()).To(BeEmpty())
			}
			// Authorized billing admins are universal in production policy and skip JIT.
			assertUserEffects("billing-admin", 0)
			assertUserEffects("tenant-admin", 0)
			// Valid references route through both real wrappers into provider revalidation.
			for _, private := range []bool{false, true} {
				err := update(caller("billing-admin", true), private, "current", foreignTenant, accountID, accountName(), nil)
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(fake.Count("GetAccount")).To(Equal(2))
			var intervals int
			Expect(observer.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&intervals)).To(Succeed())
			Expect(intervals).To(Equal(2), "same-account revalidation does not create history")
		})

		It("treats an explicit empty-mask Update as a no-op without resolving the body reference", func() {
			for _, private := range []bool{false, true} {
				// Neither stale selector exists, so resolving them would fail the request. The empty
				// mask selects nothing and the handler answers the stored interval unchanged.
				err := update(caller("billing-admin", true), private, "current", foreignTenant,
					"missing-account", "stale-account", &fieldmaskpb.FieldMask{})
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(fake.Calls()).To(BeEmpty(), "no provider call")
			var intervals int
			Expect(observer.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&intervals)).To(Succeed())
			Expect(intervals).To(Equal(2), "no interval is opened")
			var account string
			Expect(observer.QueryRow(ctx,
				`select data->'spec'->'billing_account'->>'id' from tenant_billing_assignments where id = 'current'`).Scan(&account)).To(Succeed())
			Expect(account).To(Equal(accountID), "stored selection is unchanged")
		})

		It("keeps validating a selected billing account on Create and Update", func() {
			for _, private := range []bool{false, true} {
				err := update(caller("billing-admin", true), private, "current", foreignTenant,
					"missing-account", "", &fieldmaskpb.FieldMask{Paths: []string{"spec.billing_account"}})
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("object.spec.billing_account")))

				err = create(caller("billing-admin", true), private, "missing-account-create",
					foreignTenant, "missing-account", "")
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("object.spec.billing_account")))
			}
			Expect(fake.Calls()).To(BeEmpty())
		})

		It("reports BILLING_NOT_CONFIGURED before resolving an unknown billing account", func() {
			client = newClient(billing.NewProvider("", nil, nil))
			for _, private := range []bool{false, true} {
				// Non-admins are still denied before billing availability.
				denied := update(caller("tenant-admin", false), private, "current", foreignTenant,
					"missing-account", "", nil)
				Expect(grpcstatus.Code(denied)).To(Equal(grpccodes.PermissionDenied))

				err := update(caller("billing-admin", true), private, "current", foreignTenant,
					"missing-account", "stale-account", nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				Expect(fmt.Sprint(grpcstatus.Convert(err).Details())).To(ContainSubstring("BILLING_NOT_CONFIGURED"))

				err = create(caller("billing-admin", true), private, "disabled", foreignTenant,
					"missing-account", "stale-account")
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				Expect(fmt.Sprint(grpcstatus.Convert(err).Details())).To(ContainSubstring("BILLING_NOT_CONFIGURED"))
			}
			var accounts, intervals int
			Expect(observer.QueryRow(ctx, `select count(*) from billing_accounts`).Scan(&accounts)).To(Succeed())
			Expect(accounts).To(Equal(2))
			Expect(observer.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&intervals)).To(Succeed())
			Expect(intervals).To(Equal(2))
		})

		It("keeps reference validation before a configured but unusable provider", func() {
			client = newClient(billing.NewProvider(billing.KeyM360, nil, fmt.Errorf("unreadable credential")))
			for _, private := range []bool{false, true} {
				err := update(caller("billing-admin", true), private, "current", foreignTenant,
					"missing-account", "", nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("object.spec.billing_account")))
			}
		})

		It("rolls back assignment effects on provider failure through both registered APIs", func() {
			for _, private := range []bool{false, true} {
				failed := billingfake.New(billingfake.Scenario{Failures: map[string]billingfake.Failure{
					"GetAccount": {Reason: billing.ReasonUnavailable, Retryable: true},
				}})
				client = newClient(failed.Provider())
				err := update(caller("billing-admin", true), private, "current", foreignTenant, replacementID, stableName(replacementID), nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unavailable))
				Expect(failed.Count("GetAccount")).To(Equal(1))
				// Check the original row itself, independently of the counts below: a provider failure must
				// not close the current interval and merely fail to open its replacement.
				var open bool
				Expect(observer.QueryRow(ctx,
					`select effective_to is null from tenant_billing_assignments where id = 'current'`).Scan(&open)).To(Succeed())
				Expect(open).To(BeTrue(), "the original interval must remain open after provider failure")
			}
			var intervals, tokens int
			Expect(observer.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&intervals)).To(Succeed())
			Expect(intervals).To(Equal(2))
			Expect(observer.QueryRow(ctx, `select count(*) from billing_accounts where external_billing_account_id is not null`).Scan(&tokens)).To(Succeed())
			Expect(tokens).To(BeZero())
		})

		It("returns sanitized Internal for a deferred outer commit failure without token or history partial commits", func() {
			_, err := observer.Exec(ctx, `create function reject_assignment_commit() returns trigger language plpgsql as $$
				begin raise exception 'private deferred constraint details'; end $$;
				create constraint trigger reject_assignment_commit after insert on tenant_billing_assignments
				deferrable initially deferred for each row execute function reject_assignment_commit()`)
			Expect(err).ToNot(HaveOccurred())
			for _, private := range []bool{false, true} {
				err = update(caller("billing-admin", true), private, "current", foreignTenant, replacementID, stableName(replacementID), nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Internal))
				Expect(grpcstatus.Convert(err).Message()).To(Equal("failed to end transaction"))
				var intervals, tokens int
				var current bool
				Expect(observer.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&intervals)).To(Succeed())
				Expect(intervals).To(Equal(2))
				Expect(observer.QueryRow(ctx, `select effective_to is null from tenant_billing_assignments where id = 'current'`).Scan(&current)).To(Succeed())
				Expect(current).To(BeTrue())
				Expect(observer.QueryRow(ctx, `select count(*) from billing_accounts where external_billing_account_id is not null`).Scan(&tokens)).To(Succeed())
				Expect(tokens).To(BeZero())
			}
			Expect(fake.Count("GetAccount")).To(Equal(2), "both requests reached provider reads before the failed commit")
			assertUserEffects("billing-admin", 0)
			// Removing only the deferred fault lets the same production chain commit.
			_, err = observer.Exec(ctx, `drop trigger reject_assignment_commit on tenant_billing_assignments`)
			Expect(err).ToNot(HaveOccurred())
			Expect(update(caller("billing-admin", true), false, "current", foreignTenant, replacementID, stableName(replacementID), nil)).To(Succeed())
			var intervals int
			Expect(observer.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&intervals)).To(Succeed())
			Expect(intervals).To(Equal(3))
		})
	})
})

// discoverFilterOracleCases walks the public/private proto registries, pairs every public/private message by
// name, and for each pair with a public List RPC computes the private-only field paths. This makes the resulting
// test cases self-updating: a resource added in the future is automatically covered with no changes to this file.
func discoverFilterOracleCases() []filterOracleCase {
	type pair struct {
		public  protoreflect.MessageDescriptor
		private protoreflect.MessageDescriptor
	}
	pairs := map[string]*pair{}
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		pkg := string(fd.Package())
		if pkg != packages.PublicV1 && pkg != packages.PrivateV1 {
			return true
		}
		msgs := fd.Messages()
		for i := range msgs.Len() {
			md := msgs.Get(i)
			name := string(md.Name())
			p, ok := pairs[name]
			if !ok {
				p = &pair{}
				pairs[name] = p
			}
			if pkg == packages.PublicV1 {
				p.public = md
			} else {
				p.private = md
			}
		}
		return true
	})

	var cases []filterOracleCase
	for name, p := range pairs {
		if resourcesExcludedFromDiscovery[name] {
			continue
		}
		if p.public == nil || p.private == nil {
			continue
		}
		paths := findPrivateOnlyPaths(p.private, p.public, "", map[protoreflect.FullName]bool{})
		if len(paths) == 0 {
			continue
		}
		methodPath, requestDesc, responseDesc, filterField, found := findPublicListMethod(p.public)
		if !found {
			continue
		}
		for _, path := range paths {
			cases = append(cases, filterOracleCase{
				resourceName: name,
				fieldPath:    path,
				methodPath:   methodPath,
				requestDesc:  requestDesc,
				responseDesc: responseDesc,
				filterField:  filterField,
			})
		}
	}
	// pairs is built by ranging over a map, so cases would otherwise come out in a different order on every run.
	sort.Slice(cases, func(i, j int) bool {
		if cases[i].resourceName != cases[j].resourceName {
			return cases[i].resourceName < cases[j].resourceName
		}
		return cases[i].fieldPath < cases[j].fieldPath
	})
	return cases
}

// findPrivateOnlyPaths recursively compares privateDesc's fields against publicDesc's fields by name. A field
// present on privateDesc but absent from publicDesc is a private-only path. When a field is present on both and
// both sides describe it as a singular (non-repeated, non-map) message, the comparison recurses into it, so a
// divergence nested under a shared submessage (e.g. spec.host_label_selector) is found as well as top-level ones.
func findPrivateOnlyPaths(privateDesc, publicDesc protoreflect.MessageDescriptor, prefix string,
	visited map[protoreflect.FullName]bool) []string {
	if visited[privateDesc.FullName()] {
		return nil
	}
	visited[privateDesc.FullName()] = true
	defer delete(visited, privateDesc.FullName())

	var paths []string
	privateFields := privateDesc.Fields()
	for i := range privateFields.Len() {
		privateField := privateFields.Get(i)
		fieldPath := prefix + string(privateField.Name())
		publicField := publicDesc.Fields().ByName(privateField.Name())
		if publicField == nil {
			paths = append(paths, fieldPath)
			continue
		}
		if isSingularMessage(privateField) && isSingularMessage(publicField) {
			paths = append(paths, findPrivateOnlyPaths(
				privateField.Message(), publicField.Message(), fieldPath+".", visited,
			)...)
		}
	}
	return paths
}

func isSingularMessage(field protoreflect.FieldDescriptor) bool {
	return field.Kind() == protoreflect.MessageKind && !field.IsMap() && !field.IsList()
}

// findPublicListMethod finds the service in osac.public.v1 whose List method's output has an 'items' field whose
// element type is publicDesc. Matching by the items element type (rather than constructing a method path from the
// message name) is required because pluralization isn't uniform — for example NetworkClass's service is
// NetworkClasses, not NetworkClasss.
func findPublicListMethod(publicDesc protoreflect.MessageDescriptor) (methodPath string,
	requestDesc, responseDesc protoreflect.MessageDescriptor, filterField protoreflect.FieldDescriptor, found bool) {
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if string(fd.Package()) != packages.PublicV1 {
			return true
		}
		services := fd.Services()
		for i := range services.Len() {
			service := services.Get(i)
			listMethod := service.Methods().ByName("List")
			if listMethod == nil {
				continue
			}
			output := listMethod.Output()
			itemsField := output.Fields().ByName("items")
			if itemsField == nil || !itemsField.IsList() || itemsField.Kind() != protoreflect.MessageKind {
				continue
			}
			if itemsField.Message().FullName() != publicDesc.FullName() {
				continue
			}
			input := listMethod.Input()
			filter := input.Fields().ByName("filter")
			if filter == nil {
				continue
			}
			methodPath = fmt.Sprintf("/%s/%s", listMethod.FullName().Parent(), listMethod.Name())
			requestDesc = input
			responseDesc = output
			filterField = filter
			found = true
			return false
		}
		return true
	})
	return
}

// newMessage creates a new, empty instance of the message type described by desc.
func newMessage(desc protoreflect.MessageDescriptor) proto.Message {
	messageType, err := protoregistry.GlobalTypes.FindMessageByName(desc.FullName())
	Expect(err).ToNot(HaveOccurred())
	return messageType.New().Interface()
}

var _ = Describe("Resource server filter oracle", func() {
	cases := discoverFilterOracleCases()
	// No silent caps: if discovery ever finds nothing, that's worth knowing about explicitly rather than the
	// suite quietly passing with zero assertions of the property it exists to check.
	It("Discovers at least one private-only field to test", func() {
		Expect(cases).ToNot(BeEmpty())
	})

	for _, testCase := range cases {
		It(fmt.Sprintf("Rejects a filter referencing the private-only field %q on %s", testCase.fieldPath, testCase.resourceName),
			func(ctx context.Context) {
				request := newMessage(testCase.requestDesc)
				filterExpr := fmt.Sprintf("this.%s != this.%s", testCase.fieldPath, testCase.fieldPath)
				request.ProtoReflect().Set(testCase.filterField, protoreflect.ValueOfString(filterExpr))
				response := newMessage(testCase.responseDesc)

				err := conn.Invoke(ctx, testCase.methodPath, request, response)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			},
		)
	}

	// Positive control, one per resource (not per discovered field): confirms that List's InvalidArgument rejection
	// above genuinely comes from the private-only field reference, not from every filter being rejected regardless
	// of content — which the rejection-only assertions above couldn't distinguish on their own. Every OSAC public
	// object is required to have an 'id' field (enforced by the OSAC_OBJECT_SHAPE buf lint rule), so this asserts
	// that precondition explicitly rather than letting a hypothetical future violation surface as a confusing
	// InvalidArgument that reads like a filter-oracle regression.
	for _, testCase := range dedupeByResource(cases) {
		It(fmt.Sprintf("Accepts a valid filter referencing only public fields on %s", testCase.resourceName),
			func(ctx context.Context) {
				itemDesc := testCase.responseDesc.Fields().ByName("items").Message()
				Expect(itemDesc.Fields().ByName("id")).ToNot(BeNil(),
					fmt.Sprintf("%s is expected to have an 'id' field per OSAC_OBJECT_SHAPE", itemDesc.FullName()))

				request := newMessage(testCase.requestDesc)
				request.ProtoReflect().Set(testCase.filterField, protoreflect.ValueOfString("this.id != this.id"))
				response := newMessage(testCase.responseDesc)

				err := conn.Invoke(ctx, testCase.methodPath, request, response)
				Expect(err).ToNot(HaveOccurred())
			},
		)
	}
})

// dedupeByResource returns one case per distinct resourceName, since every case for the same resource shares the
// same methodPath/requestDesc/responseDesc/filterField and a positive control only needs to run once per resource.
func dedupeByResource(cases []filterOracleCase) []filterOracleCase {
	seen := map[string]bool{}
	var result []filterOracleCase
	for _, c := range cases {
		if seen[c.resourceName] {
			continue
		}
		seen[c.resourceName] = true
		result = append(result, c)
	}
	return result
}

// registerWithFlags creates a gRPC server, registers resource servers with the given service flags,
// and returns the registered service names via GetServiceInfo.
func registerWithFlags(svcFlags *services.Flags) (map[string]grpc.ServiceInfo, *ResourceServers, error) {
	srv := grpc.NewServer()
	rs, err := RegisterResourceServers(ctx, srv, ResourceServerDeps{
		Logger:                  logger,
		PrivateAttributionLogic: attribution,
		PublicAttributionLogic:  attribution,
		TenancyLogic:            tenancy,
		MetricsRegisterer:       prometheus.NewRegistry(),
		HubScheme:               hubScheme,
		SecretStore:             vault.NewMockSecretStore(ctrl),
		TierResolver:            tierResolver,
		PrivateUsersServer:      nil,
		Services:                svcFlags,
	})
	if err != nil {
		return nil, nil, err
	}
	return srv.GetServiceInfo(), rs, nil
}

var _ = Describe("Conditional service registration", func() {
	// CaaS service names (both public and private)
	caasServices := []string{
		"osac.public.v1.ClusterTemplates",
		"osac.public.v1.AddOnOperators",
		"osac.public.v1.ClusterCatalogItems",
		"osac.public.v1.Clusters",
		"osac.public.v1.ClusterVersions",
		"osac.private.v1.ClusterTemplates",
		"osac.private.v1.AddOnOperators",
		"osac.private.v1.ClusterCatalogItems",
		"osac.private.v1.Clusters",
		"osac.private.v1.ClusterVersions",
	}

	// VMaaS service names
	vmaasServices := []string{
		"osac.public.v1.ComputeInstanceTemplates",
		"osac.public.v1.ComputeInstanceCatalogItems",
		"osac.public.v1.ComputeInstances",
		"osac.public.v1.DiskImages",
		"osac.public.v1.InstanceTypes",
		"osac.private.v1.ComputeInstanceTemplates",
		"osac.private.v1.ComputeInstanceCatalogItems",
		"osac.private.v1.ComputeInstances",
		"osac.private.v1.DiskImages",
		"osac.private.v1.InstanceTypes",
		"osac.private.v1.Volumes",
	}

	// BMaaS service names
	bmaasServices := []string{
		"osac.public.v1.BareMetalInstanceTemplates",
		"osac.public.v1.BareMetalInstanceCatalogItems",
		"osac.public.v1.BareMetalInstances",
		"osac.public.v1.BareMetalInstanceTypes",
		"osac.private.v1.BareMetalInstanceTemplates",
		"osac.private.v1.BareMetalInstanceCatalogItems",
		"osac.private.v1.BareMetalInstances",
		"osac.private.v1.BareMetalInstanceTypes",
	}

	// Shared infrastructure services (always registered regardless of flags)
	sharedServices := []string{
		"osac.public.v1.VirtualNetworks",
		"osac.public.v1.Subnets",
		"osac.public.v1.SecurityGroups",
		"osac.public.v1.Roles",
		"osac.public.v1.RoleBindings",
		"osac.public.v1.ServiceAccounts",
		"osac.public.v1.ProjectMemberships",
		"osac.public.v1.ExternalIPPools",
		"osac.public.v1.ExternalIPs",
		"osac.public.v1.ExternalIPAttachments",
		"osac.public.v1.NATGateways",
		"osac.public.v1.Tenants",
		"osac.public.v1.IdentityProviders",
		"osac.public.v1.Projects",
		"osac.public.v1.Users",
		"osac.public.v1.Secrets",
		"osac.public.v1.StorageTiers",
		"osac.private.v1.Hubs",
		"osac.private.v1.VirtualNetworks",
		"osac.private.v1.Subnets",
		"osac.private.v1.SecurityGroups",
		"osac.private.v1.NetworkClasses",
		"osac.private.v1.Roles",
		"osac.private.v1.RoleBindings",
		"osac.private.v1.ServiceAccounts",
		"osac.private.v1.ProjectMemberships",
		"osac.private.v1.ExternalIPPools",
		"osac.private.v1.ExternalIPs",
		"osac.private.v1.ExternalIPAttachments",
		"osac.private.v1.NATGateways",
		"osac.private.v1.Tenants",
		"osac.private.v1.IdentityProviders",
		"osac.private.v1.Projects",
		"osac.private.v1.Users",
		"osac.private.v1.StorageBackends",
		"osac.private.v1.Secrets",
		"osac.private.v1.StorageTiers",
	}

	It("registers all services when all flags are enabled", func() {
		info, _, err := registerWithFlags(&services.Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true})
		Expect(err).ToNot(HaveOccurred())

		for _, svc := range append(append(append(caasServices, vmaasServices...), bmaasServices...), sharedServices...) {
			Expect(info).To(HaveKey(svc), "expected service %s to be registered", svc)
		}
	})

	It("excludes BMaaS services when BMaaS is disabled", func() {
		info, _, err := registerWithFlags(&services.Flags{CaaS: true, VMaaS: true, BMaaS: false, MaaS: false})
		Expect(err).ToNot(HaveOccurred())

		for _, svc := range bmaasServices {
			Expect(info).ToNot(HaveKey(svc), "expected BMaaS service %s to NOT be registered", svc)
		}
		for _, svc := range caasServices {
			Expect(info).To(HaveKey(svc), "expected CaaS service %s to be registered", svc)
		}
		for _, svc := range vmaasServices {
			Expect(info).To(HaveKey(svc), "expected VMaaS service %s to be registered", svc)
		}
	})

	It("excludes CaaS and BMaaS services when only VMaaS is enabled", func() {
		info, _, err := registerWithFlags(&services.Flags{CaaS: false, VMaaS: true, BMaaS: false, MaaS: false})
		Expect(err).ToNot(HaveOccurred())

		for _, svc := range caasServices {
			Expect(info).ToNot(HaveKey(svc), "expected CaaS service %s to NOT be registered", svc)
		}
		for _, svc := range bmaasServices {
			Expect(info).ToNot(HaveKey(svc), "expected BMaaS service %s to NOT be registered", svc)
		}
		for _, svc := range vmaasServices {
			Expect(info).To(HaveKey(svc), "expected VMaaS service %s to be registered", svc)
		}
	})

	It("registers DiskImages when only BMaaS is enabled", func() {
		info, _, err := registerWithFlags(&services.Flags{CaaS: false, VMaaS: false, BMaaS: true, MaaS: false})
		Expect(err).ToNot(HaveOccurred())

		Expect(info).To(HaveKey("osac.public.v1.DiskImages"))
		Expect(info).To(HaveKey("osac.private.v1.DiskImages"))
	})

	It("always registers shared infrastructure regardless of flags", func() {
		info, _, err := registerWithFlags(&services.Flags{CaaS: false, VMaaS: false, BMaaS: false, MaaS: false})
		Expect(err).ToNot(HaveOccurred())

		for _, svc := range sharedServices {
			Expect(info).To(HaveKey(svc), "expected shared service %s to always be registered", svc)
		}
	})

	It("returns nil PrivateComputeInstancesServer when VMaaS is disabled", func() {
		_, rs, err := registerWithFlags(&services.Flags{CaaS: true, VMaaS: false, BMaaS: true, MaaS: false})
		Expect(err).ToNot(HaveOccurred())
		Expect(rs.PrivateComputeInstancesServer).To(BeNil())
	})

	It("returns non-nil PrivateComputeInstancesServer when VMaaS is enabled", func() {
		_, rs, err := registerWithFlags(&services.Flags{CaaS: true, VMaaS: true, BMaaS: false, MaaS: false})
		Expect(err).ToNot(HaveOccurred())
		Expect(rs.PrivateComputeInstancesServer).ToNot(BeNil())
	})
})
