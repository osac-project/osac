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
	"time"

	"github.com/osac-project/fulfillment-service/internal/auth"
)

type deviceFlow struct {
	source   *TokenSource
	logger   *slog.Logger
	listener FlowListener
}

type deviceAuthRequest struct {
	ClientId            string   `json:"client_id,omitempty" url:"client_id,omitempty"`
	CodeChallenge       string   `json:"code_challenge,omitempty" url:"code_challenge,omitempty"`
	CodeChallengeMethod string   `json:"code_challenge_method,omitempty" url:"code_challenge_method,omitempty"`
	Scope               []string `json:"scope,omitempty" url:"scope,omitempty,space"`
}

type deviceAuthResponse struct {
	DeviceCode              string `json:"device_code,omitempty"`
	UserCode                string `json:"user_code,omitempty"`
	VerificationUri         string `json:"verification_uri,omitempty"`
	VerificationUriComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in,omitempty"`
	Interval                int    `json:"interval,omitempty"`
}

func (f *deviceFlow) run(ctx context.Context) (result *auth.Token, err error) {
	// Generate the verifier and the challenge:
	verifier, challenge := f.source.generateVerifier()
	f.logger.DebugContext(
		ctx,
		"Generated PKCE code verifier and challenge",
		slog.String("!verifier", verifier),
		slog.String("!challenge", challenge),
	)

	// Send the request to the device authorization endpoint:
	authResponse, err := f.sendAuthForm(ctx, deviceAuthRequest{
		ClientId:            f.source.clientId,
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		Scope:               f.source.scopes,
	})
	if err != nil {
		return
	}
	f.logger.DebugContext(
		ctx,
		"Received device authorization response",
		slog.Int("expires_in", authResponse.ExpiresIn),
		slog.Int("interval", authResponse.Interval),
		slog.String("!device_code", authResponse.DeviceCode),
		slog.String("!user_code", authResponse.UserCode),
		slog.String("!verification_uri", authResponse.VerificationUri),
		slog.String("!verification_uri_complete", authResponse.VerificationUriComplete),
	)

	// If the server has provided an expiration time for the code, then we will use that. Otherwise we will use
	// the configured timeout.
	var expiresIn time.Duration
	if authResponse.ExpiresIn != 0 {
		expiresIn = f.source.secondsToDuration(authResponse.ExpiresIn)
		f.logger.DebugContext(
			ctx,
			"Using timeout provided by the server",
			slog.Duration("timeout", expiresIn),
		)
	} else {
		expiresIn = f.source.timeout
		f.logger.DebugContext(
			ctx,
			"Using configured timeout",
			slog.Duration("timeout", expiresIn),
		)
	}
	expiresAt := time.Now().Add(expiresIn)
	f.logger.DebugContext(
		ctx,
		"Code expiration time",
		slog.Time("expiration", expiresAt),
	)

	// Send the start event:
	err = f.listener.Start(ctx, FlowStartEvent{
		Flow:            DeviceFlow,
		ExpiresIn:       expiresIn,
		UserCode:        authResponse.UserCode,
		VerificationUri: authResponse.VerificationUri,
	})
	if err != nil {
		f.logger.ErrorContext(
			ctx,
			"Failed to send device flow start event",
			slog.Any("error", err),
		)
		err = fmt.Errorf("failed to prompt user for device code: %w", err)
		return
	}

	// If the user has specified a pool interval, then use that ignoring whatever the server suggests, or five
	// seconds if neither the user nor the server have specified anything.
	pollInterval := f.source.pollInterval
	if pollInterval == 0 {
		if authResponse.Interval > 0 {
			pollInterval = f.source.secondsToDuration(authResponse.Interval)
		} else {
			pollInterval = 5 * time.Second
		}
	}
	f.logger.DebugContext(
		ctx,
		"Using poll interval",
		slog.Duration("interval", pollInterval),
	)

	// Poll for the access token:
	tokenRequest := tokenEndpointRequest{
		ClientId:     f.source.clientId,
		CodeVerifier: verifier,
		DeviceCode:   authResponse.DeviceCode,
		GrantType:    "urn:ietf:params:oauth:grant-type:device_code",
	}
	var tokenResponse tokenEndpointResponse
	for {
		tokenResponse, err = f.source.sendTokenForm(ctx, tokenRequest)
		if err == nil {
			break
		}
		if time.Now().After(expiresAt) {
			f.logger.InfoContext(
				ctx,
				"Code expired, will not retry",
				slog.Duration("expires_in", expiresIn),
				slog.Any("error", err),
			)
			listenerErr := f.listener.End(ctx, FlowEndEvent{
				Outcome: false,
			})
			if listenerErr != nil {
				err = listenerErr
			}
			return
		}
		var endpointErr *endpointError
		if !errors.As(err, &endpointErr) {
			f.logger.WarnContext(
				ctx,
				"Unexpected error polling token endpoint, will retry",
				slog.Any("err", err),
			)
			time.Sleep(pollInterval)
			continue
		}
		switch endpointErr.ErrorCode {
		case "authorization_pending":
			f.logger.DebugContext(
				ctx,
				"Authorization pending, will retry",
				slog.Any("error", endpointErr.ErrorCode),
				slog.Any("error_description", endpointErr.ErrorDescription),
				slog.Duration("interval", pollInterval),
			)
			time.Sleep(pollInterval)
			continue
		case "slow_down":
			f.logger.DebugContext(
				ctx,
				"Slow down, will retry",
				slog.Any("error", endpointErr.ErrorCode),
				slog.Any("error_description", endpointErr.ErrorDescription),
				slog.Duration("interval", pollInterval),
			)
			time.Sleep(pollInterval)
			continue
		default:
			listenerErr := f.listener.End(ctx, FlowEndEvent{
				Outcome: false,
			})
			if listenerErr != nil {
				f.logger.ErrorContext(
					ctx,
					"Failed to send token request",
					slog.Any("err", err),
				)
			}
			err = listenerErr
			return
		}
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
	return
}

func (f *deviceFlow) sendAuthForm(ctx context.Context,
	request deviceAuthRequest) (response deviceAuthResponse, err error) {
	err = f.source.sendForm(ctx, f.source.deviceEndpoint, &request, &response)
	return
}
