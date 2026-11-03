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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"

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

var errOAuthTokenExchangeUnavailable = errors.New("OAuth token exchange is unavailable")

const (
	apiTokenCacheTTL   = 30 * time.Second
	maxCachedAPITokens = 1024
)

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

type expiringTokenExchanger interface {
	ExchangeWithExpiration(ctx context.Context, subjectToken string, subject *jwt.Token) (string, time.Time, error)
}

func (e *oauthTokenExchanger) Exchange(
	ctx context.Context, subjectToken string, subject *jwt.Token,
) (string, error) {
	token, _, err := e.ExchangeWithExpiration(ctx, subjectToken, subject)
	return token, err
}

func (e *oauthTokenExchanger) ExchangeWithExpiration(
	ctx context.Context, subjectToken string, subject *jwt.Token,
) (string, time.Time, error) {
	if subjectToken == "" || subject == nil {
		return "", time.Time{}, errors.New("MCP caller token is missing")
	}
	form := url.Values{
		"grant_type":           {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":        {subjectToken},
		"subject_token_type":   {"urn:ietf:params:oauth:token-type:access_token"},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, errors.New("cannot prepare OAuth token exchange")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	// OAuth client IDs may be URLs and therefore contain a colon. RFC 6749 §2.3.1
	// requires form-encoding each credential before constructing Basic auth.
	credentials := url.QueryEscape(e.clientID) + ":" + url.QueryEscape(e.clientSecret)
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	response, err := e.client.Do(request)
	if err != nil {
		return "", time.Time{}, errOAuthTokenExchangeUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// Authorization server error bodies may contain tokens and identity data.
		return "", time.Time{}, fmt.Errorf("OAuth token exchange failed with HTTP %d", response.StatusCode)
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
		return "", time.Time{}, errors.New("OAuth token exchange returned an invalid access token response")
	}
	apiToken, err := e.validator.Validate(ctx, result.AccessToken)
	if err != nil {
		return "", time.Time{}, errors.New("OAuth token exchange returned an invalid API token")
	}
	if err := sameCaller(subject, apiToken); err != nil {
		return "", time.Time{}, err
	}
	expiration, err := apiToken.Claims.GetExpirationTime()
	if err != nil || expiration == nil {
		return "", time.Time{}, errors.New("OAuth token exchange returned an API token without expiration")
	}
	return result.AccessToken, expiration.Time, nil
}

type cachedAPIToken struct {
	token     string
	expiresAt time.Time
}

type cachingTokenExchanger struct {
	delegate   expiringTokenExchanger
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
	mutex      sync.Mutex
	entries    map[string]cachedAPIToken
	inflight   singleflight.Group
}

func newCachingTokenExchanger(delegate expiringTokenExchanger) TokenExchanger {
	return &cachingTokenExchanger{
		delegate:   delegate,
		ttl:        apiTokenCacheTTL,
		maxEntries: maxCachedAPITokens,
		now:        time.Now,
		entries:    make(map[string]cachedAPIToken),
	}
}

func (e *cachingTokenExchanger) Exchange(
	ctx context.Context, subjectToken string, subject *jwt.Token,
) (string, error) {
	digest := sha256.Sum256([]byte(subjectToken))
	key := hex.EncodeToString(digest[:])
	now := e.now()
	if token, ok := e.cachedToken(key, now); ok {
		return token, nil
	}
	resultCh := e.inflight.DoChan(key, func() (any, error) {
		token, expiry, err := e.delegate.ExchangeWithExpiration(context.WithoutCancel(ctx), subjectToken, subject)
		if err != nil {
			return "", err
		}
		now := e.now()
		subjectExpiration, err := subject.Claims.GetExpirationTime()
		if err != nil || subjectExpiration == nil || !subjectExpiration.After(now) {
			return "", errors.New("MCP caller token has no valid expiration")
		}
		expiresAt := subjectExpiration.Time
		if !expiry.IsZero() && expiry.Before(expiresAt) {
			expiresAt = expiry
		}
		if ttlExpiry := now.Add(e.ttl); ttlExpiry.Before(expiresAt) {
			expiresAt = ttlExpiry
		}
		e.store(key, token, expiresAt, now)
		return token, nil
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return "", result.Err
		}
		token, _ := result.Val.(string)
		return token, nil
	}
}

func (e *cachingTokenExchanger) cachedToken(key string, now time.Time) (string, bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	entry, ok := e.entries[key]
	if !ok {
		return "", false
	}
	if !now.Before(entry.expiresAt) {
		delete(e.entries, key)
		return "", false
	}
	return entry.token, true
}

func (e *cachingTokenExchanger) store(key, token string, expiresAt, now time.Time) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if len(e.entries) >= e.maxEntries {
		for entryKey, entry := range e.entries {
			if !now.Before(entry.expiresAt) {
				delete(e.entries, entryKey)
			}
		}
	}
	if len(e.entries) >= e.maxEntries {
		evictionKey := ""
		evictionExpiry := time.Time{}
		for entryKey, entry := range e.entries {
			if evictionKey == "" || entry.expiresAt.Before(evictionExpiry) ||
				(entry.expiresAt.Equal(evictionExpiry) && entryKey < evictionKey) {
				evictionKey = entryKey
				evictionExpiry = entry.expiresAt
			}
		}
		if evictionKey != "" {
			delete(e.entries, evictionKey)
		}
	}
	e.entries[key] = cachedAPIToken{token: token, expiresAt: expiresAt}
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
