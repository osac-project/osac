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
	"encoding/json"
	"errors"
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

	It("rejects exchanged API tokens that change the caller", func() {
		cases := []struct {
			name    string
			claims  jwt.MapClaims
			wantErr string
		}{
			{
				name: "permissions",
				claims: jwt.MapClaims{
					"iss": "https://issuer.example.com/realms/osac", "sub": "user-1",
					"preferred_username": "alice", "groups": []any{"tenant1", "admin"},
					"exp": float64(time.Now().Add(time.Minute).Unix()),
				},
				wantErr: "OAuth token exchange changed the caller permissions",
			},
			{
				name: "subject",
				claims: jwt.MapClaims{
					"iss": "https://issuer.example.com/realms/osac", "sub": "user-2",
					"exp": float64(time.Now().Add(time.Minute).Unix()),
				},
				wantErr: "OAuth token exchange changed the caller identity",
			},
			{
				name: "issuer",
				claims: jwt.MapClaims{
					"iss": "https://other-issuer.example.com", "sub": "user-1",
					"preferred_username": "alice", "groups": []any{"tenant1"},
					"exp": float64(time.Now().Add(time.Minute).Unix()),
				},
				wantErr: "OAuth token exchange changed the caller identity",
			},
			{
				name: "expiration",
				claims: jwt.MapClaims{
					"iss": "https://issuer.example.com/realms/osac", "sub": "user-1",
					"preferred_username": "alice", "groups": []any{"tenant1"},
					"exp": float64(time.Now().Add(-time.Minute).Unix()),
				},
				wantErr: "OAuth token exchange returned an expired API token",
			},
		}

		for _, test := range cases {
			By(test.name)
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				body := `{"access_token":"api-token","token_type":"Bearer","issued_token_type":"urn:ietf:params:oauth:token-type:access_token"}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			})}
			validator.EXPECT().Validate(gomock.Any(), "api-token").Return(
				jwt.NewWithClaims(jwt.SigningMethodRS256, test.claims), nil,
			)
			exchanger, err := newOAuthTokenExchanger(
				"https://issuer.example.com/token", "https://mcp.example.com/", "fixture-secret", client, validator,
			)
			Expect(err).ToNot(HaveOccurred())

			apiToken, err := exchanger.Exchange(context.Background(), "mcp-token", incoming)
			Expect(err).To(MatchError(test.wantErr))
			Expect(apiToken).To(BeEmpty())
		}
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

	It("classifies retryable authorization-server responses separately from authentication failures", func() {
		cases := []struct {
			name         string
			statusCode   int
			transportErr error
			category     string
			grpcCode     string
			retryable    bool
		}{
			{
				name: "server failure", statusCode: http.StatusServiceUnavailable,
				category: "service_unavailable", grpcCode: "Unavailable", retryable: true,
			},
			{
				name: "rate limit", statusCode: http.StatusTooManyRequests,
				category: "service_unavailable", grpcCode: "Unavailable", retryable: true,
			},
			{
				name: "invalid credentials", statusCode: http.StatusUnauthorized,
				category: "authentication", grpcCode: "Unauthenticated", retryable: false,
			},
			{
				name: "authorization server is unreachable", transportErr: errors.New("connection refused"),
				category: "service_unavailable", grpcCode: "Unavailable", retryable: true,
			},
		}

		for _, testCase := range cases {
			By(testCase.name)
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				if testCase.transportErr != nil {
					return nil, testCase.transportErr
				}
				return &http.Response{
					StatusCode: testCase.statusCode,
					Body:       io.NopCloser(strings.NewReader("authorization-server response")),
				}, nil
			})}
			exchanger, err := newOAuthTokenExchanger(
				"https://issuer.example.com/token", "https://mcp.example.com/", "fixture-secret", client, validator,
			)
			Expect(err).ToNot(HaveOccurred())

			var exchangeErr error
			request := requestWithCaller("raw-bearer-value", safeCallerContext{Subject: "user-1"})
			request.Extra.TokenInfo.Extra[exchangeTokenExtraKey] = apiTokenSource(func(ctx context.Context) (string, error) {
				apiToken, err := exchanger.Exchange(ctx, "mcp-token", incoming)
				exchangeErr = err
				return apiToken, err
			})
			handler := handleCheckConnection(
				"https://mcp.example.com/", newResourceRegistry(ServerDeps{}), checkConnectionToolRegistry(),
				&mockTenantsClient{}, &mockSelfSubjectAccessReviewsClient{},
			)
			_, _, err = handler(context.Background(), request, CheckConnectionInput{})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(exchangeErr, errOAuthTokenExchangeUnavailable)).To(Equal(testCase.retryable))

			envelope := decodeToolError(err)
			Expect(envelope.Category).To(Equal(testCase.category))
			Expect(envelope.GRPCCode).To(Equal(testCase.grpcCode))
			Expect(envelope.Retryable).To(Equal(testCase.retryable))
		}
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
