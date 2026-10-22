/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

var _ = Describe("OAuth token exchange", func() {
	var (
		validator *auth.MockJwtValidator
		incoming  *jwt.Token
		outgoing  *jwt.Token
	)

	BeforeEach(func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		validator = auth.NewMockJwtValidator(ctrl)
		claims := jwt.MapClaims{
			"iss": "https://issuer.example.com/realms/osac", "sub": "user-1",
			"preferred_username": "alice", "groups": []any{"tenant1"},
			"exp": float64(time.Now().Add(time.Minute).Unix()),
		}
		incoming = jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		outgoing = jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	})

	It("exchanges a caller token through a confidential client and accepts only a validated API token for that caller", func() {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			Expect(r.Method).To(Equal(http.MethodPost))
			clientID, secret, ok := r.BasicAuth()
			Expect(ok).To(BeTrue())
			clientID, err := url.QueryUnescape(clientID)
			Expect(err).ToNot(HaveOccurred())
			secret, err = url.QueryUnescape(secret)
			Expect(err).ToNot(HaveOccurred())
			Expect(clientID).To(Equal("https://mcp.example.com/"))
			Expect(secret).To(Equal("fixture-secret"))
			Expect(r.ParseForm()).To(Succeed())
			Expect(r.Form.Get("grant_type")).To(Equal("urn:ietf:params:oauth:grant-type:token-exchange"))
			Expect(r.Form.Get("subject_token")).To(Equal("mcp-token"))
			Expect(r.Form.Get("requested_token_type")).To(Equal("urn:ietf:params:oauth:token-type:access_token"))
			body, err := json.Marshal(map[string]string{
				"access_token": "api-token", "token_type": "Bearer",
				"issued_token_type": "urn:ietf:params:oauth:token-type:access_token",
			})
			Expect(err).ToNot(HaveOccurred())
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		})}
		validator.EXPECT().Validate(gomock.Any(), "api-token").Return(outgoing, nil)
		exchanger, err := newOAuthTokenExchanger("https://issuer.example.com/token", "https://mcp.example.com/", "fixture-secret", client, validator)
		Expect(err).ToNot(HaveOccurred())
		apiToken, err := exchanger.Exchange(context.Background(), "mcp-token", incoming)
		Expect(err).ToNot(HaveOccurred())
		Expect(apiToken).To(Equal("api-token"))
	})

	It("rejects identity or permission expansion in an exchanged token", func() {
		outgoing = jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": "https://issuer.example.com/realms/osac", "sub": "user-1",
			"preferred_username": "alice", "groups": []any{"tenant1", "admin"},
			"exp": float64(time.Now().Add(time.Minute).Unix()),
		})
		Expect(sameCaller(incoming, outgoing)).To(MatchError("OAuth token exchange changed the caller permissions"))
		outgoing = jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": "https://issuer.example.com/realms/osac", "sub": "user-2",
			"exp": float64(time.Now().Add(time.Minute).Unix()),
		})
		Expect(sameCaller(incoming, outgoing)).To(MatchError("OAuth token exchange changed the caller identity"))
	})

	It("caches a repeated exchange until the API token expires", func() {
		now := time.Now()
		subject := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"sub": "user-1", "exp": float64(now.Add(time.Minute).Unix()),
		})
		delegate := &expiringTestTokenExchanger{expiration: now.Add(10 * time.Second)}
		exchanger := newCachingTokenExchanger(delegate).(*cachingTokenExchanger)
		exchanger.now = func() time.Time { return now }

		token, err := exchanger.Exchange(context.Background(), "mcp-token", subject)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(Equal("api-token"))
		token, err = exchanger.Exchange(context.Background(), "mcp-token", subject)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(Equal("api-token"))
		Expect(delegate.calls).To(Equal(1))

		now = now.Add(11 * time.Second)
		token, err = exchanger.Exchange(context.Background(), "mcp-token", subject)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(Equal("api-token"))
		Expect(delegate.calls).To(Equal(2))
	})

	It("returns a canceled waiter while the shared exchange completes", func() {
		now := time.Now()
		subject := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"sub": "user-1", "exp": float64(now.Add(time.Minute).Unix()),
		})
		delegate := &blockingTestTokenExchanger{
			started: make(chan struct{}),
			release: make(chan struct{}),
		}
		exchanger := newCachingTokenExchanger(delegate).(*cachingTokenExchanger)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		results := make(chan error, 1)

		go func() {
			_, err := exchanger.Exchange(ctx, "mcp-token", subject)
			results <- err
		}()

		Eventually(delegate.started).Should(BeClosed())
		cancel()
		Eventually(results).Should(Receive(Equal(context.Canceled)))
		close(delegate.release)
		Eventually(func() bool {
			_, ok := exchanger.cachedToken(cachingTokenKey("mcp-token"), time.Now())
			return ok
		}).Should(BeTrue())

		token, err := exchanger.Exchange(context.Background(), "mcp-token", subject)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(Equal("api-token"))
		Expect(delegate.calls).To(Equal(1))
	})

	It("evicts only the earliest-expiring token when the cache is full", func() {
		now := time.Now()
		subject := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"sub": "user-1", "exp": float64(now.Add(time.Hour).Unix()),
		})
		delegate := &expiringTestTokenExchanger{responses: []expiringTestTokenExchangeResponse{
			{token: "api-token-1", expiration: now.Add(time.Minute)},
			{token: "api-token-2", expiration: now.Add(2 * time.Minute)},
			{token: "api-token-3", expiration: now.Add(3 * time.Minute)},
		}}
		exchanger := newCachingTokenExchanger(delegate).(*cachingTokenExchanger)
		exchanger.ttl = time.Hour
		exchanger.maxEntries = 2
		exchanger.now = func() time.Time { return now }

		for index, subjectToken := range []string{"mcp-token-1", "mcp-token-2", "mcp-token-3"} {
			token, err := exchanger.Exchange(context.Background(), subjectToken, subject)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).To(Equal(fmt.Sprintf("api-token-%d", index+1)))
		}

		_, firstCached := exchanger.cachedToken(cachingTokenKey("mcp-token-1"), now)
		_, secondCached := exchanger.cachedToken(cachingTokenKey("mcp-token-2"), now)
		_, thirdCached := exchanger.cachedToken(cachingTokenKey("mcp-token-3"), now)
		Expect(firstCached).To(BeFalse())
		Expect(secondCached).To(BeTrue())
		Expect(thirdCached).To(BeTrue())
	})

	It("does not include an authorization-server error body in failures", func() {
		client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader("sensitive-token-value"))}, nil
		})}
		exchanger, err := newOAuthTokenExchanger("https://issuer.example.com/token", "https://mcp.example.com/", "fixture-secret", client, validator)
		Expect(err).ToNot(HaveOccurred())
		_, err = exchanger.Exchange(context.Background(), "mcp-token", incoming)
		Expect(err).To(MatchError("OAuth token exchange failed with HTTP 403"))
	})

	It("rejects an exchange response that reuses the MCP token", func() {
		client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			body := `{"access_token":"mcp-token","token_type":"Bearer","issued_token_type":"urn:ietf:params:oauth:token-type:access_token"}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		exchanger, err := newOAuthTokenExchanger("https://issuer.example.com/token", "https://mcp.example.com/", "fixture-secret", client, validator)
		Expect(err).ToNot(HaveOccurred())
		_, err = exchanger.Exchange(context.Background(), "mcp-token", incoming)
		Expect(err).To(MatchError("OAuth token exchange returned an invalid access token response"))
	})
})

var _ = Describe("safeTokenExchangeError", func() {
	It("maps transport unavailability to a retryable service-unavailable envelope", func() {
		err := fmt.Errorf("dial tcp: %w", errOAuthTokenExchangeUnavailable)
		envelope := decodeToolError(safeTokenExchangeError(err))
		Expect(envelope.Category).To(Equal("service_unavailable"))
		Expect(envelope.GRPCCode).To(Equal("Unavailable"))
		Expect(envelope.Message).To(Equal("The authentication service is currently unavailable. Retry after checking service status."))
		Expect(envelope.Retryable).To(BeTrue())
		Expect(envelope.Details).To(Equal([]string{}))
	})

	It("maps other exchange failures to a non-retryable authentication envelope", func() {
		envelope := decodeToolError(safeTokenExchangeError(errors.New("invalid_grant and sensitive-token")))
		Expect(envelope.Category).To(Equal("authentication"))
		Expect(envelope.GRPCCode).To(Equal("Unauthenticated"))
		Expect(envelope.Message).To(Equal("MCP authentication failed. Sign in again and retry."))
		Expect(envelope.Retryable).To(BeFalse())
		Expect(envelope.Details).To(Equal([]string{}))
	})
})

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type expiringTestTokenExchanger struct {
	calls      int
	expiration time.Time
	responses  []expiringTestTokenExchangeResponse
}

type expiringTestTokenExchangeResponse struct {
	token      string
	expiration time.Time
}

func (e *expiringTestTokenExchanger) ExchangeWithExpiration(
	_ context.Context, _ string, _ *jwt.Token,
) (string, time.Time, error) {
	e.calls++
	if len(e.responses) > 0 {
		response := e.responses[e.calls-1]
		return response.token, response.expiration, nil
	}
	return "api-token", e.expiration, nil
}

type blockingTestTokenExchanger struct {
	started chan struct{}
	release chan struct{}
	calls   int
}

func (e *blockingTestTokenExchanger) ExchangeWithExpiration(
	_ context.Context, _ string, _ *jwt.Token,
) (string, time.Time, error) {
	e.calls++
	close(e.started)
	<-e.release
	return "api-token", time.Now().Add(time.Minute), nil
}

func cachingTokenKey(subjectToken string) string {
	digest := sha256.Sum256([]byte(subjectToken))
	return hex.EncodeToString(digest[:])
}
