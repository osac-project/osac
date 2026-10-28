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

package idp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Service-account Keycloak client", func() {
	const clientUUID = "d3c4e3db-95b8-481d-8b5c-963da477da12"
	const clientID = "osac-sa-tenant-a-e80e9561-48cc-41c9-89b4-8c9a944b45a5"
	const credential = "test-only-credential-marker"
	const registrationToken = "test-registration-token-marker"
	const registrationPath = "/realms/osac/clients-registrations/default"
	const adminClientPath = "/admin/realms/osac/clients/" + clientUUID
	var client *Client
	var server *httptest.Server
	var calls []string
	var created map[string]any
	var failure string
	var failureStatus int
	var existenceFailureStatus int
	var clientMissing bool
	var lookupClientFound bool
	var lookupFailureStatus int
	var response string
	var updatedRepresentation map[string]json.RawMessage
	var rotationClientUUID string
	var rotationClientID string

	BeforeEach(func() {
		calls = nil
		created = nil
		failure = ""
		failureStatus = http.StatusInternalServerError
		existenceFailureStatus = 0
		clientMissing = false
		lookupClientFound = false
		lookupFailureStatus = 0
		response = ""
		updatedRepresentation = nil
		rotationClientUUID = clientUUID
		rotationClientID = clientID
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			call := r.Method + " " + r.URL.Path
			calls = append(calls, call)
			switch call {
			case "POST " + registrationPath:
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
				Expect(json.NewDecoder(r.Body).Decode(&created)).To(Succeed())
				if call == failure {
					w.WriteHeader(failureStatus)
					_, _ = fmt.Fprint(w, credential+" "+registrationToken)
					return
				}
				if response != "" {
					_, _ = fmt.Fprint(w, response)
					return
				}
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"id": clientUUID, "clientId": clientID, "secret": credential,
					"registrationAccessToken": registrationToken,
				})
			case "DELETE " + registrationPath + "/" + clientID:
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer " + registrationToken))
				if call == failure {
					w.WriteHeader(failureStatus)
					_, _ = fmt.Fprint(w, credential+" "+registrationToken)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			case "GET " + adminClientPath:
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
				if existenceFailureStatus != 0 {
					w.WriteHeader(existenceFailureStatus)
					return
				}
				if clientMissing {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(http.StatusOK)
			case "GET " + registrationPath + "/" + clientID:
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer " + registrationToken))
				w.WriteHeader(http.StatusOK)
				Expect(json.NewEncoder(w).Encode(map[string]any{
					"id": rotationClientUUID, "clientId": rotationClientID, "secret": "old-client-secret",
					"registrationAccessToken": registrationToken, "enabled": true,
				})).To(Succeed())
			case "PUT " + registrationPath + "/" + clientID:
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer " + registrationToken))
				Expect(json.NewDecoder(r.Body).Decode(&updatedRepresentation)).To(Succeed())
				w.WriteHeader(http.StatusOK)
				Expect(json.NewEncoder(w).Encode(map[string]string{
					"id": clientUUID, "clientId": clientID, "registrationAccessToken": "rotated-registration-token",
				})).To(Succeed())
			case "GET /admin/realms/osac/clients":
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
				Expect(r.URL.Query().Get("clientId")).To(Equal(clientID))
				if lookupFailureStatus != 0 {
					w.WriteHeader(lookupFailureStatus)
					return
				}
				clients := []keycloakClient{}
				if lookupClientFound {
					clients = append(clients, keycloakClient{ID: clientUUID, ClientID: clientID})
				}
				w.WriteHeader(http.StatusOK)
				Expect(json.NewEncoder(w).Encode(clients)).To(Succeed())
			default:
				Fail("unexpected request: " + call)
			}
		}))
		DeferCleanup(server.Close)
		var err error
		client, err = NewClient().SetLogger(slog.New(slog.NewTextHandler(GinkgoWriter, nil))).
			SetBaseURL(server.URL).SetHTTPClient(server.Client()).SetTokenSource(&staticTokenSource{}).Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("creates a configured confidential client and returns its one-time and scoped credentials", func() {
		id, secret, scopedToken, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(id).To(Equal(clientUUID))
		Expect(secret).To(Equal(credential))
		Expect(scopedToken).To(Equal(registrationToken))
		Expect(created).To(HaveKeyWithValue("clientId", clientID))
		Expect(created).To(HaveKeyWithValue("protocol", "openid-connect"))
		Expect(created).To(HaveKeyWithValue("enabled", true))
		for _, flag := range []string{"publicClient", "standardFlowEnabled", "implicitFlowEnabled", "directAccessGrantsEnabled", "fullScopeAllowed"} {
			Expect(created).To(HaveKeyWithValue(flag, false))
		}
		Expect(created).To(HaveKeyWithValue("serviceAccountsEnabled", true))
		Expect(created).To(HaveKeyWithValue("clientAuthenticatorType", "client-secret"))
		Expect(created).To(HaveKeyWithValue("defaultClientScopes", ConsistOf("service_account")))
		Expect(created).To(HaveKeyWithValue("optionalClientScopes", BeEmpty()))
		mappers, ok := created["protocolMappers"].([]any)
		Expect(ok).To(BeTrue())
		Expect(mappers).To(HaveLen(2))
		Expect(mappers).To(ConsistOf(
			HaveKeyWithValue("protocolMapper", "oidc-hardcoded-claim-mapper"),
			HaveKeyWithValue("protocolMapper", "oidc-audience-mapper"),
		))
		for _, value := range mappers {
			mapper := value.(map[string]any)
			config := mapper["config"].(map[string]any)
			Expect(mapper["protocol"]).To(Equal("openid-connect"))
			Expect(config["access.token.claim"]).To(Equal("true"))
			if mapper["protocolMapper"] == "oidc-hardcoded-claim-mapper" {
				Expect(config).To(HaveKeyWithValue("claim.name", "osac_tenant"))
				Expect(config).To(HaveKeyWithValue("claim.value", "tenant-a"))
				Expect(config).To(HaveKeyWithValue("jsonType.label", "String"))
			} else {
				Expect(config).To(HaveKeyWithValue("included.custom.audience", "osac-api"))
			}
		}
		Expect(calls).To(Equal([]string{"POST " + registrationPath}))
	})

	It("does not return credentials when Keycloak rejects registration", func() {
		failure = "POST " + registrationPath
		failureStatus = http.StatusConflict
		id, secret, scopedToken, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).To(HaveOccurred())
		Expect(id).To(BeEmpty())
		Expect(secret).To(BeEmpty())
		Expect(scopedToken).To(BeEmpty())
		Expect(err.Error()).ToNot(ContainSubstring(credential))
		Expect(err.Error()).ToNot(ContainSubstring(registrationToken))
	})

	It("reports an existing client when registration fails after Keycloak created it", func() {
		failure = "POST " + registrationPath
		failureStatus = http.StatusConflict
		lookupClientFound = true
		id, secret, scopedToken, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(errors.Is(err, ErrServiceAccountClientAlreadyExists)).To(BeTrue())
		Expect(id).To(BeEmpty())
		Expect(secret).To(BeEmpty())
		Expect(scopedToken).To(BeEmpty())
		Expect(calls).To(Equal([]string{
			"POST " + registrationPath,
			"GET /admin/realms/osac/clients",
		}))
	})

	It("finds only the exact deterministic client ID", func() {
		lookupClientFound = true
		id, found, err := client.FindServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(id).To(Equal(clientUUID))

		lookupClientFound = false
		id, found, err = client.FindServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeFalse())
		Expect(id).To(BeEmpty())
	})

	It("keeps client lookup errors sanitized", func() {
		lookupFailureStatus = http.StatusForbidden
		_, found, err := client.FindServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("HTTP 403"))
		Expect(found).To(BeFalse())
	})

	It("returns a scoped token with a partial response so the caller can compensate", func() {
		response = `{"id":"` + clientUUID + `","clientId":"` + clientID + `","registrationAccessToken":"` + registrationToken + `"}`
		id, secret, scopedToken, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).To(HaveOccurred())
		Expect(id).To(Equal(clientUUID))
		Expect(secret).To(BeEmpty())
		Expect(scopedToken).To(Equal(registrationToken))
		Expect(err.Error()).ToNot(ContainSubstring(registrationToken))
	})

	It("retains the scoped token when the returned client UUID is invalid", func() {
		response = `{"id":"invalid","clientId":"` + clientID + `","secret":"` + credential + `","registrationAccessToken":"` + registrationToken + `"}`
		id, secret, scopedToken, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).To(HaveOccurred())
		Expect(id).To(Equal("invalid"))
		Expect(secret).To(BeEmpty())
		Expect(scopedToken).To(Equal(registrationToken))
		Expect(err.Error()).ToNot(ContainSubstring(registrationToken))
	})

	It("deletes only the named client with its registration token", func() {
		Expect(client.DeleteServiceAccountClient(context.Background(), clientID, clientUUID, registrationToken)).To(Succeed())
		Expect(calls).To(Equal([]string{"DELETE " + registrationPath + "/" + clientID}))
	})

	It("rotates the secret with the per-client registration token and preserves client settings", func() {
		secret, updatedToken, err := client.RotateServiceAccountClientSecret(
			context.Background(), clientID, clientUUID, registrationToken)
		Expect(err).ToNot(HaveOccurred())
		Expect(secret).ToNot(BeEmpty())
		Expect(sha256.Sum256([]byte(secret))).ToNot(Equal(sha256.Sum256([]byte("old-client-secret"))))
		Expect(updatedToken).To(Equal("rotated-registration-token"))
		Expect(calls).To(Equal([]string{
			"GET " + registrationPath + "/" + clientID,
			"PUT " + registrationPath + "/" + clientID,
		}))
		Expect(updatedRepresentation).To(HaveKey("secret"))
		Expect(updatedRepresentation).ToNot(HaveKey("registrationAccessToken"))
		Expect(updatedRepresentation).To(HaveKey("enabled"))
		var sentSecret string
		Expect(json.Unmarshal(updatedRepresentation["secret"], &sentSecret)).To(Succeed())
		Expect(sha256.Sum256([]byte(sentSecret))).To(Equal(sha256.Sum256([]byte(secret))))
	})

	It("does not update a client when its registration token resolves to another identity", func() {
		rotationClientUUID = "00000000-0000-0000-0000-000000000000"
		_, _, err := client.RotateServiceAccountClientSecret(context.Background(), clientID, clientUUID, registrationToken)
		Expect(err).To(HaveOccurred())
		Expect(calls).To(Equal([]string{"GET " + registrationPath + "/" + clientID}))
	})

	It("rejects invalid rotation credentials before calling Keycloak", func() {
		for _, test := range []struct {
			clientID   string
			clientUUID string
			token      string
		}{
			{clientID: "../other", clientUUID: clientUUID, token: registrationToken},
			{clientID: clientID, clientUUID: "invalid", token: registrationToken},
			{clientID: clientID, clientUUID: clientUUID},
		} {
			_, _, err := client.RotateServiceAccountClientSecret(context.Background(), test.clientID, test.clientUUID, test.token)
			Expect(err).To(HaveOccurred())
		}
		Expect(calls).To(BeEmpty())
	})

	It("treats an absent client as already cleaned up", func() {
		failure = "DELETE " + registrationPath + "/" + clientID
		failureStatus = http.StatusNotFound
		Expect(client.DeleteServiceAccountClient(context.Background(), clientID, clientUUID, registrationToken)).To(Succeed())
	})

	It("allows cleanup without a Vault token when the admin lookup confirms absence", func() {
		clientMissing = true
		Expect(client.DeleteServiceAccountClient(context.Background(), clientID, clientUUID, "")).To(Succeed())
		Expect(calls).To(Equal([]string{"GET " + adminClientPath}))
	})

	It("keeps cleanup retryable without a Vault token when the client still exists", func() {
		err := client.DeleteServiceAccountClient(context.Background(), clientID, clientUUID, "")
		Expect(err).To(MatchError("service-account client exists without a registration token"))
		Expect(calls).To(Equal([]string{"GET " + adminClientPath}))
	})

	It("treats an unauthorized delete as complete when the admin lookup confirms absence", func() {
		failure = "DELETE " + registrationPath + "/" + clientID
		failureStatus = http.StatusUnauthorized
		clientMissing = true
		Expect(client.DeleteServiceAccountClient(context.Background(), clientID, clientUUID, registrationToken)).To(Succeed())
		Expect(calls).To(Equal([]string{
			"DELETE " + registrationPath + "/" + clientID,
			"GET " + adminClientPath,
		}))
	})

	It("keeps an unauthorized delete retryable when the client still exists", func() {
		failure = "DELETE " + registrationPath + "/" + clientID
		failureStatus = http.StatusUnauthorized
		err := client.DeleteServiceAccountClient(context.Background(), clientID, clientUUID, registrationToken)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("HTTP 401"))
		Expect(calls).To(Equal([]string{
			"DELETE " + registrationPath + "/" + clientID,
			"GET " + adminClientPath,
		}))
	})

	It("keeps forbidden deletes retryable without an absence check", func() {
		failure = "DELETE " + registrationPath + "/" + clientID
		failureStatus = http.StatusForbidden
		err := client.DeleteServiceAccountClient(context.Background(), clientID, clientUUID, registrationToken)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("HTTP 403"))
		Expect(calls).To(Equal([]string{"DELETE " + registrationPath + "/" + clientID}))
	})

	It("keeps an unauthorized delete retryable when the absence check is unauthorized", func() {
		failure = "DELETE " + registrationPath + "/" + clientID
		failureStatus = http.StatusUnauthorized
		existenceFailureStatus = http.StatusForbidden
		err := client.DeleteServiceAccountClient(context.Background(), clientID, clientUUID, registrationToken)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("HTTP 401"))
		Expect(err.Error()).To(ContainSubstring("HTTP 403"))
	})

	It("sanitizes registration failures and rejects unsafe client identities", func() {
		failure = "DELETE " + registrationPath + "/" + clientID
		Expect(client.DeleteServiceAccountClient(context.Background(), clientID, clientUUID, registrationToken)).To(HaveOccurred())
		for _, id := range []string{"", "../other", "osac-admin", "client/uuid"} {
			Expect(client.DeleteServiceAccountClient(context.Background(), id, clientUUID, registrationToken)).To(HaveOccurred())
		}
		Expect(strings.Join(calls, " ")).To(Equal(failure))
		Expect(calls).To(HaveLen(1))
	})

	It("keeps unauthorized deletes retryable when the stored client UUID is invalid", func() {
		failure = "DELETE " + registrationPath + "/" + clientID
		failureStatus = http.StatusUnauthorized
		err := client.DeleteServiceAccountClient(context.Background(), clientID, "invalid", registrationToken)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("HTTP 401"))
		Expect(calls).To(Equal([]string{"DELETE " + registrationPath + "/" + clientID}))
	})

	It("rejects invalid generated identities before registration", func() {
		for _, id := range []string{"", "osac-sa-tenant-b-" + clientUUID, "osac-sa-tenant-a-not-a-uuid"} {
			_, _, _, err := client.CreateServiceAccountClient(context.Background(), id, "tenant-a")
			Expect(err).To(HaveOccurred())
		}
		Expect(calls).To(BeEmpty())
	})
})
