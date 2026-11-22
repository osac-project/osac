/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package realmauth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	grpcoauth "google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	itesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
	"github.com/osac-project/osac/fulfillment-service/internal/tlsconfig"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	csiClientUUID  = "d5e6f7a8-b9c0-4d1e-2f3a-567890abcdef"
	sentinelName   = "csi-upgrade-sentinel"
	sentinelClient = "csi-upgrade-sentinel-client"
	sentinelUser   = "csi-upgrade-sentinel-user"
	sentinelRole   = "csi-upgrade-sentinel-role"
	sentinelScope  = "csi-upgrade-sentinel-scope"
)

// Reconstruct exactly the missing CSI scope from the pre-fix installer export
// at 8d4d0645318c46105db0cc9bd602e9a4e7d06952. Do not remove the service account's
// assigned role or change fullScopeAllowed. Additional sentinels represent
// persisted operator state absent from the replacement shipped import.
func preparePersistedRealm(f keycloakFixture) {
	GinkgoHelper()
	var realm map[string]any
	Expect(json.Unmarshal(f.corrected, &realm)).To(Succeed())
	mappings := realm["scopeMappings"].([]any)
	retained := []any{}
	removed := 0
	for _, value := range mappings {
		mapping := value.(map[string]any)
		if mapping["client"] == "osac-csi-driver" {
			Expect(mapping["roles"]).To(Equal([]any{"osac-csi-driver"}))
			removed++
		} else {
			retained = append(retained, value)
		}
	}
	Expect(removed).To(Equal(1), "the corrected artifact must contain exactly the CSI mapping")
	realm["scopeMappings"] = append(retained,
		map[string]any{"client": "osac-csi-driver", "roles": []string{sentinelName}},
		map[string]any{"client": sentinelName, "roles": []string{sentinelName}},
	)
	roles := realm["roles"].(map[string]any)
	roles["realm"] = append(roles["realm"].([]any), map[string]any{
		"id": sentinelRole, "name": sentinelName, "composite": false,
		"clientRole": false, "containerId": realm["id"], "attributes": map[string]any{"retained": []string{"yes"}},
	})
	realm["clients"] = append(realm["clients"].([]any), map[string]any{
		"id": sentinelClient, "clientId": sentinelName, "protocol": "openid-connect", "enabled": true,
		"clientAuthenticatorType": "client-secret", "secret": f.sentinelSecret, "publicClient": false,
		"fullScopeAllowed": false, "serviceAccountsEnabled": true, "standardFlowEnabled": false,
		"defaultClientScopes": []string{"roles", "basic", "profile", sentinelName},
		"attributes":          map[string]string{"upgrade-sentinel": "retained"},
	})
	realm["clientScopes"] = append(realm["clientScopes"].([]any), map[string]any{
		"id": sentinelScope, "name": sentinelName, "protocol": "openid-connect",
		"attributes": map[string]string{"display.on.consent.screen": "false"},
	})
	realm["users"] = append(realm["users"].([]any),
		map[string]any{
			"id": sentinelUser, "username": sentinelName, "enabled": true, "emailVerified": true,
			"firstName": "Upgrade", "lastName": "Sentinel", "email": "upgrade-sentinel@example.invalid",
			"realmRoles": []string{sentinelName}, "attributes": map[string]any{"retained": []string{"yes"}},
			"credentials": []any{map[string]any{"type": "password", "value": f.sentinelPassword, "temporary": false}},
		},
		map[string]any{
			"username": "service-account-" + sentinelName, "enabled": true,
			"serviceAccountClientId": sentinelName, "realmRoles": []string{sentinelName},
		},
	)
	// Events expose the count/type of actual mapping writes without recording
	// representations. Only this disposable old-state fixture enables them.
	realm["adminEventsEnabled"], realm["adminEventsDetailsEnabled"] = true, false
	data, err := json.Marshal(realm)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(f.resolved, data, 0600)).To(Succeed())
}

type upgradeHarness struct {
	fixture keycloakFixture
	client  *http.Client
	tls     *tls.Config
	ca      *trust.CertPool
	logger  *slog.Logger
}

func newUpgradeHarness(f keycloakFixture) *upgradeHarness {
	GinkgoHelper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ca, err := trust.NewCertPool().SetLogger(logger).AddFile(f.certFile).Build()
	Expect(err).ToNot(HaveOccurred())
	tlsConfig := tlsconfig.NewClientTLSConfig()
	tlsConfig.RootCAs = ca.Pool()
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	DeferCleanup(transport.CloseIdleConnections)
	return &upgradeHarness{fixture: f, tls: tlsConfig, ca: ca, logger: logger,
		client: &http.Client{Transport: transport, Timeout: 10 * time.Second}}
}

func (h *upgradeHarness) ready(ctx context.Context) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet,
			h.fixture.issuer+"/.well-known/openid-configuration", nil)
		g.Expect(err).ToNot(HaveOccurred())
		response, err := h.client.Do(request)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.Body.Close()).To(Succeed())
		g.Expect(response.StatusCode).To(Equal(http.StatusOK))
	}, 3*time.Minute, time.Second).Should(Succeed())
}

func (h *upgradeHarness) passwordToken(ctx context.Context, realm, username, password string) string {
	GinkgoHelper()
	form := url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"},
		"username": {username}, "password": {password}}
	endpoint := strings.TrimSuffix(h.fixture.issuer, "/realms/osac") + "/realms/" + realm + "/protocol/openid-connect/token"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	Expect(err).ToNot(HaveOccurred())
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h.client.Do(request)
	if err != nil {
		Fail("test credential exchange failed (details withheld)")
	}
	defer func() { Expect(response.Body.Close()).To(Succeed()) }()
	Expect(response.StatusCode).To(Equal(http.StatusOK), "test credential exchange must succeed (body withheld)")
	var token struct {
		AccessToken string `json:"access_token"`
	}
	decoded := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&token) == nil
	Expect(decoded && token.AccessToken != "").To(BeTrue(), "test exchange must return a token (body withheld)")
	return token.AccessToken
}

func (h *upgradeHarness) adminToken(ctx context.Context) string {
	// Standard bootstrap administrator in the disposable master realm. The
	// shipped OSAC realm admin/API client is not assumed to administer Keycloak.
	return h.passwordToken(ctx, "master", "upgrade-test-admin", h.fixture.bootstrapSecret)
}

func (h *upgradeHarness) adminGET(ctx context.Context, path string) any {
	GinkgoHelper()
	endpoint := strings.TrimSuffix(h.fixture.issuer, "/realms/osac") + "/admin/realms/osac/" + path
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	Expect(err).ToNot(HaveOccurred())
	request.Header.Set("Authorization", "Bearer "+h.adminToken(ctx))
	response, err := h.client.Do(request)
	if err != nil {
		Fail("test administrator read failed (details withheld)")
	}
	defer func() { Expect(response.Body.Close()).To(Succeed()) }()
	Expect(response.StatusCode).To(Equal(http.StatusOK), "test administrator read must succeed (body withheld)")
	var value any
	decoded := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&value) == nil
	Expect(decoded).To(BeTrue(), "administrator response must be JSON (body withheld)")
	return value
}

func (h *upgradeHarness) clientToken(ctx context.Context, clientID, secret string) (*oauth2.Token, error) {
	config := clientcredentials.Config{
		ClientID: clientID, ClientSecret: secret, TokenURL: h.fixture.issuer + "/protocol/openid-connect/token",
	}
	// Config.Token creates a new request every time; never reuse TokenSource's
	// cached bearer across restart, apply or rollback boundaries.
	return config.Token(context.WithValue(ctx, oauth2.HTTPClient, h.client))
}

func (h *upgradeHarness) credentialsPreserved(ctx context.Context, source string) {
	GinkgoHelper()
	for _, clientID := range []string{"osac-controller", "osac-admin", "osac-ui-backend", sentinelName} {
		secret := h.fixture.secrets.clients[clientID]
		if clientID == sentinelName {
			secret = h.fixture.sentinelSecret
		}
		token, err := h.clientToken(ctx, clientID, secret)
		issued := err == nil && token != nil && token.AccessToken != ""
		// Preserve the unresolved prerequisites/UI boundary. This is only a
		// credential-validity sentinel, not the pending four-client contracts.
		expected := source != "prerequisites" || clientID != "osac-ui-backend"
		Expect(issued).To(Equal(expected), "unchanged credential validity for %s (values withheld)", clientID)
	}
	passwordIssued := h.passwordToken(ctx, "osac", sentinelName, h.fixture.sentinelPassword) != ""
	Expect(passwordIssued).To(BeTrue())
}

func canonicalSentinel(value any) string {
	switch v := value.(type) {
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			items = append(items, canonicalSentinel(item))
		}
		sort.Strings(items) // These Admin API collections have no order contract.
		data, err := json.Marshal(items)
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	case map[string]any:
		items := map[string]string{}
		for key, item := range v {
			items[key] = canonicalSentinel(item)
		}
		data, err := json.Marshal(items)
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	default:
		data, err := json.Marshal(value)
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
}

func (h *upgradeHarness) sentinels(ctx context.Context) map[string]string {
	GinkgoHelper()
	result := map[string]string{}
	for _, path := range []string{
		"clients/" + sentinelClient, "users/" + sentinelUser, "roles/" + sentinelName,
		"client-scopes/" + sentinelScope, "users/" + sentinelUser + "/role-mappings/realm",
		"clients/" + sentinelClient + "/scope-mappings/realm",
		"clients/" + sentinelClient + "/default-client-scopes", "clients/" + csiClientUUID,
		"clients?clientId=osac-controller", "clients?clientId=osac-admin", "clients?clientId=osac-ui-backend",
	} {
		value := h.adminGET(ctx, path)
		if path == "clients/"+csiClientUUID {
			Expect(value.(map[string]any)["fullScopeAllowed"]).To(BeFalse())
		}
		result[path] = canonicalSentinel(value)
	}
	// Exclude only the mapping under test; all other direct CSI scopes must survive.
	otherScopes := []any{}
	for _, value := range h.adminGET(ctx, "clients/"+csiClientUUID+"/scope-mappings/realm").([]any) {
		if value.(map[string]any)["name"] != "osac-csi-driver" {
			otherScopes = append(otherScopes, value)
		}
	}
	result["unrelated-csi-scopes"] = canonicalSentinel(otherScopes)
	return result
}

type csiObservation struct {
	hasRole     bool
	volumes     codes.Code
	volumeCalls int32
}

func (h *upgradeHarness) observeCSI(ctx context.Context, source, phase string) csiObservation {
	GinkgoHelper()
	token, err := h.clientToken(ctx, "osac-csi-driver", h.fixture.secrets.clients["osac-csi-driver"])
	if err != nil || token == nil || token.AccessToken == "" {
		Fail("fresh CSI issuance must succeed (response withheld)")
	}
	jwks, err := auth.NewJwksCache().SetLogger(h.logger).SetCaPool(h.ca).AddIssuer(h.fixture.issuer).Build()
	Expect(err).ToNot(HaveOccurred())
	validator, err := auth.NewJwtValidator().SetLogger(h.logger).SetJwksCache(jwks).Build()
	Expect(err).ToNot(HaveOccurred())
	authn, err := auth.NewGrpcAuthnInterceptor().SetLogger(h.logger).SetJwtValidator(validator).Build()
	Expect(err).ToNot(HaveOccurred())
	evaluator, err := auth.NewEvaluator().Build()
	Expect(err).ToNot(HaveOccurred())
	authz, err := auth.NewGrpcAuthzInterceptor().SetLogger(h.logger).SetEvaluator(evaluator).Build()
	Expect(err).ToNot(HaveOccurred())
	server := itesting.NewServer(
		grpc.Creds(credentials.NewTLS(tlsconfig.NewServerTLSConfig(itesting.LocalhostCertificate(), nil))),
		grpc.ChainUnaryInterceptor(authn.UnaryServer, authz.UnaryServer),
	)
	volumes, clusters := &volumeHandler{}, &clusterHandler{}
	privatev1.RegisterVolumesServer(server.Registrar(), volumes)
	privatev1.RegisterClustersServer(server.Registrar(), clusters)
	server.Start()
	defer server.Stop()
	conn, err := grpc.NewClient(server.Address(),
		grpc.WithTransportCredentials(credentials.NewTLS(h.tls)),
		grpc.WithPerRPCCredentials(grpcoauth.TokenSource{TokenSource: oauth2.StaticTokenSource(token)}),
	)
	Expect(err).ToNot(HaveOccurred())
	defer func() { Expect(conn.Close()).To(Succeed()) }()
	rpcCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, listErr := privatev1.NewVolumesClient(conn).List(rpcCtx, &privatev1.VolumesListRequest{})
	_, unrelatedErr := privatev1.NewClustersClient(conn).List(rpcCtx, &privatev1.ClustersListRequest{})
	validated, err := validator.Validate(ctx, token.AccessToken)
	Expect(err).ToNot(HaveOccurred())
	identity, err := auth.ExtractAuthContext(validated)
	Expect(err).ToNot(HaveOccurred())
	roles, _ := identity.RealmAccess["roles"].([]any)
	hasRole := false
	for _, role := range roles {
		if role == "osac-csi-driver" {
			hasRole = true
		}
	}
	_, err = fmt.Fprintf(GinkgoWriter,
		"realm=%s phase=%s fresh_token=true jwt_verified=true csi_role=%t fullScopeAllowed=%t "+
			"Volumes/List=%s Clusters/List=%s volume_handler_calls=%d cluster_handler_calls=%d\n",
		source, phase, hasRole, h.fixture.fullScopeAllowed, status.Code(listErr), status.Code(unrelatedErr),
		volumes.calls.Load(), clusters.calls.Load())
	Expect(err).ToNot(HaveOccurred())
	Expect(h.fixture.fullScopeAllowed).To(BeFalse())
	Expect(status.Code(unrelatedErr)).To(Equal(codes.PermissionDenied))
	Expect(clusters.calls.Load()).To(BeZero())
	return csiObservation{hasRole: hasRole, volumes: status.Code(listErr), volumeCalls: volumes.calls.Load()}
}

var _ = Describe("Persisted CSI realm upgrade", func() {
	for _, source := range []string{"chart", "prerequisites"} {
		It("restores private CSI authorization for "+source, func(ctx SpecContext) {
			fixture := startKeycloakImport(ctx, source, true)
			h := newUpgradeHarness(fixture)
			h.ready(ctx)
			h.credentialsPreserved(ctx, source)
			before := h.sentinels(ctx)
			initial := h.observeCSI(ctx, source, "persisted-pre-fix")
			Expect(initial).To(Equal(csiObservation{volumes: codes.PermissionDenied}))

			Expect(os.WriteFile(fixture.resolved, fixture.corrected, 0644)).To(Succeed())
			started := runContainerCommand(ctx, fixture.tool, "inspect", "--format", "{{.State.StartedAt}}", fixture.id)
			runContainerCommand(ctx, fixture.tool, "stop", "--time", "20", fixture.id)
			runContainerCommand(ctx, fixture.tool, "start", fixture.id)
			h.ready(ctx)
			restarted := runContainerCommand(ctx, fixture.tool, "inspect", "--format", "{{.State.StartedAt}}", fixture.id)
			Expect(restarted).ToNot(Equal(started), "the same persistent container must actually restart")
			h.credentialsPreserved(ctx, source)
			Expect(reflect.DeepEqual(h.sentinels(ctx), before)).To(BeTrue(), "sentinel state must survive (content withheld)")
			after := h.observeCSI(ctx, source, "corrected-import-restart")
			// Product RED: reach the generated service with a fresh, verified token
			// before asserting desired CSI authorization. No missing tool or setup
			// failure substitutes for this persisted-state regression.
			Expect(after.volumes).To(Equal(codes.OK), "existing realms need the CSI role-scope update")
			Expect(after.volumeCalls).To(Equal(int32(1)))
			Expect(after.hasRole).To(BeTrue())
		}, SpecTimeout(8*time.Minute))
	}
})
