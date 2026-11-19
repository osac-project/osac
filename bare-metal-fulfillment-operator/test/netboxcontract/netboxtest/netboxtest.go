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

// Package netboxtest provides the stateful mock API used by NetBox tests.
package netboxtest

import (
	"crypto/x509"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FilterExact is the filter logic used by contract field definitions.
const FilterExact = "exact"

// Field is one custom-field definition attached to dcim.device.
type Field struct {
	Name        string
	Label       string
	Type        string
	FilterLogic string
	ObjectTypes []string
}

// Device is one dcim.device as the server stores it.
type Device struct {
	ID           int
	Name         string
	Status       string
	CustomFields map[string]any
	Tags         []string
	// Version drives the ETag returned with detail reads and PATCHes.
	Version int
}

// ETag returns the conditional-request token for the device.
func (d Device) ETag() string {
	return fmt.Sprintf(`"v%d"`, d.Version)
}

// Request records an API call for assertions.
type Request struct {
	Method     string
	Path       string
	Query      url.Values
	IfMatch    string
	Body       []byte
	StatusCode int
	// TokenIndex is the matched token's position in SetAuth, or -1.
	TokenIndex int
}

// Server is a stateful NetBox HTTPS endpoint.
type Server struct {
	mu       sync.Mutex
	server   *httptest.Server
	devices  map[int]*Device
	fields   map[string]*Field
	requests []Request

	tokens      []string
	tokenScheme string
	beforePatch func(deviceID int)
}

// New starts a mock NetBox HTTPS server with no devices and no custom fields.
func New() *Server {
	s := NewHandler()
	s.server = httptest.NewTLSServer(s)
	return s
}

// NewHandler creates an unstarted mock for a caller-owned HTTP listener.
func NewHandler() *Server {
	return &Server{
		devices: map[int]*Device{},
		fields:  map[string]*Field{},
	}
}

// Stop shuts the server down.
func (s *Server) Stop() { s.server.Close() }

// URL returns the server's HTTPS base URL.
func (s *Server) URL() string { return s.server.URL }

// Certificate returns the TLS certificate so a client can trust the server.
func (s *Server) Certificate() *x509.Certificate { return s.server.Certificate() }

// SetAuth configures the authorization scheme and accepted fixture tokens.
func (s *Server) SetAuth(scheme string, tokens ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenScheme, s.tokens = scheme, slices.Clone(tokens)
}

// SetBeforePatchHook injects a write between the adapter's read and PATCH.
func (s *Server) SetBeforePatchHook(hook func(deviceID int)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforePatch = hook
}

// AddField registers a dcim.device custom-field definition.
func (s *Server) AddField(field Field) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(field.ObjectTypes) == 0 {
		field.ObjectTypes = []string{"dcim.device"}
	}
	if field.FilterLogic == "" {
		field.FilterLogic = FilterExact
	}
	if field.Label == "" {
		field.Label = field.Name
	}
	copied := field
	s.fields[field.Name] = &copied
}

// AddDevice registers a device fixture.
func (s *Server) AddDevice(device Device) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := Device{
		ID:           device.ID,
		Name:         device.Name,
		Status:       device.Status,
		CustomFields: map[string]any{},
		Tags:         append([]string(nil), device.Tags...),
		Version:      device.Version,
	}
	for key, value := range device.CustomFields {
		copied.CustomFields[key] = value
	}
	s.devices[device.ID] = &copied
}

// Device returns a copy of a device fixture.
func (s *Server) Device(id int) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[id]
	if !ok {
		return Device{}, false
	}
	return copyDevice(device), true
}

// UpdateDevice makes an external edit and invalidates the device ETag.
func (s *Server) UpdateDevice(id int, mutate func(*Device)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[id]
	if !ok {
		return false
	}
	mutate(device)
	device.Version++
	return true
}

// DeleteDevice removes a device, modeling an administrator deleting it.
func (s *Server) DeleteDevice(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.devices, id)
}

// Requests returns every recorded request.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// ServeHTTP handles the fixture endpoints.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)

	recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	w = recorder

	headerScheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	s.mu.Lock()
	requiresAuth := len(s.tokens) > 0
	tokenIndex := -1
	if headerScheme == s.tokenScheme {
		tokenIndex = slices.Index(s.tokens, token)
	}
	requestIndex := len(s.requests)
	s.requests = append(s.requests, Request{
		Method:     r.Method,
		Path:       r.URL.Path,
		Query:      r.URL.Query(),
		IfMatch:    r.Header.Get("If-Match"),
		Body:       body,
		TokenIndex: tokenIndex,
	})
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.requests[requestIndex].StatusCode = recorder.status
		s.mu.Unlock()
	}()

	if requiresAuth && tokenIndex < 0 {
		writeError(w, http.StatusUnauthorized, "Invalid token")
		return
	}

	switch {
	case r.Method == http.MethodGet && strings.TrimSuffix(r.URL.Path, "/") == "/api/dcim/interfaces":
		writeJSON(w, map[string]any{"count": 0, "next": nil, "previous": nil, "results": []any{}})
	case r.Method == http.MethodGet && strings.TrimSuffix(r.URL.Path, "/") == "/api/dcim/devices":
		s.listDevices(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/dcim/devices/"):
		s.getDevice(w, r)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/dcim/devices/"):
		s.patchDevice(w, r, body)
	case r.Method == http.MethodGet && strings.TrimSuffix(r.URL.Path, "/") == "/api/extras/custom-fields":
		s.listFields(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Unsupported fixture route")
	}
}

// statusRecorder captures the status code a handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// listDevices applies the adapter's filters: exact status, exact cf_<field>
// values, and the cf_<field>__empty lookup. An unknown or filter-disabled
// custom field is rejected the way NetBox rejects an invalid filter.
func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	s.mu.Lock()
	fields := make(map[string]Field, len(s.fields))
	for name, field := range s.fields {
		fields[name] = *field
	}
	matches := make([]*Device, 0, len(s.devices))
	for _, device := range s.devices {
		matched, ok := deviceMatchesQuery(device, query, fields)
		if !ok {
			s.mu.Unlock()
			writeError(w, http.StatusBadRequest, "Invalid filter for device query")
			return
		}
		if matched {
			matches = append(matches, device)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	results := make([]map[string]any, 0, len(matches))
	for _, device := range matches {
		results = append(results, devicePayload(device))
	}
	s.mu.Unlock()

	total := len(results)
	offset, end := pageBounds(query, total)
	var next any
	if end < total {
		nextURL := url.URL{Path: "/api/dcim/devices/"}
		nextQuery := url.Values{}
		for key, values := range query {
			for _, value := range values {
				nextQuery.Add(key, value)
			}
		}
		nextQuery.Set("offset", strconv.Itoa(end))
		nextURL.RawQuery = nextQuery.Encode()
		next = nextURL.String()
	}

	writeJSON(w, map[string]any{
		"count":    total,
		"next":     next,
		"previous": nil,
		"results":  results[offset:end],
	})
}

// deviceMatchesQuery reports whether a device matches the query. The second
// return value is false when the query references a filter the server rejects.
func deviceMatchesQuery(device *Device, query url.Values, fields map[string]Field) (bool, bool) {
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		values := query[key]
		if len(values) == 0 {
			continue
		}
		value := values[len(values)-1]

		switch key {
		case "status":
			if device.Status != value {
				return false, true
			}
			continue
		case "limit", "offset", "ordering", "brief", "q":
			continue
		}

		if !strings.HasPrefix(key, "cf_") {
			return false, false
		}
		name := strings.TrimPrefix(key, "cf_")
		lookup := ""
		if strings.HasSuffix(name, "__empty") {
			name = strings.TrimSuffix(name, "__empty")
			lookup = "empty"
		}
		field, known := fields[name]
		if !known || !slices.Contains(field.ObjectTypes, "dcim.device") {
			return false, false
		}
		if lookup == "empty" {
			if (value == "true") != isEmptyValue(device.CustomFields[name]) {
				return false, true
			}
			continue
		}
		if !exactMatch(device.CustomFields[name], field.Type, value) {
			return false, true
		}
	}
	return true, true
}

// exactMatch compares a stored custom-field value with a filter value using the
// stored type, so booleans and integers match their NetBox textual forms.
func exactMatch(stored any, fieldType, value string) bool {
	if stored == nil {
		return false
	}
	switch fieldType {
	case "boolean":
		boolean, ok := stored.(bool)
		return ok && (value == "true") == boolean
	case "integer":
		switch number := stored.(type) {
		case int:
			return strconv.Itoa(number) == value
		case float64:
			return strconv.FormatFloat(number, 'f', -1, 64) == value
		case string:
			return number == value
		}
	}
	return fmt.Sprint(stored) == value
}

// isEmptyValue reports whether a custom-field value counts as empty, which is
// the assumption the adapter's unassigned-owner filter depends on.
func isEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && text == ""
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	id, err := deviceIDFromPath(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}

	s.mu.Lock()
	device, ok := s.devices[id]
	if !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	payload := devicePayload(device)
	etag := device.ETag()
	s.mu.Unlock()

	w.Header().Set("ETag", etag)
	writeJSON(w, payload)
}

// patchDevice applies one conditional partial update. Custom fields merge, so a
// PATCH that sends only osac_instance_id preserves every other value.
func (s *Server) patchDevice(w http.ResponseWriter, r *http.Request, body []byte) {
	id, err := deviceIDFromPath(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}

	var patch struct {
		Status       *string        `json:"status"`
		CustomFields map[string]any `json:"custom_fields"`
	}
	if err := json.Unmarshal(body, &patch); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid body")
		return
	}

	s.mu.Lock()
	hook := s.beforePatch
	s.mu.Unlock()
	if hook != nil {
		hook(id)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	device, ok := s.devices[id]
	if !ok {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if ifMatch := r.Header.Get("If-Match"); ifMatch != "" && ifMatch != device.ETag() {
		writeError(w, http.StatusPreconditionFailed, "Precondition failed")
		return
	}

	if patch.Status != nil {
		device.Status = *patch.Status
	}
	for key, value := range patch.CustomFields {
		device.CustomFields[key] = value
	}
	device.Version++

	w.Header().Set("ETag", device.ETag())
	writeJSON(w, devicePayload(device))
}

// listFields returns custom-field definitions for one object type, paginated the
// way NetBox paginates extras/custom-fields.
func (s *Server) listFields(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	objectType := query.Get("object_type")

	s.mu.Lock()
	matched := make([]Field, 0, len(s.fields))
	for _, field := range s.fields {
		if objectType != "" && !slices.Contains(field.ObjectTypes, objectType) {
			continue
		}
		matched = append(matched, *field)
	}
	s.mu.Unlock()

	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })

	payloads := make([]map[string]any, 0, len(matched))
	for _, field := range matched {
		payloads = append(payloads, fieldPayload(field))
	}

	total := len(payloads)
	offset, end := pageBounds(query, total)
	var next any
	if end < total {
		nextQuery := url.Values{}
		nextQuery.Set("object_type", objectType)
		nextQuery.Set("offset", strconv.Itoa(end))
		next = "/api/extras/custom-fields/?" + nextQuery.Encode()
	}

	writeJSON(w, map[string]any{
		"count":    total,
		"next":     next,
		"previous": nil,
		"results":  payloads[offset:end],
	})
}

func pageBounds(query url.Values, total int) (offset, end int) {
	offset, _ = strconv.Atoi(query.Get("offset"))
	offset = min(max(0, offset), total)
	limit := 100
	if requested, err := strconv.Atoi(query.Get("limit")); err == nil && requested > 0 {
		limit = min(requested, limit)
	}
	return offset, min(offset+limit, total)
}

func fieldPayload(field Field) map[string]any {
	return map[string]any{
		"id":           fieldID(field.Name),
		"url":          "/api/extras/custom-fields/" + strconv.Itoa(fieldID(field.Name)) + "/",
		"display":      field.Label,
		"object_types": append([]string(nil), field.ObjectTypes...),
		"type":         map[string]any{"value": field.Type, "label": typeLabel(field.Type)},
		"data_type":    field.Type,
		"name":         field.Name,
		"label":        field.Label,
		"required":     false,
		// The SDK models filter_logic as a choice object, not an enum string.
		"filter_logic": map[string]any{"value": field.FilterLogic, "label": titleCase(field.FilterLogic)},
	}
}

func devicePayload(device *Device) map[string]any {
	tags := make([]map[string]any, 0, len(device.Tags))
	for _, tag := range device.Tags {
		tags = append(tags, map[string]any{
			"id": fieldID(tag), "url": "/api/extras/tags/" + strconv.Itoa(fieldID(tag)) + "/",
			"display": tag, "name": tag, "slug": strings.ToLower(tag),
		})
	}
	customFields := make(map[string]any, len(device.CustomFields))
	for key, value := range device.CustomFields {
		customFields[key] = value
	}

	// NetBox always renders a non-empty display value: a device without a name
	// still gets a generated label. The generated SDK rejects an empty required
	// property, so a fixture that copied the name here would misrepresent the
	// server. A nameless device sends a null name instead.
	var name any
	if device.Name != "" {
		name = device.Name
	}
	display := device.Name
	if display == "" {
		display = fmt.Sprintf("Device %d", device.ID)
	}

	return map[string]any{
		"id":      device.ID,
		"url":     "/api/dcim/devices/" + strconv.Itoa(device.ID) + "/",
		"display": display,
		"name":    name,
		"device_type": map[string]any{
			"id": 1, "url": "/api/dcim/device-types/1/", "display": "Test device type",
			"manufacturer": map[string]any{
				"id": 1, "url": "/api/dcim/manufacturers/1/", "display": "Test manufacturer",
				"name": "Test manufacturer", "slug": "test-manufacturer",
			},
			"model": "Test model", "slug": "test-device-type",
		},
		"role": map[string]any{
			"id": 1, "url": "/api/dcim/device-roles/1/", "display": "Server",
			"name": "Server", "slug": "server", "_depth": 0,
		},
		"site": map[string]any{
			"id": 1, "url": "/api/dcim/sites/1/", "display": "Test site",
			"name": "Test site", "slug": "test-site",
		},
		"status":                    map[string]any{"value": device.Status, "label": statusLabel(device.Status)},
		"custom_fields":             customFields,
		"tags":                      tags,
		"console_port_count":        0,
		"console_server_port_count": 0,
		"power_port_count":          0,
		"power_outlet_count":        0,
		"front_port_count":          0,
		"rear_port_count":           0,
		"device_bay_count":          0,
		"module_bay_count":          0,
		"inventory_item_count":      0,
		"last_updated":              time.Now().UTC().Format(time.RFC3339),
	}
}

func statusLabel(status string) string { return titleCase(status) }

func copyDevice(device *Device) Device {
	copied := Device{
		ID:           device.ID,
		Name:         device.Name,
		Status:       device.Status,
		CustomFields: map[string]any{},
		Tags:         append([]string(nil), device.Tags...),
		Version:      device.Version,
	}
	for key, value := range device.CustomFields {
		copied.CustomFields[key] = value
	}
	return copied
}

func readBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	defer func() { _ = r.Body.Close() }()
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil
	}
	return raw
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"detail": detail})
}

func deviceIDFromPath(path string) (int, error) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(path, "/api/dcim/devices/"), "/")
	return strconv.Atoi(trimmed)
}

// fieldID derives a stable numeric ID from a fixture name.
func fieldID(name string) int {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(name))
	return int(hasher.Sum32()%100000) + 1
}

// typeLabel uses labels accepted by the generated SDK.
func typeLabel(fieldType string) string {
	switch fieldType {
	case "text":
		return "Text"
	case "integer":
		return "Integer"
	case "boolean":
		return "Boolean (true/false)"
	case "json":
		return "JSON"
	case "select":
		return "Selection"
	case "multiselect":
		return "Multiple selection"
	default:
		return titleCase(fieldType)
	}
}

func titleCase(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
