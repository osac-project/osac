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
	"fmt"
	"log/slog"
	"sort"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	hubscheme "github.com/osac-project/osac/fulfillment-service/internal/kubernetes/scheme"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/packages"
	"github.com/osac-project/osac/fulfillment-service/internal/recovery"
	"github.com/osac-project/osac/fulfillment-service/internal/servers"
	itesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
)

func TestRegisterServers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "gRPC server registration package")
}

var (
	ctx         context.Context
	ctrl        *gomock.Controller
	logger      *slog.Logger
	dbContainer *database.Container
	attribution *auth.MockAttributionLogic
	tenancy     *auth.MockTenancyLogic
	conn        *grpc.ClientConn
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
	tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
		Return(auth.AllTenants, nil).
		AnyTimes()

	// Start the containerized database:
	startupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dbContainer, err = database.NewContainer().
		SetLogger(logger).
		Build()
	Expect(err).ToNot(HaveOccurred())
	err = dbContainer.Start(startupCtx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx context.Context) {
		err := dbContainer.Stop(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

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
	txInterceptor, err := database.NewTxInterceptor().
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

	// Create the notifier. ExternalIPPoolsServerBuilder, ProjectsServerBuilder, and PrivateProjectsServerBuilder
	// require a concrete *database.Notifier (see register_servers.go), so this can't be nil or a different
	// events.Notifier implementation.
	notifier, err := database.NewNotifier().
		SetLogger(logger).
		SetChannel("events").
		SetPool(pool).
		Build()
	Expect(err).ToNot(HaveOccurred())
	err = notifier.Start(ctx)
	Expect(err).ToNot(HaveOccurred())

	hubScheme, err := hubscheme.NewHub()
	Expect(err).ToNot(HaveOccurred())
	metricsRegisterer := prometheus.NewRegistry()

	// Create the storage tiers DAO and tier resolver, exactly as production wires them in
	// start_grpc_server_cmd.go's run(), for the private volumes server's mandatory SetTierResolver dependency.
	storageTiersDAO, err := dao.NewGenericDAO[*privatev1.StorageTier]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	tierResolver := newDAOTierResolver(storageTiersDAO)

	// Create the private users server, exactly as production does in start_grpc_server_cmd.go's run(). It's
	// constructed outside RegisterResourceServers there because the JIT provisioning interceptor needs it before
	// the interceptor chain is built — see ResourceServerDeps.PrivateUsersServer's doc comment.
	privateUsersServer, err := servers.NewPrivateUsersServer().
		SetLogger(logger).
		SetNotifier(notifier).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
	Expect(err).ToNot(HaveOccurred())

	// Start a real gRPC server, with the same transaction interceptor production uses, and register every
	// filterable resource through the exact same function production calls:
	server := itesting.NewServer(grpc.ChainUnaryInterceptor(
		panicInterceptor.UnaryServer,
		txInterceptor.UnaryServer,
	))
	DeferCleanup(server.Stop)
	_, err = RegisterResourceServers(ctx, server.Registrar(), ResourceServerDeps{
		Logger:                  logger,
		Notifier:                notifier,
		PrivateAttributionLogic: attribution,
		PublicAttributionLogic:  attribution,
		TenancyLogic:            tenancy,
		MetricsRegisterer:       metricsRegisterer,
		HubScheme:               hubScheme,
		TierResolver:            tierResolver,
		PrivateUsersServer:      privateUsersServer,
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
var resourcesExcludedFromDiscovery = map[string]bool{
	"User": true,
}

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
