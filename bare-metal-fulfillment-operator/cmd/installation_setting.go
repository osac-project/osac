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

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/experimental/credentials"
	"google.golang.org/grpc/status"
)

const (
	installationChangedMessage = "global.networking.provisioningEnabled cannot change after installation; " +
		"reinstall to change it"
	installationCredentialsMessage = "installation API credentials unavailable; create the " +
		"fulfillment-controller-credentials Secret with client-id and client-secret keys"
	installationHTTPS = "https"
)

func verifyInstallationProvisioning(
	ctx context.Context, client privatev1.InstallationSettingsClient, configured bool,
) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := client.Get(ctx, &privatev1.InstallationSettingsGetRequest{})
	if err != nil {
		return fmt.Errorf("read installation networking setting: gRPC status %s", status.Code(err))
	}
	if response.GetNetworkingProvisioningEnabled() != configured {
		return errors.New(installationChangedMessage)
	}
	return nil
}

func checkInstallationProvisioning(ctx context.Context, configured bool) error {
	rawURL := os.Getenv("OSAC_INSTALLATION_API_URL")
	endpoint, err := url.Parse(rawURL)
	if err != nil || endpoint.Scheme != installationHTTPS || endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("OSAC_INSTALLATION_API_URL must be an HTTPS URL")
	}
	issuer := strings.TrimRight(os.Getenv("OSAC_INSTALLATION_API_ISSUER_URL"), "/")
	issuerURL, err := url.Parse(issuer)
	if err != nil || issuerURL.Scheme != installationHTTPS || issuerURL.Host == "" || issuerURL.User != nil ||
		issuerURL.RawQuery != "" || issuerURL.Fragment != "" {
		return errors.New("OSAC_INSTALLATION_API_ISSUER_URL must be an HTTPS URL")
	}
	clientID, err := os.ReadFile(os.Getenv("OSAC_INSTALLATION_API_CLIENT_ID_FILE"))
	if err != nil {
		return errors.New(installationCredentialsMessage)
	}
	clientSecret, err := os.ReadFile(os.Getenv("OSAC_INSTALLATION_API_CLIENT_SECRET_FILE"))
	if err != nil {
		return errors.New(installationCredentialsMessage)
	}
	if strings.TrimSpace(string(clientID)) == "" || strings.TrimSpace(string(clientSecret)) == "" {
		return errors.New("installation API client credentials are empty")
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return errors.New("load system CA certificates failed")
	}
	if caFile := os.Getenv("OSAC_INSTALLATION_API_CA_FILE"); caFile != "" {
		bundle, readErr := os.ReadFile(caFile)
		if readErr != nil {
			return errors.New("read installation API CA bundle failed")
		}
		if !pool.AppendCertsFromPEM(bundle) {
			return errors.New("installation API CA bundle has no certificates")
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
		return err
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
		return errors.New("connect to installation API failed")
	}
	defer conn.Close() //nolint:errcheck
	return verifyInstallationProvisioning(ctx, privatev1.NewInstallationSettingsClient(conn), configured)
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
