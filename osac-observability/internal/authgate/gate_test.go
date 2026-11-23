package authgate

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	testIssuer   = "https://keycloak.example.com/realms/osac"
	testAudience = "osac-ui"
	testRole     = "cloud-provider-admin"
	testCookie   = "_oauth2_proxy"
)

func TestGateReturnsUnauthorizedWithoutACookieOrBearerToken(t *testing.T) {
	gate, proxy, allowlist := newTestGate(t, &stubAuth{})
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/query?query=up", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if proxy.hits.Load() != 0 || allowlist.hits.Load() != 0 {
		t.Fatalf("upstream reached without credentials: proxy=%d allowlist=%d",
			proxy.hits.Load(), allowlist.hits.Load())
	}
}

func TestGateForwardsASessionCookieToTheOAuthProxy(t *testing.T) {
	gate, proxy, allowlist := newTestGate(t, &stubAuth{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query", nil)
	req.AddCookie(&http.Cookie{Name: testCookie, Value: "session"})
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if proxy.hits.Load() != 1 || allowlist.hits.Load() != 0 {
		t.Fatalf("routing: proxy=%d allowlist=%d", proxy.hits.Load(), allowlist.hits.Load())
	}
}

func TestGateExemptsTheOAuthCallbackAndHealthPaths(t *testing.T) {
	gate, proxy, _ := newTestGate(t, &stubAuth{})
	for _, path := range []string{"/oauth2/callback", "/oauth2/sign_in", "/healthz", "/ping"} {
		proxy.hits.Store(0)
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		if proxy.hits.Load() != 1 {
			t.Fatalf("%s did not reach the OAuth proxy", path)
		}
	}
}

func TestGateAnswersItsOwnLivenessProbe(t *testing.T) {
	gate, proxy, allowlist := newTestGate(t, &stubAuth{})
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if proxy.hits.Load() != 0 || allowlist.hits.Load() != 0 {
		t.Fatalf("liveness probe reached an upstream")
	}
}

func TestGateDoesNotExemptACleanedOAuthPath(t *testing.T) {
	gate, proxy, allowlist := newTestGate(t, &stubAuth{})
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth2/../api/v1/query", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if proxy.hits.Load() != 0 || allowlist.hits.Load() != 0 {
		t.Fatalf("upstream reached through a path traversal")
	}
}

func TestGateRejectsAnInvalidBearerToken(t *testing.T) {
	gate, proxy, allowlist := newTestGate(t, &stubAuth{err: ErrUnauthenticated})
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, bearerRequest("/api/v1/query", "bad"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if proxy.hits.Load() != 0 || allowlist.hits.Load() != 0 {
		t.Fatalf("upstream reached with a bad token")
	}
}

func TestGateForbidsABearerTokenWithoutTheRole(t *testing.T) {
	gate, proxy, allowlist := newTestGate(t, &stubAuth{err: ErrForbidden})
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, bearerRequest("/api/v1/query", "valid"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if proxy.hits.Load() != 0 || allowlist.hits.Load() != 0 {
		t.Fatalf("upstream reached without the role")
	}
}

func TestGateForwardsAnAuthorizedBearerTokenToTheAllowlist(t *testing.T) {
	gate, proxy, allowlist := newTestGate(t, &stubAuth{})
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, bearerRequest("/api/v1/query", "valid"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if allowlist.hits.Load() != 1 || proxy.hits.Load() != 0 {
		t.Fatalf("routing: proxy=%d allowlist=%d", proxy.hits.Load(), allowlist.hits.Load())
	}
}

// --- Keycloak JWT authentication -------------------------------------------

type jwksServer struct {
	*httptest.Server
	key *rsa.PrivateKey
	kid string
}

func newJWKS(t *testing.T) *jwksServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	j := &jwksServer{key: key, kid: "test-kid"}
	mux := http.NewServeMux()
	mux.HandleFunc("/realms/osac/protocol/openid-connect/certs", func(w http.ResponseWriter, _ *http.Request) {
		writeJWKS(t, w, j.key, j.kid)
	})
	j.Server = httptest.NewServer(mux)
	t.Cleanup(j.Close)
	return j
}

func writeJWKS(t *testing.T, w http.ResponseWriter, key *rsa.PrivateKey, kid string) {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]string{{"kty": "RSA", "kid": kid, "alg": "RS256", "n": n, "e": e}},
	}); err != nil {
		t.Fatalf("write jwks: %v", err)
	}
}

func (j *jwksServer) issuer() string { return j.URL + "/realms/osac" }

func (j *jwksServer) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	return signToken(t, j.key, j.kid, claims)
}

func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	if err != nil {
		t.Fatalf("header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func testClaims(issuer string, extra map[string]any) map[string]any {
	claims := map[string]any{
		"iss": issuer,
		"aud": testAudience,
		"exp": time.Now().Add(time.Hour).Unix(),
		"realm_access": map[string]any{
			"roles": []string{testRole},
		},
	}
	for k, v := range extra {
		claims[k] = v
	}
	return claims
}

func newKeycloakAuthenticator(t *testing.T, issuer string) *KeycloakAuthenticator {
	t.Helper()
	auth, err := NewKeycloakAuthenticator(KeycloakConfig{
		Issuer:    issuer,
		Audiences: []string{testAudience, "osac-cli"},
		AdminRole: testRole,
	})
	if err != nil {
		t.Fatalf("NewKeycloakAuthenticator: %v", err)
	}
	return auth
}

func TestKeycloakAcceptsATokenWithTheRealmRole(t *testing.T) {
	jwks := newJWKS(t)
	auth := newKeycloakAuthenticator(t, jwks.issuer())
	if err := auth.Authenticate(context.Background(), jwks.token(t, testClaims(jwks.issuer(), nil))); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
}

// TestKeycloakKeepsTheCachedKeyWhenRefreshFails pins that a Keycloak blip
// after the JWKS cache goes stale does not reject every still-valid token: the
// cached key is enough while it is the key the token names.
func TestKeycloakKeepsTheCachedKeyWhenRefreshFails(t *testing.T) {
	jwks := newJWKS(t)
	now := time.Now()
	auth, err := NewKeycloakAuthenticator(KeycloakConfig{
		Issuer:    jwks.issuer(),
		Audiences: []string{testAudience, "osac-cli"},
		AdminRole: testRole,
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewKeycloakAuthenticator: %v", err)
	}
	token := jwks.token(t, testClaims(jwks.issuer(), nil))
	if err := auth.Authenticate(context.Background(), token); err != nil {
		t.Fatalf("Authenticate with the realm reachable: %v", err)
	}

	// Expire the cache, then take the realm's JWKS endpoint away.
	now = now.Add(keyCacheTTL + time.Minute)
	jwks.Close()
	if err := auth.Authenticate(context.Background(), token); err != nil {
		t.Fatalf("Authenticate with a cached key: %v", err)
	}
}

func TestKeycloakRejectsATokenWithoutTheRealmRole(t *testing.T) {
	jwks := newJWKS(t)
	auth := newKeycloakAuthenticator(t, jwks.issuer())
	token := jwks.token(t, testClaims(jwks.issuer(), map[string]any{
		"realm_access": map[string]any{"roles": []string{"tenant-user"}},
	}))
	if err := auth.Authenticate(context.Background(), token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestKeycloakRejectsAnExpiredToken(t *testing.T) {
	jwks := newJWKS(t)
	auth := newKeycloakAuthenticator(t, jwks.issuer())
	token := jwks.token(t, testClaims(jwks.issuer(), map[string]any{
		"exp": time.Now().Add(-time.Minute).Unix(),
	}))
	if err := auth.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}

func TestKeycloakRejectsATokenForAnotherAudience(t *testing.T) {
	jwks := newJWKS(t)
	auth := newKeycloakAuthenticator(t, jwks.issuer())
	token := jwks.token(t, testClaims(jwks.issuer(), map[string]any{"aud": "someone-else"}))
	if err := auth.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}

func TestKeycloakAcceptsATokenMintedForALoginClient(t *testing.T) {
	// Keycloak does not always echo the requesting client in aud; azp always
	// names it. A token minted by an OSAC login must still be accepted.
	jwks := newJWKS(t)
	auth := newKeycloakAuthenticator(t, jwks.issuer())
	token := jwks.token(t, testClaims(jwks.issuer(), map[string]any{
		"aud": "account",
		"azp": "osac-cli",
	}))
	if err := auth.Authenticate(context.Background(), token); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
}

func TestKeycloakRejectsATokenMintedForTheBrowserClient(t *testing.T) {
	jwks := newJWKS(t)
	auth := newKeycloakAuthenticator(t, jwks.issuer())
	token := jwks.token(t, testClaims(jwks.issuer(), map[string]any{
		"aud": "account",
		"azp": "thanos-admin",
	}))
	if err := auth.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}

func TestKeycloakRejectsATokenFromAnotherIssuer(t *testing.T) {
	jwks := newJWKS(t)
	auth := newKeycloakAuthenticator(t, jwks.issuer())
	token := jwks.token(t, testClaims("https://evil.example.com/realms/osac", nil))
	if err := auth.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}

func TestKeycloakRejectsABadSignature(t *testing.T) {
	jwks := newJWKS(t)
	auth := newKeycloakAuthenticator(t, jwks.issuer())
	token := jwks.token(t, testClaims(jwks.issuer(), nil))
	if err := auth.Authenticate(context.Background(), token[:len(token)-4]+"AAAA"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}

// --- OpenShift TokenReview and group membership ----------------------------

var groupGVR = schema.GroupVersionResource{Group: "user.openshift.io", Version: "v1", Resource: "groups"}

func newOpenShiftAuthenticator(t *testing.T, review *authv1.TokenReview, groups ...*unstructured.Unstructured) *OpenShiftAuthenticator {
	t.Helper()
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{groupGVR: "GroupList"}
	objs := make([]runtime.Object, 0, len(groups))
	for _, g := range groups {
		objs = append(objs, g)
	}
	client := fake.NewSimpleClientset()
	// TokenReviews are created, not read, and their name is assigned by the API
	// server; a reactor is the fake's supported way to answer with a status.
	client.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		created := action.(k8stesting.CreateAction).GetObject().(*authv1.TokenReview)
		out := created.DeepCopy()
		out.Status = review.Status
		return true, out, nil
	})
	return &OpenShiftAuthenticator{
		Client:  client,
		Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...),
		Group:   "osac-cloud-infrastructure-admins",
	}
}

func groupObject(name string, users ...string) *unstructured.Unstructured {
	g := &unstructured.Unstructured{}
	g.SetGroupVersionKind(schema.GroupVersionKind{Group: "user.openshift.io", Version: "v1", Kind: "Group"})
	g.SetName(name)
	_ = unstructured.SetNestedStringSlice(g.Object, users, "users")
	return g
}

func authenticatedReview(username string, groups ...string) *authv1.TokenReview {
	return &authv1.TokenReview{
		Status: authv1.TokenReviewStatus{
			Authenticated: true,
			User:          authv1.UserInfo{Username: username, Groups: groups},
		},
	}
}

func TestOpenShiftAcceptsAGroupMember(t *testing.T) {
	auth := newOpenShiftAuthenticator(t, authenticatedReview("alice"),
		groupObject("osac-cloud-infrastructure-admins", "alice"))
	if err := auth.Authenticate(context.Background(), "token"); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
}

func TestOpenShiftRejectsANonMember(t *testing.T) {
	auth := newOpenShiftAuthenticator(t, authenticatedReview("alice"),
		groupObject("osac-cloud-infrastructure-admins", "bob"))
	if err := auth.Authenticate(context.Background(), "token"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestOpenShiftRejectsAnUnauthenticatedToken(t *testing.T) {
	auth := newOpenShiftAuthenticator(t, &authv1.TokenReview{
		Status: authv1.TokenReviewStatus{Authenticated: false},
	})
	if err := auth.Authenticate(context.Background(), "token"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}

func TestOpenShiftAcceptsGroupsFromTheTokenReview(t *testing.T) {
	auth := newOpenShiftAuthenticator(t, authenticatedReview("alice", "osac-cloud-infrastructure-admins"))
	if err := auth.Authenticate(context.Background(), "token"); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
}

func TestOpenShiftRejectsAMissingGroupObject(t *testing.T) {
	auth := newOpenShiftAuthenticator(t, authenticatedReview("alice"))
	if err := auth.Authenticate(context.Background(), "token"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

// --- helpers ---------------------------------------------------------------

type stubAuth struct{ err error }

func (s *stubAuth) Authenticate(context.Context, string) error { return s.err }

type countingServer struct {
	*httptest.Server
	hits atomic.Int64
}

func newCountingServer(t *testing.T, handler func(http.ResponseWriter, *http.Request)) *countingServer {
	t.Helper()
	c := &countingServer{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		if handler != nil {
			handler(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)
	return c
}

func newTestGate(t *testing.T, auth Authenticator) (*Gate, *countingServer, *countingServer) {
	t.Helper()
	proxy := newCountingServer(t, nil)
	allowlist := newCountingServer(t, nil)
	gate, err := New(Config{
		ProxyURL:          proxy.URL,
		AllowlistURL:      allowlist.URL,
		SessionCookieName: testCookie,
		Authenticator:     auth,
		ExemptPaths:       []string{"/oauth2/", "/healthz", "/ping"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gate, proxy, allowlist
}

func bearerRequest(path, token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}
