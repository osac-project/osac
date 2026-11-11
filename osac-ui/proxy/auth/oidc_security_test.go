package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchOIDCConfigRejectsInvalidIssuerURLs(t *testing.T) {
	for _, issuer := range []string{
		"http://issuer.example/realms/osac",
		"https://user@issuer.example/realms/osac",
		"https://issuer.example/realms/osac?tenant=one",
		"https://issuer.example/realms/osac#fragment",
		"relative/issuer",
	} {
		t.Run(issuer, func(t *testing.T) {
			if _, err := FetchOIDCConfig(issuer, nil); err == nil {
				t.Fatal("invalid issuer URL was accepted")
			}
		})
	}
}

func TestFetchOIDCConfigDefaultClientRejectsLoopbackResolution(t *testing.T) {
	var requests atomic.Int32
	issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer issuerServer.Close()

	if _, err := FetchOIDCConfig(issuerServer.URL, nil); err == nil {
		t.Fatal("default OIDC client accepted a loopback issuer")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("loopback issuer received %d requests, want none", got)
	}
}

func TestFetchOIDCConfigRejectsMetadataIssuerMismatch(t *testing.T) {
	var issuer string
	issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OIDCConfig{
			Issuer:             "https://attacker.example/realms/evil",
			TokenEndpoint:      issuer + "/token",
			EndSessionEndpoint: issuer + "/logout",
		})
	}))
	defer issuerServer.Close()
	issuer = issuerServer.URL + "/issuer-mismatch"

	if _, err := FetchOIDCConfig(issuer, issuerServer.Client()); err == nil {
		t.Fatal("discovery document with a different issuer was accepted")
	}
}

func TestFetchOIDCConfigRejectsCrossOriginMetadataEndpoints(t *testing.T) {
	const issuerPath = "/cross-origin-token"
	var otherOriginRequests atomic.Int32
	otherOrigin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherOriginRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer otherOrigin.Close()

	var issuer string
	issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != issuerPath+"/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OIDCConfig{
			Issuer:                issuer,
			AuthorizationEndpoint: issuer + "/authorize",
			TokenEndpoint:         otherOrigin.URL + "/token",
			EndSessionEndpoint:    issuer + "/logout",
		})
	}))
	defer issuerServer.Close()
	issuer = issuerServer.URL + issuerPath

	if _, err := FetchOIDCConfig(issuer, issuerServer.Client()); err == nil {
		t.Fatal("cross-origin token endpoint was accepted")
	}
	if got := otherOriginRequests.Load(); got != 0 {
		t.Fatalf("made %d requests to the untrusted origin, want none", got)
	}
}

func TestFetchOIDCConfigDefersCrossOriginOptionalLogoutEndpoint(t *testing.T) {
	const issuerPath = "/optional-cross-origin-logout"
	var otherOriginRequests atomic.Int32
	otherOrigin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherOriginRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer otherOrigin.Close()

	var issuer string
	issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != issuerPath+"/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OIDCConfig{
			Issuer:                issuer,
			AuthorizationEndpoint: issuer + "/authorize",
			TokenEndpoint:         issuer + "/token",
			EndSessionEndpoint:    otherOrigin.URL + "/logout",
		})
	}))
	defer issuerServer.Close()
	issuer = issuerServer.URL + issuerPath

	config, err := FetchOIDCConfig(issuer, issuerServer.Client())
	if err != nil {
		t.Fatalf("OIDC discovery rejected an optional logout endpoint: %v", err)
	}
	if err := EndSession(config, "osac-ui", "refresh-token", "id-token", issuerServer.Client()); err == nil {
		t.Fatal("cross-origin end-session endpoint was accepted")
	}
	if got := otherOriginRequests.Load(); got != 0 {
		t.Fatalf("made %d requests to the untrusted origin, want none", got)
	}
}

func TestFetchOIDCConfigDoesNotFollowCrossOriginDiscoveryRedirect(t *testing.T) {
	var redirectRequests atomic.Int32
	redirectTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()

	issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+"/discovery", http.StatusTemporaryRedirect)
	}))
	defer issuerServer.Close()
	issuer := issuerServer.URL + "/cross-origin-redirect"

	if _, err := FetchOIDCConfig(issuer, issuerServer.Client()); err == nil {
		t.Fatal("redirected OIDC discovery unexpectedly succeeded")
	}
	if got := redirectRequests.Load(); got != 0 {
		t.Fatalf("redirect target received %d requests, want none", got)
	}
}

func TestFetchOIDCConfigAllowsSameOriginDiscoveryRedirect(t *testing.T) {
	const issuerPath = "/same-origin-redirect"
	var issuer string
	issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == issuerPath+"/.well-known/openid-configuration" {
			http.Redirect(w, r, issuerPath+"/.well-known/openid-configuration/", http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path != issuerPath+"/.well-known/openid-configuration/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OIDCConfig{
			Issuer:                issuer,
			AuthorizationEndpoint: issuer + "/authorize",
			TokenEndpoint:         issuer + "/token",
		})
	}))
	defer issuerServer.Close()
	issuer = issuerServer.URL + issuerPath

	if _, err := FetchOIDCConfig(issuer, issuerServer.Client()); err != nil {
		t.Fatalf("same-origin discovery redirect failed: %v", err)
	}
}

func TestOIDCRedirectPolicyRejectsDowngradeAndUserInfo(t *testing.T) {
	issuer, err := url.Parse("https://keycloak.example.com/realms/osac")
	if err != nil {
		t.Fatal(err)
	}
	client := withOIDCIssuerRedirects(nil, issuer)
	for _, rawURL := range []string{
		"http://keycloak.example.com/realms/osac/token",
		"https://user@keycloak.example.com/realms/osac/token",
	} {
		t.Run(rawURL, func(t *testing.T) {
			target, err := url.Parse(rawURL)
			if err != nil {
				t.Fatal(err)
			}
			err = client.CheckRedirect(&http.Request{URL: target}, []*http.Request{{URL: issuer}})
			if err != http.ErrUseLastResponse {
				t.Fatalf("redirect to %q returned %v, want ErrUseLastResponse", rawURL, err)
			}
		})
	}
}

func TestTokenAndLogoutRequestsRejectCrossOriginEndpoints(t *testing.T) {
	var requests atomic.Int32
	otherOrigin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer otherOrigin.Close()

	config := &OIDCConfig{
		Issuer:             "https://keycloak.example.com/realms/osac",
		TokenEndpoint:      otherOrigin.URL + "/token",
		EndSessionEndpoint: otherOrigin.URL + "/logout",
	}
	if _, err := ExchangeCode(config, "osac-ui", "code", "verifier", "https://ui.example.com/callback", otherOrigin.Client()); err == nil {
		t.Fatal("cross-origin token endpoint was accepted")
	}
	if err := EndSession(config, "osac-ui", "refresh-token", "id-token", otherOrigin.Client()); err == nil {
		t.Fatal("cross-origin end-session endpoint was accepted")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("made %d requests to the untrusted origin, want none", got)
	}
}

func TestTokenAndLogoutRequestsDoNotFollowCrossOriginRedirects(t *testing.T) {
	for _, endpoint := range []string{"token", "logout"} {
		t.Run(endpoint, func(t *testing.T) {
			var redirectRequests atomic.Int32
			redirectTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				redirectRequests.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer redirectTarget.Close()

			var issuer string
			issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, redirectTarget.URL+"/target", http.StatusTemporaryRedirect)
			}))
			defer issuerServer.Close()
			issuer = issuerServer.URL + "/realms/osac"
			config := &OIDCConfig{
				Issuer:             issuer,
				TokenEndpoint:      issuerServer.URL + "/token",
				EndSessionEndpoint: issuerServer.URL + "/logout",
			}

			if endpoint == "token" {
				_, err := ExchangeCode(config, "osac-ui", "code", "verifier", "https://ui.example.com/callback", issuerServer.Client())
				if err == nil {
					t.Fatal("redirected token request unexpectedly succeeded")
				}
			} else if err := EndSession(config, "osac-ui", "refresh-token", "id-token", issuerServer.Client()); err == nil {
				t.Fatal("redirected end-session request unexpectedly succeeded")
			}
			if got := redirectRequests.Load(); got != 0 {
				t.Fatalf("redirect target received %d requests, want none", got)
			}
		})
	}
}

func TestDialResolvedOIDCAddressPinsTrustedPrivateDestination(t *testing.T) {
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "keycloak.example.test" {
			t.Fatalf("lookup host = %q, want keycloak.example.test", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.10")}}, nil
	}
	var dialed string
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			t.Fatalf("network = %q, want tcp", network)
		}
		dialed = address
		client, peer := net.Pipe()
		t.Cleanup(func() { _ = peer.Close() })
		return client, nil
	}

	connection, err := dialResolvedOIDCAddress(
		context.Background(), "tcp", "keycloak.example.test:443", lookup, dial,
	)
	if err != nil {
		t.Fatalf("dial resolved issuer: %v", err)
	}
	defer connection.Close()
	if dialed != "10.0.0.10:443" {
		t.Fatalf("dialed %q, want pinned IP address", dialed)
	}
}

func TestDialResolvedOIDCAddressFallsBackWhenFirstAddressStalls(t *testing.T) {
	const firstAddress = "10.0.0.10:443"
	const secondAddress = "10.0.0.11:443"
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("10.0.0.10")},
			{IP: net.ParseIP("10.0.0.11")},
		}, nil
	}
	firstAttempted := make(chan struct{})
	secondAttempted := make(chan struct{})
	dial := func(ctx context.Context, _, address string) (net.Conn, error) {
		switch address {
		case firstAddress:
			close(firstAttempted)
			<-ctx.Done()
			return nil, ctx.Err()
		case secondAddress:
			close(secondAttempted)
			client, peer := net.Pipe()
			t.Cleanup(func() { _ = peer.Close() })
			return client, nil
		default:
			return nil, errors.New("unexpected dial destination")
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	connection, err := dialResolvedOIDCAddress(ctx, "tcp", "issuer.example:443", lookup, dial)
	if err != nil {
		t.Fatalf("dial resolved issuer with fallback: %v", err)
	}
	defer connection.Close()

	for name, attempted := range map[string]<-chan struct{}{
		"first":  firstAttempted,
		"second": secondAttempted,
	} {
		select {
		case <-attempted:
		case <-ctx.Done():
			t.Fatalf("%s address was not attempted before the context expired", name)
		}
	}
}

func TestDialResolvedOIDCAddressRejectsUnsafeDNSAnswers(t *testing.T) {
	for _, rawIP := range []string{
		"127.0.0.1",
		"0.1.2.3",
		"169.254.169.254",
		"100.64.0.1",
		"192.0.2.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"::1",
		"fe80::1",
		"224.0.0.1",
		"255.255.255.255",
		"::",
		"64:ff9b:1::1",
		"64:ff9b::a9fe:a9fe",
		"2001:2::1",
		"2001::1",
		"2001:db8::1",
		"2002:a9fe:a9fe::1",
	} {
		t.Run(rawIP, func(t *testing.T) {
			lookup := func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP(rawIP)}}, nil
			}
			dialCalls := 0
			dial := func(context.Context, string, string) (net.Conn, error) {
				dialCalls++
				return nil, nil
			}

			if _, err := dialResolvedOIDCAddress(context.Background(), "tcp", "issuer.example:443", lookup, dial); err == nil {
				t.Fatal("unsafe DNS answer was accepted")
			}
			if dialCalls != 0 {
				t.Fatalf("attempted %d network dials for unsafe DNS answer", dialCalls)
			}
		})
	}
}

func TestPublicAndPrivateOIDCAddressRangesRemainAllowed(t *testing.T) {
	for _, rawIP := range []string{
		"8.8.8.8",
		"10.0.0.10",
		"172.16.0.10",
		"192.168.0.10",
		"fc00::10",
		"fd00::10",
		"64:ff9b::808:808",
		"2002:808:808::1",
	} {
		t.Run(rawIP, func(t *testing.T) {
			if isUnsafeOIDCAddress(net.ParseIP(rawIP)) {
				t.Fatalf("private unicast address %q was rejected", rawIP)
			}
		})
	}
}

func TestDialResolvedOIDCAddressRejectsMixedSafeAndUnsafeAnswers(t *testing.T) {
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("10.0.0.10")},
			{IP: net.ParseIP("127.0.0.1")},
		}, nil
	}
	dialCalls := 0
	dial := func(context.Context, string, string) (net.Conn, error) {
		dialCalls++
		return nil, nil
	}

	if _, err := dialResolvedOIDCAddress(context.Background(), "tcp", "issuer.example:443", lookup, dial); err == nil {
		t.Fatal("mixed safe and unsafe DNS answers were accepted")
	}
	if dialCalls != 0 {
		t.Fatalf("attempted %d network dials for mixed DNS answers", dialCalls)
	}
}

func TestSameOIDCIssuerNormalizesDefaultPortAndTrailingSlash(t *testing.T) {
	if !sameOIDCIssuer("https://KEYCLOAK.example.com:443/realms/osac/", "https://keycloak.example.com/realms/osac") {
		t.Fatal("equivalent issuer URLs did not match")
	}
	if sameOIDCIssuer("https://keycloak.example.com/realms/osac", "https://attacker.example/realms/osac") {
		t.Fatal("different issuer origins matched")
	}
}
