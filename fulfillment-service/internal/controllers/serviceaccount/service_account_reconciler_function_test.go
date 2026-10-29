//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package serviceaccount

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	testutils "github.com/osac-project/osac/fulfillment-service/internal/testing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestServiceAccount(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Service account reconciler")
}

type updateServer struct {
	privatev1.UnimplementedServiceAccountsServer
	requests chan *privatev1.ServiceAccountsUpdateRequest
	err      error
	version  int32
	started  bool
}

func (s *updateServer) Update(_ context.Context, request *privatev1.ServiceAccountsUpdateRequest) (*privatev1.ServiceAccountsUpdateResponse, error) {
	clonedRequest := proto.Clone(request).(*privatev1.ServiceAccountsUpdateRequest)
	s.requests <- clonedRequest
	if s.err != nil {
		return nil, s.err
	}
	requestedVersion := request.GetObject().GetMetadata().GetVersion()
	if !s.started {
		s.version = requestedVersion
		s.started = true
	}
	if request.GetLock() && requestedVersion != s.version {
		return nil, status.Errorf(codes.Aborted, "requested version is %d but current version is %d", requestedVersion, s.version)
	}
	s.version++
	updated := proto.Clone(request.GetObject()).(*privatev1.ServiceAccount)
	updated.GetMetadata().SetVersion(s.version)
	return privatev1.ServiceAccountsUpdateResponse_builder{Object: updated}.Build(), nil
}

type secretsServer struct {
	privatev1.UnimplementedSecretsServer
	items     []*privatev1.Secret
	creates   []*privatev1.SecretsCreateRequest
	createErr error
	updateErr error
	deleteErr error
	nextID    int
}

func (s *secretsServer) Create(_ context.Context, request *privatev1.SecretsCreateRequest) (*privatev1.SecretsCreateResponse, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	for _, item := range s.items {
		if item.GetMetadata().GetTenant() == request.GetObject().GetMetadata().GetTenant() &&
			item.GetMetadata().GetName() == request.GetObject().GetMetadata().GetName() {
			return nil, status.Error(codes.AlreadyExists, "secret already exists")
		}
	}
	s.creates = append(s.creates, proto.Clone(request).(*privatev1.SecretsCreateRequest))
	s.nextID++
	secret := proto.Clone(request.GetObject()).(*privatev1.Secret)
	secret.SetId(fmt.Sprintf("secret-%d", s.nextID))
	secret.SetBackend(privatev1.SecretBackend_SECRET_BACKEND_VAULT)
	s.items = append(s.items, secret)
	return privatev1.SecretsCreateResponse_builder{Object: proto.Clone(secret).(*privatev1.Secret)}.Build(), nil
}

func (s *secretsServer) List(_ context.Context, request *privatev1.SecretsListRequest) (*privatev1.SecretsListResponse, error) {
	filter := request.GetFilter()
	name, tenant := secretListFilterValues(&filter)
	items := make([]*privatev1.Secret, 0, len(s.items))
	for _, item := range s.items {
		if name != "" && item.GetMetadata().GetName() != name {
			continue
		}
		if tenant != "" && item.GetMetadata().GetTenant() != tenant {
			continue
		}
		items = append(items, proto.Clone(item).(*privatev1.Secret))
	}
	total := int32(len(items))
	if request.HasLimit() && int(request.GetLimit()) < len(items) {
		items = items[:request.GetLimit()]
	}
	return privatev1.SecretsListResponse_builder{Items: items, Total: total, Size: int32(len(items))}.Build(), nil
}

func secretListFilterValues(filter *string) (name, tenant string) {
	if filter == nil {
		return
	}
	for _, part := range strings.Split(*filter, " && ") {
		for key, value := range map[string]*string{"name": &name, "tenant": &tenant} {
			prefix := "this.metadata." + key + " == "
			if strings.HasPrefix(part, prefix) {
				*value, _ = strconv.Unquote(strings.TrimPrefix(part, prefix))
			}
		}
	}
	return
}

func (s *secretsServer) Get(_ context.Context, request *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
	for _, secret := range s.items {
		if secret.GetId() == request.GetId() {
			return privatev1.SecretsGetResponse_builder{Object: proto.Clone(secret).(*privatev1.Secret)}.Build(), nil
		}
	}
	return nil, status.Error(codes.NotFound, "secret not found")
}

func (s *secretsServer) Update(_ context.Context, request *privatev1.SecretsUpdateRequest) (*privatev1.SecretsUpdateResponse, error) {
	if s.updateErr != nil {
		err := s.updateErr
		s.updateErr = nil
		return nil, err
	}
	for _, secret := range s.items {
		if secret.GetId() == request.GetObject().GetId() {
			secret.SetData(cloneSecretData(request.GetObject().GetData()))
			return privatev1.SecretsUpdateResponse_builder{Object: proto.Clone(secret).(*privatev1.Secret)}.Build(), nil
		}
	}
	return nil, status.Error(codes.NotFound, "secret not found")
}

func (s *secretsServer) Delete(_ context.Context, request *privatev1.SecretsDeleteRequest) (*privatev1.SecretsDeleteResponse, error) {
	if s.deleteErr != nil {
		return nil, s.deleteErr
	}
	for index, secret := range s.items {
		if secret.GetId() == request.GetId() {
			s.items = append(s.items[:index], s.items[index+1:]...)
			return &privatev1.SecretsDeleteResponse{}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "secret not found")
}

var _ = Describe("Service-account reconciler", func() {
	const (
		accountID         = "66ab2c29-e08c-43b3-8519-172b55630ada"
		clientUUID        = "da845ff3-9fdb-44ad-b421-4761bcfe4177"
		registrationToken = "registration-token"
		clientID          = "osac-sa-tenant-a-66ab2c29-e08c-43b3-8519-172b55630ada"
	)
	var (
		account   *privatev1.ServiceAccount
		idpClient *idp.MockClientInterface
		api       *updateServer
		secretAPI *secretsServer
		run       func(context.Context, *privatev1.ServiceAccount) error
	)
	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		idpClient = idp.NewMockClientInterface(ctrl)
		api = &updateServer{requests: make(chan *privatev1.ServiceAccountsUpdateRequest, 20)}
		secretAPI = &secretsServer{}
		server := testutils.NewServer()
		privatev1.RegisterServiceAccountsServer(server.Registrar(), api)
		privatev1.RegisterSecretsServer(server.Registrar(), secretAPI)
		server.Start()
		DeferCleanup(server.Stop)
		connection, err := grpc.NewClient(server.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)
		fn, err := NewFunction().SetLogger(slog.New(slog.NewTextHandler(GinkgoWriter, nil))).
			SetConnection(connection).SetIdpClient(idpClient).Build()
		Expect(err).ToNot(HaveOccurred())
		run = fn.Run
		_, err = controllers.NewReconciler[*privatev1.ServiceAccount]().SetLogger(slog.Default()).
			SetName("service-account").SetClient(connection).SetFunction(run).
			SetEventFilter("has(event.service_account)").Build()
		Expect(err).ToNot(HaveOccurred())
		account = privatev1.ServiceAccount_builder{
			Id: accountID,
			Metadata: privatev1.Metadata_builder{
				Name: "robot", Tenant: "tenant-a", Version: 7,
				DeletionTimestamp: timestamppb.Now(),
				Finalizers:        []string{finalizers.Controller, "another-controller"},
			}.Build(),
			Status: privatev1.ServiceAccountStatus_builder{
				KeycloakClientUuid: clientUUID,
				State:              privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY,
			}.Build(),
		}.Build()
	})

	seedSecret := func(name, tenant, id string, data map[string][]byte) *privatev1.Secret {
		secret := privatev1.Secret_builder{
			Id:       id,
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_OPAQUE,
			Backend:  privatev1.SecretBackend_SECRET_BACKEND_VAULT,
			Data:     cloneSecretData(data),
		}.Build()
		secretAPI.items = append(secretAPI.items, secret)
		return secret
	}
	seedRegistrationSecret := func() {
		secret := seedSecret("robot", auth.SystemTenant, "registration-secret-id", map[string][]byte{
			serviceaccounts.RegistrationServiceAccountIDKey: []byte(accountID),
			serviceaccounts.RegistrationAccessTokenKey:      []byte(registrationToken),
			serviceaccounts.RegistrationClientUUIDKey:       []byte(clientUUID),
		})
		account.GetStatus().SetRegistrationSecret(&privatev1.SecretLocalReference{Id: secret.GetId(), Name: secret.GetMetadata().GetName()})
	}
	seedClientSecret := func() {
		secret := seedSecret(serviceaccounts.ClientSecretName(accountID), "tenant-a", "client-secret-id", map[string][]byte{
			clientIDDataKey: []byte(clientID), clientSecretDataKey: []byte("client-secret"),
		})
		secret.SetType(privatev1.SecretType_SECRET_TYPE_SERVICE_ACCOUNT_CREDENTIAL)
		secret.GetMetadata().SetAnnotations(map[string]string{
			tenantAnnotation: "tenant-a", ownerReferenceAnnotation: accountID,
		})
		account.GetStatus().SetClientSecret(&privatev1.SecretLocalReference{Id: secret.GetId(), Name: secret.GetMetadata().GetName()})
	}
	updates := func() []*privatev1.ServiceAccountsUpdateRequest {
		result := make([]*privatev1.ServiceAccountsUpdateRequest, 0, len(api.requests))
		for len(api.requests) > 0 {
			result = append(result, <-api.requests)
		}
		return result
	}

	It("ignores live accounts and accounts without its finalizer", func() {
		account.GetMetadata().ClearDeletionTimestamp()
		seedClientSecret()
		Expect(run(context.Background(), account)).To(Succeed())
		account.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		account.GetMetadata().SetFinalizers([]string{"another-controller"})
		Expect(run(context.Background(), account)).To(Succeed())
		Expect(api.requests).To(BeEmpty())
	})

	It("provisions the client and makes it ready after creating a Secret with both credentials", func() {
		account.GetMetadata().ClearDeletionTimestamp()
		account.GetStatus().SetKeycloakClientUuid("")
		account.GetStatus().SetState(privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_PENDING)
		idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), clientID, "tenant-a").
			Return(clientUUID, "client-secret", registrationToken, nil)

		Expect(run(context.Background(), account)).To(Succeed())

		var clientSecret *privatev1.Secret
		var registrationSecret *privatev1.Secret
		for _, secret := range secretAPI.items {
			switch secret.GetMetadata().GetTenant() {
			case "tenant-a":
				clientSecret = secret
			case auth.SystemTenant:
				registrationSecret = secret
			}
		}
		Expect(clientSecret).ToNot(BeNil())
		Expect(clientSecret.GetData()).To(Equal(map[string][]byte{
			clientIDDataKey: []byte(clientID), clientSecretDataKey: []byte("client-secret"),
		}))
		Expect(clientSecret.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(tenantAnnotation, "tenant-a"))
		Expect(clientSecret.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(ownerReferenceAnnotation, accountID))
		Expect(registrationSecret).ToNot(BeNil())
		Expect(registrationSecret.GetMetadata().GetName()).To(Equal("robot"))
		Expect(registrationSecret.GetMetadata().GetAnnotations()).To(BeEmpty())
		Expect(registrationSecret.GetData()[serviceaccounts.RegistrationServiceAccountIDKey]).To(Equal([]byte(accountID)))
		Expect(secretAPI.creates).To(HaveLen(2))
		Expect(secretAPI.creates[0].GetObject().GetBackend()).To(Equal(privatev1.SecretBackend_SECRET_BACKEND_UNSPECIFIED))

		var readyUpdate *privatev1.ServiceAccountsUpdateRequest
		for _, update := range updates() {
			if update.GetObject().GetStatus().GetState() == privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY &&
				slices.Contains(update.GetUpdateMask().GetPaths(), "status.keycloak_client_uuid") {
				readyUpdate = update
			}
		}
		Expect(readyUpdate).ToNot(BeNil())
		Expect(readyUpdate.GetObject().GetStatus().GetState()).To(Equal(privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY))
		Expect(readyUpdate.GetUpdateMask().GetPaths()).To(ContainElements(
			"status.keycloak_client_uuid", "status.client_secret", "status.state", "status.message"))
	})
	It("finishes provisioning from the persisted client UUID and Secret reference", func() {
		account.GetMetadata().ClearDeletionTimestamp()
		account.GetStatus().SetKeycloakClientUuid(clientUUID)
		account.GetStatus().SetState(privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_PENDING)
		seedClientSecret()
		seedRegistrationSecret()

		Expect(run(context.Background(), account)).To(Succeed())
		Expect(account.GetStatus().GetState()).To(Equal(privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY))
		Expect(account.GetStatus().GetClientSecret().GetId()).To(Equal("client-secret-id"))
		request := <-api.requests
		Expect(request.GetUpdateMask().GetPaths()).To(Equal([]string{"status.state", "status.message"}))
	})

	It("retries secret rotation after its staged credential write fails", func() {
		account.GetMetadata().ClearDeletionTimestamp()
		account.GetStatus().SetSecretRotationState(
			privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_PENDING)
		account.GetStatus().SetSecretRotationGeneration("c8dded6a-2881-4d25-9f1a-d964fc62272c")
		seedClientSecret()
		seedRegistrationSecret()
		secretAPI.updateErr = status.Error(codes.Unavailable, "temporary Secret-store failure")
		idpClient.EXPECT().RotateServiceAccountClientSecret(gomock.Any(), clientID, clientUUID, registrationToken).
			Return("discarded-secret", registrationToken, nil)
		idpClient.EXPECT().RotateServiceAccountClientSecret(gomock.Any(), clientID, clientUUID, registrationToken).
			Return("published-secret", registrationToken, nil)

		err := run(context.Background(), account)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(account.GetStatus().GetSecretRotationState()).To(Equal(
			privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_ERROR))

		Expect(run(context.Background(), account)).To(Succeed())
		Expect(account.GetStatus().GetSecretRotationState()).To(Equal(
			privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_COMPLETED))
		Expect(secretAPI.items).To(HaveLen(2))
		Expect(secretAPI.items[0].GetData()[clientSecretDataKey]).To(Equal([]byte("published-secret")))
		Expect(secretAPI.items[1].GetData()[serviceaccounts.RegistrationAccessTokenKey]).To(Equal([]byte(registrationToken)))
		Expect(secretAPI.items[1].GetData()[serviceaccounts.RegistrationClientUUIDKey]).To(Equal([]byte(clientUUID)))
	})

	It("preserves the Keycloak client UUID when clearing provisioning-only credentials", func() {
		account.GetMetadata().ClearDeletionTimestamp()
		seedClientSecret()
		registration := seedSecret("robot", auth.SystemTenant, "registration-secret-id", map[string][]byte{
			serviceaccounts.RegistrationServiceAccountIDKey: []byte(accountID),
			serviceaccounts.RegistrationAccessTokenKey:      []byte(registrationToken),
			serviceaccounts.RegistrationClientUUIDKey:       []byte(clientUUID),
			serviceaccounts.PendingClientSecretKey:          []byte("pending-secret"),
			serviceaccounts.PendingClientSecretRefIDKey:     []byte("pending-reference"),
			serviceaccounts.PendingClientSecretRefNameKey:   []byte("pending-name"),
		})
		account.GetStatus().SetRegistrationSecret(&privatev1.SecretLocalReference{
			Id: registration.GetId(), Name: registration.GetMetadata().GetName(),
		})

		Expect(run(context.Background(), account)).To(Succeed())
		Expect(registration.GetData()).To(Equal(map[string][]byte{
			serviceaccounts.RegistrationServiceAccountIDKey: []byte(accountID),
			serviceaccounts.RegistrationAccessTokenKey:      []byte(registrationToken),
			serviceaccounts.RegistrationClientUUIDKey:       []byte(clientUUID),
		}))
	})

	It("uses a numeric suffix when the service-account name is already taken", func() {
		account.GetMetadata().ClearDeletionTimestamp()
		account.GetStatus().SetKeycloakClientUuid("")
		account.GetStatus().SetState(privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_PENDING)
		seedSecret("robot", auth.SystemTenant, "other-registration-secret", map[string][]byte{
			serviceaccounts.RegistrationServiceAccountIDKey: []byte("another-account"),
		})
		idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), clientID, "tenant-a").
			Return(clientUUID, "client-secret", registrationToken, nil)

		Expect(run(context.Background(), account)).To(Succeed())
		Expect(account.GetStatus().GetRegistrationSecret().GetName()).To(Equal("robot-1"))
		found := false
		for _, secret := range secretAPI.items {
			found = found || secret.GetMetadata().GetName() == "robot-1"
		}
		Expect(found).To(BeTrue())
		for _, update := range updates() {
			if slices.Contains(update.GetUpdateMask().GetPaths(), "status.registration_secret") {
				Expect(update.GetObject().GetStatus().GetRegistrationSecret().GetId()).ToNot(BeEmpty())
				Expect(update.GetObject().GetStatus().GetRegistrationSecret().GetName()).To(Equal("robot-1"))
			}
		}
	})

	It("reports an existing untracked Keycloak client as failed", func() {
		account.GetMetadata().ClearDeletionTimestamp()
		account.GetStatus().SetKeycloakClientUuid("")
		account.GetStatus().SetState(privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_PENDING)
		idpClient.EXPECT().CreateServiceAccountClient(gomock.Any(), clientID, "tenant-a").
			Return("", "", "", idp.ErrServiceAccountClientAlreadyExists)

		err := run(context.Background(), account)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		request := <-api.requests
		Expect(request.GetObject().GetStatus().GetState()).To(Equal(privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_FAILED))
		Expect(request.GetObject().GetStatus().GetMessage()).To(Equal(serviceAccountOrphanClientError))
		Expect(request.GetUpdateMask().GetPaths()).To(Equal([]string{"status.state", "status.message"}))
	})

	It("deletes the Keycloak client and both Secrets before removing its finalizer", func() {
		seedClientSecret()
		seedRegistrationSecret()
		idpClient.EXPECT().DeleteServiceAccountClient(gomock.Any(), clientID, clientUUID, registrationToken).Return(nil)

		Expect(run(context.Background(), account)).To(Succeed())
		Expect(secretAPI.items).To(BeEmpty())
		request := <-api.requests
		Expect(request.GetLock()).To(BeTrue())
		Expect(request.GetUpdateMask().GetPaths()).To(ContainElements("metadata.finalizers", "status.state", "status.message", "status.registration_secret"))
		Expect(request.GetObject().GetMetadata().GetFinalizers()).To(Equal([]string{"another-controller"}))
	})

	It("retains the finalizer when registration credentials are missing but the Keycloak client remains", func() {
		idpClient.EXPECT().FindServiceAccountClient(gomock.Any(), clientID, "tenant-a").Return(clientUUID, true, nil)
		err := run(context.Background(), account)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		request := <-api.requests
		Expect(request.GetObject().GetStatus().GetState()).To(Equal(privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_FAILED))
		Expect(request.GetObject().GetStatus().GetMessage()).To(Equal(serviceAccountOrphanClientError))
		Expect(account.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

})

var _ = Describe("Registration Secret naming", func() {
	It("uses the service-account name and adds a suffix while keeping DNS name length", func() {
		Expect(serviceaccounts.RegistrationSecretName("robot", 0)).To(Equal("robot"))
		Expect(serviceaccounts.RegistrationSecretName("robot", 1)).To(Equal("robot-1"))
		Expect(serviceaccounts.RegistrationSecretName(strings.Repeat("a", 63), 2)).To(Equal(strings.Repeat("a", 61) + "-2"))
		Expect(serviceaccounts.RegistrationSecretName("robot", 999999999999)).To(Equal("robot-999999999999"))
	})
})

var _ = Describe("Registration Secret data handling", func() {
	It("copies credential bytes without aliasing source maps", func() {
		original := map[string][]byte{"token": []byte("one")}
		cloned := cloneSecretData(original)
		cloned["token"][0] = 't'
		Expect(original["token"]).To(Equal([]byte("one")))
	})
})
