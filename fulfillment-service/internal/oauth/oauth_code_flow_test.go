/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package oauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/ghttp"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/text"
)

var _ = Describe("OAuth code flow", func() {
	var (
		ctx      context.Context
		ctrl     *gomock.Controller
		store    auth.TokenStore
		server   *Server
		listener *MockFlowListener
	)

	BeforeEach(func() {
		var err error

		// Create the context:
		ctx = context.Background()

		// Create the mock controller:
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		// Create an empty token store:
		store, err = auth.NewMemoryTokenStore().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the server that responds to the discovery requests any number of times. Other responses will
		// be added in specific tests.
		server = NewServer()
		DeferCleanup(server.Close)
		server.RouteToHandler(
			http.MethodGet,
			"/.well-known/oauth-authorization-server",
			RespondWithJSONEncoded(
				http.StatusOK,
				&ServerMetadata{
					Issuer:                server.URL(),
					TokenEndpoint:         fmt.Sprintf("%s/token", server.URL()),
					AuthorizationEndpoint: fmt.Sprintf("%s/auth", server.URL()),
				},
				http.Header{
					"Content-Type": {
						"application/json",
					},
				},
			),
		)

		// Create a mock listener that accepts any event:
		listener = NewMockFlowListener(ctrl)
		listener.EXPECT().Start(gomock.Any(), gomock.Any()).
			Return(nil).
			AnyTimes()
		listener.EXPECT().End(gomock.Any(), gomock.Any()).
			Return(nil).
			AnyTimes()
	})

	// sendRedirect sends a redirect request to the given URI with the the given query parameters.
	sendRedirect := func(redirectUri string, redirectQuery url.Values) {
		redirectUri = fmt.Sprintf("%s?%s", redirectUri, redirectQuery.Encode())
		response, err := http.Get(redirectUri)
		Expect(err).ToNot(HaveOccurred())
		_, err = io.Copy(io.Discard, response.Body)
		Expect(err).ToNot(HaveOccurred())
		err = response.Body.Close()
		Expect(err).ToNot(HaveOccurred())
	}

	// redirectWithOverrides sends a request to the redirect URI with the the required query parameters,
	// and with the possibility to override some of them.
	redirectWithOverrides := func(authUri string, overrides map[string]any) {
		defer GinkgoRecover()

		// Parse the URI:
		parsedAuthUri, err := url.Parse(authUri)
		Expect(err).ToNot(HaveOccurred())

		// Get the redirect URI from the query parameters:
		authQuery := parsedAuthUri.Query()
		redirectUri := authQuery.Get("redirect_uri")
		Expect(redirectUri).ToNot(BeEmpty())

		// Get the state and the PKCE code challenge:
		state := authQuery.Get("state")
		Expect(state).ToNot(BeEmpty())
		challenge := authQuery.Get("code_challenge")
		Expect(challenge).ToNot(BeEmpty())

		// Add the parameters that are necessary for a correct redirect request:
		redirectQuery := url.Values{}
		redirectQuery.Set("code", "my_code")
		redirectQuery.Set("state", state)
		redirectQuery.Set("code_challenge", challenge)

		// Replace the parameters with the given overrides. If the override is nil then the original parameter
		// is completely, otherwise it is replaced by the result of converting the override to a string.
		for name, value := range overrides {
			switch value := value.(type) {
			case nil:
				redirectQuery.Del(name)
			default:
				redirectQuery.Set(name, fmt.Sprintf("%v", value))
			}
		}

		// Send the request to the redirect URI:
		sendRedirect(redirectUri, redirectQuery)
	}

	// redirectWithError sends a request to the redirect URI with the the required query parameters,
	redirectWithError := func(authUri, errorCode, errorDescription string) {
		defer GinkgoRecover()

		// Parse the URI:
		parsedAuthUri, err := url.Parse(authUri)
		Expect(err).ToNot(HaveOccurred())

		// Get the query parameters:
		authQuery := parsedAuthUri.Query()
		redirectUri := authQuery.Get("redirect_uri")
		Expect(redirectUri).ToNot(BeEmpty())

		// Prepare the parameters for the redirect URI:
		redirectQuery := url.Values{}
		redirectQuery.Set("error", errorCode)
		redirectQuery.Set("error_description", errorDescription)

		// Send the request to the redirect URI:
		redirectUri = fmt.Sprintf("%s?%s", redirectUri, redirectQuery.Encode())
		response, err := http.Get(redirectUri)
		Expect(err).ToNot(HaveOccurred())
		_, err = io.Copy(io.Discard, response.Body)
		Expect(err).ToNot(HaveOccurred())
		err = response.Body.Close()
		Expect(err).ToNot(HaveOccurred())
	}

	respondWithToken := RespondWithJSONEncoded(
		http.StatusOK,
		tokenEndpointResponse{
			AccessToken:  "my_access_token",
			RefreshToken: "my_refresh_token",
			ExpiresIn:    3600,
			TokenType:    "Bearer",
		},
		http.Header{
			"Content-Type": {
				"application/json",
			},
		},
	)

	respondWithError := RespondWithJSONEncoded(
		http.StatusBadRequest,
		endpointError{
			ErrorCode:        "my_error",
			ErrorDescription: "My error",
		},
		http.Header{
			"Content-Type": {
				"application/json",
			},
		},
	)

	It("Generates authorization URL with the required parameters", func() {
		// Create the source using a open function saves the authorization URL, so that we can verify it
		// later. Note that the function also returns an error. That is so that we don't need to mock the
		// rest of the interactions with the server.
		var authUri string
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetScopes("read", "write").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, url string) error {
				go redirectWithError(url, "my_error", "My error")
				authUri = url
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(HaveOccurred())

		// Verify that the authorization URL contain all the parameters:
		Expect(authUri).ToNot(BeEmpty())
		parsedAuthUri, err := url.Parse(authUri)
		Expect(err).ToNot(HaveOccurred())
		authQuery := parsedAuthUri.Query()

		By("Having client identifier", func() {
			clientId := authQuery.Get("client_id")
			Expect(clientId).To(Equal("my_client"))
		})

		By("Not having client secret", func() {
			Expect(authQuery).ToNot(HaveKey("client_secret"))
		})

		By("Having resonse type", func() {
			responseType := authQuery.Get("response_type")
			Expect(responseType).To(Equal("code"))
		})

		By("Having scopes", func() {
			scopes := authQuery.Get("scope")
			Expect(scopes).To(Equal("read write"))
		})

		By("Having redirect URI", func() {
			redirectUri := authQuery.Get("redirect_uri")
			Expect(redirectUri).ToNot(BeEmpty())
		})

		By("Having state", func() {
			state := authQuery.Get("state")
			Expect(state).ToNot(BeEmpty())
		})

		By("Having code challenge", func() {
			codeChallenge := authQuery.Get("code_challenge")
			Expect(codeChallenge).ToNot(BeEmpty())
		})

		By("Having code challenge method", func() {
			codeChallengeMethod := authQuery.Get("code_challenge_method")
			Expect(codeChallengeMethod).To(Equal("S256"))
		})
	})

	It("Returns an error when the redirect requests contains an error", func() {
		// Create the source using a open function that produces an error:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, url string) error {
				go redirectWithError(url, "my_error", "My error")
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(MatchError("failed to obtain token: my_error: My error"))
	})

	It("Returns an error when the redirect requests doesn't contain a code", func() {
		// Create the source using a open function that removes the code:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, authUri string) error {
				go redirectWithOverrides(authUri, map[string]any{
					"code": nil,
				})
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(MatchError("failed to obtain token: no authorization code received"))
	})

	It("Returns an error when the redirect requests has an incorrect state", func() {
		// Create the source using a open function that sets an incorrect state:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, authUri string) error {
				go redirectWithOverrides(authUri, map[string]any{
					"state": "junk",
				})
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(MatchError("failed to obtain token: state mismatch"))
	})

	It("Returns an error when the timeout is reached", func() {
		// Create the source with a short timeout, and with an open function that doesn't send the redirect
		// request:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetTimeout(1 * time.Millisecond).
			SetOpenFunc(func(ctx context.Context, authUri string) error {
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(MatchError("failed to obtain token: timeout waiting for code"))
	})

	It("Sends the token request", func() {
		// Prepare the server to check the request and send a valid token:
		server.AppendHandlers(
			CombineHandlers(
				func(w http.ResponseWriter, r *http.Request) {
					// Check the request method and path:
					Expect(r.Method).To(Equal(http.MethodPost))
					Expect(r.URL.Path).To(Equal("/token"))

					// Check the content type, and parse the form:
					Expect(r.Header.Get("Content-Type")).To(Equal("application/x-www-form-urlencoded"))
					err := r.ParseForm()
					Expect(err).ToNot(HaveOccurred())

					// Check the form:
					Expect(r.Form.Get("client_id")).To(Equal("my_client"))
					Expect(r.Form.Get("code")).To(Equal("my_code"))
					Expect(r.Form.Get("code_verifier")).ToNot(BeEmpty())
					Expect(r.Form.Get("grant_type")).To(Equal("authorization_code"))
					Expect(r.Form.Get("redirect_uri")).To(MatchRegexp("^http://"))
				},
				respondWithToken,
			),
		)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, authUri string) error {
				go redirectWithOverrides(authUri, nil)
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Returns the token generated by the authorization server", func() {
		// Prepare the server to respond with a valid token:
		server.AppendHandlers(respondWithToken)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, authUri string) error {
				go redirectWithOverrides(authUri, nil)
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).ToNot(BeNil())
		Expect(token.Access).To(Equal("my_access_token"))
		Expect(token.Refresh).To(Equal("my_refresh_token"))
		Expect(token.Expiry).To(BeTemporally("~", time.Now().Add(3600*time.Second), time.Second))
	})

	It("Returns the token error returned by the authorization server", func() {
		// Prepare the server to respond with a valid token:
		server.AppendHandlers(respondWithError)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, authUri string) error {
				go redirectWithOverrides(authUri, nil)
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).To(MatchError("failed to obtain token: my_error: My error"))
		Expect(token).To(BeNil())
	})

	It("Returns a generic error when the authorization server returns an unexpected response", func() {
		// Prepare the server to respond with an HTML error page:
		server.AppendHandlers(
			RespondWith(
				http.StatusBadGateway,
				text.Dedent(`
					<!DOCTYPE html>
					<html>
					<body>
					<h1>Bad gateway</h1>
					</body>
					</html>
				`),
				http.Header{
					"Content-Type": {
						"text/html",
					},
				},
			),
		)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, authUri string) error {
				go redirectWithOverrides(authUri, nil)
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).To(MatchError(MatchRegexp(
			"^failed to obtain token: unexpected response code 502 from endpoint '.*'",
		)))
		Expect(token).To(BeNil())
	})

	It("Calls the listener's start and end methods for success", func() {
		// Prepare the server to respond with a valid token:
		server.AppendHandlers(respondWithToken)

		// Prepare a listener that verifies the events:
		listener = NewMockFlowListener(ctrl)
		listener.EXPECT().Start(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, event FlowStartEvent) error {
				// Verify the fields that are used by the code flow:
				Expect(event.Flow).To(Equal(CodeFlow))
				Expect(event.AuthorizationUri).ToNot(BeEmpty())

				// Make sure that the fields that aren't used by the code flow are empty:
				Expect(event.UserCode).To(BeEmpty())
				Expect(event.ExpiresIn).To(BeZero())
				Expect(event.VerificationUri).To(BeEmpty())
				Expect(event.VerificationUriComplete).To(BeEmpty())

				return nil
			}).
			Times(1)
		listener.EXPECT().End(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, event FlowEndEvent) error {
				Expect(event.Outcome).To(BeTrue())
				return nil
			}).
			Times(1)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, authUri string) error {
				go redirectWithOverrides(authUri, nil)
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Calls the listener's start and end methods for failure", func() {
		// Prepare the server to respond with a valid token:
		server.AppendHandlers(respondWithToken)

		// Prepare a listener that verifies the events:
		listener = NewMockFlowListener(ctrl)
		listener.EXPECT().Start(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, event FlowStartEvent) error {
				// Verify the fields that are used by the code flow:
				Expect(event.Flow).To(Equal(CodeFlow))
				Expect(event.AuthorizationUri).ToNot(BeEmpty())

				// Make sure that the fields that aren't used by the code flow are empty:
				Expect(event.UserCode).To(BeEmpty())
				Expect(event.ExpiresIn).To(BeZero())
				Expect(event.VerificationUri).To(BeEmpty())
				Expect(event.VerificationUriComplete).To(BeEmpty())

				return nil
			}).
			Times(1)
		listener.EXPECT().End(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, event FlowEndEvent) error {
				Expect(event.Outcome).To(BeFalse())
				return nil
			}).
			Times(1)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, authUri string) error {
				go redirectWithError(authUri, "my_error", "My error")
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).To(MatchError("failed to obtain token: my_error: My error"))
		Expect(token).To(BeNil())
	})

	It("Returns an error if the listener's start method fails", func() {
		// Prepare the server to respond with a valid token:
		server.AppendHandlers(respondWithToken)

		// Prepare a listener that verifies the events:
		listener = NewMockFlowListener(ctrl)
		listener.EXPECT().Start(gomock.Any(), gomock.Any()).
			Return(errors.New("my listener start error")).
			Times(1)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, authUri string) error {
				go redirectWithOverrides(authUri, nil)
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).To(MatchError("failed to obtain token: my listener start error"))
		Expect(token).To(BeNil())
	})

	It("Returns an error if the listener's end method fails", func() {
		// Prepare the server to respond with a valid token:
		server.AppendHandlers(respondWithToken)

		// Prepare a listener that verifies the events:
		listener = NewMockFlowListener(ctrl)
		listener.EXPECT().Start(gomock.Any(), gomock.Any()).
			Return(nil).
			Times(1)
		listener.EXPECT().End(gomock.Any(), gomock.Any()).
			Return(errors.New("my listener end error")).
			Times(1)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, authUri string) error {
				go redirectWithOverrides(authUri, nil)
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).To(MatchError("failed to obtain token: my listener end error"))
		Expect(token).To(BeNil())
	})

	It("Uses the default redirect URI when not explicitly set", func() {
		// Create the source and capture the authorization URI to verify the redirect URI:
		var authUri string
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetOpenFunc(func(ctx context.Context, url string) error {
				go redirectWithError(url, "my_error", "My error")
				authUri = url
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(HaveOccurred())

		// Parse the authorization URI and verify the redirect URI uses localhost with a random port:
		parsedAuthUri, err := url.Parse(authUri)
		Expect(err).ToNot(HaveOccurred())
		authQuery := parsedAuthUri.Query()
		redirectUri := authQuery.Get("redirect_uri")
		Expect(redirectUri).To(MatchRegexp(`^http://localhost:\d+$`))
	})

	It("Uses the custom redirect URI when explicitly set", func() {
		// Create the source with a custom redirect URI:
		var authUri string
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetRedirectUri("http://localhost:9876").
			SetOpenFunc(func(ctx context.Context, url string) error {
				go redirectWithError(url, "my_error", "My error")
				authUri = url
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(HaveOccurred())

		// Parse the authorization URI and verify the redirect URI uses the custom address:
		parsedAuthUri, err := url.Parse(authUri)
		Expect(err).ToNot(HaveOccurred())
		authQuery := parsedAuthUri.Query()
		redirectUri := authQuery.Get("redirect_uri")
		Expect(redirectUri).To(Equal("http://localhost:9876"))
	})

	It("Preserves the path in the redirect URI", func() {
		// Create the source with a redirect URI that includes a path:
		var authUri string
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetRedirectUri("http://localhost:9876/callback").
			SetOpenFunc(func(ctx context.Context, url string) error {
				go redirectWithError(url, "my_error", "My error")
				authUri = url
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(HaveOccurred())

		// Parse the authorization URI and verify the redirect URI preserves the path:
		parsedAuthUri, err := url.Parse(authUri)
		Expect(err).ToNot(HaveOccurred())
		authQuery := parsedAuthUri.Query()
		redirectUri := authQuery.Get("redirect_uri")
		Expect(redirectUri).To(Equal("http://localhost:9876/callback"))
	})

	It("Uses random port when redirect URI has port 0", func() {
		// Create the source with a redirect URI that has port 0:
		var authUri string
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetRedirectUri("http://localhost:0").
			SetOpenFunc(func(ctx context.Context, url string) error {
				go redirectWithError(url, "my_error", "My error")
				authUri = url
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(HaveOccurred())

		// Parse the authorization URI and verify the redirect URI uses localhost with a dynamically
		// allocated port (not 0):
		parsedAuthUri, err := url.Parse(authUri)
		Expect(err).ToNot(HaveOccurred())
		authQuery := parsedAuthUri.Query()
		redirectUri := authQuery.Get("redirect_uri")
		Expect(redirectUri).To(MatchRegexp(`^http://localhost:\d+$`))
		Expect(redirectUri).ToNot(Equal("http://localhost:0"))
	})

	It("Preserves path when using random port", func() {
		// Create the source with a redirect URI that has port 0 and a path:
		var authUri string
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetRedirectUri("http://localhost:0/oauth/callback").
			SetOpenFunc(func(ctx context.Context, url string) error {
				go redirectWithError(url, "my_error", "My error")
				authUri = url
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(HaveOccurred())

		// Parse the authorization URI and verify the redirect URI preserves the path with dynamic port:
		parsedAuthUri, err := url.Parse(authUri)
		Expect(err).ToNot(HaveOccurred())
		authQuery := parsedAuthUri.Query()
		redirectUri := authQuery.Get("redirect_uri")
		Expect(redirectUri).To(MatchRegexp(`^http://localhost:\d+/oauth/callback$`))
		Expect(redirectUri).ToNot(ContainSubstring(":0/"))
	})
})
