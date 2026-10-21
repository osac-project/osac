/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package idp

import (
	"context"
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
)

// CreateServiceAccountClient returns a partial UUID on failure so the caller can compensate.
// The secret is returned only after the client is fully configured and enabled.
func (c *Client) CreateServiceAccountClient(ctx context.Context, clientID, tenantID string) (clientUUID, clientSecret string, err error) {
	prefix := "osac-sa-" + tenantID + "-"
	if tenantID == "" || !strings.HasPrefix(clientID, prefix) {
		return "", "", errors.New("invalid service-account client identity")
	}
	if _, parseErr := uuid.Parse(strings.TrimPrefix(clientID, prefix)); parseErr != nil {
		return "", "", errors.New("invalid service-account client identity")
	}
	mayHaveCreated := false
	defer func() {
		if err == nil {
			return
		}
		clientSecret = ""
		// Cancellation or a lost POST response can leave a disabled client without a known UUID.
		if mayHaveCreated && clientUUID == "" {
			recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			recovered, lookupErr := c.findServiceAccountClient(recoveryCtx, clientID)
			if lookupErr == nil {
				clientUUID = recovered
			}
		}
	}()

	path := "/admin/realms/" + url.PathEscape(c.realmName) + "/clients"
	response, requestErr := c.httpClient.DoRequest(ctx, http.MethodPost, path, map[string]any{
		"clientId":                  clientID,
		"protocol":                  "openid-connect",
		"clientAuthenticatorType":   "client-secret",
		"enabled":                   false,
		"publicClient":              false,
		"serviceAccountsEnabled":    true,
		"standardFlowEnabled":       false,
		"implicitFlowEnabled":       false,
		"directAccessGrantsEnabled": false,
		"fullScopeAllowed":          false,
		"defaultClientScopes":       []string{},
		"optionalClientScopes":      []string{},
	})
	if requestErr != nil {
		var apiErr *apiclient.APIError
		mayHaveCreated = !errors.As(requestErr, &apiErr) || apiErr.StatusCode >= http.StatusInternalServerError
		return "", "", serviceAccountClientError("create client", requestErr)
	}
	response.Body.Close()
	mayHaveCreated = true
	clientUUID, err = c.findServiceAccountClient(ctx, clientID)
	if err != nil {
		return
	}
	path += "/" + url.PathEscape(clientUUID)

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
	response, err = c.httpClient.DoRequest(ctx, http.MethodPost, path+"/protocol-mappers/add-models", mappers)
	if err != nil {
		err = serviceAccountClientError("configure client mappers", err)
		return
	}
	response.Body.Close()

	response, err = c.httpClient.DoRequest(ctx, http.MethodGet, path+"/client-secret", nil)
	if err != nil {
		err = serviceAccountClientError("retrieve initial client credential", err)
		return
	}
	var credential keycloakCred
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&credential)
	response.Body.Close()
	if decodeErr != nil || credential.Value == "" {
		err = errors.New("invalid initial client credential response")
		return
	}
	response, err = c.httpClient.DoRequest(ctx, http.MethodPut, path, map[string]bool{"enabled": true})
	if err != nil {
		err = serviceAccountClientError("enable client", err)
		return
	}
	response.Body.Close()
	clientSecret = credential.Value
	return
}

func (c *Client) findServiceAccountClient(ctx context.Context, clientID string) (string, error) {
	query := url.Values{"clientId": {clientID}, "exact": {"true"}}
	path := "/admin/realms/" + url.PathEscape(c.realmName) + "/clients?" + query.Encode()
	response, err := c.httpClient.DoRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", serviceAccountClientError("resolve created client", err)
	}
	defer response.Body.Close()
	var clients []keycloakClient
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&clients); err != nil {
		return "", errors.New("invalid client lookup response")
	}
	if len(clients) != 1 || clients[0].ClientID != clientID {
		return "", errors.New("created client was not uniquely identified")
	}
	if _, err := uuid.Parse(clients[0].ID); err != nil {
		return "", errors.New("invalid client UUID in lookup response")
	}
	return clients[0].ID, nil
}

// DisableServiceAccountClient is idempotent when the client is already absent.
func (c *Client) DisableServiceAccountClient(ctx context.Context, clientUUID string) error {
	return c.cleanServiceAccountClient(ctx, http.MethodPut, clientUUID, map[string]bool{"enabled": false})
}

// DeleteServiceAccountClient is idempotent when the client is already absent.
func (c *Client) DeleteServiceAccountClient(ctx context.Context, clientUUID string) error {
	return c.cleanServiceAccountClient(ctx, http.MethodDelete, clientUUID, nil)
}

func (c *Client) cleanServiceAccountClient(ctx context.Context, method, clientUUID string, body any) error {
	if _, err := uuid.Parse(clientUUID); err != nil {
		return errors.New("invalid service-account client UUID")
	}
	path := "/admin/realms/" + url.PathEscape(c.realmName) + "/clients/" + url.PathEscape(clientUUID)
	response, err := c.httpClient.DoRequest(ctx, method, path, body)
	if err != nil {
		var apiErr *apiclient.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return serviceAccountClientError("clean up client", err)
	}
	response.Body.Close()
	return nil
}

// Upstream response bodies and transport errors may contain credentials.
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
