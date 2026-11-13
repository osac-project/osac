//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/osac-project/osac/terraform-provider/internal/osaccli"
)

// requestTimeout bounds each individual HTTP request. The creation of a bare metal instance polls with repeated
// requests, so this doesn't bound the whole operation.
const requestTimeout = 60 * time.Second

// apiError is the error shape returned by the REST gateway: an HTTP status plus the gRPC code and message of the
// original error.
type apiError struct {
	Status  int
	Code    int
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s (HTTP %d, code %d)", e.Message, e.Status, e.Code)
}

// isNotFound returns true if the given error is the 'not found' error of the REST API.
func isNotFound(err error) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// Client is the connection to the fulfillment service REST gateway. The gateway is exposed on the same address that
// the `osac login` command stores, so the provider reuses the session created by the CLI: the same server address,
// CA certificates and OAuth credentials. The gateway marshals the objects with the original proto field names,
// which are the same names that the generated Terraform schemas use, so responses convert to state values and plans
// convert to request bodies without any per-resource mapping.
type Client struct {
	httpClient    *http.Client
	baseURL       string
	tokenSource   *osaccli.TokenSource
	logger        *slog.Logger
	DefaultTenant string
}

// NewClient creates a client using the given OSAC CLI settings.
func NewClient(ctx context.Context, logger *slog.Logger, settings *osaccli.Settings) (*Client, error) {
	// Create the token source. It is nil when there is no way to get tokens, and in that case the requests are
	// anonymous.
	var tokenSource *osaccli.TokenSource
	if settings.General.Flow != "" || settings.General.TokenScript != "" || settings.Secrets.AccessToken != "" {
		var err error
		tokenSource, err = osaccli.NewTokenSource(logger, settings, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create token source: %w", err)
		}
	}

	// Resolve the base URL. The gateway is served by the same ingress proxy as the gRPC API, on the standard HTTPS
	// port:
	baseURL := "https://" + strings.TrimSuffix(settings.General.Address, ":443")
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13},
	}
	if settings.General.Plaintext {
		baseURL = "http://" + settings.General.Address
	} else {
		caPool, err := settings.CaPool()
		if err != nil {
			return nil, fmt.Errorf("failed to create CA pool: %w", err)
		}
		transport.TLSClientConfig.RootCAs = caPool
		if settings.General.Insecure {
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
	}
	result := &Client{
		httpClient: &http.Client{
			Timeout:   requestTimeout,
			Transport: transport,
		},
		baseURL:       baseURL,
		tokenSource:   tokenSource,
		logger:        logger,
		DefaultTenant: settings.General.Tenant,
	}
	logger.DebugContext(ctx, "Created fulfillment service REST client", slog.String("url", baseURL))
	return result, nil
}

// Close releases the resources of the client. It is included for symmetry with the previous gRPC client.
func (c *Client) Close() error {
	return nil
}

// do sends a request to the gateway and returns the response body. Requests that fail with the unauthenticated
// error are retried once with a fresh token, like the gRPC client used to do.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any) ([]byte, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to encode the request: %w", err)
		}
	}
	attempt := func() ([]byte, int, error) {
		requestURL := c.baseURL + path
		if len(query) > 0 {
			requestURL += "?" + query.Encode()
		}
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		request, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
		if err != nil {
			return nil, 0, err
		}
		if payload != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if c.tokenSource != nil {
			token, err := c.tokenSource.Token(ctx)
			if err != nil {
				return nil, 0, fmt.Errorf("failed to get authorization token: %w", err)
			}
			request.Header.Set("Authorization", "Bearer "+token.Access)
		}
		response, err := c.httpClient.Do(request)
		if err != nil {
			return nil, 0, err
		}
		defer func() {
			_ = response.Body.Close()
		}()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, response.StatusCode, err
		}
		return data, response.StatusCode, nil
	}
	data, status, err := attempt()
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized && c.tokenSource != nil {
		c.tokenSource.Invalidate()
		data, status, err = attempt()
		if err != nil {
			return nil, err
		}
	}
	if status < 200 || status > 299 {
		var details struct {
			Code    int    `json:"code,omitempty"`
			Message string `json:"message,omitempty"`
		}
		_ = json.Unmarshal(data, &details)
		return nil, &apiError{Status: status, Code: details.Code, Message: details.Message}
	}
	return data, nil
}

// objectSummary is the minimal shape of an API object, used to extract identifiers and tenants.
type objectSummary struct {
	Id       string `json:"id"`
	Metadata struct {
		Name   string `json:"name"`
		Tenant string `json:"tenant"`
	} `json:"metadata"`
}

// list returns the objects of the given collection that match the given CEL filter.
func (c *Client) list(ctx context.Context, path, filter string) ([]objectSummary, error) {
	query := url.Values{}
	if filter != "" {
		query.Set("filter", filter)
	}
	data, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Items []objectSummary `json:"items"`
	}
	err = json.Unmarshal(data, &result)
	if err != nil {
		return nil, fmt.Errorf("failed to decode the list response: %w", err)
	}
	return result.Items, nil
}

// get returns the object with the given identifier.
func (c *Client) get(ctx context.Context, path, id string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path+"/"+id, nil, nil)
}

// create creates an object in the given collection and returns the response.
func (c *Client) create(ctx context.Context, path string, body any) ([]byte, error) {
	return c.do(ctx, http.MethodPost, path, nil, body)
}

// update sends a masked update of the object with the given identifier and returns the response.
func (c *Client) update(ctx context.Context, path, id string, body any, mask []string) ([]byte, error) {
	query := url.Values{}
	query.Set("updateMask", strings.Join(mask, ","))
	return c.do(ctx, http.MethodPatch, path+"/"+id, query, body)
}

// delete removes the object with the given identifier.
func (c *Client) delete(ctx context.Context, path, id string) error {
	_, err := c.do(ctx, http.MethodDelete, path+"/"+id, nil, nil)
	return err
}

// lookupID resolves the identifier of a data source lookup: the identifier itself when given, or the name through
// a list request, which must match exactly one object.
func (c *Client) lookupID(ctx context.Context, path, kind, name, id string) (string, error) {
	if id != "" {
		return id, nil
	}
	items, err := c.list(ctx, path, nameFilter(name))
	if err != nil {
		return "", fmt.Errorf("failed to find %s: %w", kind, err)
	}
	if len(items) != 1 {
		return "", fmt.Errorf("expected exactly one %s named '%s', but found %d", kind, name, len(items))
	}
	return items[0].Id, nil
}

// nameFilter builds the CEL filter that the fulfillment service uses to select objects by name.
func nameFilter(name string) string {
	return fmt.Sprintf("this.metadata.name == '%s'", name)
}

// resolveReference resolves the name or identifier of a reference to its identifier and whether it lives in the
// shared tenant. The server resolves references in the tenant of the containing object unless they are explicitly
// marked as shared, so both are needed to build a valid request body.
func (c *Client) resolveReference(ctx context.Context, path, kind, value string) (id string, shared bool, err error) {
	var object objectSummary
	if uuidPattern.MatchString(value) {
		data, err := c.get(ctx, path, value)
		if err != nil {
			return "", false, fmt.Errorf("failed to resolve the %s '%s': %w", kind, value, err)
		}
		if err := json.Unmarshal(data, &object); err != nil {
			return "", false, fmt.Errorf("failed to resolve the %s '%s': %w", kind, value, err)
		}
	} else {
		items, err := c.list(ctx, path, nameFilter(value))
		if err != nil {
			return "", false, fmt.Errorf("failed to resolve the %s '%s': %w", kind, value, err)
		}
		if len(items) != 1 {
			return "", false, fmt.Errorf(
				"expected exactly one %s named '%s', but found %d", kind, value, len(items),
			)
		}
		object = items[0]
	}
	return object.Id, object.Metadata.Tenant == sharedTenant, nil
}
