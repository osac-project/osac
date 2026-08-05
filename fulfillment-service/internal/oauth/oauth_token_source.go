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
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/google/go-querystring/query"
	"github.com/skratchdot/open-golang/open"

	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/network"
	"github.com/osac-project/fulfillment-service/internal/templating"
)

//go:embed templates
var templatesFS embed.FS

// Flow is the type of OAuth flow to use.
type Flow string

const (
	CredentialsFlow Flow = "credentials"
	CodeFlow        Flow = "code"
	DeviceFlow      Flow = "device"
	PasswordFlow    Flow = "password"
)

// TokenSourceBuilder contains the logic needed to create a token source that uses OAuth client credentials grant,
// authorization code flow, device flow, or resource owner password credentials flow to obtain and automatically renew
// access tokens.
type TokenSourceBuilder struct {
	logger       *slog.Logger
	store        auth.TokenStore
	flow         Flow
	listener     FlowListener
	issuer       string
	clientId     string
	clientSecret string
	username     string
	password     string
	scopes       []string
	insecure     bool
	caPool       *x509.CertPool
	interactive  bool
	timeout      time.Duration
	httpClient   *http.Client
	openFunc     func(ctx context.Context, url string) error
	pollInterval time.Duration
	redirectUri  string
}

type TokenSource struct {
	logger           *slog.Logger
	store            auth.TokenStore
	flow             Flow
	flowRunner       flowRunner
	issuer           string
	clientId         string
	clientSecret     string
	username         string
	password         string
	scopes           []string
	insecure         bool
	caPool           *x509.CertPool
	interactive      bool
	timeout          time.Duration
	discoverOnce     sync.Once
	tokenEndpoint    string
	authEndpoint     string
	deviceEndpoint   string
	templatingEngine *templating.Engine
	httpClient       *http.Client
	openFunc         func(ctx context.Context, url string) error
	pollInterval     time.Duration
	redirectUri      string
}

// flowRunner is the interface that defines the methods that are common to all objects that can run a flow.
type flowRunner interface {
	// run runs the flow and returns the resulting token or an error.
	run(ctx context.Context) (result *auth.Token, err error)
}

type tokenEndpointRequest struct {
	ClientId     string   `json:"client_id,omitempty" url:"client_id,omitempty"`
	ClientSecret string   `json:"client_secret,omitempty" url:"client_secret,omitempty"`
	Code         string   `json:"code,omitempty" url:"code,omitempty"`
	CodeVerifier string   `json:"code_verifier,omitempty" url:"code_verifier,omitempty"`
	DeviceCode   string   `json:"device_code,omitempty" url:"device_code,omitempty"`
	GrantType    string   `json:"grant_type,omitempty" url:"grant_type,omitempty"`
	Password     string   `json:"password,omitempty" url:"password,omitempty"`
	RedirectUri  string   `json:"redirect_uri,omitempty" url:"redirect_uri,omitempty"`
	RefreshToken string   `json:"refresh_token,omitempty" url:"refresh_token,omitempty"`
	Scope        []string `json:"scope,omitempty" url:"scope,omitempty,space"`
	Username     string   `json:"username,omitempty" url:"username,omitempty"`
}

type tokenEndpointResponse struct {
	AccessToken  string `json:"access_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
}

type endpointError struct {
	ErrorCode        string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

func (r *endpointError) Error() string {
	return fmt.Sprintf("%s: %s", r.ErrorCode, r.ErrorDescription)
}

// NewTokenSource creates a builder that can then be used to configure and create a token source that uses OAuth
// client credentials grant, authorization code flow, or device flow to obtain and renew tokens automatically.
func NewTokenSource() *TokenSourceBuilder {
	return &TokenSourceBuilder{
		interactive: true,
	}
}

// SetLogger sets the logger. This is mandatory.
func (b *TokenSourceBuilder) SetLogger(value *slog.Logger) *TokenSourceBuilder {
	b.logger = value
	return b
}

// SetFlow sets the OAuth flow to use. This is mandatory.
func (b *TokenSourceBuilder) SetFlow(value Flow) *TokenSourceBuilder {
	b.flow = value
	return b
}

// SetListener sets the listener for interactions with the user. This is mandatory for the code and device flows.
func (b *TokenSourceBuilder) SetListener(value FlowListener) *TokenSourceBuilder {
	b.listener = value
	return b
}

// SetIssuer sets the OAuth issuer URL for discovery. This is mandatory.
func (b *TokenSourceBuilder) SetIssuer(value string) *TokenSourceBuilder {
	b.issuer = value
	return b
}

// SetClientId sets the client identifier. This is mandatory for the client credentials flow, and optional for the other
// flows.
func (b *TokenSourceBuilder) SetClientId(value string) *TokenSourceBuilder {
	b.clientId = value
	return b
}

// SetClientSecret sets the client secret. This is mandatory for client credentials flow and optional for other flows.
func (b *TokenSourceBuilder) SetClientSecret(value string) *TokenSourceBuilder {
	b.clientSecret = value
	return b
}

// SetUsername sets the username for the resource owner password credentials flow. This is mandatory for the password
// flow.
func (b *TokenSourceBuilder) SetUsername(value string) *TokenSourceBuilder {
	b.username = value
	return b
}

// SetPassword sets the password for the resource owner password credentials flow. This is mandatory for the password
// flow.
func (b *TokenSourceBuilder) SetPassword(value string) *TokenSourceBuilder {
	b.password = value
	return b
}

// SetScopes sets the scopes to request. This is optional.
func (b *TokenSourceBuilder) SetScopes(value ...string) *TokenSourceBuilder {
	b.scopes = value
	return b
}

// SetHttpClient sets the HTTP client to use. This is optional and defaults to http.DefaultClient.
func (b *TokenSourceBuilder) SetHttpClient(value *http.Client) *TokenSourceBuilder {
	b.httpClient = value
	return b
}

// SetOpenFunc sets the function to use to open a URL used by the code flow in the browser. This is optional and
// defaults to a function that is generally appropriate for all platforms, so there is no need to set it.
// This is intended mostly for unit tests, where it is convenient to use a mock function to avoid opening a real
// browser.
func (b *TokenSourceBuilder) SetOpenFunc(value func(ctx context.Context, url string) error) *TokenSourceBuilder {
	b.openFunc = value
	return b
}

// SetInsecure sets whether to skip TLS certificate verification. This is optional and defaults to false.
func (b *TokenSourceBuilder) SetInsecure(value bool) *TokenSourceBuilder {
	b.insecure = value
	return b
}

// SetCaPool sets the certificate pool that contains the certificates of the certificate authorities that are trusted
// when connecting using TLS. This is optional, and the default is to use trust the certificate authorities trusted by
// the operating system.
func (b *TokenSourceBuilder) SetCaPool(value *x509.CertPool) *TokenSourceBuilder {
	b.caPool = value
	return b
}

// SetInteractive sets whether interaction with the user is allowed. When set to false, the token source will not
// initiate code or device flows, and will only use tokens from the store. This is optional and defaults to true.
func (b *TokenSourceBuilder) SetInteractive(value bool) *TokenSourceBuilder {
	b.interactive = value
	return b
}

// SeStore sets the token store that will be used to load/save tokens from/to persistent storage. This is optional. If
// not provided then the tokens will be stored in memory for the lifetime of the token source.
func (b *TokenSourceBuilder) SetStore(value auth.TokenStore) *TokenSourceBuilder {
	b.store = value
	return b
}

// SetTimeout sets the maximum time to wait for a token to be obtained. This is important for the code and device
// flows, where it isn't reasonable to wait for ever till the user completes the authorization process. This is
// optional and the default is five minutes.
//
// Note that for the device flow the server will typically suggest a timeout as part of the response to the request
// to generate the device code. In that case the timeout set here will be ignored.
func (b *TokenSourceBuilder) SetTimeout(value time.Duration) *TokenSourceBuilder {
	b.timeout = value
	return b
}

// SetPollInterval sets the interval to use when polling for the token in the device flow. This is optional and the
// default is to use whatever the server suggests, or else five seconds if the server doesn't suggest anything.
func (b *TokenSourceBuilder) SetPollInterval(value time.Duration) *TokenSourceBuilder {
	b.pollInterval = value
	return b
}

// SetRedirectUri sets the redirect URI for the OAuth authorization code flow. This is the complete URI that will be
// sent to the authorization endpoint, and the local HTTP server will be started to listen on the host and port
// extracted from this URI. For example, 'http://localhost:8080' or 'http://my-host:10000/callback'. If the port is 0,
// a random available port will be selected and the redirect URI will be updated accordingly. This is optional and
// defaults to 'http://localhost:0', which binds to localhost with a randomly selected port.
func (b *TokenSourceBuilder) SetRedirectUri(value string) *TokenSourceBuilder {
	b.redirectUri = value
	return b
}

// Build uses the data stored in the builder to build a new OAuth token source.
func (b *TokenSourceBuilder) Build() (result *TokenSource, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.issuer == "" {
		err = errors.New("issuer is mandatory")
		return
	}
	if b.clientId == "" {
		err = errors.New("client identifier is mandatory")
		return
	}
	if err = b.validateFlowParameters(); err != nil {
		return
	}
	if b.store == nil {
		err = errors.New("token store is mandatory")
		return
	}
	if b.timeout < 0 {
		err = fmt.Errorf("timeout must be greater than zero, but it is %s", b.timeout)
		return
	}

	// Resolve defaults into a local config without mutating the builder:
	resolved, err := b.resolveDefaults()
	if err != nil {
		return
	}

	// Create the object early, as we need its reference for the underlying flow:
	source := &TokenSource{
		logger:           b.logger,
		store:            b.store,
		flow:             b.flow,
		issuer:           b.issuer,
		clientId:         b.clientId,
		clientSecret:     b.clientSecret,
		username:         b.username,
		password:         b.password,
		scopes:           resolved.scopes,
		insecure:         b.insecure,
		caPool:           resolved.caPool,
		interactive:      b.interactive,
		timeout:          resolved.timeout,
		templatingEngine: resolved.templatingEngine,
		httpClient:       resolved.httpClient,
		openFunc:         resolved.openFunc,
		pollInterval:     b.pollInterval,
		redirectUri:      resolved.redirectUri,
	}
	source.flowRunner = b.createFlowRunner(source)

	// Return the source:
	result = source
	return
}

// resolvedConfig holds the defaults computed during Build() without mutating the builder.
type resolvedConfig struct {
	scopes           []string
	timeout          time.Duration
	caPool           *x509.CertPool
	httpClient       *http.Client
	openFunc         func(context.Context, string) error
	redirectUri      string
	templatingEngine *templating.Engine
}

// validateFlowParameters validates per-flow parameter requirements.
func (b *TokenSourceBuilder) validateFlowParameters() error {
	switch b.flow {
	case CredentialsFlow:
		if b.clientId == "" {
			return errors.New("client identifier is mandatory for the client credentials flow")
		}
		if b.clientSecret == "" {
			return errors.New("client secret is mandatory for the client credentials flow")
		}
	case CodeFlow:
		if b.listener == nil && b.interactive {
			return errors.New("listener is mandatory for the authorization code flow")
		}
	case DeviceFlow:
		if b.listener == nil && b.interactive {
			return errors.New("listener is mandatory for the device flow")
		}
	case PasswordFlow:
		if b.username == "" {
			return errors.New("username is mandatory for the resource owner password credentials flow")
		}
		if b.password == "" {
			return errors.New("password is mandatory for the resource owner password credentials flow")
		}
	default:
		return fmt.Errorf(
			"unsupported flow '%s', should be '%s', '%s', '%s' or '%s'",
			b.flow, CredentialsFlow, CodeFlow, DeviceFlow, PasswordFlow,
		)
	}
	return nil
}

// resolveDefaults computes default values for optional builder fields and returns them in a
// resolvedConfig struct. The builder itself is not modified.
func (b *TokenSourceBuilder) resolveDefaults() (cfg resolvedConfig, err error) {
	// Sort the scopes so that when they are used, for example in the authorization URL, they are in a predicatable
	// and repeatable order:
	cfg.scopes = slices.Clone(b.scopes)
	slices.Sort(cfg.scopes)

	// Create the templating engine:
	cfg.templatingEngine, err = templating.NewEngine().
		SetLogger(b.logger).
		AddFS(templatesFS).
		SetDir("templates").
		Build()
	if err != nil {
		return
	}

	// Set the default timeout:
	cfg.timeout = b.timeout
	if cfg.timeout == 0 {
		cfg.timeout = 5 * time.Minute
	}

	// Set the default CA pool if needed:
	cfg.caPool = b.caPool
	if cfg.caPool == nil {
		cfg.caPool, err = network.NewCertPool().
			SetLogger(b.logger).
			AddSystemFiles(true).
			AddKubernetesFiles(true).
			Build()
		if err != nil {
			err = fmt.Errorf("failed to build CA pool: %w", err)
			return
		}
	}

	// Create HTTP client with optional insecure TLS configuration:
	cfg.httpClient = b.httpClient
	if cfg.httpClient == nil {
		cfg.httpClient = http.DefaultClient
	}
	tlsConfig := &tls.Config{
		RootCAs: cfg.caPool,
	}
	if b.insecure {
		tlsConfig.InsecureSkipVerify = true
	}
	cfg.httpClient.Transport = &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	// Set the default open function:
	cfg.openFunc = b.openFunc
	if cfg.openFunc == nil {
		cfg.openFunc = func(ctx context.Context, url string) error {
			return open.Run(url)
		}
	}

	// Validate and set the default redirect URI:
	cfg.redirectUri = b.redirectUri
	if cfg.redirectUri == "" {
		cfg.redirectUri = defaultRedirectUri
	}
	parsedRedirectUri, parseErr := url.Parse(cfg.redirectUri)
	if parseErr != nil {
		err = fmt.Errorf("failed to parse redirect URI '%s': %w", cfg.redirectUri, parseErr)
		return
	}
	if parsedRedirectUri.Host == "" {
		err = fmt.Errorf("redirect URI '%s' must include a host", cfg.redirectUri)
		return
	}

	return
}

// createFlowRunner creates the appropriate flow runner for the configured OAuth flow.
func (b *TokenSourceBuilder) createFlowRunner(source *TokenSource) flowRunner {
	switch b.flow {
	case CredentialsFlow:
		return &credentialsFlow{
			source: source,
			logger: b.logger.With("flow", "credentials"),
		}
	case CodeFlow:
		return &codeFlow{
			source:   source,
			logger:   b.logger.With("flow", "code"),
			listener: b.listener,
		}
	case DeviceFlow:
		return &deviceFlow{
			source:   source,
			logger:   b.logger.With("flow", "device"),
			listener: b.listener,
		}
	case PasswordFlow:
		return &passwordFlow{
			source: source,
			logger: b.logger.With("flow", "password"),
		}
	default:
		return nil
	}
}

// Token returns the a token, renewing it or requesting a new one as needed.
func (s *TokenSource) Token(ctx context.Context) (result *auth.Token, err error) {
	// Try to load an existing token first, and remember to save it if there was no error and the returned token
	// is different to the one we initially loaded.
	loaded, err := s.store.Load(ctx)
	if err != nil {
		err = fmt.Errorf("failed to load token: %w", err)
		return
	}
	defer func() {
		if result != nil && !s.sameTokens(loaded, result) {
			err := s.store.Save(ctx, result)
			if err != nil {
				s.logger.ErrorContext(
					ctx,
					"Failed to save token",
					slog.Any("error", err),
				)
			}
		}
	}()

	// If the loaded token doesn't expire soon, then we can use it directly, there is no need to request a new one:
	if loaded != nil && s.isFresh(loaded) {
		result = loaded
		return
	}

	// If we have a refresh token, then we can try to use it to renew the access token. If this fails we will
	// report it in the log, and continue with the next step to get a new token.
	if loaded != nil && loaded.Refresh != "" {
		result, err = s.runRefresh(ctx, loaded.Refresh)
		if err == nil {
			return
		}
		s.logger.DebugContext(
			ctx,
			"Failed to refresh the token, will try to request a new one",
			slog.Any("error", err),
		)
	}

	// At this point we don't have a fresh token. We can run the flow to request a new one, but only if the
	// interactive mode is enabled, or if the flow isn't interactive.
	flowInteractive := true
	switch s.flowRunner.(type) {
	case *credentialsFlow, *passwordFlow:
		flowInteractive = false
	}
	if !flowInteractive || s.interactive {
		result, err = s.runFlow(ctx)
		return
	}

	// If we are here then either there wasn't a token in the store, or it was about to expire, and we/ can't
	// generate a new one. But the token may still be valid, so we can use it as a last resort.
	if loaded != nil && !s.isExpired(loaded) {
		s.logger.WarnContext(
			ctx,
			"Using token that expires soon",
			slog.String("!access", loaded.Access),
			slog.Time("expiry", loaded.Expiry),
		)
		result = loaded
		return
	}

	// If we are here there wasn't a token in the store, or it was already expired, all we can do is return
	// an error.
	err = errors.New("no token available")
	return
}

func (s *TokenSource) sameTokens(a, b *auth.Token) bool {
	if a == nil && b == nil {
		return true
	}
	if a != nil && b != nil {
		return a.Access == b.Access && a.Refresh == b.Refresh && a.Expiry.Equal(b.Expiry)
	}
	return false
}

// discover performs endpoint discovery using the configured issuer URL.
func (s *TokenSource) discover(ctx context.Context) error {
	// Create discovery tool:
	tool, err := NewDiscoveryTool().
		SetLogger(s.logger).
		SetIssuer(s.issuer).
		SetInsecure(s.insecure).
		SetCaPool(s.caPool).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create discovery tool: %w", err)
	}

	// Perform discovery:
	metadata, err := tool.Discover(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover metadata: %w", err)
	}

	// Set discovered endpoints:
	s.tokenEndpoint = metadata.TokenEndpoint
	if metadata.AuthorizationEndpoint != "" {
		s.authEndpoint = metadata.AuthorizationEndpoint
	}
	if metadata.DeviceAuthorizationEndpoint != "" {
		s.deviceEndpoint = metadata.DeviceAuthorizationEndpoint
	}
	s.logger.DebugContext(
		ctx,
		"Successfully discovered metadata",
		slog.String("token_url", s.tokenEndpoint),
		slog.String("auth_url", s.authEndpoint),
		slog.String("device_url", s.deviceEndpoint),
	)

	// If no scopes were explicitly configured, set the default scopes based on what the server supports:
	if len(s.scopes) == 0 && len(metadata.ScopesSupported) > 0 {
		for _, scope := range defaultScopes {
			if slices.Contains(metadata.ScopesSupported, scope) {
				s.scopes = append(s.scopes, scope)
			}
		}
		slices.Sort(s.scopes)
		s.logger.DebugContext(
			ctx,
			"Using default scopes based on server support",
			slog.Any("supported", metadata.ScopesSupported),
			slog.Any("default", defaultScopes),
			slog.Any("selected", s.scopes),
		)
	}

	return nil
}

// isFresh checks if the given token can still be used. Note that if the token expires soon it will not be considered
// fresh, even if it is still valid. This is to avoid using a token that is about to expire, and may expire in the
// middle of the operation that it is used for.
func (s *TokenSource) isFresh(token *auth.Token) bool {
	// Token must have an access token to be considered fresh
	if token.Access == "" {
		return false
	}
	return token.Expiry.IsZero() || time.Until(token.Expiry) > 30*time.Second
}

// isExpired check if the given token is expired.
func (s *TokenSource) isExpired(token *auth.Token) bool {
	return !token.Expiry.IsZero() && time.Until(token.Expiry) <= 0
}

func (s *TokenSource) runRefresh(ctx context.Context, refreshToken string) (result *auth.Token, err error) {
	// Perform discovery if not already done:
	s.discoverOnce.Do(func() {
		err = s.discover(ctx)
	})
	if err != nil {
		err = fmt.Errorf("failed to discover metadata: %w", err)
		return
	}

	// Send the request to get the new tokens:
	tokenResponse, err := s.sendTokenForm(ctx, tokenEndpointRequest{
		ClientId:     s.clientId,
		GrantType:    "refresh_token",
		RefreshToken: refreshToken,
	})
	if err != nil {
		return
	}

	// Prepare the new token:
	token := &auth.Token{
		Access:  tokenResponse.AccessToken,
		Refresh: tokenResponse.RefreshToken,
		Expiry:  s.secondsToTime(tokenResponse.ExpiresIn),
	}

	// Some authorization servers may not return a new refresh token. In those cases we should keep the old one.
	if token.Refresh == "" {
		s.logger.DebugContext(
			ctx,
			"Server didn't return a new refresh token, will keep the old one",
			slog.String("!refresh", refreshToken),
		)
		token.Refresh = refreshToken
	}

	// Return the new token:
	result = token
	return
}

func (s *TokenSource) runFlow(ctx context.Context) (result *auth.Token, err error) {
	// Perform discovery if not already done:
	s.discoverOnce.Do(func() {
		err = s.discover(ctx)
	})
	if err != nil {
		err = fmt.Errorf("failed to discover metadata: %w", err)
		return
	}

	// Run the flow:
	token, err := s.flowRunner.run(ctx)
	if err != nil {
		err = fmt.Errorf("failed to obtain token: %w", err)
		return
	}
	if token == nil {
		err = errors.New("flow ended without a token")
		return
	}
	s.logger.DebugContext(
		ctx,
		"Successfully obtained token",
		slog.String("!access", token.Access),
		slog.String("!refresh", token.Refresh),
		slog.Time("expiry", token.Expiry),
	)

	// Save the token to storage:
	err = s.store.Save(ctx, token)
	if err != nil {
		err = fmt.Errorf("failed to save token: %w", err)
		return
	}
	s.logger.DebugContext(
		ctx,
		"Successfully saved token",
		slog.String("!access", token.Access),
		slog.String("!refresh", token.Refresh),
		slog.Time("expiry", token.Expiry),
	)

	// Return the token:
	result = token
	return
}

// sendForm sends a request containing a set of fields and returns the response. The request should be a struct with
// `url` tags that will be used to encode the request into a URL encoded form. If the response code is 200 it writes
// the result into the given response, which should be a pointer to a struct that has JSON tags. If the response code
// is not 200 then an error is returned. The error will be of type `endpointError` if the server returned a valid JSON
// error response with at least the `error` field. Otherwise will be a generic error.
func (s *TokenSource) sendForm(ctx context.Context, endpoint string, form any, result any) error {
	values, err := query.Values(form)
	if err != nil {
		return err
	}
	response, err := s.httpClient.PostForm(endpoint, values)
	if err != nil {
		return err
	}
	defer func() {
		err := response.Body.Close()
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to close response body",
				slog.String("endpoint", endpoint),
				slog.Any("error", err),
			)
		}
	}()
	switch response.StatusCode {
	case http.StatusOK:
		contentType := response.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil {
			return fmt.Errorf(
				"failed to parse content type '%s' from endpoint '%s': %w",
				contentType, endpoint, err,
			)
		}
		if mediaType != "application/json" {
			return fmt.Errorf(
				"expected 'application/json' content type from endpoint '%s', but got '%s'",
				endpoint, contentType,
			)
		}
		decoder := json.NewDecoder(response.Body)
		return decoder.Decode(result)
	case http.StatusBadRequest:
		contentType := response.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil {
			return fmt.Errorf(
				"failed to parse content type '%s' from endpoint '%s': %w",
				contentType, endpoint, err,
			)
		}
		if mediaType != "application/json" {
			return fmt.Errorf(
				"expected 'application/json' content type from endpoint '%s', but got '%s'",
				endpoint, contentType,
			)
		}
		decoder := json.NewDecoder(response.Body)
		var endpointErr endpointError
		err = decoder.Decode(&endpointErr)
		if err != nil {
			return fmt.Errorf(
				"failed to decode error response from endpoint '%s': %w",
				endpoint, err,
			)
		}
		return &endpointErr
	default:
		s.logger.ErrorContext(
			ctx,
			"Unexpected response code",
			slog.String("endpoint", endpoint),
			slog.Int("code", response.StatusCode),
		)
		return fmt.Errorf(
			"unexpected response code %d from endpoint '%s'",
			response.StatusCode, endpoint,
		)
	}
}

func (s *TokenSource) sendTokenForm(ctx context.Context,
	request tokenEndpointRequest) (response tokenEndpointResponse, err error) {
	err = s.sendForm(ctx, s.tokenEndpoint, &request, &response)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to send token form",
			slog.String("endpoint", s.tokenEndpoint),
			slog.Any("!request", request),
			slog.Any("error", err),
		)
		return
	}
	return
}

func (s *TokenSource) secondsToDuration(seconds int) time.Duration {
	return time.Duration(seconds * int(time.Second))
}

func (s *TokenSource) secondsToTime(seconds int) time.Time {
	return time.Now().Add(s.secondsToDuration(seconds))
}

func (s *TokenSource) generateVerifier() (verifier, challenge string) {
	// Generate a random verifier:
	verifier = s.encode(s.random(32))

	// Calculate the challenge by hashing the verifier string:
	hash := sha256.Sum256([]byte(verifier))
	challenge = s.encode(hash[:])

	return
}

func (s *TokenSource) generateState() string {
	return s.encode(s.random(16))
}

func (s *TokenSource) random(length int) []byte {
	bytes := make([]byte, length)
	_, _ = rand.Read(bytes)
	return bytes
}

func (s *TokenSource) encode(data []byte) string {
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(data)
}

// Invalidate clears the cached token, forcing a fresh token to be generated on the next Token() call.
func (s *TokenSource) Invalidate(ctx context.Context) error {
	// Save an empty token to invalidate the cache
	return s.store.Save(ctx, &auth.Token{})
}

// defaultRedirectUri is the default redirect URI to use for the authorization code flow. It binds to localhost with
// a dynamically allocated port.
const defaultRedirectUri = "http://localhost:0"

// defaultScopes is the list of scopes that will be requested by default if the server supports them and the user
// doesn't explicitly set scopes.
var defaultScopes = []string{
	"openid",
}
