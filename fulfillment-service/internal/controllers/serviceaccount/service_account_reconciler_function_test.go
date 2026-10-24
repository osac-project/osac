/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package serviceaccount

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	testutils "github.com/osac-project/osac/fulfillment-service/internal/testing"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestServiceAccount(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Service account cleanup")
}

type updateServer struct {
	privatev1.UnimplementedServiceAccountsServer
	requests chan *privatev1.ServiceAccountsUpdateRequest
	err      error
}

func (s *updateServer) Update(_ context.Context, request *privatev1.ServiceAccountsUpdateRequest) (*privatev1.ServiceAccountsUpdateResponse, error) {
	s.requests <- request
	return privatev1.ServiceAccountsUpdateResponse_builder{Object: request.GetObject()}.Build(), s.err
}

var _ = Describe("Service-account deletion", func() {
	const accountID = "66ab2c29-e08c-43b3-8519-172b55630ada"
	const clientUUID = "da845ff3-9fdb-44ad-b421-4761bcfe4177"
	const registrationToken = "registration-token"
	var account *privatev1.ServiceAccount
	var client *idp.MockClientInterface
	var secretStore *vault.MockSecretStore
	var api *updateServer
	var run func(context.Context, *privatev1.ServiceAccount) error
	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		client = idp.NewMockClientInterface(ctrl)
		secretStore = vault.NewMockSecretStore(ctrl)
		api = &updateServer{requests: make(chan *privatev1.ServiceAccountsUpdateRequest, 4)}
		server := testutils.NewServer()
		privatev1.RegisterServiceAccountsServer(server.Registrar(), api)
		server.Start()
		DeferCleanup(server.Stop)
		connection, err := grpc.NewClient(server.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)
		fn, err := NewFunction().SetLogger(slog.New(slog.NewTextHandler(GinkgoWriter, nil))).SetConnection(connection).
			SetIdpClient(client).SetSecretStore(secretStore).Build()
		Expect(err).ToNot(HaveOccurred())
		run = fn.Run
		// Constructing the generic reconciler also verifies private List/Get and event discovery.
		_, err = controllers.NewReconciler[*privatev1.ServiceAccount]().SetLogger(slog.Default()).SetName("service-account").SetClient(connection).SetFunction(run).SetEventFilter("has(event.service_account)").Build()
		Expect(err).ToNot(HaveOccurred())
		account = privatev1.ServiceAccount_builder{Id: accountID,
			Metadata: privatev1.Metadata_builder{Name: "robot", Tenant: "tenant-a", Version: 7, DeletionTimestamp: timestamppb.Now(), Finalizers: []string{finalizers.Controller, "another-controller"}}.Build(),
			Status:   privatev1.ServiceAccountStatus_builder{ClientId: "osac-sa-tenant-a-" + accountID, KeycloakClientUuid: clientUUID}.Build(),
		}.Build()
	})
	It("ignores live accounts", func() {
		account.GetMetadata().ClearDeletionTimestamp()
		Expect(run(context.Background(), account)).To(Succeed())
		Expect(api.requests).To(BeEmpty())
	})
	It("ignores accounts without its finalizer", func() {
		account.GetMetadata().SetFinalizers([]string{"another-controller"})
		Expect(run(context.Background(), account)).To(Succeed())
		Expect(api.requests).To(BeEmpty())
	})
	It("deletes the named client with its scoped credential before a locked finalizer update", func() {
		original := proto.Clone(account)
		gomock.InOrder(
			secretStore.EXPECT().Fetch(gomock.Any(), "tenant-a", "", serviceaccounts.RegistrationSecretName(accountID)).Return(map[string][]byte{
				serviceaccounts.RegistrationAccessTokenKey: []byte(registrationToken),
			}, nil),
			client.EXPECT().DeleteServiceAccountClient(gomock.Any(), "osac-sa-tenant-a-"+accountID, clientUUID, registrationToken).
				DoAndReturn(func(ctx context.Context, _, _, _ string) error {
					deadline, ok := ctx.Deadline()
					Expect(ok).To(BeTrue())
					remaining := time.Until(deadline)
					Expect(remaining).To(BeNumerically(">", 0))
					Expect(remaining).To(BeNumerically("<=", serviceAccountCleanupTimeout))
					return nil
				}),
			secretStore.EXPECT().Delete(gomock.Any(), "tenant-a", "", serviceaccounts.RegistrationSecretName(accountID)).
				DoAndReturn(func(context.Context, string, string, string) error {
					Expect(api.requests).To(BeEmpty())
					return nil
				}),
		)
		Expect(run(context.Background(), account)).To(Succeed())
		update := <-api.requests
		Expect(update.GetLock()).To(BeTrue())
		Expect(update.GetObject().GetMetadata().GetVersion()).To(Equal(int32(7)))
		Expect(update.GetUpdateMask().GetPaths()).To(Equal([]string{"metadata.finalizers"}))
		Expect(update.GetObject().GetMetadata().GetFinalizers()).To(Equal([]string{"another-controller"}))
		Expect(proto.Equal(account, original)).To(BeTrue())
	})
	DescribeTable("preserves finalizers on cleanup failure", func(stage string, failure error) {
		switch stage {
		case "fetch":
			secretStore.EXPECT().Fetch(gomock.Any(), "tenant-a", "", serviceaccounts.RegistrationSecretName(accountID)).Return(nil, failure)
		case "missing token":
			secretStore.EXPECT().Fetch(gomock.Any(), "tenant-a", "", serviceaccounts.RegistrationSecretName(accountID)).Return(map[string][]byte{}, nil)
		case "Keycloak delete":
			secretStore.EXPECT().Fetch(gomock.Any(), "tenant-a", "", serviceaccounts.RegistrationSecretName(accountID)).Return(map[string][]byte{
				serviceaccounts.RegistrationAccessTokenKey: []byte(registrationToken),
			}, nil)
			client.EXPECT().DeleteServiceAccountClient(gomock.Any(), "osac-sa-tenant-a-"+accountID, clientUUID, registrationToken).Return(failure)
		case "Vault delete":
			secretStore.EXPECT().Fetch(gomock.Any(), "tenant-a", "", serviceaccounts.RegistrationSecretName(accountID)).Return(map[string][]byte{
				serviceaccounts.RegistrationAccessTokenKey: []byte(registrationToken),
			}, nil)
			client.EXPECT().DeleteServiceAccountClient(gomock.Any(), "osac-sa-tenant-a-"+accountID, clientUUID, registrationToken).Return(nil)
			secretStore.EXPECT().Delete(gomock.Any(), "tenant-a", "", serviceaccounts.RegistrationSecretName(accountID)).Return(failure)
		}
		err := run(context.Background(), account)
		Expect(err).To(HaveOccurred())
		if stage == "Keycloak delete" || stage == "Vault delete" {
			Expect(controllers.IsRequeueable(err)).To(BeTrue())
		}
		Expect(api.requests).To(BeEmpty())
		Expect(account.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	}, Entry("fetch error", "fetch", errors.New("unavailable")),
		Entry("missing registration token", "missing token", errors.New("unused")),
		Entry("Keycloak delete error", "Keycloak delete", errors.New("unavailable")),
		Entry("Vault delete error", "Vault delete", errors.New("unavailable")))
	It("retains the finalizer if the Vault secret is missing while Keycloak remains", func() {
		secretStore.EXPECT().Fetch(gomock.Any(), "tenant-a", "", serviceaccounts.RegistrationSecretName(accountID)).Return(nil,
			status.Error(codes.NotFound, "secret not found"))
		client.EXPECT().DeleteServiceAccountClient(gomock.Any(), "osac-sa-tenant-a-"+accountID, clientUUID, "").
			Return(errors.New("service-account client exists without a registration token"))

		err := run(context.Background(), account)
		Expect(err).To(HaveOccurred())
		Expect(api.requests).To(BeEmpty())
		Expect(account.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})
	DescribeTable("rejects corrupt identity without targeting another client", func(change func(*privatev1.ServiceAccount)) {
		change(account)
		Expect(run(context.Background(), account)).To(HaveOccurred())
		Expect(api.requests).To(BeEmpty())
	}, Entry("missing UUID", func(a *privatev1.ServiceAccount) { a.GetStatus().SetKeycloakClientUuid("") }),
		Entry("invalid UUID", func(a *privatev1.ServiceAccount) { a.GetStatus().SetKeycloakClientUuid("other/client") }),
		Entry("mismatched client ID", func(a *privatev1.ServiceAccount) { a.GetStatus().SetClientId("osac-sa-other-" + accountID) }),
		Entry("invalid account ID", func(a *privatev1.ServiceAccount) { a.SetId("invalid") }),
		Entry("empty tenant", func(a *privatev1.ServiceAccount) { a.GetMetadata().SetTenant("") }))
	It("retries finalizer removal after Vault cleanup succeeded but the update conflicted", func() {
		gomock.InOrder(
			secretStore.EXPECT().Fetch(gomock.Any(), "tenant-a", "", serviceaccounts.RegistrationSecretName(accountID)).Return(map[string][]byte{
				serviceaccounts.RegistrationAccessTokenKey: []byte(registrationToken),
			}, nil),
			client.EXPECT().DeleteServiceAccountClient(gomock.Any(), "osac-sa-tenant-a-"+accountID, clientUUID, registrationToken).Return(nil),
			secretStore.EXPECT().Delete(gomock.Any(), "tenant-a", "", serviceaccounts.RegistrationSecretName(accountID)).Return(nil),
			secretStore.EXPECT().Fetch(gomock.Any(), "tenant-a", "", serviceaccounts.RegistrationSecretName(accountID)).Return(nil,
				status.Error(codes.NotFound, "secret not found")),
			client.EXPECT().DeleteServiceAccountClient(gomock.Any(), "osac-sa-tenant-a-"+accountID, clientUUID, "").Return(nil),
		)
		api.err = status.Error(codes.Aborted, "stale version")
		Expect(status.Code(run(context.Background(), account))).To(Equal(codes.Aborted))
		<-api.requests
		Expect(account.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))

		api.err = nil
		account.GetMetadata().SetVersion(8)
		Expect(run(context.Background(), account)).To(Succeed())
		update := <-api.requests
		Expect(update.GetObject().GetMetadata().GetVersion()).To(Equal(int32(8)))
		Expect(update.GetObject().GetMetadata().GetFinalizers()).To(Equal([]string{"another-controller"}))
	})
})
