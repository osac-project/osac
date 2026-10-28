/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package secretapi provides an authenticated client for the private OSAC
// Secrets API.
package secretapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/oauth"
	experimentalcredentials "google.golang.org/grpc/experimental/credentials"
)

const (
	tokenHTTPTimeout = 30 * time.Second
	rpcTimeout       = 30 * time.Second
)

// Config contains the Secret API endpoint, OAuth client credential files, and
// optional CA bundle path.
type Config struct {
	Endpoint         string
	IssuerURL        string
	ClientIDFile     string
	ClientSecretFile string
	CAFile           string
}

// Filter is a CEL expression used to select Secrets.
type Filter string

// Secret is the generic data returned by the OSAC Secret API.
type Secret struct {
	ID     string
	Tenant string
	Labels map[string]string
	Data   map[string][]byte
}

// ListResult contains metadata for matching Secrets and the API-wide match
// count. List results intentionally omit Secret data.
type ListResult struct {
	Items []Secret
	Total int32
}

type secretsAPI interface {
	List(ctx context.Context, request *privatev1.SecretsListRequest, options ...grpc.CallOption) (*privatev1.SecretsListResponse, error)
	Get(ctx context.Context, request *privatev1.SecretsGetRequest, options ...grpc.CallOption) (*privatev1.SecretsGetResponse, error)
}

// client calls the private Secrets API using the configured OAuth client.
type client struct {
	api  secretsAPI
	conn *grpc.ClientConn
}

// New creates an authenticated TLS client. OAuth client credentials are read
// from mounted files and are never retained in the returned configuration.
func New(config Config) (*client, error) {
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	issuerURL, err := validateIssuerURL(config.IssuerURL)
	if err != nil {
		return nil, err
	}
	clientID, err := readCredentialFile(config.ClientIDFile, "client ID")
	if err != nil {
		return nil, err
	}
	clientSecret, err := readCredentialFile(config.ClientSecretFile, "client secret")
	if err != nil {
		return nil, err
	}
	tlsConfig, err := newTLSConfig(config.CAFile)
	if err != nil {
		return nil, err
	}
	conn, err := newConnection(endpoint, tlsConfig, newTokenSource(issuerURL, clientID, clientSecret, tlsConfig))
	if err != nil {
		return nil, err
	}
	return &client{api: privatev1.NewSecretsClient(conn), conn: conn}, nil
}

// parseEndpoint extracts the gRPC authority; the operator Helm chart enforces HTTPS.
func parseEndpoint(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return "", errors.New("Secret API endpoint must include a host")
	}
	return parsed.Host, nil
}

func validateIssuerURL(issuerURL string) (string, error) {
	parsed, err := url.Parse(issuerURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || strings.ContainsAny(issuerURL, "?#") {
		return "", errors.New("Secret API issuer URL must use HTTPS")
	}
	return strings.TrimRight(issuerURL, "/"), nil
}

func readCredentialFile(path, name string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read Secret API %s file: %w", name, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("Secret API %s file is empty", name)
	}
	return value, nil
}

func newTLSConfig(caFile string) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile == "" {
		return config, nil
	}
	caBundle, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read Secret API CA file: %w", err)
	}
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if !rootCAs.AppendCertsFromPEM(caBundle) {
		return nil, errors.New("Secret API CA file contains no valid certificates")
	}
	config.RootCAs = rootCAs
	return config, nil
}

func newTokenSource(issuerURL, clientID, clientSecret string, tlsConfig *tls.Config) oauth2.TokenSource {
	tokenContext := context.WithValue(
		context.Background(),
		oauth2.HTTPClient,
		newTokenHTTPClient(tlsConfig),
	)
	credentials := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     issuerURL + "/protocol/openid-connect/token",
		AuthStyle:    oauth2.AuthStyleInHeader,
	}
	return credentials.TokenSource(tokenContext)
}

func newTokenHTTPClient(tlsConfig *tls.Config) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: tlsConfig.Clone(),
		},
		Timeout: tokenHTTPTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newConnection(endpoint string, tlsConfig *tls.Config, tokenSource oauth2.TokenSource) (*grpc.ClientConn, error) {
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(experimentalcredentials.NewTLSWithALPNDisabled(tlsConfig.Clone())),
		grpc.WithPerRPCCredentials(oauth.TokenSource{TokenSource: tokenSource}),
	}
	conn, err := grpc.NewClient(endpoint, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Secret API gRPC client: %w", err)
	}
	return conn, nil
}

// FilterByTenantAndLabel returns CEL for a tenant and label key.
func FilterByTenantAndLabel(tenant, labelKey string) Filter {
	return Filter(tenantFilter(tenant) + " && " + labelExistsFilter(labelKey))
}

func tenantFilter(tenant string) string {
	return "this.metadata.tenant == " + strconv.Quote(tenant)
}

func labelExistsFilter(key string) string {
	return strconv.Quote(key) + " in this.metadata.labels"
}

// List returns matching Secret metadata. Secret data is available only from
// Get and is never copied from a List response.
func (c *client) List(ctx context.Context, filter Filter, limit int32) (ListResult, error) {
	filterValue := string(filter)
	if strings.TrimSpace(filterValue) == "" {
		return ListResult{}, errors.New("Secret API filter is required")
	}
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	response, err := c.api.List(ctx, &privatev1.SecretsListRequest{Filter: &filterValue, Limit: &limit})
	if err != nil {
		return ListResult{}, fmt.Errorf("failed to list OSAC Secrets: %w", err)
	}
	result := ListResult{Total: response.GetTotal(), Items: make([]Secret, 0, len(response.GetItems()))}
	for _, item := range response.GetItems() {
		result.Items = append(result.Items, secretFromProto(item, false))
	}
	return result, nil
}

// Get returns one Secret, including its opaque data fields.
func (c *client) Get(ctx context.Context, id string) (Secret, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	response, err := c.api.Get(ctx, &privatev1.SecretsGetRequest{Id: id})
	if err != nil {
		return Secret{}, fmt.Errorf("failed to get OSAC Secret: %w", err)
	}
	return secretFromProto(response.GetObject(), true), nil
}

func secretFromProto(secret *privatev1.Secret, includeData bool) Secret {
	result := Secret{ID: secret.GetId()}
	if metadata := secret.GetMetadata(); metadata != nil {
		result.Tenant = metadata.GetTenant()
		result.Labels = metadata.GetLabels()
	}
	if includeData {
		result.Data = secret.GetData()
	}
	return result
}

// Close releases the gRPC connection owned by the client.
func (c *client) Close() error {
	return c.conn.Close()
}
