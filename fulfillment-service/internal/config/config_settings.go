/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package config

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spf13/pflag"
	"google.golang.org/grpc"

	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/network"
	"github.com/osac-project/fulfillment-service/internal/oauth"
	"github.com/osac-project/fulfillment-service/internal/packages"
	"github.com/osac-project/fulfillment-service/internal/version"
)

// SettingsBuilder contains the data and logic needed to build a Settings object. Don't create instances of this type
// directly, use the NewSettings function instead.
type SettingsBuilder struct {
	logger *slog.Logger
	dir    string
}

// Settings is the type used to store the configuration of the client. General settings are persisted to a JSON file,
// while secret data (tokens, credentials) is stored in the operating system keyring.
type Settings struct {
	logger  *slog.Logger
	lock    *sync.RWMutex
	dir     string
	general generalSettings
	secret  secretSettings
	caPool  *x509.CertPool
}

// NewSettings creates a builder that can then be used to configure and create a Settings object.
func NewSettings() *SettingsBuilder {
	return &SettingsBuilder{}
}

// SetLogger sets the logger that will be used by the settings. This is mandatory.
func (b *SettingsBuilder) SetLogger(value *slog.Logger) *SettingsBuilder {
	b.logger = value
	return b
}

// SetDir sets the directory where the settings files will be stored. This is mandatory.
func (b *SettingsBuilder) SetDir(value string) *SettingsBuilder {
	b.dir = value
	return b
}

// Build uses the data stored in the builder to create and configure a new Settings object.
func (b *SettingsBuilder) Build() (result *Settings, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.dir == "" {
		err = errors.New("directory is mandatory")
		return
	}

	// Create the object:
	result = &Settings{
		logger: b.logger,
		dir:    b.dir,
		lock:   &sync.RWMutex{},
	}
	return
}

// generalSettings contains the non-secret fields of the configuration. These are persisted to a JSON file.
type generalSettings struct {
	TokenScript string     `json:"token_script,omitempty"`
	Plaintext   bool       `json:"plaintext,omitempty"`
	Insecure    bool       `json:"insecure,omitempty"`
	CaFiles     []CaFile   `json:"ca_files,omitempty"`
	Address     string     `json:"address,omitempty"`
	Private     bool       `json:"private,omitempty"`
	Flow        oauth.Flow `json:"flow,omitempty"`
	Issuer      string     `json:"issuer,omitempty"`
	Scopes      []string   `json:"scopes,omitempty"`
	RedirectUri string     `json:"redirect_uri,omitempty"`
	Tenant      string     `json:"tenant,omitempty"`
}

// secretSettings contains the secret fields of the configuration. These are stored as a single
// JSON-encoded entry in the operating system keyring rather than in the configuration file.
type secretSettings struct {
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenExpiry  time.Time `json:"token_expiry,omitempty"`
	ClientId     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
	User         string    `json:"user,omitempty"`
	Password     string    `json:"password,omitempty"`
}

// CaFile represents a CA certificate file with its name and optionally its content. The content is stored for relative
// paths to allow the configuration to work when the tool is used from a different directory.
type CaFile struct {
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

// Armed returns true if the settings have the minimum configuration needed to connect to the server. It returns
// false if the receiver is nil.
func (s *Settings) Armed() bool {
	return s != nil && s.general.Address != ""
}

// Reset resets all settings to their zero values.
func (s *Settings) Reset() {
	s.general = generalSettings{}
	s.secret = secretSettings{}
	s.caPool = nil
}

// Address returns the server address.
func (s *Settings) Address() string {
	return s.general.Address
}

// SetAddress sets the server address.
func (s *Settings) SetAddress(value string) {
	s.general.Address = value
}

// Plaintext returns whether plaintext connections are used.
func (s *Settings) Plaintext() bool {
	return s.general.Plaintext
}

// SetPlaintext sets whether to use plaintext connections.
func (s *Settings) SetPlaintext(value bool) {
	s.general.Plaintext = value
}

// Insecure returns whether TLS verification is skipped.
func (s *Settings) Insecure() bool {
	return s.general.Insecure
}

// SetInsecure sets whether to skip TLS verification.
func (s *Settings) SetInsecure(value bool) {
	s.general.Insecure = value
}

// Private returns whether private packages are enabled.
func (s *Settings) Private() bool {
	return s.general.Private
}

// SetPrivate sets whether private packages are enabled.
func (s *Settings) SetPrivate(value bool) {
	s.general.Private = value
}

// CaFiles returns the list of CA certificate files.
func (s *Settings) CaFiles() []CaFile {
	return s.general.CaFiles
}

// AddCaFile appends a CA certificate file to the configuration.
func (s *Settings) AddCaFile(value CaFile) {
	s.general.CaFiles = append(s.general.CaFiles, value)
}

// TokenScript returns the token script path.
func (s *Settings) TokenScript() string {
	return s.general.TokenScript
}

// SetTokenScript sets the token script path.
func (s *Settings) SetTokenScript(value string) {
	s.general.TokenScript = value
}

// SetTokenExpiry sets the token expiry time.
func (s *Settings) SetTokenExpiry(value time.Time) {
	s.secret.TokenExpiry = value
}

// Flow returns the OAuth flow type.
func (s *Settings) Flow() oauth.Flow {
	return s.general.Flow
}

// SetFlow sets the OAuth flow type.
func (s *Settings) SetFlow(value oauth.Flow) {
	s.general.Flow = value
}

// Issuer returns the OAuth issuer URL.
func (s *Settings) Issuer() string {
	return s.general.Issuer
}

// SetIssuer sets the OAuth issuer URL.
func (s *Settings) SetIssuer(value string) {
	s.general.Issuer = value
}

// Scopes returns the OAuth scopes.
func (s *Settings) Scopes() []string {
	return s.general.Scopes
}

// SetScopes sets the OAuth scopes.
func (s *Settings) SetScopes(value []string) {
	s.general.Scopes = value
}

// RedirectUri returns the OAuth redirect URI.
func (s *Settings) RedirectUri() string {
	return s.general.RedirectUri
}

// SetRedirectUri sets the OAuth redirect URI.
func (s *Settings) SetRedirectUri(value string) {
	s.general.RedirectUri = value
}

// SetAccessToken sets the access token.
func (s *Settings) SetAccessToken(value string) {
	s.secret.AccessToken = value
}

// ClientId returns the OAuth client identifier.
func (s *Settings) ClientId() string {
	return s.secret.ClientId
}

// ClientSecret returns the OAuth client secret.
func (s *Settings) ClientSecret() string {
	return s.secret.ClientSecret
}

// SetRefreshToken sets the refresh token.
func (s *Settings) SetRefreshToken(value string) {
	s.secret.RefreshToken = value
}

// SetClientId sets the OAuth client identifier.
func (s *Settings) SetClientId(value string) {
	s.secret.ClientId = value
}

// SetClientSecret sets the OAuth client secret.
func (s *Settings) SetClientSecret(value string) {
	s.secret.ClientSecret = value
}

// SetUser sets the OAuth user name.
func (s *Settings) SetUser(value string) {
	s.secret.User = value
}

// SetPassword sets the OAuth password.
func (s *Settings) SetPassword(value string) {
	s.secret.Password = value
}

// Tenant returns the saved tenant name.
func (s *Settings) Tenant() string {
	return s.general.Tenant
}

// SetTenant sets the saved tenant name.
func (s *Settings) SetTenant(value string) {
	s.general.Tenant = value
}

// Load populates the settings from the configuration file and the secret store.
func (s *Settings) Load(ctx context.Context) error {
	err := s.loadGeneral(ctx)
	if err != nil {
		return err
	}
	err = s.loadSecret(ctx)
	if err != nil {
		return err
	}
	err = s.createCaPool(ctx)
	if err != nil {
		return fmt.Errorf("failed to create CA pool: %w", err)
	}
	return nil
}

func (s *Settings) loadGeneral(ctx context.Context) error {
	file := filepath.Join(s.dir, "config.json")
	_, err := os.Stat(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to check if config file '%s' exists: %w", file, err)
	}
	data, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		return fmt.Errorf("failed to read config file '%s': %w", file, err)
	}
	if len(data) > 0 {
		err = json.Unmarshal(data, &s.general)
		if err != nil {
			return fmt.Errorf("failed to parse config file '%s': %w", file, err)
		}
	}
	s.logger.DebugContext(
		ctx,
		"Loaded general settings from file",
		slog.String("file", file),
		slog.Any("settings", s.general),
	)
	return nil
}

func (s *Settings) loadSecret(ctx context.Context) error {
	store, err := NewSecretStore().
		SetLogger(s.logger).
		SetDir(s.dir).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create secret store: %w", err)
	}
	err = store.Load(ctx, &s.secret)
	if err != nil {
		return fmt.Errorf("failed to load secrets: %w", err)
	}
	return nil
}

// Save saves the settings to the configuration file and writes secret fields to the secret store.
func (s *Settings) Save(ctx context.Context) error {
	err := s.saveGeneral(ctx)
	if err != nil {
		return err
	}
	err = s.saveSecret(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *Settings) saveGeneral(ctx context.Context) error {
	file := filepath.Join(s.dir, "config.json")
	data, err := json.MarshalIndent(&s.general, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	err = os.MkdirAll(s.dir, os.FileMode(0755))
	if err != nil {
		return fmt.Errorf("failed to create directory %s: %w", s.dir, err)
	}
	err = os.WriteFile(file, data, 0600)
	if err != nil {
		return fmt.Errorf("failed to write file '%s': %w", file, err)
	}
	s.logger.DebugContext(
		ctx,
		"Saved general settings to file",
		slog.String("file", file),
		slog.Any("settings", s.general),
	)
	return nil
}

func (s *Settings) saveSecret(ctx context.Context) error {
	store, err := NewSecretStore().
		SetLogger(s.logger).
		SetDir(s.dir).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create secret store: %w", err)
	}
	var object any
	if s.secret != (secretSettings{}) {
		object = &s.secret
	}
	err = store.Save(ctx, object)
	if err != nil {
		return fmt.Errorf("failed to save secrets: %w", err)
	}
	s.logger.DebugContext(
		ctx,
		"Saved secret settings to store",
		slog.Any("!settings", s.secret),
	)
	return nil
}

// TokenSource creates a token source from the configuration.
func (c *Settings) TokenSource(ctx context.Context) (result auth.TokenSource, err error) {
	// Get the token store:
	tokenStore := c.TokenStore()

	// If an OAuth flow has been configured, then use it to create a non interactive OAuth token source:
	if c.general.Flow != "" {
		var caPool *x509.CertPool
		caPool, err = c.CaPool(ctx)
		if err != nil {
			err = fmt.Errorf("failed to get CA pool: %w", err)
			return
		}
		result, err = oauth.NewTokenSource().
			SetLogger(c.logger).
			SetFlow(c.general.Flow).
			SetInteractive(false).
			SetIssuer(c.general.Issuer).
			SetClientId(c.secret.ClientId).
			SetClientSecret(c.secret.ClientSecret).
			SetScopes(c.general.Scopes...).
			SetRedirectUri(c.general.RedirectUri).
			SetUsername(c.secret.User).
			SetPassword(c.secret.Password).
			SetInsecure(c.general.Insecure).
			SetCaPool(caPool).
			SetStore(tokenStore).
			Build()
		if err != nil {
			err = fmt.Errorf("failed to create OAuth token source: %w", err)
		}
		return
	}

	// If a token script has been configured, then use it to create a script token source:
	if c.general.TokenScript != "" {
		result, err = auth.NewScriptTokenSource().
			SetLogger(c.logger).
			SetScript(c.general.TokenScript).
			SetStore(tokenStore).
			Build()
		if err != nil {
			err = fmt.Errorf("failed to create script token source: %w", err)
		}
		return
	}

	// Finally, if there is an access token try to use it:
	if c.secret.AccessToken != "" {
		result, err = auth.NewStaticTokenSource().
			SetLogger(c.logger).
			SetToken(&auth.Token{
				Access: c.secret.AccessToken,
			}).
			Build()
		if err != nil {
			err = fmt.Errorf("failed to create static token source: %w", err)
		}
		return
	}

	// If we are here then there is no way to get tokens, so it will all be anonymous.
	result = nil
	return
}

// Conect creates a gRPC connection from the configuration.
func (c *Settings) Connect(ctx context.Context, flags *pflag.FlagSet) (result *grpc.ClientConn, err error) {
	// Try to create a token source. This will be nil if no token source can be created, which means that the
	// connection will be anonymous.
	tokenSource, err := c.TokenSource(ctx)
	if err != nil {
		err = fmt.Errorf("failed to create token source: %w", err)
		return
	}

	return c.connect(ctx, flags, tokenSource)
}

// ConnectPlain creates a gRPC connection without channel-level token credentials.
// Use this for services that handle their own auth via per-call credentials
// (e.g., console proxy with ticket-based PerRPCCredentials).
func (c *Settings) ConnectPlain(ctx context.Context, flags *pflag.FlagSet) (*grpc.ClientConn, error) {
	return c.connect(ctx, flags, nil)
}

// connect builds a gRPC connection, optionally with token-based auth.
func (c *Settings) connect(ctx context.Context, flags *pflag.FlagSet, tokenSource auth.TokenSource) (result *grpc.ClientConn, err error) {
	// Create the version interceptor:
	versionInterceptor, err := version.NewInterceptor().
		SetLogger(c.logger).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create version interceptor: %w", err)
		return
	}

	// Create the gRPC client:
	caPool, err := c.CaPool(ctx)
	if err != nil {
		err = fmt.Errorf("failed to get CA pool: %w", err)
		return
	}
	result, err = network.NewGrpcClient().
		SetLogger(c.logger).
		SetPlaintext(c.general.Plaintext).
		SetInsecure(c.general.Insecure).
		SetCaPool(caPool).
		SetTokenSource(tokenSource).
		SetAddress(c.general.Address).
		SetKeepAlive(15 * time.Second).
		SetKeepAliveTimeout(10 * time.Second).
		AddUnaryInterceptor(versionInterceptor.UnaryClient).
		AddStreamInterceptor(versionInterceptor.StreamClient).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create gRPC client: %w", err)
		return
	}

	return
}

// Packages returns the list of packages that should be enabled according to the configuration. The public packages
// will always be enabled, but the private packages will be enabled only if the `private` flag is true.
//
// The packages are returned as a map, where the key is the name of the package and the value is an integer indicating
// the relative order of the types of the package order of the package when presented to the user. For example, if the
// package 'private.v1' has order 1 and package 'fulfillment.v1' has order 2, then the types of the 'private.v1' should
// be presented first, even if the alphabetical order would put the 'fulfillment.v1' types first.
func (c *Settings) Packages() map[string]int {
	result := map[string]int{}
	for _, name := range packages.Public {
		result[name] = 1
	}
	if c.general.Private {
		for _, name := range packages.Private {
			result[name] = 0
		}
	}
	return result
}

// TokenStore returns an implementation of the auth.TokenStore interface that loads and saves tokens from/to
// the configuration.
func (c *Settings) TokenStore() auth.TokenStore {
	return &settingsTokenStore{
		settings: c,
		lock:     c.lock,
	}
}

// CaPool returns the CA pool from the configuration. If the CA pool is not set, it will be created and cached.
func (c *Settings) CaPool(ctx context.Context) (result *x509.CertPool, err error) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	if c.caPool == nil {
		err = c.createCaPool(ctx)
		if err != nil {
			return
		}
	}
	result = c.caPool
	return
}

func (c *Settings) createCaPool(ctx context.Context) error {
	// Classify configured CA entries into certificate content and directory paths.
	//
	// Entries with a relative path are ignored because the tool may be invoked from a different working directory.
	// This is acceptable because the CLI always calculates absolute paths at configuration time.
	//
	// Entries with an absolute path and stored content represent regular files whose content was captured by the
	// CLI at configuration time. We try to re-read the file from disk so that rotated certificates are picked up
	// automatically; if the file is no longer accessible we fall back to the stored content.
	//
	// Entries with an absolute path but no stored content represent directories. These are passed through to the
	// certifiacte pool builder so that their contents are scanned on every invocation, allowing the user to add or
	// remove certificate files inside the directory.
	var (
		caCerts []any
		caFiles []string
	)
	for _, caFile := range c.general.CaFiles {
		caPath := filepath.Clean(caFile.Name)
		caContent := caFile.Content
		if !filepath.IsAbs(caPath) {
			c.logger.WarnContext(
				ctx,
				"Ignoring CA entry with relative path",
				slog.String("file", caPath),
			)
			continue
		}
		if caContent != "" {
			caBytes, err := os.ReadFile(caPath)
			if err != nil {
				c.logger.WarnContext(
					ctx,
					"CA file is not readable, using stored content",
					slog.String("file", caPath),
					slog.String("error", err.Error()),
				)
				caCerts = append(caCerts, caContent)
			} else {
				caCerts = append(caCerts, caBytes)
			}
		} else {
			caInfo, err := os.Stat(caPath)
			if err != nil || !caInfo.IsDir() {
				c.logger.WarnContext(
					ctx,
					"CA entry without stored content is not an accessible directory",
					slog.String("file", caPath),
				)
				continue
			}
			caFiles = append(caFiles, caPath)
		}
	}

	// Create the CA pool:
	var err error
	c.caPool, err = network.NewCertPool().
		SetLogger(c.logger).
		AddSystemFiles(true).
		AddKubernetesFiles(true).
		AddCertificates(caCerts...).
		AddFiles(caFiles...).
		Build()
	return err
}

// settingsTokenStore is a token source that loads and saves tokens from/to the secret settings.
type settingsTokenStore struct {
	settings *Settings
	lock     *sync.RWMutex
}

func (s *settingsTokenStore) Load(ctx context.Context) (result *auth.Token, err error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	if s.settings.secret.AccessToken == "" {
		return
	}
	result = &auth.Token{
		Access:  s.settings.secret.AccessToken,
		Refresh: s.settings.secret.RefreshToken,
		Expiry:  s.settings.secret.TokenExpiry,
	}
	return
}

func (s *settingsTokenStore) Save(ctx context.Context, token *auth.Token) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if token == nil {
		return errors.New("token cannot be nil")
	}
	accessChanged := s.settings.secret.AccessToken != token.Access
	refreshChanged := s.settings.secret.RefreshToken != token.Refresh
	expiryChanged := s.settings.secret.TokenExpiry != token.Expiry
	if !accessChanged && !refreshChanged && !expiryChanged {
		return nil
	}
	s.settings.secret.AccessToken = token.Access
	s.settings.secret.RefreshToken = token.Refresh
	s.settings.secret.TokenExpiry = token.Expiry
	return s.settings.saveSecret(ctx)
}
