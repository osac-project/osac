/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/experimental/credentials"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/controller"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/networkingmode"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	installationCredentialsMessage = "installation API credentials unavailable; create the " +
		"fulfillment-controller-credentials Secret with client-id and client-secret keys"
	installationHTTPS = "https"
)

// newProvisioningMode creates the resolver of the installation-wide provider networking mode. A deployment with an
// installation API endpoint reads the mode from the deployment's NetworkClass. A standalone deployment, which has no
// fulfillment service and no NetworkClass, uses its explicit OSAC_ENABLE_NETWORKING_PROVISIONING value.
func newProvisioningMode(ctx context.Context) (controller.ProvisioningMode, error) {
	configured := configuredNetworkingProvisioning()
	if os.Getenv("OSAC_INSTALLATION_API_URL") == "" {
		setupLog.Info("installation API not configured; using the standalone networking mode",
			"enabled", loadNetworkingProvisioning())
		return controller.StaticProvisioningMode(loadNetworkingProvisioning()), nil
	}
	conn, err := newInstallationAPIConnection(ctx)
	if err != nil {
		return nil, err
	}
	return networkingmode.NewResolver(privatev1.NewNetworkClassesClient(conn), configured), nil
}

// configuredNetworkingProvisioning returns the value rendered for this rollout, or nil when the deployment doesn't set
// it. An unset value asserts nothing; the NetworkClass is the authority.
func configuredNetworkingProvisioning() *bool {
	if value, ok := os.LookupEnv(envEnableNetworkingProvisioning); ok && value != "" {
		enabled := loadNetworkingProvisioning()
		return &enabled
	}
	return nil
}

// newInstallationAPIConnection opens the authenticated connection to the fulfillment private API.
func newInstallationAPIConnection(ctx context.Context) (*grpc.ClientConn, error) {
	rawURL := os.Getenv("OSAC_INSTALLATION_API_URL")
	endpoint, err := url.Parse(rawURL)
	if err != nil || endpoint.Scheme != installationHTTPS || endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("OSAC_INSTALLATION_API_URL must be an HTTPS URL")
	}
	issuer := strings.TrimRight(os.Getenv("OSAC_INSTALLATION_API_ISSUER_URL"), "/")
	issuerURL, err := url.Parse(issuer)
	if err != nil || issuerURL.Scheme != installationHTTPS || issuerURL.Host == "" || issuerURL.User != nil ||
		issuerURL.RawQuery != "" || issuerURL.Fragment != "" {
		return nil, errors.New("OSAC_INSTALLATION_API_ISSUER_URL must be an HTTPS URL")
	}
	clientID, err := os.ReadFile(os.Getenv("OSAC_INSTALLATION_API_CLIENT_ID_FILE"))
	if err != nil {
		return nil, errors.New(installationCredentialsMessage)
	}
	clientSecret, err := os.ReadFile(os.Getenv("OSAC_INSTALLATION_API_CLIENT_SECRET_FILE"))
	if err != nil {
		return nil, errors.New(installationCredentialsMessage)
	}
	if strings.TrimSpace(string(clientID)) == "" || strings.TrimSpace(string(clientSecret)) == "" {
		return nil, errors.New("installation API client credentials are empty")
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("load system CA certificates failed")
	}
	if caFile := os.Getenv("OSAC_INSTALLATION_API_CA_FILE"); caFile != "" {
		bundle, readErr := os.ReadFile(caFile)
		if readErr != nil {
			return nil, errors.New("read installation API CA bundle failed")
		}
		if !pool.AppendCertsFromPEM(bundle) {
			return nil, errors.New("installation API CA bundle has no certificates")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	httpClient := &http.Client{
		Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	tokenURL, err := discoverInstallationTokenURL(ctx, httpClient, issuerURL)
	if err != nil {
		return nil, err
	}
	tokenContext := context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	config := &clientcredentials.Config{
		ClientID: strings.TrimSpace(string(clientID)), ClientSecret: strings.TrimSpace(string(clientSecret)),
		TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInHeader,
	}
	source := config.TokenSource(tokenContext)
	tlsConfig := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	conn, err := grpc.NewClient(endpoint.Host,
		grpc.WithTransportCredentials(credentials.NewTLSWithALPNDisabled(tlsConfig)),
		grpc.WithPerRPCCredentials(oauth.TokenSource{TokenSource: source}),
	)
	if err != nil {
		return nil, errors.New("connect to installation API failed")
	}
	context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	return conn, nil
}

func discoverInstallationTokenURL(ctx context.Context, httpClient *http.Client, issuer *url.URL) (string, error) {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, issuer.String()+"/.well-known/openid-configuration", nil,
	)
	if err != nil {
		return "", errors.New("build issuer discovery request failed")
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return "", errors.New("discover installation API issuer failed")
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("issuer discovery returned status %d", response.StatusCode)
	}
	var metadata struct {
		Issuer        string `json:"issuer"`
		TokenEndpoint string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&metadata); err != nil {
		return "", errors.New("decode issuer metadata failed")
	}
	tokenURL, err := url.Parse(metadata.TokenEndpoint)
	if metadata.Issuer != issuer.String() || err != nil || tokenURL.Scheme != installationHTTPS ||
		!strings.EqualFold(tokenURL.Host, issuer.Host) || tokenURL.User != nil || tokenURL.RawQuery != "" ||
		tokenURL.Fragment != "" {
		return "", errors.New("issuer metadata or token endpoint does not match configured issuer")
	}
	return tokenURL.String(), nil
}
