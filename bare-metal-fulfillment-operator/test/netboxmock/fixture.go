/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package netboxmock

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/wiremock"
)

//go:embed mappings.json
var mappings []byte

//go:embed list.hbs
var listTemplate string

//go:embed patch.hbs
var patchTemplate string

// Device is a NetBox device seeded in WireMock or read back from its state.
type Device struct {
	ID           int
	Status       string
	CustomFields map[string]any
}

// Fixture configures and inspects the NetBox WireMock mappings.
type Fixture struct {
	admin   wiremock.Admin
	bearers []string
}

// Configure loads the NetBox mappings and seeds a fixture for one spec. The
// selector defines the query the spec expects, including its custom filters.
func Configure(
	ctx context.Context, admin wiremock.Admin, tokens []string, selector map[string]string, devices ...Device,
) (*Fixture, error) {
	fixture := &Fixture{admin: admin, bearers: make([]string, 0, len(tokens))}
	expressions := make([]string, 0, len(tokens))
	for _, token := range tokens {
		bearer := "Bearer " + token
		if token == "" || slices.Contains(fixture.bearers, bearer) {
			return nil, errors.New("NetBox fixture requires distinct, non-empty tokens")
		}
		fixture.bearers = append(fixture.bearers, bearer)
		expressions = append(expressions, regexp.QuoteMeta(bearer))
	}
	if len(expressions) == 0 {
		return nil, errors.New("NetBox fixture requires at least one token")
	}
	authorization := "(?:" + strings.Join(expressions, "|") + ")"

	// WireMock selects this list mapping only for the expected query. The
	// response template reads each selected device's current state on demand.
	const equalTo = "equalTo"
	query := map[string]any{
		"status":                     map[string]string{equalTo: "staged"},
		"cf_osac_managed":            map[string]string{equalTo: "true"},
		"cf_osac_instance_id__empty": map[string]string{equalTo: "true"},
	}
	for name, value := range selector {
		query["cf_"+name] = map[string]string{equalTo: value}
	}
	contexts := make([]string, 0, len(devices))
	for _, device := range devices {
		if deviceMatchesSelector(device, selector) {
			contexts = append(contexts, deviceContext(device.ID))
		}
	}

	var stubs struct {
		Mappings []struct {
			Name           string            `json:"name"`
			Priority       int               `json:"priority,omitempty"`
			Request        map[string]any    `json:"request"`
			Response       map[string]any    `json:"response"`
			EventListeners []json.RawMessage `json:"serveEventListeners,omitempty"`
		} `json:"mappings"`
	}
	if err := json.Unmarshal(mappings, &stubs); err != nil {
		return nil, fmt.Errorf("decode NetBox WireMock mappings: %w", err)
	}

	for _, stub := range stubs.Mappings {
		switch stub.Name {
		case "seed":
			continue
		case "list":
			stub.Request["queryParameters"] = query
			stub.Response["body"] = listTemplate
			stub.Response["transformerParameters"] = map[string]any{"contexts": contexts}
		case "patch":
			stub.Response["body"] = patchTemplate
		}
		headers, ok := stub.Request["headers"].(map[string]any)
		if !ok {
			headers = make(map[string]any)
			stub.Request["headers"] = headers
		}
		if stub.Name == "unauthorized" {
			headers["Authorization"] = map[string]any{"or": []any{
				map[string]bool{"absent": true},
				map[string]string{"doesNotMatch": authorization},
			}}
		} else {
			headers["Authorization"] = map[string]string{"matches": authorization}
		}
	}

	if err := fixture.post(ctx, "/__admin/mappings/import", stubs); err != nil {
		return nil, err
	}
	for _, device := range devices {
		path := "/__fixture/devices/" + strconv.Itoa(device.ID)
		payload := DevicePayload(device.ID, fmt.Sprintf("device-%d", device.ID), device.Status, device.CustomFields)
		if err := fixture.post(ctx, path, payload); err != nil {
			return nil, err
		}
	}
	return fixture, nil
}

func deviceMatchesSelector(device Device, selector map[string]string) bool {
	if device.CustomFields["osac_managed"] != true {
		return false
	}
	for name, value := range selector {
		stored, ok := device.CustomFields[name]
		if !ok || fmt.Sprint(stored) != value {
			return false
		}
	}
	return true
}

func deviceContext(id int) string {
	return "netbox-device-" + strconv.Itoa(id)
}

func (f *Fixture) post(ctx context.Context, path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode NetBox fixture data: %w", err)
	}
	_, err = f.admin(ctx, http.MethodPost, path, data)
	return err
}

// Device reads a device snapshot through the admin API without adding a NetBox
// API call to the journal being asserted by the spec.
func (f *Fixture) Device(ctx context.Context, id int) (Device, error) {
	var device Device
	data, err := f.admin(ctx, http.MethodGet, "/__admin/state-extension/contexts/"+deviceContext(id), nil)
	if err != nil {
		return device, err
	}
	var state struct {
		Properties struct {
			Document string `json:"document"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return device, fmt.Errorf("decode NetBox fixture state: %w", err)
	}
	var doc struct {
		ID     int `json:"id"`
		Status struct {
			Value string `json:"value"`
		} `json:"status"`
		CustomFields map[string]any `json:"custom_fields"`
	}
	if err := json.Unmarshal([]byte(state.Properties.Document), &doc); err != nil {
		return device, fmt.Errorf("decode NetBox fixture device %d: %w", id, err)
	}
	return Device{ID: doc.ID, Status: doc.Status.Value, CustomFields: doc.CustomFields}, nil
}

// Request contains the parts of a journal entry asserted by the tests.
type Request struct {
	Query      url.Values
	IfMatch    string
	TokenIndex int
	Claimed    bool
}

// RequestsFor returns matched API calls in chronological order. The tests
// identify synthetic tokens by index so failure output never includes them.
func (f *Fixture) RequestsFor(ctx context.Context, method, path string) ([]Request, error) {
	data, err := f.admin(ctx, http.MethodGet, "/__admin/requests", nil)
	if err != nil {
		return nil, err
	}
	var journal struct {
		Requests []struct {
			Request struct {
				Method  string         `json:"method"`
				URL     string         `json:"url"`
				Headers journalHeaders `json:"headers"`
				Body    string         `json:"body"`
			} `json:"request"`
			Response struct {
				Status int `json:"status"`
			} `json:"response"`
			WasMatched bool `json:"wasMatched"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, fmt.Errorf("decode WireMock request journal: %w", err)
	}
	slices.Reverse(journal.Requests) // WireMock returns the newest request first.
	var requests []Request
	for _, event := range journal.Requests {
		if !event.WasMatched || event.Request.Method != method {
			continue
		}
		target, err := url.ParseRequestURI(event.Request.URL)
		if err != nil {
			return nil, err
		}
		tokenIndex := slices.Index(f.bearers, event.Request.Headers.Get("Authorization"))
		if target.Path != path || tokenIndex < 0 {
			continue
		}
		var claimed bool
		if method == http.MethodPatch && event.Response.Status == http.StatusOK {
			var payload struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal([]byte(event.Request.Body), &payload); err != nil {
				return nil, fmt.Errorf("decode NetBox fixture claim request: %w", err)
			}
			claimed = payload.Status == "active"
		}
		requests = append(requests, Request{
			Query: target.Query(), IfMatch: event.Request.Headers.Get("If-Match"),
			TokenIndex: tokenIndex, Claimed: claimed,
		})
	}
	return requests, nil
}

// WireMock encodes a single header value as a string and repeated values as
// an array. Header names retain their original case in the journal.
type journalHeaders map[string]json.RawMessage

func (h journalHeaders) Get(name string) string {
	for key, raw := range h {
		if !strings.EqualFold(key, name) {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return value
		}
		var values []string
		if json.Unmarshal(raw, &values) == nil && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}
