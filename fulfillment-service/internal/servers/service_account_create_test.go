/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package servers

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	testutils "github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Service account creation", func() {
	const clientUUID = "d3c4e3db-95b8-481d-8b5c-963da477da12"
	const secondClientUUID = "a3c4e3db-95b8-481d-8b5c-963da477da12"
	const credential = "test-only-credential-marker"
	const registrationToken = "test-only-registration-token-marker"
	var public *ServiceAccountsServer
	var idpClient *idp.MockClientInterface
	var secretStore *memorySecretStore
	var request *publicv1.ServiceAccountsCreateRequest
	var create func(context.Context, *publicv1.ServiceAccountsCreateRequest) (*publicv1.ServiceAccountsCreateResponse, error)
	var caller context.Context
	BeforeEach(func() {
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		caller = auth.ContextWithSubject(context.Background(), &auth.Subject{User: "admin", Tenants: collections.NewSet(testTenant)})
		tenancyLogic, err := auth.NewDefaultTenancyLogic().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		idpClient = idp.NewMockClientInterface(ctrl)
		secretStore = newMemorySecretStore()
		public, err = NewServiceAccountsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancyLogic).SetIdpClient(idpClient).SetSecretStore(secretStore).Build()
		Expect(err).ToNot(HaveOccurred())
		txInterceptor, err := database.NewTxInterceptor().SetLogger(logger).SetManager(tm).
			SetMethodsWithoutAutomaticTransaction(publicv1.ServiceAccounts_Create_FullMethodName).Build()
		Expect(err).ToNot(HaveOccurred())
		create = func(ctx context.Context, req *publicv1.ServiceAccountsCreateRequest) (*publicv1.ServiceAccountsCreateResponse, error) {
			response, err := txInterceptor.UnaryServer(ctx, req, &grpc.UnaryServerInfo{FullMethod: publicv1.ServiceAccounts_Create_FullMethodName},
				func(ctx context.Context, req any) (any, error) {
					return public.Create(ctx, req.(*publicv1.ServiceAccountsCreateRequest))
				})
			if response == nil {
				return nil, err
			}
			return response.(*publicv1.ServiceAccountsCreateResponse), err
		}
		request = publicv1.ServiceAccountsCreateRequest_builder{Object: publicv1.ServiceAccount_builder{
			Metadata: publicv1.Metadata_builder{Name: "robot", DisplayName: "Build robot"}.Build(),
		}.Build()}.Build()
	})
	It("reserves a generated identity and returns a transient initial credential", func() {
		var assignedClientID string
		idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), gomock.Any(), testTenant).
			DoAndReturn(func(ctx context.Context, clientID, tenant string) (string, string, string, error) {
				assignedClientID = clientID
				id := strings.TrimPrefix(clientID, "osac-sa-"+testTenant+"-")
				_, err := uuid.Parse(id)
				Expect(err).ToNot(HaveOccurred())
				_, err = database.TxFromContext(ctx)
				Expect(err).To(MatchError("failed to get transaction from context"))
				_, hasDeadline := ctx.Deadline()
				Expect(hasDeadline).To(BeTrue())
				var count int
				Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts where id=$1 and tenant=$2`, id, tenant).Scan(&count)).To(Succeed())
				Expect(count).To(BeZero(), "Keycloak setup must happen before persistence")
				return clientUUID, credential, registrationToken, nil
			})
		request.GetObject().SetStatus(publicv1.ServiceAccountStatus_builder{ClientId: "forged"}.Build())
		request.GetObject().GetMetadata().SetAnnotations(map[string]string{"osac.openshift.io/tenant": "forged"})
		response, err := create(caller, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetClientSecret()).To(Equal(credential))
		Expect(response.GetObject().GetStatus().GetClientId()).To(Equal(assignedClientID))
		Expect(response.GetObject().GetMetadata().GetDisplayName()).To(Equal("Build robot"))
		Expect(response.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
		Expect(response.GetObject().GetMetadata().GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/tenant", testTenant))
		storedCredential, err := secretStore.Fetch(ctx, testTenant, "", serviceaccounts.RegistrationSecretName(response.GetObject().GetId()))
		Expect(err).ToNot(HaveOccurred())
		Expect(storedCredential[serviceaccounts.RegistrationAccessTokenKey]).To(Equal([]byte(registrationToken)))
		var data, target string
		Expect(testPool.QueryRow(ctx, `select data::text, data->'status'->>'keycloak_client_uuid' from service_accounts where id=$1`, response.GetObject().GetId()).Scan(&data, &target)).To(Succeed())
		Expect(target).To(Equal(clientUUID))
		Expect(strings.Contains(data, credential)).To(BeFalse())
		var count int
		Expect(testPool.QueryRow(ctx, `
			select count(*) from changes
			where "table"='service_accounts' and data->>'id'=$1`, response.GetObject().GetId()).Scan(&count)).To(Succeed())
		Expect(count).To(BeNumerically(">", 0))
		Expect(testPool.QueryRow(ctx, `
			select count(*) from changes
			where "table"='service_accounts' and data::text like $1`, "%"+credential+"%").Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(request.GetObject().GetStatus().GetClientId()).To(Equal("forged"), "input must remain unmodified")
	})
	It("validates a dry run without creating a row, client, or secret", func() {
		idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		dry := metadata.NewIncomingContext(caller, metadata.Pairs(DryRunMetadataKey, "true"))
		response, err := create(dry, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetClientSecret()).To(BeEmpty())
		Expect(response.GetObject().GetId()).To(BeEmpty())
		Expect(response.GetObject().GetStatus().GetClientId()).To(BeEmpty())
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})
	DescribeTable("rejects invalid ownership and grant input before IDP work", func(change func(*publicv1.ServiceAccount)) {
		change(request.GetObject())
		_, err := create(caller, request)
		Expect(status.Code(err)).To(Or(Equal(codes.InvalidArgument), Equal(codes.PermissionDenied)))
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	}, Entry("caller ID", func(o *publicv1.ServiceAccount) { o.SetId("chosen") }),
		Entry("tenant mismatch", func(o *publicv1.ServiceAccount) { o.GetMetadata().SetTenant("foreign") }),
		Entry("project", func(o *publicv1.ServiceAccount) { o.GetMetadata().SetProject("team") }),
		Entry("unknown grant", func(o *publicv1.ServiceAccount) {
			o.SetSpec(publicv1.ServiceAccountSpec_builder{Roles: []*publicv1.RoleReference{publicv1.RoleReference_builder{Id: "unknown"}.Build()}}.Build())
		}),
		Entry("bad name", func(o *publicv1.ServiceAccount) { o.GetMetadata().SetName("Invalid_name") }))
	It("rejects a duplicate name before creating another client", func() {
		idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), gomock.Any(), testTenant).Return(clientUUID, credential, registrationToken, nil)
		_, err := create(caller, request)
		Expect(err).ToNot(HaveOccurred())
		response, err := create(caller, request)
		Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		Expect(response).To(BeNil())
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
	})
	It("allows only one concurrent create for a tenant and name", func() {
		type result struct {
			response *publicv1.ServiceAccountsCreateResponse
			code     codes.Code
		}
		arrived := make(chan struct{}, 2)
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseCalls := func() { releaseOnce.Do(func() { close(release) }) }
		defer releaseCalls()
		var mu sync.Mutex
		var next int
		clientUUIDByID := make(map[string]string)
		idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), gomock.Any(), testTenant).
			DoAndReturn(func(_ context.Context, clientID, _ string) (string, string, string, error) {
				mu.Lock()
				generatedUUID := clientUUID
				if next == 1 {
					generatedUUID = secondClientUUID
				}
				next++
				clientUUIDByID[clientID] = generatedUUID
				mu.Unlock()
				arrived <- struct{}{}
				<-release
				return generatedUUID, credential, registrationToken, nil
			}).Times(2)
		var cleanedClientID string
		idpClient.EXPECT().DeleteServiceAccountClient(gomock.Any(), gomock.Any(), gomock.Any(), registrationToken).DoAndReturn(
			func(_ context.Context, id, _, _ string) error { cleanedClientID = id; return nil }).Times(1)
		start := make(chan struct{})
		results := make(chan result, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Go(func() {
				defer GinkgoRecover()
				<-start
				response, err := create(caller, proto.Clone(request).(*publicv1.ServiceAccountsCreateRequest))
				results <- result{response: response, code: status.Code(err)}
			})
		}
		close(start)
		Eventually(arrived).Should(Receive())
		Eventually(arrived).Should(Receive())
		releaseCalls()
		workers.Wait()
		first, second := <-results, <-results
		Expect([]codes.Code{first.code, second.code}).To(ConsistOf(codes.OK, codes.AlreadyExists))
		created := first
		if created.code != codes.OK {
			created = second
		}
		createdClientID := created.response.GetObject().GetStatus().GetClientId()
		mu.Lock()
		var expectedCleanupClientID string
		for id := range clientUUIDByID {
			if id != createdClientID {
				expectedCleanupClientID = id
			}
		}
		mu.Unlock()
		Expect(cleanedClientID).To(Equal(expectedCleanupClientID))
	})
	It("compensates a client if persistence fails after successful setup", func() {
		_, err := testPool.Exec(ctx, `create function reject_service_account_insert() returns trigger language plpgsql as $$
		begin raise exception 'test insert failure'; end $$;
		create trigger reject_service_account_insert before insert on service_accounts
		for each row execute function reject_service_account_insert()`)
		Expect(err).ToNot(HaveOccurred())
		gomock.InOrder(
			idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), gomock.Any(), testTenant).Return(clientUUID, credential, registrationToken, nil),
			idpClient.EXPECT().DeleteServiceAccountClient(gomock.Any(), gomock.Any(), gomock.Any(), registrationToken).Return(nil),
		)
		response, err := create(caller, request)
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(response).To(BeNil())
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})
	It("deletes the stored credential even when client compensation fails", func() {
		_, err := testPool.Exec(ctx, `create function reject_service_account_insert() returns trigger language plpgsql as $$
		begin raise exception 'test insert failure'; end $$;
		create trigger reject_service_account_insert before insert on service_accounts
		for each row execute function reject_service_account_insert()`)
		Expect(err).ToNot(HaveOccurred())
		idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), gomock.Any(), testTenant).
			Return(clientUUID, credential, registrationToken, nil)
		idpClient.EXPECT().DeleteServiceAccountClient(gomock.Any(), gomock.Any(), gomock.Any(), registrationToken).
			DoAndReturn(func(context.Context, string, string, string) error {
				secretStore.mu.Lock()
				storedSecrets := len(secretStore.data)
				secretStore.mu.Unlock()
				Expect(storedSecrets).To(Equal(1), "the credential must be stored before client compensation")
				return errors.New("Keycloak cleanup failed")
			})

		response, err := create(caller, request)
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(response).To(BeNil())
		secretStore.mu.Lock()
		storedSecrets := len(secretStore.data)
		secretStore.mu.Unlock()
		Expect(storedSecrets).To(BeZero(), "Vault cleanup should run despite the Keycloak deletion failure")
	})
	It("returns INTERNAL without a credential when commit fails after successful setup", func() {
		_, err := testPool.Exec(ctx, `create function reject_service_account_commit() returns trigger language plpgsql as $$
		begin raise exception 'test commit failure'; end $$;
		create constraint trigger reject_service_account_commit after insert on service_accounts
		deferrable initially deferred for each row execute function reject_service_account_commit()`)
		Expect(err).ToNot(HaveOccurred())
		var buffer bytes.Buffer
		capturedLogger := slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
		tenancyLogic, err := auth.NewDefaultTenancyLogic().SetLogger(capturedLogger).Build()
		Expect(err).ToNot(HaveOccurred())
		api, err := NewServiceAccountsServer().SetLogger(capturedLogger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancyLogic).SetIdpClient(idpClient).SetSecretStore(secretStore).Build()
		Expect(err).ToNot(HaveOccurred())
		logs, err := logging.NewInterceptor().SetLogger(capturedLogger).SetBodies(true).Build()
		Expect(err).ToNot(HaveOccurred())
		transactions, err := database.NewTxInterceptor().SetLogger(capturedLogger).SetManager(tm).Build()
		Expect(err).ToNot(HaveOccurred())
		server := testutils.NewServer(grpc.ChainUnaryInterceptor(logs.UnaryServer, transactions.UnaryServer,
			func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				return handler(auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: collections.NewSet(testTenant)}), req)
			}))
		publicv1.RegisterServiceAccountsServer(server.Registrar(), api)
		server.Start()
		DeferCleanup(server.Stop)
		connection, err := grpc.NewClient(server.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)
		var clientID string
		gomock.InOrder(
			idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), gomock.Any(), testTenant).
				DoAndReturn(func(_ context.Context, id, _ string) (string, string, string, error) {
					clientID = id
					return clientUUID, credential, registrationToken, nil
				}),
			idpClient.EXPECT().DeleteServiceAccountClient(gomock.Any(), gomock.Any(), gomock.Any(), registrationToken).Return(nil),
		)
		response, err := publicv1.NewServiceAccountsClient(connection).Create(caller, request)
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(response).To(BeNil())
		Expect(buffer.String()).ToNot(ContainSubstring(credential))
		Expect(buffer.String()).To(ContainSubstring(clientID))
		Expect(buffer.String()).To(ContainSubstring("Failed to create service account"))
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("uses detached bounded compensation after request cancellation", func() {
		cancelled, cancel := context.WithCancel(caller)
		defer cancel()
		idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), gomock.Any(), testTenant).
			DoAndReturn(func(context.Context, string, string) (string, string, string, error) {
				cancel()
				return clientUUID, "", registrationToken, context.Canceled
			})
		gomock.InOrder(
			idpClient.EXPECT().DeleteServiceAccountClient(gomock.Any(), gomock.Any(), gomock.Any(), registrationToken).DoAndReturn(func(cleanup context.Context, _, _, _ string) error {
				Expect(cleanup.Err()).ToNot(HaveOccurred())
				_, bounded := cleanup.Deadline()
				Expect(bounded).To(BeTrue())
				return nil
			}),
		)
		response, err := create(cancelled, request)
		Expect(err).To(HaveOccurred())
		Expect(response).To(BeNil())
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})

	DescribeTable("rolls back setup failures and compensates only known created clients", func(partialUUID string, cleanupFails bool) {
		partialToken := ""
		if partialUUID != "" {
			partialToken = registrationToken
		}
		idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), gomock.Any(), testTenant).Return(partialUUID, "", partialToken, errors.New("upstream "+credential))
		if partialUUID != "" {
			var cleanupError error
			if cleanupFails {
				cleanupError = errors.New("cleanup " + credential)
			}
			gomock.InOrder(
				idpClient.EXPECT().DeleteServiceAccountClient(gomock.Any(), gomock.Any(), gomock.Any(), registrationToken).DoAndReturn(func(ctx context.Context, _, _, _ string) error {
					Expect(ctx.Err()).ToNot(HaveOccurred())
					_, bounded := ctx.Deadline()
					Expect(bounded).To(BeTrue())
					return cleanupError
				}),
			)
		}
		response, err := create(caller, request)
		Expect(status.Code(err)).To(Equal(codes.Unavailable))
		Expect(response).To(BeNil())
		Expect(strings.Contains(err.Error(), credential)).To(BeFalse())
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	}, Entry("no created client", "", false), Entry("partial setup", clientUUID, false), Entry("failed cleanup", clientUUID, true))
})
