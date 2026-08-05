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
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/google/go-querystring/query"

	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/network"
)

type codeFlow struct {
	source    *TokenSource
	logger    *slog.Logger
	listener  FlowListener
	codeChan  chan string
	errorChan chan error
	state     string
}

type authEndpointRequest struct {
	ClientId            string   `json:"client_id,omitempty" url:"client_id,omitempty"`
	CodeChallenge       string   `json:"code_challenge,omitempty" url:"code_challenge,omitempty"`
	CodeChallengeMethod string   `json:"code_challenge_method,omitempty" url:"code_challenge_method,omitempty"`
	RedirectUri         string   `json:"redirect_uri,omitempty" url:"redirect_uri,omitempty"`
	ResponseType        string   `json:"response_type,omitempty" url:"response_type,omitempty"`
	Scope               []string `json:"scope,omitempty" url:"scope,omitempty,space"`
	State               string   `json:"state,omitempty" url:"state,omitempty"`
}

func (f *codeFlow) run(ctx context.Context) (result *auth.Token, err error) {
	// Generate the verifier and the challenge:
	verifier, challenge := f.source.generateVerifier()
	f.logger.DebugContext(
		ctx,
		"Generated PKCE code verifier and challenge",
		slog.String("!verifier", verifier),
		slog.String("!challenge", challenge),
	)

	// Generate the random state:
	f.state = f.source.generateState()
	f.logger.DebugContext(
		ctx,
		"Generated PKCE code state",
		slog.String("!state", f.state),
	)

	// Start the local HTTP server to handle the redirect:
	f.codeChan = make(chan string)
	f.errorChan = make(chan error)
	server, redirectUri, err := f.startServer(ctx)
	if err != nil {
		f.logger.ErrorContext(
			ctx,
			"Failed to start redirect server",
			slog.Any("error", err),
		)
		return
	}
	defer func() {
		err := server.Close()
		if err != nil {
			f.logger.ErrorContext(
				ctx,
				"Failed to stop redirect server",
				slog.Any("error", err),
			)
		}
	}()

	// Calculate the authorization URL and open it in the browser:
	authRequest := authEndpointRequest{
		ClientId:            f.source.clientId,
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		RedirectUri:         redirectUri,
		ResponseType:        "code",
		Scope:               f.source.scopes,
		State:               f.state,
	}
	authQuery, err := query.Values(authRequest)
	if err != nil {
		return
	}
	authUri := fmt.Sprintf("%s?%s", f.source.authEndpoint, authQuery.Encode())
	f.logger.DebugContext(
		ctx,
		"Generated authorization URI",
		slog.String("!uri", authUri),
	)
	err = f.listener.Start(ctx, FlowStartEvent{
		Flow:             CodeFlow,
		AuthorizationUri: authUri,
	})
	if err != nil {
		f.logger.ErrorContext(
			ctx,
			"Failed to start code flow",
			slog.Any("error", err),
		)
		return
	}
	f.logger.DebugContext(
		ctx,
		"Opening browser",
		slog.String("!url", authUri),
	)
	err = f.source.openFunc(ctx, authUri)
	if err != nil {
		f.logger.WarnContext(
			ctx,
			"Failed to open browser",
			slog.String("!url", authUri),
			slog.Any("error", err),
		)
		return
	}

	// Wait till the redirect server receives the authorization code, or an error occurs:
	f.logger.DebugContext(
		ctx,
		"Waiting for code or error",
	)
	var code string
	select {
	case code = <-f.codeChan:
		f.logger.DebugContext(
			ctx,
			"Successfully obtained code",
			slog.String("!code", code),
		)
	case err = <-f.errorChan:
		f.logger.ErrorContext(
			ctx,
			"Failed to obtain code",
			slog.Any("error", err),
		)
	case <-time.After(f.source.timeout):
		f.logger.ErrorContext(
			ctx,
			"Timeout waiting for code",
		)
		err = errors.New("timeout waiting for code")
	}
	if err != nil {
		listenerErr := f.listener.End(ctx, FlowEndEvent{
			Outcome: false,
		})
		if listenerErr != nil {
			f.logger.ErrorContext(
				ctx,
				"Failed to receive code",
				slog.Any("error", err),
			)
			err = listenerErr
		}
		return
	}

	// Exchange the authorization code for the access token::
	tokenResponse, err := f.source.sendTokenForm(ctx, tokenEndpointRequest{
		ClientId:     f.source.clientId,
		Code:         code,
		CodeVerifier: verifier,
		GrantType:    "authorization_code",
		RedirectUri:  redirectUri,
	})
	if err != nil {
		listenerErr := f.listener.End(ctx, FlowEndEvent{
			Outcome: false,
		})
		if listenerErr != nil {
			f.logger.ErrorContext(
				ctx,
				"Failed send token request",
				slog.Any("error", err),
			)
			err = listenerErr
		}
		return
	}

	// Notify user of authentication success:
	err = f.listener.End(ctx, FlowEndEvent{
		Outcome: true,
	})
	if err != nil {
		return
	}

	// Return the token:
	result = &auth.Token{
		Access:  tokenResponse.AccessToken,
		Refresh: tokenResponse.RefreshToken,
		Expiry:  f.source.secondsToTime(tokenResponse.ExpiresIn),
	}
	f.logger.DebugContext(
		ctx,
		"Successfully exchanged code for token",
		slog.Any("!access", result.Access),
		slog.Any("!refresh", result.Refresh),
		slog.Time("expiry", result.Expiry),
	)
	return
}

func (f *codeFlow) startServer(ctx context.Context) (server *http.Server, redirectUri string, err error) {
	// Parse the redirect URI to extract the host and port for the listener:
	parsedUri, err := url.Parse(f.source.redirectUri)
	if err != nil {
		err = fmt.Errorf("failed to parse redirect URI '%s': %w", f.source.redirectUri, err)
		return
	}
	address := parsedUri.Host

	// Create the listener:
	listener, err := network.NewListener().
		SetLogger(f.logger).
		SetNetwork("tcp").
		SetAddress(address).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create listener: %w", err)
		return
	}

	// The port number may have been allocated dynamically if it was initially zero, so we need to get the actual
	// address from the listener and rebuild the redirect URI if needed.
	actualAddress := listener.Addr().String()
	if address != actualAddress {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		_, actualPort, _ := net.SplitHostPort(actualAddress)
		actualAddress = net.JoinHostPort(host, actualPort)
		parsedUri.Host = actualAddress
		redirectUri = parsedUri.String()
	} else {
		redirectUri = f.source.redirectUri
	}
	logger := f.logger.With(
		slog.String("address", actualAddress),
	)
	logger.DebugContext(ctx, "Created redirect listener")

	// Create and start the server:
	server = &http.Server{
		Handler:           http.HandlerFunc(f.serve),
		Addr:              actualAddress,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(
				ctx,
				"Redirect server error",
				slog.Any("error", err),
			)
			return
		}
	}()
	logger.DebugContext(ctx, "Started redirect server")

	return
}

func (f *codeFlow) serve(w http.ResponseWriter, r *http.Request) {
	// Get the context from the request:
	ctx := r.Context()

	// Log the request:
	query := r.URL.Query()
	f.logger.DebugContext(
		ctx,
		"Received redirect request",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("!query", query),
	)

	// Check for error parameter:
	errorCode := query.Get("error")
	errorDescription := query.Get("error_description")
	if errorCode != "" {
		f.logger.DebugContext(
			ctx,
			"Received error redirect request",
			slog.String("error", errorCode),
			slog.String("error_description", errorDescription),
		)
		w.WriteHeader(http.StatusBadRequest)
		err := f.source.templatingEngine.Execute(w, "auth_error.html", map[string]any{
			"Code":        errorCode,
			"Description": errorDescription,
		})
		if err != nil {
			f.logger.ErrorContext(
				ctx,
				"Failed to render error template",
				slog.Any("error", err),
			)
			fmt.Fprint(w, "Authentication failed")
		}
		err = &endpointError{
			ErrorCode:        errorCode,
			ErrorDescription: errorDescription,
		}
		f.errorChan <- err
		return
	}

	// Check that we have an authorization code:
	code := query.Get("code")
	if code == "" {
		w.WriteHeader(http.StatusBadRequest)
		renderErr := f.source.templatingEngine.Execute(w, "no_code.html", nil)
		if renderErr != nil {
			f.logger.ErrorContext(
				ctx,
				"Failed to render no code template",
				slog.Any("error", renderErr),
			)
			fmt.Fprint(w, "Authentication failed")
		}
		f.errorChan <- errors.New("no authorization code received")
		return
	}

	// Verify state to prevent CSRF attacks:
	state := query.Get("state")
	if state != f.state {
		w.WriteHeader(http.StatusBadRequest)
		renderErr := f.source.templatingEngine.Execute(w, "state_mismatch.html", nil)
		if renderErr != nil {
			f.logger.ErrorContext(
				ctx,
				"Failed to render state mismatch template",
				slog.Any("error", renderErr),
			)
			fmt.Fprint(w, "Authentication failed")
		}
		f.errorChan <- errors.New("state mismatch")
		return
	}

	// Success response:
	w.WriteHeader(http.StatusOK)
	renderErr := f.source.templatingEngine.Execute(w, "auth_success.html", nil)
	if renderErr != nil {
		f.logger.ErrorContext(
			ctx,
			"Failed to render auth_success template",
			slog.Any("error", renderErr),
		)
	}

	// Send the code:
	f.logger.DebugContext(
		ctx,
		"Sending code",
		slog.String("!code", code),
	)
	f.codeChan <- code
	f.logger.DebugContext(
		ctx,
		"Sent code",
		slog.String("!code", code),
	)
}
