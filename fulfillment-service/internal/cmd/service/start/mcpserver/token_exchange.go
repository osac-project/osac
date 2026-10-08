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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

// TokenExchanger obtains a public API token for the same user represented by an MCP token.
// The MCP token must never be sent to Fulfillment's public API.
type TokenExchanger interface {
	Exchange(ctx context.Context, subjectToken string, subject *jwt.Token) (string, error)
}

// TokenExchangerFunc adapts a function for tests and alternate OAuth providers.
type TokenExchangerFunc func(context.Context, string, *jwt.Token) (string, error)

func (f TokenExchangerFunc) Exchange(ctx context.Context, subjectToken string, subject *jwt.Token) (string, error) {
	return f(ctx, subjectToken, subject)
}

type oauthTokenExchanger struct {
	endpoint     string
	clientID     string
	clientSecret string
	client       *http.Client
	validator    auth.JwtValidator
}

func newOAuthTokenExchanger(endpoint, clientID, clientSecret string, client *http.Client, validator auth.JwtValidator) (TokenExchanger, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("OAuth token exchange endpoint must be an HTTPS URL")
	}
	if clientID == "" || clientSecret == "" || client == nil || validator == nil {
		return nil, errors.New("OAuth token exchange client credentials, HTTP client, and API token validator are required")
	}
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if safeClient.Timeout == 0 {
		safeClient.Timeout = 10 * time.Second
	}
	return &oauthTokenExchanger{endpoint: endpoint, clientID: clientID, clientSecret: clientSecret, client: &safeClient, validator: validator}, nil
}

func (e *oauthTokenExchanger) Exchange(ctx context.Context, subjectToken string, subject *jwt.Token) (string, error) {
	if subjectToken == "" || subject == nil {
		return "", errors.New("MCP caller token is missing")
	}
	form := url.Values{
		"grant_type":           {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":        {subjectToken},
		"subject_token_type":   {"urn:ietf:params:oauth:token-type:access_token"},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("cannot prepare OAuth token exchange")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	// OAuth client IDs may be URLs and therefore contain a colon. RFC 6749 §2.3.1
	// requires form-encoding each credential before constructing Basic auth.
	credentials := url.QueryEscape(e.clientID) + ":" + url.QueryEscape(e.clientSecret)
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	response, err := e.client.Do(request)
	if err != nil {
		return "", errors.New("OAuth token exchange is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// Authorization server error bodies may contain tokens and identity data.
		return "", fmt.Errorf("OAuth token exchange failed with HTTP %d", response.StatusCode)
	}
	var result struct {
		AccessToken     string `json:"access_token"`
		IssuedTokenType string `json:"issued_token_type"`
		TokenType       string `json:"token_type"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 || json.Unmarshal(body, &result) != nil || result.AccessToken == "" ||
		result.AccessToken == subjectToken ||
		result.IssuedTokenType != "urn:ietf:params:oauth:token-type:access_token" || !strings.EqualFold(result.TokenType, "Bearer") {
		return "", errors.New("OAuth token exchange returned an invalid access token response")
	}
	apiToken, err := e.validator.Validate(ctx, result.AccessToken)
	if err != nil {
		return "", errors.New("OAuth token exchange returned an invalid API token")
	}
	if err := sameCaller(subject, apiToken); err != nil {
		return "", err
	}
	return result.AccessToken, nil
}

func sameCaller(source, target *jwt.Token) error {
	sourceSubject, sourceErr := source.Claims.GetSubject()
	targetSubject, targetErr := target.Claims.GetSubject()
	sourceIssuer, sourceIssuerErr := source.Claims.GetIssuer()
	targetIssuer, targetIssuerErr := target.Claims.GetIssuer()
	if sourceErr != nil || targetErr != nil || sourceSubject == "" || sourceSubject != targetSubject ||
		sourceIssuerErr != nil || targetIssuerErr != nil || sourceIssuer == "" || sourceIssuer != targetIssuer {
		return errors.New("OAuth token exchange changed the caller identity")
	}
	sourceContext, sourceErr := auth.ExtractAuthContext(source)
	targetContext, targetErr := auth.ExtractAuthContext(target)
	if sourceErr != nil || targetErr != nil || !reflect.DeepEqual(sourceContext, targetContext) {
		return errors.New("OAuth token exchange changed the caller permissions")
	}
	targetExpiry, err := target.Claims.GetExpirationTime()
	if err != nil || targetExpiry == nil || !targetExpiry.After(time.Now()) {
		return errors.New("OAuth token exchange returned an expired API token")
	}
	return nil
}
