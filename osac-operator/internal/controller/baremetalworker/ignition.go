/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// maxIgnitionBytes bounds the discovery ignition response to guard operator memory against an
// unexpectedly large or hostile response. Discovery ignition is ~15KB in practice.
const maxIgnitionBytes = 1 << 20 // 1 MiB

// IgnitionFetcher is the seam the bare-metal worker reconciler uses to fetch discovery ignition
// from an InfraEnv's boot-artifacts endpoint (assisted-service). It is separate from
// FulfillmentClient because it targets a different service over HTTP, not the fulfillment-service
// gRPC API — its failures are independent of the FulfillmentServiceUnavailable signal.
type IgnitionFetcher interface {
	// FetchIgnition GETs the discovery ignition content from the given URL.
	FetchIgnition(ctx context.Context, url string) ([]byte, error)
}

type ignitionFetcher struct {
	http *http.Client
}

// NewIgnitionFetcher builds an IgnitionFetcher. A nil httpClient defaults to a zero-value
// http.Client (per-call deadlines come from the context).
func NewIgnitionFetcher(httpClient *http.Client) IgnitionFetcher {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &ignitionFetcher{http: httpClient}
}

// FetchIgnition GETs the discovery ignition from url under the per-call deadline.
func (f *ignitionFetcher) FetchIgnition(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building ignition request: %w", err)
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching ignition: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIgnitionBytes))
	if err != nil {
		return nil, fmt.Errorf("reading ignition body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := body
		if len(snippet) > 256 {
			snippet = snippet[:256]
		}
		return nil, fmt.Errorf("fetching ignition: unexpected status %d: %s", resp.StatusCode, snippet)
	}
	return body, nil
}
