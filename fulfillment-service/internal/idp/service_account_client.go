//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package idp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osac-project/osac/fulfillment-service/internal/apiclient"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

var ErrServiceAccountClientAlreadyExists = errors.New(
	"service-account client already exists without recoverable registration credentials",
)

const serviceAccountClientLookupTimeout = 10 * time.Second

func (c *Client) CreateServiceAccountClient(ctx context.Context, clientID, tenantID string) (
	clientUUID, clientSecret, registrationAccessToken string, err error,
) {
	prefix := "osac-sa-" + tenantID + "-"
	if tenantID == "" || !strings.HasPrefix(clientID, prefix) {
		return "", "", "", errors.New("invalid service-account client identity")
	}
	if _, parseErr := uuid.Parse(strings.TrimPrefix(clientID, prefix)); parseErr != nil {
		return "", "", "", errors.New("invalid service-account client identity")
	}

	path := "/realms/" + url.PathEscape(c.realmName) + "/clients-registrations/default"
	mappers := []map[string]any{
		{
			"name":           "osac-tenant",
			"protocol":       "openid-connect",
			"protocolMapper": "oidc-hardcoded-claim-mapper",
			"config": map[string]string{
				"claim.name":           "osac_tenant",
				"claim.value":          tenantID,
				"jsonType.label":       "String",
				"access.token.claim":   "true",
				"id.token.claim":       "false",
				"userinfo.token.claim": "false",
			},
		},
		{
			"name":           "osac-api-audience",
			"protocol":       "openid-connect",
			"protocolMapper": "oidc-audience-mapper",
			"config": map[string]string{
				"included.custom.audience": "osac-api",
				"access.token.claim":       "true",
				"id.token.claim":           "false",
			},
		},
	}
	response, err := c.httpClient.DoRequest(ctx, http.MethodPost, path, map[string]any{
		"clientId":                  clientID,
		"protocol":                  "openid-connect",
		"clientAuthenticatorType":   "client-secret",
		"enabled":                   true,
		"publicClient":              false,
		"serviceAccountsEnabled":    true,
		"standardFlowEnabled":       false,
		"implicitFlowEnabled":       false,
		"directAccessGrantsEnabled": false,
		"fullScopeAllowed":          false,
		"defaultClientScopes":       []string{"service_account"},
		"optionalClientScopes":      []string{},
		"protocolMappers":           mappers,
	})
	if err != nil {
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), serviceAccountClientLookupTimeout)
		_, found, lookupErr := c.FindServiceAccountClient(lookupCtx, clientID, tenantID)
		cancel()
		if lookupErr == nil && found {
			return "", "", "", ErrServiceAccountClientAlreadyExists
		}
		if lookupErr != nil {
			return "", "", "", errors.Join(
				serviceAccountClientError("create client", err),
				fmt.Errorf("failed to verify service-account client state: %w", lookupErr),
			)
		}
		return "", "", "", serviceAccountClientError("create client", err)
	}
	defer response.Body.Close()

	var created struct {
		ID                      string `json:"id"`
		ClientID                string `json:"clientId"`
		Secret                  string `json:"secret"`
		RegistrationAccessToken string `json:"registrationAccessToken"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&created)
	if created.ClientID == clientID {
		clientUUID = created.ID
		registrationAccessToken = created.RegistrationAccessToken
	}
	if decodeErr != nil || created.ClientID != clientID {
		return clientUUID, "", registrationAccessToken, errors.New("invalid service-account client registration response")
	}
	if _, parseErr := uuid.Parse(clientUUID); parseErr != nil {
		return clientUUID, "", registrationAccessToken, errors.New("invalid Keycloak client UUID in registration response")
	}
	if created.Secret == "" || registrationAccessToken == "" {
		return clientUUID, "", registrationAccessToken, errors.New("incomplete service-account client registration response")
	}
	return clientUUID, created.Secret, registrationAccessToken, nil
}

// FindServiceAccountClient looks up the deterministic client ID with the
// controller's view-clients permission. It never returns client credentials.
func (c *Client) FindServiceAccountClient(ctx context.Context, clientID, tenantID string) (string, bool, error) {
	prefix := "osac-sa-" + tenantID + "-"
	if tenantID == "" || !strings.HasPrefix(clientID, prefix) || !validServiceAccountClientID(clientID) {
		return "", false, errors.New("invalid service-account client identity")
	}
	path := "/admin/realms/" + url.PathEscape(c.realmName) + "/clients?clientId=" + url.QueryEscape(clientID)
	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", false, serviceAccountClientError("find client", err)
	}
	defer response.Body.Close()

	var clients []keycloakClient
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&clients); err != nil {
		return "", false, errors.New("invalid service-account client lookup response")
	}
	var found *keycloakClient
	for index := range clients {
		if clients[index].ClientID != clientID {
			continue
		}
		if found != nil {
			return "", false, errors.New("ambiguous service-account client lookup response")
		}
		found = &clients[index]
	}
	if found == nil {
		return "", false, nil
	}
	if _, err = uuid.Parse(found.ID); err != nil {
		return "", false, errors.New("invalid Keycloak client UUID in lookup response")
	}
	return found.ID, true, nil
}

// RotateServiceAccountClientSecret replaces the secret through the client's
// registration token, so rotation does not require realm-wide manage-clients.
// Keycloak can rotate the registration token on update; both values must be
// staged by the caller before publishing the secret to the tenant-scoped Secret.
func (c *Client) RotateServiceAccountClientSecret(ctx context.Context, clientID, clientUUID,
	registrationAccessToken string) (clientSecret, newRegistrationAccessToken string, err error) {
	if !validServiceAccountClientID(clientID) || registrationAccessToken == "" {
		return "", "", errors.New("invalid service-account client rotation credentials")
	}
	if _, err := uuid.Parse(clientUUID); err != nil {
		return "", "", errors.New("invalid service-account client UUID")
	}
	client, err := c.clientWithRegistrationToken(registrationAccessToken)
	if err != nil {
		return "", "", errors.New("failed to configure service-account client rotation")
	}
	path := "/realms/" + url.PathEscape(c.realmName) + "/clients-registrations/default/" + url.PathEscape(clientID)
	response, err := client.DoRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", "", serviceAccountClientError("get client for secret rotation", err)
	}
	var representation map[string]json.RawMessage
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&representation)
	closeErr := response.Body.Close()
	if decodeErr != nil || closeErr != nil {
		return "", "", errors.New("invalid service-account client configuration for secret rotation")
	}
	var returnedID, returnedClientID string
	if json.Unmarshal(representation["id"], &returnedID) != nil || returnedID != clientUUID ||
		json.Unmarshal(representation["clientId"], &returnedClientID) != nil || returnedClientID != clientID {
		return "", "", errors.New("service-account registration token resolved to a different client")
	}

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", "", errors.New("failed to generate service-account client secret")
	}
	clientSecret = base64.RawURLEncoding.EncodeToString(secretBytes)
	encodedSecret, err := json.Marshal(clientSecret)
	if err != nil {
		return "", "", errors.New("failed to encode service-account client secret")
	}
	representation["secret"] = encodedSecret
	delete(representation, "registrationAccessToken")

	response, err = client.DoRequest(ctx, http.MethodPut, path, representation)
	if err != nil {
		return "", "", serviceAccountClientError("rotate client secret", err)
	}
	defer response.Body.Close()
	var updated struct {
		ID                      string `json:"id"`
		ClientID                string `json:"clientId"`
		RegistrationAccessToken string `json:"registrationAccessToken"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&updated); err != nil ||
		updated.ID != clientUUID || updated.ClientID != clientID {
		return "", "", errors.New("invalid service-account client rotation response")
	}
	if updated.RegistrationAccessToken == "" {
		updated.RegistrationAccessToken = registrationAccessToken
	}
	return clientSecret, updated.RegistrationAccessToken, nil
}

func (c *Client) DeleteServiceAccountClient(ctx context.Context, clientID, clientUUID, registrationAccessToken string) error {
	if !validServiceAccountClientID(clientID) {
		return errors.New("invalid service-account client cleanup credentials")
	}
	if registrationAccessToken == "" {
		// The token is absent only after Vault cleanup succeeded but the
		// controller could not persist finalizer removal. Verify Keycloak state
		// before allowing the controller to finish cleanup.
		exists, err := c.serviceAccountClientExists(ctx, clientUUID)
		if err != nil {
			return serviceAccountClientError("verify client existence", err)
		}
		if exists {
			return errors.New("service-account client exists without a registration token")
		}
		return nil
	}
	client, err := c.clientWithRegistrationToken(registrationAccessToken)
	if err != nil {
		return errors.New("failed to configure service-account client cleanup")
	}
	path := "/realms/" + url.PathEscape(c.realmName) + "/clients-registrations/default/" + url.PathEscape(clientID)
	response, err := client.DoRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		var apiErr *apiclient.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case http.StatusNotFound:
				return nil
			case http.StatusUnauthorized:
				exists, checkErr := c.serviceAccountClientExists(ctx, clientUUID)
				if checkErr == nil && !exists {
					return nil
				}
				deleteErr := serviceAccountClientError("delete client", err)
				if checkErr != nil {
					return fmt.Errorf("%w; failed to verify client absence: %w", deleteErr,
						serviceAccountClientError("verify client existence", checkErr))
				}
				return deleteErr
			}
		}
		return serviceAccountClientError("delete client", err)
	}
	return response.Body.Close()
}

func (c *Client) serviceAccountClientExists(ctx context.Context, clientUUID string) (bool, error) {
	if _, err := uuid.Parse(clientUUID); err != nil {
		return false, errors.New("invalid service-account client UUID")
	}
	path := "/admin/realms/" + url.PathEscape(c.realmName) + "/clients/" + url.PathEscape(clientUUID)
	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		var apiErr *apiclient.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return true, response.Body.Close()
}

func (c *Client) clientWithRegistrationToken(token string) (*apiclient.Client, error) {
	tokenSource, err := auth.NewStaticTokenSource().SetLogger(c.logger).
		SetToken(&auth.Token{Access: token}).Build()
	if err != nil {
		return nil, err
	}
	return apiclient.NewClient().SetLogger(c.logger).SetBaseURL(c.baseURL).
		SetTokenSource(tokenSource).SetHTTPClient(c.registrationHTTPClient).Build()
}

func validServiceAccountClientID(clientID string) bool {
	if !strings.HasPrefix(clientID, "osac-sa-") || len(clientID) < len("osac-sa-")+1+36 {
		return false
	}
	tenantEnd := len(clientID) - 37
	if tenantEnd <= len("osac-sa-") || clientID[tenantEnd] != '-' {
		return false
	}
	_, err := uuid.Parse(clientID[len(clientID)-36:])
	return err == nil && !strings.ContainsAny(clientID, "/\\")
}

func serviceAccountClientError(operation string, err error) error {
	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("failed to %s: HTTP %d", operation, apiErr.StatusCode)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("failed to %s: %w", operation, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("failed to %s: %w", operation, context.DeadlineExceeded)
	}
	return fmt.Errorf("failed to %s", operation)
}
