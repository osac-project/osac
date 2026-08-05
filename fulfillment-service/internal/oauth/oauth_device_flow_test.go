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
	"maps"
	"net/http"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/ghttp"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/text"
)

var _ = Describe("OAuth device flow", func() {
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
					Issuer:                      server.URL(),
					TokenEndpoint:               fmt.Sprintf("%s/token", server.URL()),
					AuthorizationEndpoint:       fmt.Sprintf("%s/auth", server.URL()),
					DeviceAuthorizationEndpoint: fmt.Sprintf("%s/device", server.URL()),
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

	respondWithDeviceAuth := func(overrides ...map[string]any) http.HandlerFunc {
		response := map[string]any{
			"device_code":               "my_device_code",
			"user_code":                 "ABCD-EFGH",
			"verification_uri":          "https://example.com/device",
			"verification_uri_complete": "https://example.com/device?user_code=ABCD-EFGH",
			"expires_in":                10,
			"interval":                  1,
		}
		for _, values := range overrides {
			maps.Copy(response, values)
		}
		return RespondWithJSONEncoded(
			http.StatusOK,
			response,
			http.Header{
				"Content-Type": {
					"application/json",
				},
			},
		)
	}

	respondWithToken := RespondWithJSONEncoded(
		http.StatusOK,
		map[string]any{
			"access_token":  "my_access_token",
			"refresh_token": "my_refresh_token",
			"expires_in":    3600,
			"token_type":    "Bearer",
		},
		http.Header{
			"Content-Type": {
				"application/json",
			},
		},
	)

	respondWithError := func(errorCode, errorDescription string) http.HandlerFunc {
		return RespondWithJSONEncoded(
			http.StatusBadRequest,
			endpointError{
				ErrorCode:        errorCode,
				ErrorDescription: errorDescription,
			},
			http.Header{
				"Content-Type": {
					"application/json",
				},
			},
		)
	}

	It("Sends device authorization request with the required parameters", func() {
		// Prepare the server to check the device authorization request:
		server.AppendHandlers(
			CombineHandlers(
				func(w http.ResponseWriter, r *http.Request) {
					// Check the request method and path:
					Expect(r.Method).To(Equal(http.MethodPost))
					Expect(r.URL.Path).To(Equal("/device"))

					// Check the content type, and parse the form:
					Expect(r.Header.Get("Content-Type")).To(Equal("application/x-www-form-urlencoded"))
					err := r.ParseForm()
					Expect(err).ToNot(HaveOccurred())

					// Check the form:
					Expect(r.Form.Get("client_id")).To(Equal("my_client"))
					Expect(r.Form.Get("scope")).To(Equal("read write"))
					Expect(r.Form.Get("code_challenge")).ToNot(BeEmpty())
					Expect(r.Form.Get("code_challenge_method")).To(Equal("S256"))
				},
				respondWithDeviceAuth(),
			),
			respondWithToken,
		)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetScopes("read", "write").
			SetListener(listener).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Returns an error when device authorization endpoint fails", func() {
		// Prepare the server to respond with an error:
		server.AppendHandlers(
			respondWithError("invalid_request", "Invalid request"),
		)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(HaveOccurred())
	})

	It("Returns an error when device authorization endpoint returns HTML", func() {
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
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(MatchError(MatchRegexp(
			"^failed to obtain token: unexpected response code 502 from endpoint '.*'",
		)))
	})

	It("Sends the token request with the device code", func() {
		// Prepare the server to respond with device authorization and then check the token request:
		server.AppendHandlers(
			respondWithDeviceAuth(),
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
					Expect(r.Form.Get("device_code")).To(Equal("my_device_code"))
					Expect(r.Form.Get("code_verifier")).ToNot(BeEmpty())
					Expect(r.Form.Get("grant_type")).To(Equal("urn:ietf:params:oauth:grant-type:device_code"))
				},
				respondWithToken,
			),
		)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Returns the token generated by the authorization server", func() {
		// Prepare the server to respond with device authorization and a valid token:
		server.AppendHandlers(
			respondWithDeviceAuth(),
			respondWithToken,
		)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
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

	It("Polls for the token when authorization is pending", func() {
		// Use a counter to track the number of token requests:
		var count atomic.Int32

		// Prepare the server to respond with device authorization and then authorization_pending twice before
		// returning a token:
		server.AppendHandlers(
			respondWithDeviceAuth(),
			CombineHandlers(
				func(w http.ResponseWriter, r *http.Request) {
					count.Add(1)
				},
				respondWithError("authorization_pending", "Authorization pending"),
			),
			CombineHandlers(
				func(w http.ResponseWriter, r *http.Request) {
					count.Add(1)
				},
				respondWithError("authorization_pending", "Authorization pending"),
			),
			CombineHandlers(
				func(w http.ResponseWriter, r *http.Request) {
					count.Add(1)
				},
				respondWithToken,
			),
		)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetPollInterval(1 * time.Millisecond).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).ToNot(BeNil())

		// Verify that the token request was made three times:
		Expect(count.Load()).To(Equal(int32(3)))
	})

	It("Polls for the token again when asked to slow down", func() {
		// Use a counter to track the number of token requests:
		var count atomic.Int32

		// Prepare the server to respond with device authorization and then slow down twice before returning
		// a token:
		server.AppendHandlers(
			respondWithDeviceAuth(),
			CombineHandlers(
				func(w http.ResponseWriter, r *http.Request) {
					count.Add(1)
				},
				respondWithError("slow_down", "Slow down"),
			),
			CombineHandlers(
				func(w http.ResponseWriter, r *http.Request) {
					count.Add(1)
				},
				respondWithError("slow_down", "Slow down"),
			),
			CombineHandlers(
				func(w http.ResponseWriter, r *http.Request) {
					count.Add(1)
				},
				respondWithToken,
			),
		)

		// Create the source with a vers short poll interval, to make the test faster:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetPollInterval(1 * time.Millisecond).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).ToNot(BeNil())

		// Verify that the token request was made three times:
		Expect(count.Load()).To(Equal(int32(3)))
	})

	It("Stops polling when receiving an error other than authorization pending or slow down", func() {
		// Prepare the server to respond with device authorization and then an error:
		server.AppendHandlers(
			respondWithDeviceAuth(),
			respondWithError("access_denied", "Access denied"),
		)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).To(MatchError("flow ended without a token"))
	})

	It("Returns a generic error when the token endpoint returns an unexpected response", func() {
		// Prepare the server to respond with device authorization and then an HTML error page for
		// ever. Eventually the request will time out will return the last error.
		server.AppendHandlers(
			respondWithDeviceAuth(
				map[string]any{
					"expires_in": 1,
				},
			),
		)
		server.RouteToHandler(
			http.MethodPost,
			"/token",
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
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
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
		// Prepare the server to respond with device authorization and a valid token:
		server.AppendHandlers(
			respondWithDeviceAuth(),
			respondWithToken,
		)

		// Prepare a listener that verifies the events:
		listener = NewMockFlowListener(ctrl)
		listener.EXPECT().Start(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, event FlowStartEvent) error {
				// Verify the fields that are used by the device flow:
				Expect(event.Flow).To(Equal(DeviceFlow))
				Expect(event.UserCode).To(Equal("ABCD-EFGH"))
				Expect(event.VerificationUri).ToNot(BeEmpty())
				Expect(event.ExpiresIn).To(Equal(10 * time.Second))

				// Make sure that the fields that aren't used by the device flow are empty:
				Expect(event.AuthorizationUri).To(BeEmpty())

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
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Calls the listener's start and end methods for failure", func() {
		// Prepare the server to respond with device authorization and an error:
		server.AppendHandlers(
			respondWithDeviceAuth(),
			respondWithError("access_denied", "Access denied"),
		)

		// Prepare a listener that verifies the events:
		listener = NewMockFlowListener(ctrl)
		listener.EXPECT().Start(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, event FlowStartEvent) error {
				// Verify the fields that are used by the device flow:
				Expect(event.Flow).To(Equal(DeviceFlow))
				Expect(event.UserCode).To(Equal("ABCD-EFGH"))
				Expect(event.VerificationUri).ToNot(BeEmpty())
				Expect(event.ExpiresIn).To(Equal(10 * time.Second))

				// Make sure that the fields that aren't used by the device flow are empty:
				Expect(event.AuthorizationUri).To(BeEmpty())

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
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).To(HaveOccurred())
		Expect(token).To(BeNil())
	})

	It("Returns an error if the listener's start method fails", func() {
		// Prepare the server to respond with device authorization:
		server.AppendHandlers(
			respondWithDeviceAuth(),
		)

		// Prepare a listener that returns an error:
		listener = NewMockFlowListener(ctrl)
		listener.EXPECT().Start(gomock.Any(), gomock.Any()).
			Return(errors.New("my listener start error")).
			Times(1)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).To(MatchError("failed to obtain token: failed to prompt user for device code: my listener start error"))
		Expect(token).To(BeNil())
	})

	It("Returns an error if the listener's end method fails", func() {
		// Prepare the server to respond with device authorization and a valid token:
		server.AppendHandlers(
			respondWithDeviceAuth(),
			respondWithToken,
		)

		// Prepare a listener that returns an error on end:
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
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).To(MatchError("failed to obtain token: my listener end error"))
		Expect(token).To(BeNil())
	})

	It("Keeps polling when the connection to the server fails", func() {
		// Prepare the server to respond with device authorization, then with an error and then with a token:
		// by closing the connection:
		server.AppendHandlers(
			respondWithDeviceAuth(),
			func(w http.ResponseWriter, r *http.Request) {
				hijacker, ok := w.(http.Hijacker)
				if ok {
					conn, _, err := hijacker.Hijack()
					Expect(err).ToNot(HaveOccurred())
					err = conn.Close()
					Expect(err).ToNot(HaveOccurred())
				}
			},
			respondWithToken,
		)

		// Create the source with a short poll interval to make retries faster:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetPollInterval(10 * time.Millisecond).
			SetTimeout(100 * time.Millisecond).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).ToNot(BeNil())
	})

	It("Fails authentication if there is no response after the code has expired", func() {
		// Prepare the server to respond with a code that expires quickly, so that the test won't take
		// long. And then configure the token endpoint to respond with authorization pending for ever.
		server.AppendHandlers(
			respondWithDeviceAuth(map[string]any{
				"expires_in": 1,
			}),
		)
		server.RouteToHandler(
			http.MethodPost,
			"/token",
			respondWithError("authorization_pending", "Authorization pending"),
		)

		// Prepare a listener that verifies the end event is called with failure:
		listener = NewMockFlowListener(ctrl)
		listener.EXPECT().Start(gomock.Any(), gomock.Any()).
			Return(nil).
			Times(1)
		listener.EXPECT().End(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, event FlowEndEvent) error {
				Expect(event.Outcome).To(BeFalse())
				return nil
			}).
			Times(1)

		// Create the source with a short timeout and poll interval:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(DeviceFlow).
			SetClientId("my_client").
			SetListener(listener).
			SetPollInterval(100 * time.Millisecond).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).To(MatchError("failed to obtain token: authorization_pending: Authorization pending"))
		Expect(token).To(BeNil())
	})
})
