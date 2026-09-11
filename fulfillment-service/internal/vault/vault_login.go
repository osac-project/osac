/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package vault

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/network"
)

const (
	applicationJSON      = "application/json"
	vaultNamespaceHeader = "X-Vault-Namespace"
	contentTypeHeader    = "Content-Type"
)

func newHTTPClient(caPool *x509.CertPool) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("unexpected default transport type")
	}
	cloned := transport.Clone()
	tlsConfig := network.NewClientTLSConfig()
	if caPool != nil {
		tlsConfig.RootCAs = caPool
	}
	cloned.TLSClientConfig = tlsConfig
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: cloned,
	}, nil
}

type vaultLoginResponse struct {
	Auth *vaultAuthData `json:"auth"`
}

type vaultAuthData struct {
	ClientToken   string `json:"client_token"`
	LeaseDuration int    `json:"lease_duration"`
}

// loginToVault performs a JWT auth login against a Vault endpoint. It sends the given JWT to
// {vaultAddress}/v1/auth/{mountPath}/login with the specified namespace header, and returns
// the client token and lease duration.
func loginToVault(
	ctx context.Context,
	httpClient *http.Client,
	vaultAddress string,
	vaultNamespace string,
	authMountPath string,
	role string,
	jwt string,
) (string, int, error) {
	loginURL := fmt.Sprintf("%s/v1/auth/%s/login", vaultAddress, authMountPath)

	body, err := json.Marshal(map[string]string{
		"jwt":  jwt,
		"role": role,
	})
	if err != nil {
		return "", 0, fmt.Errorf("failed to marshal vault login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create vault login request: %w", err)
	}
	req.Header.Set(contentTypeHeader, applicationJSON)
	req.Header.Set(vaultNamespaceHeader, vaultNamespace)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("vault login request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("failed to read vault response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("vault login returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var loginResp vaultLoginResponse
	if err := json.Unmarshal(respBody, &loginResp); err != nil {
		return "", 0, fmt.Errorf("failed to decode vault login response: %w", err)
	}

	if loginResp.Auth == nil || loginResp.Auth.ClientToken == "" {
		return "", 0, errors.New("vault login response missing auth token")
	}

	return loginResp.Auth.ClientToken, loginResp.Auth.LeaseDuration, nil
}
