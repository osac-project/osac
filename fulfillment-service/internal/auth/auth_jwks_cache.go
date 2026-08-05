/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"

	kubefiles "github.com/osac-project/fulfillment-service/internal/kubernetes/files"
)

// JwksCache is a cache that knows how to discover and load JSON web key sets.
//
//go:generate mockgen -destination=auth_jwks_cache_mock.go -package=auth . JwksCache
type JwksCache interface {
	// Get returns the key for the given issuer URL and key identifier. If the key is not found, and it isn't
	// possible to load it from one of the configured issuers, an error is returned.
	//
	// When there an error is reurned, it will be ErrBadIssuer if the issuer URL is not trusted, or ErrBadKey if
	// the key is not found. Any other error situation will be returned with an undefined error type, and it is not
	// safe to take the error message and send it directly to clients, as it may contain useless, inappropriate, or
	// security sensitive information.
	//
	// The keys is returned as 'any' value because the actual type of the key is determined by the issuer, and there
	// is no common interface or type to represent all possible key types.
	Get(ctx context.Context, issuerUrl string, keyId string) (keyObj any, err error)
}

// JwksCacheBuilder contains the data and logic needed to build a cache that knows how to discover and load JSON web key
// sets from a list of trusted issuers. Don't create instances of this type directly, use the NewJwksCache function
type JwksCacheBuilder struct {
	logger     *slog.Logger
	rootDir    string
	issuerUrls []string
	kubeIssuer bool
	caPool     *x509.CertPool
	maxTTL     time.Duration
	minTTL     time.Duration
}

// jwksCache is a cache that knows how to discover and load JSON web key sets from a list of trusted issuers. Don't
// create instances of this type directly, use the NewJwksCache function instead.
type jwksCache struct {
	logger      *slog.Logger
	issuersInfo map[string]*jwksCacheIssuerInfo
	kubeIssuer  bool
	maxTTL      time.Duration
	minTTL      time.Duration
	httpClient  *http.Client
	cache       map[jwksCacheTag]any
	cacheLock   sync.RWMutex
}

// ErrBadKey is returned when the key is not found.
var ErrBadKey = errors.New("bad key")

// ErrBadIssuer is returned when the issuer URL is not trusted.
var ErrBadIssuer = errors.New("bad issuer")

// jwksCacheIssuerInfo holds discovered information about a trusted issuer.
type jwksCacheIssuerInfo struct {
	// issuerUrl is the URL of the issuer.
	issuerUrl string

	// jwksUrl is the URL of the JSON web key set, discovered via OpenID Connect discovery. It is empty until
	// discovery is performed.
	jwksUrl string

	// tokenFile is the file that contains the bearer token that will be used to authenticate to the OpenID
	// discovery and JSON web key set endpoints for this issuer. This is intended for use with the Kubernetes API
	// server issuer, where the authentication with a service account token is required.
	tokenFile string

	// refreshNano stores the time of the last successful refresh as Unix nanoseconds. It is accessed atomically
	// so that the read path in Get does not need to acquire a lock.
	refreshNano atomic.Int64

	// refreshLock serializes refresh operations for this issuer.
	refreshLock sync.Mutex
}

// age returns how much time has elapsed since this issuer was last successfully refreshed. If the issuer has never been
// refreshed, it returns a value that will always exceed any configured TTL.
func (i *jwksCacheIssuerInfo) age() time.Duration {
	nano := i.refreshNano.Load()
	if nano == 0 {
		return time.Duration(1<<63 - 1)
	}
	return time.Since(time.Unix(0, nano))
}

// jwksCacheTag is a tag that identifies a key in the keys cache.
type jwksCacheTag struct {
	issuerUrl string
	keyId     string
}

// NewJwksCache creates a builder that can then be used to configure and create a new JWKS authentication interceptor.
func NewJwksCache() *JwksCacheBuilder {
	return &JwksCacheBuilder{
		minTTL: 1 * time.Minute,
		maxTTL: 1 * time.Hour,
	}
}

// SetLogger sets the logger that will be used to write to the log. This is mandatory.
func (b *JwksCacheBuilder) SetLogger(value *slog.Logger) *JwksCacheBuilder {
	b.logger = value
	return b
}

// AddIssuer adds a trusted token issuer URL. At least one issuer URL must be configured, either using this method, the
// AddIssuers method or the AddKubernetesIssuer method.
func (b *JwksCacheBuilder) AddIssuer(value string) *JwksCacheBuilder {
	b.issuerUrls = append(b.issuerUrls, value)
	return b
}

// AddIssuers adds a list of trusted token issuer URLs. At least one token issuer must be configured, either using this
// method, the AddIssuer method or the AddKubernetesIssuer method.
func (b *JwksCacheBuilder) AddIssuers(values ...string) *JwksCacheBuilder {
	b.issuerUrls = append(b.issuerUrls, values...)
	return b
}

// AddKubernetesIssuer specifies if the Kubernetes API server issuer should be automatically added to the list of
// issuers. When this is set to 'true', the cache will check if it is running inside a Kubernetes pod, and if so, it
// will automatically add the Kubernetes issuer URL to the list of trusted issuers. The default is 'false'.
func (b *JwksCacheBuilder) AddKubernetesIssuer(value bool) *JwksCacheBuilder {
	b.kubeIssuer = value
	return b
}

// SetCaPool sets the certificate authorities that will be trusted when verifying the TLS certificate of the servers
// where JSON web key sets are loaded from. If not set, the system's root CA pool will be used.
func (b *JwksCacheBuilder) SetCaPool(value *x509.CertPool) *JwksCacheBuilder {
	b.caPool = value
	return b
}

// SetTTL sets the minimum and maximum time-to-live for cached JSON web keys sets. The defaults are one minute and one
// hour, respectively.
func (b *JwksCacheBuilder) SetTTL(min, max time.Duration) *JwksCacheBuilder {
	b.minTTL = min
	b.maxTTL = max
	return b
}

// SetRoot sets a custom root directory for resolving file paths. This method is primarily intended for unit tests where
// you need to simulate the presence of files (like Kubernetes token files) in a controlled environment. When a root is
// set, all file paths (both absolute and relative) will be resolved relative to this root directory.
//
// For regular use, there is typically no need to call this method as the default behavior of using paths as-is from the
// filesystem is appropriate for production environments.
func (b *JwksCacheBuilder) SetRoot(value string) *JwksCacheBuilder {
	b.rootDir = value
	return b
}

// Build uses the data stored in the builder to create and configure a new interceptor.
func (b *JwksCacheBuilder) Build() (result JwksCache, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.minTTL < 0 {
		err = errors.New("minimum TTL must be zero or positive")
		return
	}
	if b.maxTTL < 0 {
		err = errors.New("maximum TTL must be zero or positive")
		return
	}
	if b.minTTL > b.maxTTL {
		err = errors.New("minimum TTL must be less than or equal to maximum TTL")
		return
	}

	// Check the list of issuers provided by the caller:
	issuersInfo := map[string]*jwksCacheIssuerInfo{}
	for _, issuerUrl := range b.issuerUrls {
		issuerUrl = strings.TrimSpace(issuerUrl)
		if issuerUrl == "" {
			err = errors.New("invalid empty issuer URL")
			return
		}
		var parsed *url.URL
		parsed, err = url.Parse(issuerUrl)
		if err != nil {
			err = fmt.Errorf("invalid issuer URL '%s': %w", issuerUrl, err)
			return
		}
		if parsed.Scheme != "https" {
			err = fmt.Errorf("issuer URL '%s' must use the HTTPS scheme", issuerUrl)
			return
		}
		issuerUrl = parsed.String()
		issuersInfo[issuerUrl] = &jwksCacheIssuerInfo{
			issuerUrl: issuerUrl,
		}
	}

	// Add the Kubernetes issuer, if requested by the caller:
	if b.kubeIssuer {
		tokenFile := b.resolvePath(kubefiles.ServiceAccountToken)
		var tokenObject *jwt.Token
		tokenObject, err = b.loadKubeToken(tokenFile)
		if err != nil {
			return
		}
		if tokenObject != nil {
			var issuerUrl string
			issuerUrl, err = tokenObject.Claims.GetIssuer()
			if err != nil {
				err = fmt.Errorf(
					"failed to get the 'iss' claim from Kubernetes service account token: %w",
					err,
				)
				return
			}
			var issuerParsed *url.URL
			issuerParsed, err = url.Parse(issuerUrl)
			if err != nil {
				err = fmt.Errorf(
					"failed to parse the 'iss' claim '%s' from Kubernetes service account token: %w",
					issuerUrl, err,
				)
				return
			}
			if issuerParsed.Scheme != "https" {
				err = fmt.Errorf(
					"the 'iss' claim '%s' from Kubernetes service account token must use HTTPS",
					issuerUrl,
				)
				return
			}
			if issuerParsed.Host == "" {
				err = fmt.Errorf(
					"the 'iss' claim '%s' from Kubernetes service account token must have a host",
					issuerUrl,
				)
				return
			}
			issuerUrl = issuerParsed.String()
			issuersInfo[issuerUrl] = &jwksCacheIssuerInfo{
				issuerUrl: issuerUrl,
				tokenFile: tokenFile,
			}
		}
	}

	// Check that at least one issuer has been configured, as otherwise the interceptor will not be able to
	// authenticate any request.
	if len(issuersInfo) == 0 {
		err = errors.New("at least one issuer must be configured")
		return
	}

	// Create the HTTP client used to download JSON web key sets:
	httpClient := &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 10 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			TLSClientConfig: &tls.Config{
				RootCAs: b.caPool,
			},
		},
	}

	// Create and populate the object:
	result = &jwksCache{
		logger:      b.logger,
		issuersInfo: issuersInfo,
		kubeIssuer:  b.kubeIssuer,
		httpClient:  httpClient,
		maxTTL:      b.maxTTL,
		minTTL:      b.minTTL,
		cache:       map[jwksCacheTag]any{},
	}
	return
}

// loadKubeToken loads the Kubernetes service account token file and parses it, but without verifying the signature.
// Returns nil if the file does not exist.
func (b *JwksCacheBuilder) loadKubeToken(tokenFile string) (result *jwt.Token, err error) {
	// Try to load the file:
	tokenBytes, err := os.ReadFile(tokenFile) //nolint:gosec
	if errors.Is(err, os.ErrNotExist) {
		err = nil
		return
	}
	if err != nil {
		err = fmt.Errorf(
			"failed to read Kubernetes service account token file '%s': %w",
			tokenFile, err,
		)
		return
	}
	tokenText := strings.TrimSpace(string(tokenBytes))

	// Try to parse the token, but without verifying the signature, as we don't have keys to verify the signature
	// yet. This is safe because we only use it to parse the Kubernetes service account token, and we only use it
	// to extract the issuer URL.
	tokenParser := jwt.NewParser()
	tokenObject, _, err := tokenParser.ParseUnverified(tokenText, jwt.MapClaims{})
	if err != nil {
		err = fmt.Errorf(
			"failed to parse Kubernetes service account token from file '%s': %w",
			tokenFile, err,
		)
		return
	}

	// Return the token:
	result = tokenObject
	return
}

// resolvePath resolves a file path using the custom root directory if set. If no root is set, returns the original
func (b *JwksCacheBuilder) resolvePath(path string) string {
	if b.rootDir == "" {
		return path
	}
	if filepath.IsAbs(path) {
		return filepath.Join(b.rootDir, path[1:])
	}
	return filepath.Join(b.rootDir, path)
}

// Get returns the key for the given issuer URL and key identifier. If the key is not found, an error is returned.
func (c *jwksCache) Get(ctx context.Context, issuerUrl string, keyId string) (result any, err error) {
	// Find the issuer information:
	issuerInfo, ok := c.issuersInfo[issuerUrl]
	if !ok {
		err = ErrBadIssuer
		return
	}

	// Try to find the key in the cache. If found but expired according to the TTL, return it immediately (so the
	// current request is not delayed) but trigger a background refresh. The context is detached from the request so
	// that the background work is not cancelled when the request completes.
	keyObj, ok := c.getEntry(issuerUrl, keyId)
	if ok {
		if c.maxTTL > 0 && issuerInfo.age() > c.maxTTL {
			refreshCtx := context.WithoutCancel(ctx)
			refreshCtx, refreshCancel := context.WithTimeout(refreshCtx, 15*time.Second)
			go func() {
				defer refreshCancel()
				err := c.refresh(refreshCtx, issuerInfo)
				if err != nil {
					c.logger.ErrorContext(
						refreshCtx,
						"Failed to refresh JSON web key set",
						slog.String("issuer", issuerUrl),
						slog.Any("error", err),
					)
				}
			}()
		}
		result = keyObj
		return
	}

	// If the key is not in the cache, try to refresh synchronously:
	err = c.refresh(ctx, issuerInfo)
	if err != nil {
		return
	}

	// Try again after refresh:
	keyObj, ok = c.getEntry(issuerUrl, keyId)
	if ok {
		result = keyObj
		return
	}

	// Report that we do not have a matching key:
	err = ErrBadKey
	return
}

// getEntry returns the key for the given issuer and key identifier. If the key is not found, the second return value
// is false.
func (c *jwksCache) getEntry(issuerUrl string, keyId string) (result any, ok bool) {
	c.cacheLock.RLock()
	defer c.cacheLock.RUnlock()
	keyTag := jwksCacheTag{
		issuerUrl: issuerUrl,
		keyId:     keyId,
	}
	result, ok = c.cache[keyTag]
	return
}

// replaceEntries replaces the keys in the cache for the given issuer with the contents of the given map. It will remove
// all existing keys for the issuer, and add the new ones. The given map should contain the key identifiers as the map
// keys, and the keys as the map values.
func (c *jwksCache) replaceEntries(issuerUrl string, keyMap map[string]any) {
	c.cacheLock.Lock()
	defer c.cacheLock.Unlock()
	for keyTag := range c.cache {
		if keyTag.issuerUrl == issuerUrl {
			delete(c.cache, keyTag)
		}
	}
	for keyId, keyObj := range keyMap {
		keyTag := jwksCacheTag{
			issuerUrl: issuerUrl,
			keyId:     keyId,
		}
		c.cache[keyTag] = keyObj
	}
}

// refresh loads all JSON web key sets into a temporary map and, only if that succeeds, replaces the live cache.
// This avoids leaving the cache empty when a network condition prevents reaching the remote key sets. The method is
// throttled per-issuer to at most once per the minimum TTL.
func (c *jwksCache) refresh(ctx context.Context, issuerInfo *jwksCacheIssuerInfo) error {
	issuerInfo.refreshLock.Lock()
	defer issuerInfo.refreshLock.Unlock()
	if issuerInfo.age() <= c.minTTL {
		return nil
	}
	keyMap, err := c.loadKeys(ctx, issuerInfo)
	if err != nil {
		return err
	}
	c.replaceEntries(issuerInfo.issuerUrl, keyMap)
	issuerInfo.refreshNano.Store(time.Now().UnixNano())
	return nil
}

// loadKeys loads the JSON web key sets from the given issuer, discovering the JSON web key set URI via OpenID Connect
// discovery if not already done.
func (c *jwksCache) loadKeys(ctx context.Context, issuerInfo *jwksCacheIssuerInfo) (result map[string]any,
	err error) {
	if issuerInfo.jwksUrl == "" {
		err = c.discoverJwksUrl(ctx, issuerInfo)
		if err != nil {
			return
		}
	}
	result, err = c.loadJwksUrl(ctx, issuerInfo)
	return
}

// discoverJwksUrl fetches the OpenID discovery document from the issuer and returns saves the 'jwks_uri' into the
// given issuer information structure.
func (c *jwksCache) discoverJwksUrl(ctx context.Context, issuerInfo *jwksCacheIssuerInfo) error {
	discoUrl := issuerInfo.issuerUrl + "/.well-known/openid-configuration"
	discoRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, discoUrl, nil)
	if err != nil {
		return fmt.Errorf("failed to create discovery request: %w", err)
	}
	if issuerInfo.tokenFile != "" {
		tokenText, err := c.loadTokenFile(issuerInfo.tokenFile)
		if err != nil {
			return err
		}
		discoRequest.Header.Set(Authorization, fmt.Sprintf("Bearer %s", tokenText))
	}
	discoResponse, err := c.httpClient.Do(discoRequest)
	if err != nil {
		return fmt.Errorf("failed to fetch discovery document from '%s': %w", discoUrl, err)
	}
	defer discoResponse.Body.Close()
	if discoResponse.StatusCode != http.StatusOK {
		return fmt.Errorf(
			"discovery request to '%s' failed with status code %d",
			discoUrl, discoResponse.StatusCode,
		)
	}
	var discoDoc struct {
		JwksUri string `json:"jwks_uri"`
	}
	err = json.NewDecoder(discoResponse.Body).Decode(&discoDoc)
	if err != nil {
		return fmt.Errorf("failed to parse discovery document from '%s': %w", discoUrl, err)
	}
	if discoDoc.JwksUri == "" {
		return fmt.Errorf("discovery document from '%s' does not contain a 'jwks_uri' value", discoUrl)
	}
	jwksUri, err := url.Parse(discoDoc.JwksUri)
	if err != nil {
		return fmt.Errorf("failed to parse discovered JSON web key set URL '%s': %w", discoDoc.JwksUri, err)
	}
	if jwksUri.Scheme != "https" {
		return fmt.Errorf("discovered JSON web key set URL '%s' must use the HTTPS scheme", discoDoc.JwksUri)
	}
	c.logger.InfoContext(
		ctx,
		"Discovered JSON web key set URL",
		slog.String("issuer", issuerInfo.issuerUrl),
		slog.String("url", discoDoc.JwksUri),
	)
	issuerInfo.jwksUrl = discoDoc.JwksUri
	return nil
}

// loadJwksUrl loads a JSON web key set from a URL into the given target cache.
func (c *jwksCache) loadJwksUrl(ctx context.Context, issuerInfo *jwksCacheIssuerInfo) (result map[string]any,
	err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, issuerInfo.jwksUrl, nil)
	if err != nil {
		return
	}
	if issuerInfo.tokenFile != "" {
		var tokenText string
		tokenText, err = c.loadTokenFile(issuerInfo.tokenFile)
		if err != nil {
			return
		}
		request.Header.Set(Authorization, fmt.Sprintf("Bearer %s", tokenText))
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return
	}
	defer func() {
		err := response.Body.Close()
		if err != nil {
			c.logger.ErrorContext(
				ctx,
				"Failed to close response body",
				slog.String("url", issuerInfo.jwksUrl),
				slog.Any("error", err),
			)
		}
	}()
	if response.StatusCode != http.StatusOK {
		err = fmt.Errorf(
			"request to load keys from '%s' failed with status code %d",
			issuerInfo.jwksUrl, response.StatusCode,
		)
		return
	}
	result, err = c.readKeys(ctx, issuerInfo, response.Body)
	return
}

// loadTokenFile loads a token from a file.
func (c *jwksCache) loadTokenFile(tokenFile string) (result string, err error) {
	tokenBytes, err := os.ReadFile(tokenFile) //nolint:gosec
	if err != nil {
		err = fmt.Errorf("failed to read token from file '%s': %w", tokenFile, err)
		return
	}
	result = strings.TrimSpace(string(tokenBytes))
	return
}

// readKeys reads and parses a JSON web key set from the given reader into the given target cache.
func (c *jwksCache) readKeys(ctx context.Context, issuerInfo *jwksCacheIssuerInfo,
	reader io.Reader) (result map[string]any, err error) {
	jsonReader := io.LimitReader(reader, jwksMaxSize+1)
	jsonData, err := io.ReadAll(jsonReader)
	if err != nil {
		return
	}
	if len(jsonData) > jwksMaxSize {
		err = fmt.Errorf("JSON web key set is too large, maximum size is %d bytes", jwksMaxSize)
		return
	}
	var setData jwksCacheKeySetData
	err = json.Unmarshal(jsonData, &setData)
	if err != nil {
		return
	}
	keyMap := map[string]any{}
	for _, keyData := range setData.Keys {
		if keyData.Kid == "" {
			c.logger.WarnContext(
				ctx,
				"Skipping key without identifier",
			)
			continue
		}
		keyObj, err := c.parseKey(keyData)
		if err != nil {
			c.logger.WarnContext(
				ctx,
				"Skipping key that cannot be parsed",
				slog.String("kid", keyData.Kid),
				slog.Any("error", err),
			)
			continue
		}
		keyTag := jwksCacheTag{
			issuerUrl: issuerInfo.issuerUrl,
			keyId:     keyData.Kid,
		}
		keyMap[keyTag.keyId] = keyObj
		c.logger.InfoContext(
			ctx,
			"Loaded key",
			slog.String("issuer", keyTag.issuerUrl),
			slog.String("kid", keyTag.keyId),
		)
	}
	result = keyMap
	return
}

// parseKey converts JWKS key data to an RSA public key.
func (c *jwksCache) parseKey(data jwksCacheKeyData) (result any, err error) {
	if data.Kty == "" {
		err = errors.New("key type is missing")
		return
	}
	if !strings.EqualFold(data.Kty, "RSA") {
		err = fmt.Errorf("key type '%s' is not supported", data.Kty)
		return
	}
	if data.N == "" || data.E == "" {
		err = errors.New("RSA key is missing required 'n' or 'e' field")
		return
	}
	nb, err := base64.RawURLEncoding.DecodeString(data.N)
	if err != nil {
		return
	}
	eb, err := base64.RawURLEncoding.DecodeString(data.E)
	if err != nil {
		return
	}
	result = &rsa.PublicKey{
		N: new(big.Int).SetBytes(nb),
		E: int(new(big.Int).SetBytes(eb).Int64()),
	}
	return
}

// jwksCacheKeySetData is the JSON representation of a key set.
type jwksCacheKeySetData struct {
	Keys []jwksCacheKeyData `json:"keys"`
}

// jwksCacheKeyData is the JSON representation of a single key in aweb key.
type jwksCacheKeyData struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	E   string `json:"e"`
	N   string `json:"n"`
}

// jwksMaxSize is the maximum size of a JSON web key set in bytes, currently set to 1 MiB. This is to prevent `
// denial of service attacks by limiting the size of the JSON web key set that can be loaded. This is a security
const jwksMaxSize = 1 << 20
