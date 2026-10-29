/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package billingstub is a deterministic HTTP stand-in for the M360 read API, driven by the same
// billingfake.Scenario as the in-process fake. Pointing a real M360 configuration at it exercises the real adapter
// without credentials or a live tenant. It serves GET only: any other method is answered 405 and recorded, so a test
// can prove no write was attempted. Every Stub owns its scenario and request log (no globals).
package billingstub

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
)

// Request is one recorded request. Op is the adapter method name that scripts its failures, empty for a request
// the stub does not serve. Failed is true when the stub answered with an error status.
type Request struct {
	Method string
	Path   string
	Query  string
	Op     string
	Failed bool
}

// Stub is a scripted M360 server that records its requests.
type Stub struct {
	mu       sync.Mutex
	scenario billingfake.Scenario
	failed   map[string]int
	requests []Request
	server   *httptest.Server
}

// New builds a Stub from a scenario. Call Start to serve it.
func New(s billingfake.Scenario) *Stub {
	return &Stub{scenario: s, failed: map[string]int{}}
}

// Start serves the stub on addr, such as 127.0.0.1:0 for a free local port or 0.0.0.0:8080 for a deployed suite.
func (s *Stub) Start(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.server = httptest.NewUnstartedServer(s)
	s.server.Listener = listener
	s.server.Start()
	return nil
}

// URL is the base URL to configure as OSAC_BILLING_BASE_URL, valid after Start.
func (s *Stub) URL() string { return s.server.URL }

// Close stops the server; it is a no-op before Start.
func (s *Stub) Close() {
	if s.server != nil {
		s.server.Close()
	}
}

// SetScenario swaps the scenario of a running stub and restarts failure counting. The request log is kept.
func (s *Stub) SetScenario(scenario billingfake.Scenario) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scenario, s.failed = scenario, map[string]int{}
}

// Requests returns a copy of the recorded requests in order.
func (s *Stub) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Writes returns the recorded requests that were not GET.
func (s *Stub) Writes() []Request {
	return slices.DeleteFunc(s.Requests(), func(r Request) bool { return r.Method == http.MethodGet })
}

// Count is the number of requests for op, failed or not.
func (s *Stub) Count(op string) int { return s.count(op, false) }

// Succeeded is the number of requests for op that were answered without an error status.
func (s *Stub) Succeeded(op string) int { return s.count(op, true) }

// count totals the recorded requests for op, optionally only the successful ones.
func (s *Stub) count(op string, onlySucceeded bool) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Op == op && (!onlySucceeded || !r.Failed) {
			n++
		}
	}
	return n
}

// maxPageSize is the largest page M360 honours; a larger size is capped, not rejected.
const maxPageSize = 20

var (
	versionPrefix = regexp.MustCompile(`^/api/[^/]+/external`)
	rateCardPath  = regexp.MustCompile(`^/services/([^/]+)/rate-cards/([^/]+)$`)
	attrsPath     = regexp.MustCompile(`^/services/([^/]+)/priceable-attributes$`)
)

// ServeHTTP answers only GETs, records the request and applies scripted failures.
func (s *Stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	request := Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery}
	status, body := http.StatusMethodNotAllowed, any(nil)
	if r.Method == http.MethodGet {
		status, body = s.route(&request, r)
	}
	request.Failed = status >= 300
	s.requests = append(s.requests, request)
	if status >= 300 {
		// M360 answers errors with a flat body, not the success envelope.
		body = map[string]any{"status": status, "error": http.StatusText(status), "message": "scripted", "path": r.URL.Path}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// route answers a GET, applying the scripted failure of the op it maps to.
func (s *Stub) route(request *Request, r *http.Request) (int, any) {
	path := versionPrefix.ReplaceAllString(r.URL.Path, "")
	query := r.URL.Query()
	var serve func() (int, any)
	switch {
	case path == "/accounts":
		request.Op = "ListAccounts"
		// The health check and the status probe are a one-item, unfiltered first page; both count as HealthCheck.
		if query.Get("size") == "1" && query.Get("q") == "" && query.Get("page") == "" {
			request.Op = "HealthCheck"
		}
		serve = func() (int, any) { return s.listAccounts(query) }
	case strings.HasPrefix(path, "/accounts/"):
		request.Op = "GetAccount"
		serve = func() (int, any) { return s.account(strings.TrimPrefix(path, "/accounts/")) }
	case path == "/services":
		request.Op = "FindServiceByName"
		serve = func() (int, any) { return s.listServices(query) }
	case attrsPath.MatchString(path):
		request.Op = "ListPriceableAttributes"
		serve = func() (int, any) { return s.attributes(attrsPath.FindStringSubmatch(path)[1], query) }
	case rateCardPath.MatchString(path):
		request.Op = "GetServiceRateCard"
		m := rateCardPath.FindStringSubmatch(path)
		serve = func() (int, any) { return s.rateCard(m[1], m[2]) }
	default:
		return http.StatusNotFound, nil
	}
	// A one-item unfiltered page is also what ListAccounts(Size 1) sends, so a ListAccounts failure applies too.
	for _, op := range []string{request.Op, "ListAccounts"} {
		if spec, ok := s.scenario.Failures[op]; ok && (spec.Times == 0 || s.failed[op] < spec.Times) {
			s.failed[op]++
			return failureStatus(spec), nil
		}
		if request.Op != "HealthCheck" {
			break
		}
	}
	return serve()
}

// failureStatus is the scripted status, or the one the adapter classifies as the scripted reason.
func failureStatus(f billingfake.Failure) int {
	if f.Status != 0 {
		return f.Status
	}
	switch f.Reason {
	case billing.ReasonUnauthorized:
		return http.StatusUnauthorized
	case billing.ReasonNotFound:
		return http.StatusNotFound
	case billing.ReasonConflict:
		return http.StatusConflict
	case billing.ReasonTimeout:
		return http.StatusRequestTimeout
	case billing.ReasonUnavailable:
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

// matches is M360's case-insensitive substring search; the adapter then applies the exact match.
func matches(q string, fields ...string) bool {
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), strings.ToLower(q)) {
			return true
		}
	}
	return q == ""
}

// wireAccount is the M360 account shape. Status and ApprovalStatus are UUIDs or null; the readable name is in
// the __r object, which is null when the account has no status.
func wireAccount(a billingfake.AccountSpec) map[string]any {
	return map[string]any{"Id": a.ID, "Name": a.Name, "ExternalId": a.ExternalID,
		"Status__r": statusRef(a.RawStatus), "ApprovalStatus__r": statusRef(a.ApprovalStatus)}
}

// statusRef builds an M360 status reference, nil for an empty name.
func statusRef(name string) map[string]string {
	if name == "" {
		return nil
	}
	return map[string]string{"Name": name}
}

// envelope wraps data in the M360 success response: the adapter treats any other shape as malformed.
func envelope(data any) (int, any) {
	return http.StatusOK, map[string]any{"success": true, "errors": []any{}, "data": data}
}

// paged answers one zero-based page of items, the way M360 does: page is an index, not an offset, and size is
// capped at maxPageSize.
func paged[T any](items []T, query url.Values) (int, any) {
	size, _ := strconv.Atoi(query.Get("size"))
	if size <= 0 || size > maxPageSize {
		size = maxPageSize
	}
	number, _ := strconv.Atoi(query.Get("page"))
	number = max(number, 0)
	// Clamp the index before multiplying so a huge page number cannot overflow.
	start := min(number, len(items)) * size
	end := min(start+size, len(items))
	content := append([]T{}, items[min(start, len(items)):end]...)
	return envelope(map[string]any{"content": content, "number": number, "size": size, "last": end >= len(items)})
}

// listAccounts answers the filtered, paged /accounts collection.
func (s *Stub) listAccounts(query url.Values) (int, any) {
	items := []map[string]any{}
	for _, a := range s.scenario.Accounts {
		if matches(query.Get("q"), a.Name, a.ExternalID) {
			items = append(items, wireAccount(a))
		}
	}
	return paged(items, query)
}

// account answers a single account by id, 404 when unknown.
func (s *Stub) account(id string) (int, any) {
	for _, a := range s.scenario.Accounts {
		if a.ID == id {
			return envelope(wireAccount(a))
		}
	}
	return http.StatusNotFound, nil
}

// service returns the scenario service with id, or nil.
func (s *Stub) service(id string) *billingfake.ServiceSpec {
	for i := range s.scenario.Services {
		if s.scenario.Services[i].ID == id {
			return &s.scenario.Services[i]
		}
	}
	return nil
}

// listServices answers the filtered, paged /services collection.
func (s *Stub) listServices(query url.Values) (int, any) {
	items := []map[string]string{}
	for _, svc := range s.scenario.Services {
		if matches(query.Get("q"), svc.Name) {
			items = append(items, map[string]string{"Id": svc.ID, "Name": svc.Name})
		}
	}
	return paged(items, query)
}

// attributes answers the priceable attributes of a service, 404 when unknown.
func (s *Stub) attributes(id string, query url.Values) (int, any) {
	svc := s.service(id)
	if svc == nil {
		return http.StatusNotFound, nil
	}
	items := []map[string]string{}
	for _, attr := range svc.Attributes {
		items = append(items, map[string]string{"id": attr.ID, "name": attr.Name})
	}
	return paged(items, query)
}

// rateCard is not enveloped: M360 returns the card at the top level, with the currency and rows as the adapter
// decodes them. A row without a unit rate (TIERED, NO_PRICE) sends null.
func (s *Stub) rateCard(id, currency string) (int, any) {
	svc := s.service(id)
	if svc == nil {
		return http.StatusNotFound, nil
	}
	spec, ok := svc.Rates[currency]
	if !ok {
		return http.StatusNotFound, nil
	}
	if spec.Currency != "" {
		currency = spec.Currency
	}
	rows := []map[string]any{}
	for _, row := range spec.Rows {
		values := []map[string]string{}
		for _, v := range row.AttributeValues {
			values = append(values, map[string]string{"attributeName": v.Name, "attributeValue": v.Value})
		}
		rows = append(rows, map[string]any{"priceDimId": row.DimensionID, "priceMethod": row.PriceMethod,
			"unitRate": unitRate(row.UnitRate), "attributeValues": values})
	}
	return http.StatusOK, map[string]any{"currency": currency, "rows": rows}
}

// unitRate keeps the provider's decimal text as a JSON number, exponent form included.
func unitRate(text string) any {
	if text == "" {
		return nil
	}
	return json.Number(text)
}
