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

// Package netboxmock serves the device API subset used by the BMF tests.
package netboxmock

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const devicesPath = "/api/dcim/devices/"

// Device is a snapshot of the mock device's current state.
type Device struct {
	Status       string
	CustomFields map[string]any
	version      int
}

func (d Device) etag() string {
	return fmt.Sprintf(`"v%d"`, d.version)
}

// Request records the parts of an API call used by the tests.
type Request struct {
	Method     string
	Path       string
	Query      url.Values
	IfMatch    string
	TokenIndex int
	Claimed    bool
}

// Server is an HTTP handler for an inventory of test devices.
type Server struct {
	mu       sync.Mutex
	devices  map[int]Device
	tokens   []string
	requests []Request
}

// New returns a fixture which accepts the supplied Bearer tokens.
func New(tokens ...string) *Server {
	return &Server{devices: make(map[int]Device), tokens: slices.Clone(tokens)}
}

// AddDevice registers a fixture with the given status and custom fields.
func (s *Server) AddDevice(id int, status string, fields map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	customFields := make(map[string]any, len(fields))
	maps.Copy(customFields, fields)
	s.devices[id] = Device{Status: status, CustomFields: customFields}
}

// Device returns a snapshot of the fixture with the given ID.
func (s *Server) Device(id int) Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	device := s.devices[id]
	device.CustomFields = maps.Clone(device.CustomFields)
	return device
}

// RequestsFor returns recorded calls matching the given method and path.
func (s *Server) RequestsFor(method, path string) []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var matches []Request
	for _, request := range s.requests {
		if request.Method == method && request.Path == path {
			matches = append(matches, request)
		}
	}
	return matches
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	tokenIndex := -1
	if scheme == "Bearer" {
		for i, candidate := range s.tokens {
			if subtle.ConstantTimeCompare([]byte(token), []byte(candidate)) == 1 {
				tokenIndex = i
			}
		}
	}
	if tokenIndex < 0 {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	request := Request{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(),
		IfMatch: r.Header.Get("If-Match"), TokenIndex: tokenIndex,
	}
	defer func() { s.requests = append(s.requests, request) }()

	path := strings.TrimSuffix(r.URL.Path, "/") + "/"
	switch {
	case r.Method == http.MethodGet && path == "/api/dcim/interfaces/":
		writeJSON(w, map[string]any{"count": 0, "next": nil, "previous": nil, "results": []any{}})
	case r.Method == http.MethodGet && path == devicesPath:
		s.listDevices(w, request.Query)
	case r.Method == http.MethodGet && strings.HasPrefix(path, devicesPath):
		s.getDevice(w, r)
	case r.Method == http.MethodPatch && strings.HasPrefix(path, devicesPath):
		request.Claimed = s.patchDevice(w, r, request.IfMatch)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) listDevices(w http.ResponseWriter, query url.Values) {
	var ids []int
	for id, device := range s.devices {
		if deviceMatchesQuery(device, query) {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	results := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		results = append(results, deviceResponse(id, s.devices[id]))
	}
	writeJSON(w, map[string]any{"count": len(results), "next": nil, "previous": nil, "results": results})
}

// deviceMatchesQuery evaluates the filters against the seeded device values.
func deviceMatchesQuery(device Device, query url.Values) bool {
	if status := query.Get("status"); status != "" && status != device.Status {
		return false
	}
	for key := range query {
		field, ok := strings.CutPrefix(key, "cf_")
		if !ok {
			continue
		}
		value := query.Get(key)
		if name, emptyLookup := strings.CutSuffix(field, "__empty"); emptyLookup {
			stored := device.CustomFields[name]
			if (value == "true") != (stored == nil || stored == "") {
				return false
			}
			continue
		}
		stored, ok := device.CustomFields[field]
		if !ok || fmt.Sprint(stored) != value {
			return false
		}
	}
	return true
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	id, err := deviceIDFromPath(r.URL.Path)
	device, found := s.devices[id]
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("ETag", device.etag())
	writeJSON(w, deviceResponse(id, device))
}

func (s *Server) patchDevice(w http.ResponseWriter, r *http.Request, ifMatch string) bool {
	id, err := deviceIDFromPath(r.URL.Path)
	device, found := s.devices[id]
	if err != nil || !found {
		http.NotFound(w, r)
		return false
	}
	if ifMatch != device.etag() {
		http.Error(w, "precondition failed", http.StatusPreconditionFailed)
		return false
	}

	var patch struct {
		Status       *string        `json:"status"`
		CustomFields map[string]any `json:"custom_fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return false
	}

	if patch.Status != nil {
		device.Status = *patch.Status
	}
	maps.Copy(device.CustomFields, patch.CustomFields)
	device.version++
	s.devices[id] = device

	w.Header().Set("ETag", device.etag())
	writeJSON(w, deviceResponse(id, device))
	return patch.Status != nil && *patch.Status == "active"
}

func deviceIDFromPath(path string) (int, error) {
	id := strings.TrimSuffix(strings.TrimPrefix(path, devicesPath), "/")
	return strconv.Atoi(id)
}

func deviceResponse(id int, device Device) map[string]any {
	return DevicePayload(id, fmt.Sprintf("device-%d", id), device.Status, device.CustomFields)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// DevicePayload returns the dcim.device response shape the generated SDK
// requires, so tests share one fixture instead of maintaining their own copies.
//
//nolint:goconst // Keep the JSON keys visible in the wire fixture.
func DevicePayload(id int, name, status string, customFields map[string]any) map[string]any {
	fields := maps.Clone(customFields)
	label := status
	if status != "" {
		label = strings.ToUpper(status[:1]) + status[1:]
	}
	return map[string]any{
		"id":      id,
		"url":     "/api/dcim/devices/" + strconv.Itoa(id) + "/",
		"display": name,
		"name":    name,
		"device_type": map[string]any{
			"id":      1,
			"url":     "/api/dcim/device-types/1/",
			"display": "Test device type",
			"manufacturer": map[string]any{
				"id": 1, "url": "/api/dcim/manufacturers/1/", "display": "Test manufacturer",
				"name": "Test manufacturer", "slug": "test-manufacturer",
			},
			"model": "Test model",
			"slug":  "test-device-type",
		},
		"role": map[string]any{
			"id": 1, "url": "/api/dcim/device-roles/1/", "display": "Server",
			"name": "Server", "slug": "server", "_depth": 0,
		},
		"site": map[string]any{
			"id": 1, "url": "/api/dcim/sites/1/", "display": "Test site",
			"name": "Test site", "slug": "test-site",
		},
		"status":                    map[string]any{"value": status, "label": label},
		"custom_fields":             fields,
		"console_port_count":        0,
		"console_server_port_count": 0,
		"power_port_count":          0,
		"power_outlet_count":        0,
		"front_port_count":          0,
		"rear_port_count":           0,
		"device_bay_count":          0,
		"module_bay_count":          0,
		"inventory_item_count":      0,
	}
}
