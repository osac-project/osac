/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package m360

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

const (
	maxRetries         = 3
	maxResponseBytes   = 4 << 20
	breakerThreshold   = 5
	breakerCooldown    = 30 * time.Second
	defaultAttemptTime = 10 * time.Second

	maxPageSize = 20 // the largest size M360 honours
	// maxPages bounds pagination so a provider that never reports a last page cannot loop forever.
	maxPages = 50
)

// client issues GET requests only. Response bodies are decoded or discarded, never logged or put in errors.
type client struct {
	cfg            billing.Config
	http           *http.Client
	logger         *slog.Logger
	breaker        *breaker // pricing/coverage only; see get and getDiscovery
	attemptTimeout time.Duration
	backoffBase    time.Duration
	backoffMax     time.Duration
}

// newClient builds a client with the default retry, breaker and backoff settings.
func newClient(cfg billing.Config, logger *slog.Logger) *client {
	return &client{
		cfg: cfg,
		// A redirect would carry the bearer token to wherever the provider points; surface it as a failure instead.
		http:           &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		logger:         logger,
		breaker:        newBreaker(breakerThreshold, breakerCooldown),
		attemptTimeout: defaultAttemptTime,
		backoffBase:    100 * time.Millisecond,
		backoffMax:     time.Second,
	}
}

// get is for pricing and coverage reads (services, attributes, rate cards): it goes through the pricing breaker.
// It decodes a 2xx JSON response into out (which may be nil to ignore the body).
func (c *client) get(ctx context.Context, op, path string, query url.Values, out any) error {
	return c.do(ctx, c.breaker, op, path, query, out)
}

// getDiscovery is for account reads and the health check. Story 7.01 keeps account discovery out of the pricing
// breaker, so these calls neither trip it nor are blocked by it.
func (c *client) getDiscovery(ctx context.Context, op, path string, query url.Values, out any) error {
	return c.do(ctx, nil, op, path, query, out)
}

// do sends one request through breaker b, retrying retryable failures with
// backoff up to maxRetries. A definitive answer — success or a non-retryable
// error — resets the breaker; exhausted retries trip it. Caller cancellation
// aborts without a verdict.
func (c *client) do(ctx context.Context, b *breaker, op, path string, query url.Values, out any) error {
	if !b.allow() {
		return &billing.Error{Reason: billing.ReasonUnavailable, Op: op, Retryable: true}
	}
	var err *billing.Error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 && !c.sleep(ctx, c.backoff(attempt)) {
			b.abort()
			return ctxError(op, ctx)
		}
		err = c.attempt(ctx, op, path, query, out)
		if err == nil {
			b.success()
			return nil
		}
		c.logger.DebugContext(ctx, "billing request failed", "op", op, "reason", err.Reason, "attempt", attempt+1)
		if ctx.Err() != nil {
			b.abort()
			return ctxError(op, ctx)
		}
		if !err.Retryable {
			b.success()
			return err
		}
	}
	b.failure()
	return err
}

// attempt performs a single GET, classifying transport, status and decoding
// failures into a *billing.Error.
func (c *client) attempt(ctx context.Context, op, path string, query url.Values, out any) *billing.Error {
	ctx, cancel := context.WithTimeout(ctx, c.attemptTimeout)
	defer cancel()

	u := strings.TrimSuffix(c.cfg.BaseURL, "/") + "/api/" + c.cfg.APIVersion + "/external" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return &billing.Error{Reason: billing.ReasonMalformed, Op: op}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.Credential)

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return &billing.Error{Reason: billing.ReasonTimeout, Op: op, Retryable: true}
		}
		return &billing.Error{Reason: billing.ReasonUnavailable, Op: op, Retryable: true}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return classifyStatus(op, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return billing.NewError(billing.ReasonUnavailable, op, true, resp.StatusCode)
	}
	var probe struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &probe) != nil || probe.Error != nil {
		return billing.NewError(billing.ReasonMalformed, op, false, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if json.Unmarshal(body, out) != nil {
		return billing.NewError(billing.ReasonMalformed, op, false, resp.StatusCode)
	}
	if v, ok := out.(interface{ valid() bool }); ok && !v.valid() {
		return billing.NewError(billing.ReasonMalformed, op, false, resp.StatusCode)
	}
	return nil
}

// envelope is M360's ApiResponse wrapper. Data is a pointer so a null or missing payload is detectable.
type envelope[T any] struct {
	Success bool              `json:"success"`
	Errors  []json.RawMessage `json:"errors"`
	Data    *T                `json:"data"`
}

// valid is false for a 2xx the provider reports as failed or that carries no data, so it never reads as empty.
func (e *envelope[T]) valid() bool { return e.Success && len(e.Errors) == 0 && e.Data != nil }

// pageData is the zero-based page inside an envelope.
type pageData[T any] struct {
	Content []T  `json:"content"`
	Number  int  `json:"number"`
	Last    bool `json:"last"`
}

// next is the token of the following page, empty on the last one.
func (p pageData[T]) next() string {
	if p.Last {
		return ""
	}
	return strconv.Itoa(p.Number + 1)
}

// getAllPages reads every page of a pricing collection, adding extra to each request.
func getAllPages[T any](ctx context.Context, c *client, op, path string, extra url.Values) ([]T, error) {
	var all []T
	token := ""
	for range maxPages {
		query := url.Values{"size": {strconv.Itoa(maxPageSize)}}
		for k, v := range extra {
			query[k] = v
		}
		if token != "" {
			query.Set("page", token)
		}
		var resp envelope[pageData[T]]
		if err := c.get(ctx, op, path, query, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Data.Content...)
		if token = resp.Data.next(); token == "" {
			return all, nil
		}
	}
	return nil, &billing.Error{Reason: billing.ReasonMalformed, Op: op}
}

// isNotFound reports whether err carries a provider 404.
func isNotFound(err error) bool {
	var e *billing.Error
	return errors.As(err, &e) && e.HTTPStatus() == http.StatusNotFound
}

// notFoundOn404 reclassifies a 404 as NOT_FOUND. Only reads of a resource addressed by id call it: a 404 on a
// collection route means a wrong base URL or API version and stays MALFORMED.
func notFoundOn404(op string, err error) error {
	if !isNotFound(err) {
		return err
	}
	return billing.NewError(billing.ReasonNotFound, op, false, http.StatusNotFound)
}

// classifyStatus maps an HTTP status to a *billing.Error with its Reason and
// retryability.
func classifyStatus(op string, status int) *billing.Error {
	reason, retryable := billing.ReasonMalformed, false
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		reason = billing.ReasonUnauthorized
	case status == http.StatusConflict:
		reason = billing.ReasonConflict
	case status == http.StatusRequestTimeout:
		reason, retryable = billing.ReasonTimeout, true
	case status == http.StatusTooManyRequests || status >= 500:
		reason, retryable = billing.ReasonUnavailable, true
	}
	return billing.NewError(reason, op, retryable, status)
}

// ctxError classifies a caller cancellation as TIMEOUT when a deadline
// expired, otherwise UNAVAILABLE.
func ctxError(op string, ctx context.Context) *billing.Error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &billing.Error{Reason: billing.ReasonTimeout, Op: op}
	}
	return &billing.Error{Reason: billing.ReasonUnavailable, Op: op}
}

// backoff is the exponential wait before retry number attempt, capped at
// backoffMax.
func (c *client) backoff(attempt int) time.Duration {
	return min(c.backoffBase<<(attempt-1), c.backoffMax)
}

// sleep waits for d unless ctx is done first, reporting whether the wait
// completed.
func (c *client) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
