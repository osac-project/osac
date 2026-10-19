/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package m360

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/osac-project/osac-metering/shared/m360http"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	accountPageSize  = 20
	maxResponseBytes = 4 << 20
)

// Config contains the M360 connection settings. The API key is loaded by the
// service from its configured Secret and is never included in errors.
type Config struct {
	BaseURL           string
	APIVersion        string
	APIKey            string
	HTTPClient        *http.Client
	MetricsRegisterer prometheus.Registerer
	AllowInsecureHTTP bool // test-only opt-in for local httptest servers; production wiring leaves this false.
}

// Client translates the M360 external account API into OSAC's billing model.
type Client struct {
	transport *m360http.Client
	metrics   *requestMetrics
	updateMu  sync.Mutex
}

var _ billing.Provider = (*Client)(nil)

// NewClient creates a billing client for the M360 external API.
func NewClient(config Config) (*Client, error) {
	transport, err := m360http.NewClient(m360http.Config{
		BaseURL: config.BaseURL, APIVersion: config.APIVersion, APIKey: config.APIKey,
		HTTPClient: config.HTTPClient, AllowInsecureHTTP: config.AllowInsecureHTTP,
	})
	if err != nil {
		return nil, err
	}
	return &Client{transport: transport, metrics: newRequestMetrics(config.MetricsRegisterer)}, nil
}

// ListAccounts returns all accounts, following M360's zero-based pagination.
func (c *Client) ListAccounts(ctx context.Context) ([]billing.Account, error) {
	accounts := make([]billing.Account, 0)
	for page := 0; ; page++ {
		var response accountPage
		if err := c.getAccountsPage(ctx, page, accountPageSize, &response); err != nil {
			return nil, err
		}
		for _, account := range response.Content {
			if account.ID == "" || account.Name == "" {
				return nil, errors.New("M360 account list contained an account without an ID or name")
			}
			accounts = append(accounts, normalizeAccount(account))
		}
		if response.Last || len(response.Content) == 0 ||
			(response.TotalPages > 0 && page+1 >= response.TotalPages) {
			return accounts, nil
		}
	}
}

// GetStatus checks account API reachability by requesting a single account
// row. Other operation APIs are not probed, and usage delivery health is
// outside fulfillment's visibility.
func (c *Client) GetStatus(ctx context.Context) (billing.ProviderStatus, error) {
	var response accountPage
	if err := c.getAccountsPage(ctx, 0, 1, &response); err != nil {
		return billing.ProviderStatus{State: billing.ProviderStateUnavailable}, err
	}
	return billing.ProviderStatus{
		State:              billing.ProviderStateAvailable,
		UsageDeliveryState: billing.UsageDeliveryStateUnknown,
		Message:            "M360 account API reachable; pricing, costs, and invoices are configured but not probed; usage delivery is not monitored by fulfillment",
		Capabilities: []billing.ProviderCapability{
			billing.ProviderCapabilityPricing,
			billing.ProviderCapabilityCosts,
			billing.ProviderCapabilityInvoices,
		},
	}, nil
}

// GetAccount retrieves one provider account by its M360 account ID.
func (c *Client) GetAccount(ctx context.Context, reference string) (billing.Account, error) {
	if strings.TrimSpace(reference) == "" {
		return billing.Account{}, errors.New("M360 account reference is required")
	}
	var response m360Account
	if err := c.requestJSON(ctx, http.MethodGet, c.accountURL(reference), nil, &response); err != nil {
		if errors.Is(err, errM360NotFound) {
			return billing.Account{}, fmt.Errorf("%w: M360 account not found", billing.ErrAccountNotFound)
		}
		return billing.Account{}, err
	}
	if response.ID == "" || response.Name == "" {
		return billing.Account{}, errors.New("M360 account response did not contain an ID and name")
	}
	return normalizeAccount(response), nil
}

// UpdateAccountExternalID changes only the account's external ID when its
// current value still matches the value observed by the caller. M360's update
// API leaves scalar fields omitted from the request unchanged, but does not
// document conditional updates; the lock and pre-write check prevent stale
// writes from concurrent users of this client, not external writers.
func (c *Client) UpdateAccountExternalID(ctx context.Context, reference, expectedExternalID, externalID string) error {
	if strings.TrimSpace(reference) == "" {
		return errors.New("M360 account reference is required")
	}
	c.updateMu.Lock()
	defer c.updateMu.Unlock()

	current, err := billing.RetryProviderRead(ctx, func(ctx context.Context) (billing.Account, error) {
		return c.GetAccount(ctx, reference)
	})
	if err != nil {
		return err
	}
	if current.ExternalID != expectedExternalID {
		return billing.ErrAccountExternalIDChanged
	}

	body := struct {
		ExternalID string `json:"ExternalId"`
	}{ExternalID: externalID}
	return c.requestJSON(ctx, http.MethodPut, c.accountURL(reference), body, nil)
}

func (c *Client) getAccountsPage(ctx context.Context, page, size int, response *accountPage) error {
	endpoint := c.apiURL("accounts")
	query := endpoint.Query()
	query.Set("page", strconv.Itoa(page))
	query.Set("size", strconv.Itoa(size))
	endpoint.RawQuery = query.Encode()
	if err := c.requestJSON(ctx, http.MethodGet, endpoint, nil, response); err != nil {
		return err
	}
	return nil
}

func (c *Client) accountURL(reference string) *url.URL {
	return c.apiURL("accounts", reference)
}

func (c *Client) apiURL(segments ...string) *url.URL {
	return c.transport.Endpoint("external", segments...)
}

func (c *Client) requestJSON(ctx context.Context, method string, endpoint *url.URL, body any, response any) (resultErr error) {
	startedAt := time.Now()
	if c.metrics != nil {
		defer func() { c.metrics.record(m360Operation(endpoint), time.Since(startedAt), resultErr) }()
	}
	var requestBody io.Reader
	contentType := ""
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return errors.New("failed to encode M360 request")
		}
		requestBody = strings.NewReader(string(encoded))
		contentType = "application/json"
	}
	request, err := c.transport.NewRequest(ctx, method, endpoint, requestBody, contentType)
	if err != nil {
		return errors.New("failed to create M360 request")
	}
	result, err := c.transport.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if isTimeoutError(err) {
			return fmt.Errorf("%w: %w: M360 API request timed out", billing.ErrProviderUnavailable, billing.ErrProviderReadRetryable)
		}
		return fmt.Errorf("%w: M360 API request failed", billing.ErrProviderUnavailable)
	}
	defer func() { _ = result.Body.Close() }()
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		if result.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: M360 API returned HTTP %d", errM360NotFound, result.StatusCode)
		}
		if result.StatusCode == http.StatusRequestTimeout || result.StatusCode == http.StatusTooManyRequests ||
			(result.StatusCode >= http.StatusInternalServerError && result.StatusCode < 600) {
			return fmt.Errorf("%w: %w: M360 API returned HTTP %d", billing.ErrProviderUnavailable, billing.ErrProviderReadRetryable, result.StatusCode)
		}
		return fmt.Errorf("M360 API returned HTTP %d", result.StatusCode)
	}
	if response == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(result.Body, maxResponseBytes))
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(result.Body, maxResponseBytes))
	var envelope providerEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return errors.New("M360 API returned an invalid response")
	}
	if !envelope.Success {
		return errors.New("M360 API rejected the request")
	}
	if err := json.Unmarshal(envelope.Data, response); err != nil {
		return errors.New("M360 API returned an invalid response")
	}
	return nil
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func normalizeAccount(account m360Account) billing.Account {
	state := billing.AccountStateUnknown
	switch strings.ToLower(account.Status.Name) {
	case "active":
		state = billing.AccountStateActive
	case "inactive", "deactivated":
		state = billing.AccountStateInactive
	}
	currency := account.DefaultCurrency
	if currency == "" {
		currency = account.CurrencyCode
	}
	return billing.Account{
		Reference:   account.ID,
		DisplayName: account.Name,
		ExternalID:  account.ExternalID,
		Currency:    currency,
		State:       state,
	}
}

type providerEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

type accountPage struct {
	Content    []m360Account `json:"content"`
	TotalPages int           `json:"totalPages"`
	Total      int           `json:"totalElements"`
	Page       int           `json:"number"`
	Last       bool          `json:"last"`
}

type m360Account struct {
	ID              string `json:"Id"`
	Name            string `json:"Name"`
	ExternalID      string `json:"ExternalId"`
	DefaultCurrency string `json:"DefaultCurrency"`
	CurrencyCode    string `json:"CurrencyCode"`
	Status          struct {
		Name string `json:"Name"`
	} `json:"Status__r"`
}
