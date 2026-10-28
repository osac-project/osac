/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package secretapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/google/cel-go/cel"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc"
)

type fakeSecretsRPC struct {
	listContext  context.Context
	listRequest  *privatev1.SecretsListRequest
	listResponse *privatev1.SecretsListResponse
	listError    error
	getContext   context.Context
	getRequest   *privatev1.SecretsGetRequest
	getResponse  *privatev1.SecretsGetResponse
	getError     error
}

func (f *fakeSecretsRPC) List(
	ctx context.Context,
	request *privatev1.SecretsListRequest,
	_ ...grpc.CallOption,
) (*privatev1.SecretsListResponse, error) {
	f.listContext = ctx
	f.listRequest = request
	return f.listResponse, f.listError
}

func (f *fakeSecretsRPC) Get(
	ctx context.Context,
	request *privatev1.SecretsGetRequest,
	_ ...grpc.CallOption,
) (*privatev1.SecretsGetResponse, error) {
	f.getContext = ctx
	f.getRequest = request
	return f.getResponse, f.getError
}

var _ = Describe("Secret API CEL filters", func() {
	It("builds valid CEL for a tenant and escaped label key", func() {
		labelKey := `device"42\segment`
		got := FilterByTenantAndLabel("system", labelKey)

		want := Filter(`this.metadata.tenant == "system" && "device\"42\\segment" in this.metadata.labels`)
		Expect(got).To(Equal(want))

		env, err := cel.NewEnv(cel.Variable("this", cel.DynType))
		Expect(err).NotTo(HaveOccurred())
		ast, issues := env.Compile(string(got))
		Expect(issues).To(BeNil())
		program, err := env.Program(ast)
		Expect(err).NotTo(HaveOccurred())

		result, _, err := program.Eval(map[string]interface{}{
			"this": map[string]interface{}{
				"metadata": map[string]interface{}{
					"tenant": "system",
					"labels": map[string]interface{}{labelKey: ""},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Value()).To(BeTrue())
	})
})

var _ = Describe("Secret API client", func() {
	Describe("OAuth token source", func() {
		It("exchanges client credentials for a bearer token", func() {
			type tokenRequest struct {
				path, clientID, clientSecret, grantType string
			}
			requests := make(chan tokenRequest, 1)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					http.Error(w, "invalid form", http.StatusBadRequest)
					return
				}
				clientID, clientSecret, _ := r.BasicAuth()
				requests <- tokenRequest{r.URL.Path, clientID, clientSecret, r.Form.Get("grant_type")}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{\"access_token\":\"test-access-token\",\"token_type\":\"Bearer\",\"expires_in\":3600}"))
			}))
			defer server.Close()

			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			issuerURL, err := validateIssuerURL(server.URL + "/realms/osac/")
			Expect(err).NotTo(HaveOccurred())
			tokenSource := newTokenSource(issuerURL, "test-client", "client-secret", &tls.Config{RootCAs: roots})
			token, err := tokenSource.Token()

			Expect(err).NotTo(HaveOccurred())
			Expect(token.AccessToken).To(Equal("test-access-token"))
			Expect(<-requests).To(Equal(tokenRequest{"/realms/osac/protocol/openid-connect/token", "test-client", "client-secret", "client_credentials"}))
		})

		It("does not follow redirects from the token endpoint", func() {
			redirected := make(chan struct{}, 1)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				redirected <- struct{}{}
				w.WriteHeader(http.StatusOK)
			}))
			defer target.Close()

			issuer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			}))
			defer issuer.Close()

			roots := x509.NewCertPool()
			roots.AddCert(issuer.Certificate())
			issuerURL, err := validateIssuerURL(issuer.URL + "/realms/osac")
			Expect(err).NotTo(HaveOccurred())
			tokenSource := newTokenSource(issuerURL, "test-client", "client-secret", &tls.Config{RootCAs: roots})
			_, err = tokenSource.Token()

			Expect(err).To(HaveOccurred())
			Consistently(redirected).ShouldNot(Receive())
		})
	})

	Describe("List", func() {
		It("sends the filter and limit and returns metadata without secret data", func() {
			api := &fakeSecretsRPC{
				listResponse: &privatev1.SecretsListResponse{
					Total: 1,
					Items: []*privatev1.Secret{{
						Id:       "secret-1",
						Metadata: &privatev1.Metadata{Tenant: "system", Labels: map[string]string{"scope": "device"}},
						Data:     map[string][]byte{"password": []byte("must-not-be-returned-from-list")},
					}},
				},
			}
			client := &client{api: api}

			got, err := client.List(context.Background(), `this.metadata.tenant == "system"`, 2)

			Expect(err).NotTo(HaveOccurred())
			Expect(api.listRequest.GetFilter()).To(Equal(`this.metadata.tenant == "system"`))
			Expect(api.listRequest.GetLimit()).To(Equal(int32(2)))
			deadline, ok := api.listContext.Deadline()
			Expect(ok).To(BeTrue())
			Expect(time.Until(deadline)).To(BeNumerically(">", 0))
			Expect(time.Until(deadline)).To(BeNumerically("<=", rpcTimeout))
			Expect(got.Total).To(Equal(int32(1)))
			Expect(got.Items).To(HaveLen(1))
			Expect(got.Items[0].ID).To(Equal("secret-1"))
			Expect(got.Items[0].Tenant).To(Equal("system"))
			Expect(got.Items[0].Labels).To(Equal(map[string]string{"scope": "device"}))
			Expect(got.Items[0].Data).To(BeEmpty())
		})

		It("returns an empty result when no Secrets match", func() {
			client := &client{api: &fakeSecretsRPC{}}

			got, err := client.List(context.Background(), `this.metadata.tenant == "system"`, 2)

			Expect(err).NotTo(HaveOccurred())
			Expect(got.Total).To(BeZero())
			Expect(got.Items).To(BeEmpty())
			Expect(got.Items).NotTo(BeNil())
		})

		It("returns an error when the API request fails", func() {
			api := &fakeSecretsRPC{listError: errors.New("unavailable")}
			client := &client{api: api}

			_, err := client.List(context.Background(), "filter", 2)

			Expect(err).To(MatchError(ContainSubstring("failed to list OSAC Secrets")))
			Expect(err.Error()).NotTo(ContainSubstring("password"))
		})

		It("requires a filter and passes the count-only limit through", func() {
			api := &fakeSecretsRPC{listResponse: &privatev1.SecretsListResponse{Total: 3}}
			client := &client{api: api}

			_, err := client.List(context.Background(), "", 2)
			Expect(err).To(MatchError(ContainSubstring("filter is required")))
			Expect(api.listRequest).To(BeNil())

			result, err := client.List(context.Background(), "filter", 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(api.listRequest.GetLimit()).To(BeZero())
			Expect(result.Total).To(Equal(int32(3)))
			Expect(result.Items).To(BeEmpty())
		})
	})

	Describe("Get", func() {
		It("returns Secret metadata and binary data", func() {
			password := []byte{0x00, 0xff, 0x41}
			api := &fakeSecretsRPC{
				getResponse: &privatev1.SecretsGetResponse{Object: &privatev1.Secret{
					Id:       "secret-1",
					Metadata: &privatev1.Metadata{Tenant: "system", Labels: map[string]string{"scope": "device"}},
					Data:     map[string][]byte{"username": []byte("admin"), "password": password},
				}},
			}
			client := &client{api: api}

			got, err := client.Get(context.Background(), "secret-1")

			Expect(err).NotTo(HaveOccurred())
			Expect(api.getRequest.GetId()).To(Equal("secret-1"))
			deadline, ok := api.getContext.Deadline()
			Expect(ok).To(BeTrue())
			Expect(time.Until(deadline)).To(BeNumerically(">", 0))
			Expect(time.Until(deadline)).To(BeNumerically("<=", rpcTimeout))
			Expect(got.ID).To(Equal("secret-1"))
			Expect(got.Tenant).To(Equal("system"))
			Expect(got.Data["username"]).To(Equal([]byte("admin")))
			Expect(got.Data["password"]).To(Equal(password))
		})

		It("returns an error when the API request fails", func() {
			api := &fakeSecretsRPC{getError: errors.New("unavailable")}
			client := &client{api: api}

			_, err := client.Get(context.Background(), "secret-1")

			Expect(err).To(MatchError(ContainSubstring("failed to get OSAC Secret")))
		})
	})

	Describe("New", func() {
		DescribeTable("rejects invalid endpoints",
			func(endpoint, wantErr string) {
				_, err := New(Config{Endpoint: endpoint})
				Expect(err).To(MatchError(wantErr))
			},
			Entry("requires a host", "", "Secret API endpoint must include a host"),
			Entry("rejects malformed URLs", "https://%zz", "Secret API endpoint must include a host"),
		)

		DescribeTable("uses only the endpoint host",
			func(endpoint, wantHost string) {
				host, err := parseEndpoint(endpoint)
				Expect(err).NotTo(HaveOccurred())
				Expect(host).To(Equal(wantHost))
			},
			Entry("ignores path", "https://secrets.example.test:8001/api", "secrets.example.test:8001"),
			Entry("ignores query", "https://secrets.example.test:8001?region=west", "secrets.example.test:8001"),
			Entry("ignores fragment", "https://secrets.example.test:8001#secrets", "secrets.example.test:8001"),
		)

		issuerWithUserInfo := url.URL{
			Scheme: "https", Host: "keycloak.example.test", Path: "/realms/osac",
			User: url.UserPassword("test-user", "test-password"),
		}
		DescribeTable("rejects invalid issuer URLs before reading credentials",
			func(issuerURL string, wantErr string) {
				_, err := New(Config{
					Endpoint:         "https://secrets.example.test:8001",
					IssuerURL:        issuerURL,
					ClientIDFile:     "/missing/client-id",
					ClientSecretFile: "/missing/client-secret",
				})
				Expect(err).To(MatchError(wantErr))
			},
			Entry("requires an issuer", "", "Secret API issuer URL must use HTTPS"),
			Entry("requires HTTPS", "http://keycloak.example.test/realms/osac", "Secret API issuer URL must use HTTPS"),
			Entry("requires a host", "https:///realms/osac", "Secret API issuer URL must use HTTPS"),
			Entry("rejects user info", issuerWithUserInfo.String(), "Secret API issuer URL must use HTTPS"),
			Entry("rejects query parameters", "https://keycloak.example.test/realms/osac?tenant=shared", "Secret API issuer URL must use HTTPS"),
			Entry("rejects an empty query", "https://keycloak.example.test/realms/osac?", "Secret API issuer URL must use HTTPS"),
			Entry("rejects fragments", "https://keycloak.example.test/realms/osac#token", "Secret API issuer URL must use HTTPS"),
			Entry("rejects an empty fragment", "https://keycloak.example.test/realms/osac#", "Secret API issuer URL must use HTTPS"),
			Entry("rejects malformed URLs", "https://%zz/realms/osac", "Secret API issuer URL must use HTTPS"),
		)

		It("requires non-empty client credential files without exposing their contents", func() {
			tempDir := GinkgoT().TempDir()
			clientIDFile := filepath.Join(tempDir, "client-id")
			clientSecretFile := filepath.Join(tempDir, "client-secret")
			Expect(os.WriteFile(clientIDFile, []byte("client-id-value"), 0o600)).To(Succeed())
			Expect(os.WriteFile(clientSecretFile, []byte(""), 0o600)).To(Succeed())

			_, err := New(Config{
				Endpoint:         "https://fulfillment-internal-api:8001",
				IssuerURL:        "https://keycloak.example.test/realms/osac",
				ClientIDFile:     clientIDFile,
				ClientSecretFile: clientSecretFile,
			})

			Expect(err).To(MatchError(ContainSubstring("client secret file is empty")))
			Expect(err.Error()).NotTo(ContainSubstring("client-id-value"))
		})

		It("creates a closable client from mounted OAuth credentials", func() {
			tempDir := GinkgoT().TempDir()
			clientIDFile := filepath.Join(tempDir, "client-id")
			clientSecretFile := filepath.Join(tempDir, "client-secret")
			Expect(os.WriteFile(clientIDFile, []byte("client-id"), 0o600)).To(Succeed())
			Expect(os.WriteFile(clientSecretFile, []byte("client-secret"), 0o600)).To(Succeed())

			client, err := New(Config{
				Endpoint:         "https://fulfillment-internal-api:8001",
				IssuerURL:        "https://keycloak.example.test/realms/osac",
				ClientIDFile:     clientIDFile,
				ClientSecretFile: clientSecretFile,
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(client.Close()).To(Succeed())
		})
	})
})
