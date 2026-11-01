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

package it

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/serviceaccount"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/oauth"
	"github.com/osac-project/osac/fulfillment-service/internal/servers"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	testutils "github.com/osac-project/osac/fulfillment-service/internal/testing"
	"github.com/osac-project/osac/fulfillment-service/internal/tlsconfig"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	"github.com/osac-project/osac/fulfillment-service/internal/validation"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Service-account registration and cleanup use real Keycloak; failures are injected at the DCR boundary.
type serviceAccountFaultTransport struct {
	base                    http.RoundTripper
	failRegistration        bool
	failDelete              bool
	mu                      sync.Mutex
	clientID                string
	secret                  string
	registrationAccessToken string
}

func (t *serviceAccountFaultTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/clients-registrations/default") {
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		_ = request.Body.Close()
		request.Body = io.NopCloser(bytes.NewReader(data))
		var client struct {
			ClientID string `json:"clientId"`
		}
		if err = json.Unmarshal(data, &client); err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.clientID = client.ClientID
		fail := t.failRegistration
		t.failRegistration = false
		t.mu.Unlock()
		if fail {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("injected registration outage")), Request: request}, nil
		}
	}
	if request.Method == http.MethodDelete && strings.Contains(request.URL.Path, "/clients-registrations/default/") && t.failDelete {
		t.mu.Lock()
		t.failDelete = false
		t.mu.Unlock()
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("injected cleanup outage")), Request: request}, nil
	}
	response, err := t.base.RoundTrip(request)
	if err == nil && request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/clients-registrations/default") && response.StatusCode == http.StatusCreated {
		data, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		var value struct {
			Secret                  string `json:"secret"`
			RegistrationAccessToken string `json:"registrationAccessToken"`
		}
		if err = json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.secret = value.Secret
		t.registrationAccessToken = value.RegistrationAccessToken
		t.mu.Unlock()
	}
	return response, err
}

var _ = Describe("Service-account infrastructure failure boundaries", Serial, func() {
	DescribeTable("keeps failed Create credentials off the wire and out of storage", func(ctx context.Context, mode string) {
		lateCommit := mode == "commit"
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		var logBuffer bytes.Buffer
		capture, err := logging.NewLogger().SetWriter(&logBuffer).SetLevel("debug").Build()
		Expect(err).ToNot(HaveOccurred())
		container, err := database.NewContainer().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(container.Start(ctx)).To(Succeed())
		DeferCleanup(func(ctx context.Context) { Expect(container.Stop(ctx)).To(Succeed()) })
		instance, err := container.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(instance.Close)
		pool, err := instance.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)
		manager, err := database.NewTxManager().SetLogger(capture).SetPool(pool).Build()
		Expect(err).ToNot(HaveOccurred())
		tenancy, err := auth.NewDefaultTenancyLogic().SetLogger(capture).Build()
		Expect(err).ToNot(HaveOccurred())
		tenant := "sa-fault-" + uuid.New()[24:]
		tx, err := manager.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		tenantDAO, err := dao.NewGenericDAO[*privatev1.Tenant]().SetLogger(capture).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		seedCtx := database.TxIntoContext(auth.ContextWithSubject(ctx, &auth.Subject{Tenants: collections.NewSet(tenant)}), tx)
		_, err = tenantDAO.Create().SetObject(privatev1.Tenant_builder{Id: tenant, Metadata: privatev1.Metadata_builder{Name: tenant, Tenant: tenant}.Build()}.Build()).Do(seedCtx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tx.End(ctx)).To(Succeed())
		if lateCommit {
			_, err = pool.Exec(ctx, `create function reject_service_account_commit() returns trigger language plpgsql as $$
    begin raise exception 'injected deferred failure'; end $$;
    create constraint trigger reject_service_account_commit after insert on service_accounts
    deferrable initially deferred for each row execute function reject_service_account_commit()`)
			Expect(err).ToNot(HaveOccurred())
		}
		// Read the same projected credentials mounted by the API and controller charts.
		credentials, err := tool.KubeClientSet().CoreV1().Secrets("osac").Get(ctx, "fulfillment-controller-credentials", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		store, err := auth.NewMemoryTokenStore().SetLogger(capture).Build()
		Expect(err).ToNot(HaveOccurred())
		issuer := serviceAccountKeycloakIssuer(ctx)
		tokenSource, err := oauth.NewTokenSource().SetLogger(capture).SetStore(store).SetCaPool(tool.CaPool()).SetIssuer(issuer).
			SetFlow(oauth.CredentialsFlow).SetClientId(string(credentials.Data["client-id"])).SetClientSecret(string(credentials.Data["client-secret"])).Build()
		Expect(err).ToNot(HaveOccurred())
		tls := tlsconfig.NewClientTLSConfig()
		tls.RootCAs = tool.CaPool().Pool()
		base := &http.Transport{TLSClientConfig: tls}
		DeferCleanup(base.CloseIdleConnections)
		transport := &serviceAccountFaultTransport{base: base, failRegistration: mode == "registration", failDelete: mode == "cleanup"}
		client, err := idp.NewClient().SetLogger(capture).SetBaseURL("https://" + keycloakAddr).SetCaPool(tool.CaPool()).SetTokenSource(tokenSource).SetHTTPClient(&http.Client{Transport: transport}).Build()
		Expect(err).ToNot(HaveOccurred())
		attribution, err := auth.NewSystemAttributionLogic().SetLogger(capture).Build()
		Expect(err).ToNot(HaveOccurred())
		secretStore := vault.NewMockSecretStore(ctrl)
		var secretStoreMu sync.Mutex
		storedSecrets := map[string]map[string][]byte{}
		secretStore.EXPECT().Store(gomock.Any(), gomock.Any(), "", gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, secretTenant, _, name string, data map[string][]byte) error {
				Expect(secretTenant).To(Or(Equal(tenant), Equal(auth.SystemTenant)))
				secretStoreMu.Lock()
				defer secretStoreMu.Unlock()
				storedSecrets[name] = map[string][]byte{}
				for key, value := range data {
					storedSecrets[name][key] = append([]byte(nil), value...)
				}
				return nil
			}).AnyTimes()
		secretStore.EXPECT().Fetch(gomock.Any(), gomock.Any(), "", gomock.Any()).DoAndReturn(
			func(_ context.Context, secretTenant, _, name string) (map[string][]byte, error) {
				Expect(secretTenant).To(Or(Equal(tenant), Equal(auth.SystemTenant)))
				secretStoreMu.Lock()
				defer secretStoreMu.Unlock()
				data, ok := storedSecrets[name]
				if !ok {
					return nil, status.Error(codes.NotFound, "secret not found")
				}
				copy := map[string][]byte{}
				for key, value := range data {
					copy[key] = append([]byte(nil), value...)
				}
				return copy, nil
			}).AnyTimes()
		secretStore.EXPECT().Delete(gomock.Any(), gomock.Any(), "", gomock.Any()).DoAndReturn(
			func(_ context.Context, secretTenant, _, name string) error {
				Expect(secretTenant).To(Or(Equal(tenant), Equal(auth.SystemTenant)))
				secretStoreMu.Lock()
				defer secretStoreMu.Unlock()
				delete(storedSecrets, name)
				return nil
			}).AnyTimes()
		api, err := servers.NewPrivateServiceAccountsServer().SetLogger(capture).SetTenancyLogic(tenancy).SetAttributionLogic(attribution).
			Build()
		Expect(err).ToNot(HaveOccurred())
		secretAPI, err := servers.NewPrivateSecretsServer().SetLogger(capture).SetTenancyLogic(tenancy).
			SetAttributionLogic(attribution).SetSecretStore(secretStore).Build()
		Expect(err).ToNot(HaveOccurred())
		logs, err := logging.NewInterceptor().SetLogger(capture).SetBodies(true).Build()
		Expect(err).ToNot(HaveOccurred())
		transactions, err := database.NewTxInterceptor().SetLogger(capture).SetManager(manager).Build()
		Expect(err).ToNot(HaveOccurred())
		validator, err := validation.NewProtovalidateInterceptor().SetLogger(capture).Build()
		Expect(err).ToNot(HaveOccurred())
		keys, err := auth.NewJwksCache().SetLogger(capture).SetCaPool(tool.CaPool()).AddIssuer(issuer).SetRoot(GinkgoT().TempDir()).Build()
		Expect(err).ToNot(HaveOccurred())
		jwtValidator, err := auth.NewJwtValidator().SetLogger(capture).SetJwksCache(keys).Build()
		Expect(err).ToNot(HaveOccurred())
		authenticate, err := auth.NewGrpcAuthnInterceptor().SetLogger(capture).SetJwtValidator(jwtValidator).Build()
		Expect(err).ToNot(HaveOccurred())
		evaluator, err := auth.NewEvaluator().Build()
		Expect(err).ToNot(HaveOccurred())
		authorize, err := auth.NewGrpcAuthzInterceptor().SetLogger(capture).SetEvaluator(evaluator).Build()
		Expect(err).ToNot(HaveOccurred())
		server := testutils.NewServer(grpc.ChainUnaryInterceptor(logs.UnaryServer, validator.UnaryServer, transactions.UnaryServer, authenticate.UnaryServer, authorize.UnaryServer))
		privatev1.RegisterServiceAccountsServer(server.Registrar(), api)
		privatev1.RegisterSecretsServer(server.Registrar(), secretAPI)
		server.Start()
		DeferCleanup(server.Stop)
		connection, err := grpc.NewClient(server.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)
		admin, err := tool.makeKeycloakTokenSource(ctx, adminUsername, adminsPassword)
		Expect(err).ToNot(HaveOccurred())
		token, err := admin.Token(ctx)
		Expect(err).ToNot(HaveOccurred())
		// Keycloak and the local JWT validator can sample opposite sides of a one-second
		// boundary when the token's issued-at claim is checked.
		time.Sleep(time.Second)
		caller := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token.Access))
		// Register orphan cleanup before invoking Create, including when an assertion fails.
		DeferCleanup(func(ctx context.Context) {
			transport.mu.Lock()
			clientID := transport.clientID
			transport.mu.Unlock()
			if clientID == "" {
				return
			}
			code, body, e := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/clients?clientId="+clientID+"&exact=true", nil)
			Expect(e).ToNot(HaveOccurred())
			Expect(code).To(Equal(http.StatusOK))
			var clients []struct {
				ID string `json:"id"`
			}
			Expect(json.Unmarshal(body, &clients)).To(Succeed())
			for _, remaining := range clients {
				code, _, e := tool.KeycloakAdminRequest(ctx, http.MethodDelete, "/clients/"+remaining.ID, nil)
				Expect(e).ToNot(HaveOccurred())
				Expect(code).To(Or(Equal(http.StatusNoContent), Equal(http.StatusNotFound)))
			}
		})
		apiClient := privatev1.NewServiceAccountsClient(connection)
		created, createErr := apiClient.Create(caller, privatev1.ServiceAccountsCreateRequest_builder{Object: privatev1.ServiceAccount_builder{Metadata: privatev1.Metadata_builder{Name: "robot", Tenant: tenant}.Build()}.Build()}.Build())
		reconciler, err := serviceaccount.NewFunction().SetLogger(capture).SetConnection(connection).SetIdpClient(client).Build()
		Expect(err).ToNot(HaveOccurred())
		if lateCommit {
			Expect(status.Code(createErr)).To(Equal(codes.Internal))
			Expect(created).To(BeNil())
			var rows, changes int
			Expect(pool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&rows)).To(Succeed())
			Expect(rows).To(BeZero())
			Expect(pool.QueryRow(ctx, `select count(*) from changes where "table"='service_accounts'`).Scan(&changes)).To(Succeed())
			Expect(changes).To(BeZero())
			transport.mu.Lock()
			clientID := transport.clientID
			transport.mu.Unlock()
			Expect(clientID).To(BeEmpty(), "database failure must happen before Keycloak setup")
			return
		}
		Expect(createErr).ToNot(HaveOccurred())
		id := created.GetObject().GetId()
		if mode == "registration" {
			Expect(reconciler.Run(caller, created.GetObject())).To(HaveOccurred())
			transport.mu.Lock()
			clientID, secret := transport.clientID, transport.secret
			transport.mu.Unlock()
			Expect(clientID).To(HavePrefix("osac-sa-" + tenant + "-"))
			Expect(secret).To(BeEmpty())
			pending, getErr := apiClient.Get(caller, privatev1.ServiceAccountsGetRequest_builder{Id: id}.Build())
			Expect(getErr).ToNot(HaveOccurred())
			Expect(pending.GetObject().GetStatus().GetKeycloakClientUuid()).To(BeEmpty())
			Expect(pending.GetObject().GetStatus().GetClientSecret().GetId()).To(BeEmpty())
			code, body, getErr := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/clients?clientId="+clientID+"&exact=true", nil)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(code).To(Equal(http.StatusOK))
			var clients []map[string]any
			Expect(json.Unmarshal(body, &clients)).To(Succeed())
			Expect(clients).To(BeEmpty(), "failed registration must not leave a Keycloak client")
			return
		}

		Expect(reconciler.Run(caller, created.GetObject())).To(Succeed())
		_, err = apiClient.Get(caller, privatev1.ServiceAccountsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		clientID := serviceaccounts.ClientID(tenant, id)
		Expect(clientID).To(HavePrefix("osac-sa-" + tenant + "-"))
		_, err = apiClient.Delete(caller, privatev1.ServiceAccountsDeleteRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		pending, err := apiClient.Get(caller, privatev1.ServiceAccountsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(reconciler.Run(caller, pending.GetObject())).To(HaveOccurred())
		retained, err := apiClient.Get(caller, privatev1.ServiceAccountsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(retained.GetObject().GetMetadata().GetFinalizers()).To(ContainElement("fulfillment-controller"))
		Expect(keycloakClient(ctx, clientID)["enabled"]).To(BeTrue())
		Expect(reconciler.Run(caller, retained.GetObject())).To(Succeed())
		_, err = apiClient.Get(caller, privatev1.ServiceAccountsGetRequest_builder{Id: id}.Build())
		Expect(status.Code(err)).To(Equal(codes.NotFound))
		var archived int
		Expect(pool.QueryRow(ctx, `select count(*) from archived_service_accounts where id=$1`, id).Scan(&archived)).To(Succeed())
		Expect(archived).To(Equal(1))
		transport.mu.Lock()
		secret := transport.secret
		transport.mu.Unlock()
		Expect(secret).ToNot(BeEmpty())
		Expect(strings.Contains(logBuffer.String(), secret)).To(BeFalse())
	}, Entry("registration failure leaves a pending account without a client", "registration"), Entry("commit failure creates no account or client", "commit"), Entry("cleanup retry retains finalizer until Keycloak deletion", "cleanup"))
})
