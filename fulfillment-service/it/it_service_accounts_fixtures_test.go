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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/oauth"
	"github.com/osac-project/osac/fulfillment-service/internal/tlsconfig"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func serviceAccountRequest(tenant, name string) *publicv1.ServiceAccountsCreateRequest {
	return publicv1.ServiceAccountsCreateRequest_builder{Object: publicv1.ServiceAccount_builder{
		Metadata: publicv1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
	}.Build()}.Build()
}

func deleteServiceAccount(ctx context.Context, client publicv1.ServiceAccountsClient, id string) {
	GinkgoHelper()
	_, err := client.Delete(ctx, publicv1.ServiceAccountsDeleteRequest_builder{Id: id}.Build())
	if status.Code(err) == codes.NotFound {
		return
	}
	Expect(err).ToNot(HaveOccurred())
	Eventually(ctx, func() codes.Code {
		_, e := client.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: id}.Build())
		return status.Code(e)
	}, 2*time.Minute, 2*time.Second).Should(Equal(codes.NotFound))
}

func keycloakClient(ctx context.Context, clientID string) map[string]any {
	GinkgoHelper()
	code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/clients?clientId="+url.QueryEscape(clientID)+"&exact=true", nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(code).To(Equal(http.StatusOK))
	var clients []map[string]any
	Expect(json.Unmarshal(body, &clients)).To(Succeed())
	Expect(clients).To(HaveLen(1))
	Expect(clients[0]["clientId"]).To(Equal(clientID))
	return clients[0]
}

func serviceAccountKeycloakIssuer(ctx context.Context) string {
	GinkgoHelper()
	tls := tlsconfig.NewClientTLSConfig()
	tls.RootCAs = tool.CaPool().Pool()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://"+keycloakAddr+"/realms/osac/.well-known/openid-configuration", nil)
	Expect(err).ToNot(HaveOccurred())
	response, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: tls}}).Do(request)
	Expect(err).ToNot(HaveOccurred())
	defer response.Body.Close()
	Expect(response.StatusCode).To(Equal(http.StatusOK))
	var metadata struct {
		Issuer string `json:"issuer"`
	}
	Expect(json.NewDecoder(response.Body).Decode(&metadata)).To(Succeed())
	Expect(metadata.Issuer).To(HavePrefix("https://"))
	return metadata.Issuer
}

func keycloakClientSecret(ctx context.Context, clientID string) string {
	GinkgoHelper()
	client := keycloakClient(ctx, clientID)
	code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/clients/"+client["id"].(string)+"/client-secret", nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(code).To(Equal(http.StatusOK))
	var secret struct {
		Value string `json:"value"`
	}
	Expect(json.Unmarshal(body, &secret)).To(Succeed())
	Expect(secret.Value).ToNot(BeEmpty())
	return secret.Value
}

func keycloakServiceAccountClientIDs(ctx context.Context, tenant string) []string {
	GinkgoHelper()
	code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/clients?first=0&max=1000", nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(code).To(Equal(http.StatusOK))
	var clients []struct {
		ClientID string `json:"clientId"`
	}
	Expect(json.Unmarshal(body, &clients)).To(Succeed())
	prefix := "osac-sa-" + tenant + "-"
	var result []string
	for _, client := range clients {
		if strings.HasPrefix(client.ClientID, prefix) {
			result = append(result, client.ClientID)
		}
	}
	return result
}

func serviceAccountClaims(ctx context.Context, clientID, secret string) jwt.MapClaims {
	GinkgoHelper()
	source, err := serviceAccountTokenSource(ctx, clientID, secret)
	Expect(err).ToNot(HaveOccurred())
	token, err := source.Token(ctx)
	Expect(err).ToNot(HaveOccurred())
	issuer := serviceAccountKeycloakIssuer(ctx)
	keys, err := auth.NewJwksCache().SetLogger(logger).SetCaPool(tool.CaPool()).AddIssuer(issuer).SetRoot(GinkgoT().TempDir()).Build()
	Expect(err).ToNot(HaveOccurred())
	validator, err := auth.NewJwtValidator().SetLogger(logger).SetJwksCache(keys).
		SetExpirationLeeway(5 * time.Second).AddAudience("osac-api").Build()
	Expect(err).ToNot(HaveOccurred())
	verified, err := validator.Validate(ctx, token.Access)
	Expect(err).ToNot(HaveOccurred())
	return verified.Claims.(jwt.MapClaims)
}

func serviceAccountTokenSource(ctx context.Context, clientID, secret string) (auth.TokenSource, error) {
	GinkgoHelper()
	issuer := serviceAccountKeycloakIssuer(ctx)
	store, err := auth.NewMemoryTokenStore().SetLogger(logger).Build()
	if err != nil {
		return nil, err
	}
	source, err := oauth.NewTokenSource().SetLogger(logger).SetStore(store).SetCaPool(tool.CaPool()).SetIssuer(issuer).
		SetFlow(oauth.CredentialsFlow).SetClientId(clientID).SetClientSecret(secret).Build()
	if err != nil {
		return nil, err
	}
	return source, nil
}

func serviceAccountTokenError(ctx context.Context, clientID, secret string) error {
	GinkgoHelper()
	source, err := serviceAccountTokenSource(ctx, clientID, secret)
	if err != nil {
		return err
	}
	_, err = source.Token(ctx)
	return err
}

// Read only SQL against the Kind fixture, using the database container's local peer authentication.
func serviceAccountSQL(ctx context.Context, query string) string {
	GinkgoHelper()
	output, err := tool.runCommand(ctx, kubectlCmd, "exec", "-n", "osac-infra", "deployment/postgres", "-c", "server", "--",
		"psql", "-U", "postgres", "-d", "osac_service", "-Atqc", query)
	Expect(err).ToNot(HaveOccurred())
	return strings.TrimSpace(string(output))
}

func assertServiceAccountStoredWithoutSecret(ctx context.Context, id, secret string) {
	GinkgoHelper()
	// IDs are generated by the API; never interpolate caller-controlled SQL.
	data := serviceAccountSQL(ctx, fmt.Sprintf(`select data::text from service_accounts where id='%s'`, id))
	Expect(data).ToNot(BeEmpty())
	Expect(strings.Contains(data, secret)).To(BeFalse())
}
