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

package netboxclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const testToken = "test-netbox-api-token"

func TestNormalizeEndpoint(t *testing.T) {
	tests := []struct {
		name              string
		endpoint          string
		allowInsecureHTTP bool
		want              string
		wantErr           bool
	}{
		{name: "origin", endpoint: "https://netbox.example.test", want: "https://netbox.example.test"},
		{name: "trailing slash", endpoint: "https://netbox.example.test/", want: "https://netbox.example.test"},
		{name: "trailing slash and whitespace", endpoint: " https://netbox.example.test/ ", want: "https://netbox.example.test"},
		{name: "loopback IPv4 HTTP requires opt-in", endpoint: "http://127.0.0.1:8080", wantErr: true},
		{
			name:              "loopback IPv4 HTTP with opt-in",
			endpoint:          "http://127.0.0.1:8080",
			allowInsecureHTTP: true,
			want:              "http://127.0.0.1:8080",
		},
		{name: "loopback IPv6 HTTP requires opt-in", endpoint: "http://[::1]:8080", wantErr: true},
		{
			name:              "loopback IPv6 HTTP with opt-in",
			endpoint:          "http://[::1]:8080",
			allowInsecureHTTP: true,
			want:              "http://[::1]:8080",
		},
		{name: "localhost HTTP requires opt-in", endpoint: "http://localhost:8080", wantErr: true},
		{
			name:              "localhost HTTP with opt-in",
			endpoint:          "http://localhost:8080",
			allowInsecureHTTP: true,
			want:              "http://localhost:8080",
		},
		{name: "remote HTTP requires opt-in", endpoint: "http://netbox.example.test", wantErr: true},
		{
			name:              "remote HTTP with opt-in",
			endpoint:          "http://netbox.example.test",
			allowInsecureHTTP: true,
			want:              "http://netbox.example.test",
		},
		{name: "localhost subdomain HTTP requires opt-in", endpoint: "http://netbox.localhost", wantErr: true},
		{name: "unsupported scheme is rejected", endpoint: "ftp://netbox.example.test", wantErr: true},
		{name: "api path is rejected", endpoint: "https://netbox.example.test/api", wantErr: true},
		{name: "other path is rejected", endpoint: "https://netbox.example.test/netbox", wantErr: true},
		{name: "credentials are rejected", endpoint: "https://user:password@netbox.example.test", wantErr: true},
		{name: "query is rejected", endpoint: "https://netbox.example.test?token=secret", wantErr: true},
		{name: "fragment is rejected", endpoint: "https://netbox.example.test/#devices", wantErr: true},
		{name: "missing host is rejected", endpoint: "https:///api", wantErr: true},
		{name: "malformed URL is rejected", endpoint: "not a URL", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeEndpoint(tt.endpoint, tt.allowInsecureHTTP)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeEndpoint(%q) error = %v, wantErr %t", tt.endpoint, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("normalizeEndpoint(%q) = %q, want %q", tt.endpoint, got, tt.want)
			}
		})
	}
}

func TestReadToken(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{name: "trims mounted file newline", content: testToken + "\n", want: testToken},
		{name: "trims surrounding whitespace", content: " \t" + testToken + " \n", want: testToken},
		{name: "empty token", content: " \n", wantErr: true},
		{name: "embedded newline", content: "first\nsecond", wantErr: true},
		{name: "embedded space", content: "first second", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := readToken(path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("readToken() error = %v, wantErr %t", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("readToken() = %q, want %q", got, tt.want)
			}
		})
	}

	if _, err := readToken(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, errInvalidToken) {
		t.Fatalf("readToken(missing) error = %v, want %v", err, errInvalidToken)
	}
	if _, err := readToken(""); !errors.Is(err, errInvalidToken) {
		t.Fatalf("readToken(empty path) error = %v, want %v", err, errInvalidToken)
	}
}

func TestNewRejectsInvalidCA(t *testing.T) {
	tokenFile := writeTestToken(t)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := New(Config{Endpoint: "https://netbox.example.test", TokenFile: tokenFile, CAFile: caFile})
	if !errors.Is(err, errInvalidCA) {
		t.Fatalf("New() error = %v, want %v", err, errInvalidCA)
	}
}

func TestNewValidatesHTTPAndRequiresTokenFile(t *testing.T) {
	validToken := writeTestToken(t)
	tests := []struct {
		name   string
		config Config
		want   error
	}{
		{
			name:   "loopback HTTP endpoint requires opt-in",
			config: Config{Endpoint: "http://127.0.0.1:8080", TokenFile: validToken},
			want:   errInsecureHTTPNotAllowed,
		},
		{
			name: "loopback HTTP endpoint with opt-in",
			config: Config{
				Endpoint:          "http://127.0.0.1:8080",
				TokenFile:         validToken,
				AllowInsecureHTTP: true,
			},
		},
		{
			name:   "remote HTTP endpoint requires opt-in",
			config: Config{Endpoint: "http://netbox.example.test", TokenFile: validToken},
			want:   errInsecureHTTPNotAllowed,
		},
		{
			name: "remote HTTP endpoint with opt-in",
			config: Config{
				Endpoint:          "http://netbox.example.test",
				TokenFile:         validToken,
				AllowInsecureHTTP: true,
			},
		},
		{name: "missing token file", config: Config{Endpoint: "https://netbox.example.test"}, want: errInvalidToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.config)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("New() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("New() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestGetDeviceReturnsTypedDeviceAndETag(t *testing.T) {
	etag := `W/"device-revision-42"`
	server, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/dcim/devices/42/" {
			t.Errorf("request = %s %s, want GET /api/dcim/devices/42/", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Token "+testToken {
			t.Errorf("Authorization = %q, want mounted token", got)
		}
		w.Header().Set("ETag", etag)
		writeJSON(t, w, http.StatusOK, json.RawMessage(deviceResponse(42, "host-42", "active")))
	}))
	_ = server

	snapshot, err := client.GetDevice(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetDevice() error = %v", err)
	}
	if snapshot.ETag != etag {
		t.Fatalf("GetDevice() ETag = %q, want %q", snapshot.ETag, etag)
	}
	if snapshot.Device.ID != "42" || snapshot.Device.Name != "host-42" || snapshot.Device.Status != "active" {
		t.Fatalf("GetDevice() device = %#v", snapshot.Device)
	}
	if got := snapshot.Device.CustomFields["osac_instance_id"]; got != "instance-42" {
		t.Fatalf("custom field osac_instance_id = %#v, want instance-42", got)
	}
}

func TestForEachDeviceEncodesStatusAndCustomFieldFilters(t *testing.T) {
	server, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/dcim/devices/" {
			t.Errorf("request = %s %s, want GET /api/dcim/devices/", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("status"); got != "active" {
			t.Errorf("status filter = %q, want active", got)
		}
		if got := r.URL.Query().Get("cf_osac_instance_id"); got != "instance-42" {
			t.Errorf("custom field filter = %q, want instance-42", got)
		}
		if got := r.URL.Query().Get("limit"); got != "100" {
			t.Errorf("limit = %q, want 100", got)
		}
		if got := r.URL.Query().Get("offset"); got != "0" {
			t.Errorf("offset = %q, want 0", got)
		}
		if got := r.Header.Get("Authorization"); got != "Token "+testToken {
			t.Errorf("Authorization = %q, want mounted token", got)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"count":    1,
			"next":     nil,
			"previous": nil,
			"results":  []json.RawMessage{json.RawMessage(deviceResponse(42, "host-42", "active"))},
		})
	}))
	_ = server

	var devices []Device
	err := client.ForEachDevice(context.Background(), DeviceQuery{
		"status":              "active",
		"cf_osac_instance_id": "instance-42",
	}, func(device Device) (bool, error) {
		devices = append(devices, device)
		return true, nil
	})
	if err != nil {
		t.Fatalf("ForEachDevice() error = %v", err)
	}
	if len(devices) != 1 || devices[0].ID != "42" || devices[0].Name != "host-42" {
		t.Fatalf("ForEachDevice() visited %#v, want one host-42 device", devices)
	}
}

func TestForEachDeviceFollowsPagination(t *testing.T) {
	var calls atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		expectedOffset := "0"
		if call == 2 {
			expectedOffset = "100"
		}
		if got := r.URL.Query().Get("offset"); got != expectedOffset {
			t.Errorf("page %d offset = %q, want %q", call, got, expectedOffset)
		}
		if call == 1 {
			results := make([]json.RawMessage, int(defaultPageSize))
			for i := range results {
				results[i] = json.RawMessage(deviceResponse(1000+i, fmt.Sprintf("host-%d", 1000+i), "active"))
			}
			writeJSON(t, w, http.StatusOK, map[string]any{
				"count":    101,
				"next":     "https://netbox.example.test/api/dcim/devices/?limit=100&offset=100",
				"previous": nil,
				"results":  results,
			})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"count":    101,
			"next":     nil,
			"previous": "https://netbox.example.test/api/dcim/devices/?limit=100&offset=0",
			"results":  []json.RawMessage{json.RawMessage(deviceResponse(1100, "host-1100", "active"))},
		})
	}))

	var devices []Device
	err := client.ForEachDevice(context.Background(), nil, func(device Device) (bool, error) {
		devices = append(devices, device)
		return true, nil
	})
	if err != nil {
		t.Fatalf("ForEachDevice() error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("GET page calls = %d, want 2", got)
	}
	if len(devices) != 101 || devices[0].ID != "1000" || devices[100].ID != "1100" {
		t.Fatalf("ForEachDevice() visited %d devices, want IDs 1000 through 1100", len(devices))
	}
}

func TestForEachDeviceStopsWhenVisitorStops(t *testing.T) {
	var calls atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"count":    201,
			"next":     "https://netbox.example.test/api/dcim/devices/?limit=100&offset=100",
			"previous": nil,
			"results":  []json.RawMessage{json.RawMessage(deviceResponse(42, "host-42", "active"))},
		})
	}))

	var visited int
	err := client.ForEachDevice(context.Background(), nil, func(Device) (bool, error) {
		visited++
		return false, nil
	})
	if err != nil {
		t.Fatalf("ForEachDevice() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("GET page calls = %d, want 1 after visitor stops", got)
	}
	if visited != 1 {
		t.Fatalf("visitor calls = %d, want 1", visited)
	}
}

func TestForEachDeviceStopsOnContextCancellation(t *testing.T) {
	var calls atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"count":    201,
			"next":     "https://netbox.example.test/api/dcim/devices/?limit=100&offset=100",
			"previous": nil,
			"results":  []json.RawMessage{json.RawMessage(deviceResponse(42, "host-42", "active"))},
		})
	}))

	ctx, cancel := context.WithCancel(context.Background())
	err := client.ForEachDevice(ctx, nil, func(Device) (bool, error) {
		cancel()
		return true, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ForEachDevice() error = %v, want context.Canceled", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("GET page calls = %d, want 1 after cancellation", got)
	}
}

func TestForEachDeviceRejectsNextAfterReportedCount(t *testing.T) {
	var calls atomic.Int32
	var visited atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSON(t, w, http.StatusOK, map[string]any{
			"count":    1,
			"next":     "https://netbox.example.test/api/dcim/devices/?limit=100&offset=1",
			"previous": nil,
			"results":  []json.RawMessage{json.RawMessage(deviceResponse(42, "host-42", "active"))},
		})
	}))

	err := client.ForEachDevice(context.Background(), nil, func(Device) (bool, error) {
		visited.Add(1)
		return true, nil
	})
	assertAPIError(t, err, ErrorKindInvalidResponse, 0)
	if got := calls.Load(); got != 1 {
		t.Fatalf("GET page calls = %d, want 1", got)
	}
	if got := visited.Load(); got != 0 {
		t.Fatalf("visitor calls = %d, want 0 for invalid pagination", got)
	}
}

func TestForEachDeviceRejectsUnsupportedQueryWithoutRequest(t *testing.T) {
	var calls atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	err := client.ForEachDevice(context.Background(), DeviceQuery{"name": "host-42"}, func(Device) (bool, error) {
		return true, nil
	})
	assertAPIError(t, err, ErrorKindInvalidRequest, 0)
	if got := calls.Load(); got != 0 {
		t.Fatalf("server calls = %d, want 0 for unsupported query", got)
	}
}

func TestForEachDeviceCustomFieldUsesDeviceObjectType(t *testing.T) {
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/extras/custom-fields/" {
			t.Errorf("request = %s %s, want GET /api/extras/custom-fields/", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("object_type"); got != "dcim.device" {
			t.Errorf("object_type = %q, want dcim.device", got)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"count":    1,
			"next":     nil,
			"previous": nil,
			"results": []any{map[string]any{
				"id":           7,
				"url":          "/api/extras/custom-fields/7/",
				"display":      "OSAC instance ID",
				"object_types": []string{"dcim.device"},
				"type":         map[string]string{"value": "text", "label": "Text"},
				"data_type":    "text",
				"name":         "osac_instance_id",
				"label":        "OSAC instance ID",
				"required":     false,
			}},
		})
	}))

	var fields []CustomField
	err := client.ForEachDeviceCustomField(context.Background(), func(field CustomField) (bool, error) {
		fields = append(fields, field)
		return true, nil
	})
	if err != nil {
		t.Fatalf("ForEachDeviceCustomField() error = %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("ForEachDeviceCustomField() visited %d fields, want 1", len(fields))
	}
	field := fields[0]
	if field.Name != "osac_instance_id" || field.Label != "OSAC instance ID" || field.Type != "text" || field.DataType != "text" || field.Required {
		t.Fatalf("ForEachDeviceCustomField() field = %#v", field)
	}
	if len(field.ObjectTypes) != 1 || field.ObjectTypes[0] != "dcim.device" {
		t.Fatalf("custom field object types = %#v, want [dcim.device]", field.ObjectTypes)
	}
}

func TestForEachDeviceCustomFieldFollowsPagination(t *testing.T) {
	var calls atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("object_type"); got != "dcim.device" {
			t.Errorf("object_type = %q, want dcim.device", got)
		}
		if got := r.URL.Query().Get("limit"); got != "100" {
			t.Errorf("limit = %q, want 100", got)
		}

		calls.Add(1)
		switch r.URL.Query().Get("offset") {
		case "0":
			results := make([]any, 0, 100)
			for id := 1; id <= 100; id++ {
				results = append(results, customFieldResult(id, fmt.Sprintf("field-%d", id)))
			}
			writeJSON(t, w, http.StatusOK, map[string]any{
				"count":    101,
				"next":     "https://netbox.example.test/api/extras/custom-fields/?limit=100&offset=100",
				"previous": nil,
				"results":  results,
			})
		case "100":
			writeJSON(t, w, http.StatusOK, map[string]any{
				"count":    101,
				"next":     nil,
				"previous": "https://netbox.example.test/api/extras/custom-fields/?limit=100&offset=0",
				"results":  []any{customFieldResult(101, "field-101")},
			})
		default:
			t.Errorf("offset = %q, want 0 or 100", r.URL.Query().Get("offset"))
			w.WriteHeader(http.StatusBadRequest)
		}
	}))

	var names []string
	err := client.ForEachDeviceCustomField(context.Background(), func(field CustomField) (bool, error) {
		names = append(names, field.Name)
		return true, nil
	})
	if err != nil {
		t.Fatalf("ForEachDeviceCustomField() error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("GET page calls = %d, want 2", got)
	}
	if len(names) != 101 || names[0] != "field-1" || names[100] != "field-101" {
		t.Fatalf("visited %d custom fields, want field-1 through field-101", len(names))
	}
}

func TestForEachDeviceCustomFieldStopsWhenVisitorStops(t *testing.T) {
	var calls atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		results := make([]any, 0, 100)
		for id := 1; id <= 100; id++ {
			results = append(results, customFieldResult(id, fmt.Sprintf("field-%d", id)))
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"count":    101,
			"next":     "https://netbox.example.test/api/extras/custom-fields/?limit=100&offset=100",
			"previous": nil,
			"results":  results,
		})
	}))

	var visited int
	err := client.ForEachDeviceCustomField(context.Background(), func(CustomField) (bool, error) {
		visited++
		return false, nil
	})
	if err != nil {
		t.Fatalf("ForEachDeviceCustomField() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("GET page calls = %d, want 1 after visitor stops", got)
	}
	if visited != 1 {
		t.Fatalf("visitor calls = %d, want 1", visited)
	}
}

func TestForEachDeviceCustomFieldRejectsNextAfterReportedCount(t *testing.T) {
	var visited atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"count":    1,
			"next":     "https://netbox.example.test/api/extras/custom-fields/?limit=100&offset=1",
			"previous": nil,
			"results": []any{map[string]any{
				"id":           7,
				"url":          "/api/extras/custom-fields/7/",
				"display":      "OSAC instance ID",
				"object_types": []string{"dcim.device"},
				"type":         map[string]string{"value": "text", "label": "Text"},
				"data_type":    "text",
				"name":         "osac_instance_id",
				"label":        "OSAC instance ID",
				"required":     false,
			}},
		})
	}))

	err := client.ForEachDeviceCustomField(context.Background(), func(CustomField) (bool, error) {
		visited.Add(1)
		return true, nil
	})
	assertAPIError(t, err, ErrorKindInvalidResponse, 0)
	if got := visited.Load(); got != 0 {
		t.Fatalf("visitor calls = %d, want 0 for invalid pagination", got)
	}
}

func TestNextPaginationOffsetRejectsInvalidMetadata(t *testing.T) {
	tests := []struct {
		name        string
		offset      int32
		total       int32
		resultCount int
		hasNext     bool
	}{
		{name: "negative offset", offset: -1, total: 1, resultCount: 1},
		{name: "negative count", offset: 0, total: -1, resultCount: 1},
		{name: "page exceeds requested limit", offset: 0, total: 101, resultCount: 101, hasNext: true},
		{name: "empty page with next", offset: 0, total: 1, resultCount: 0, hasNext: true},
		{name: "offset exceeds SDK range", offset: int32(maxInt32Offset) - 50, total: int32(maxInt32Offset), resultCount: 100, hasNext: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := nextPaginationOffset(tt.offset, tt.total, tt.resultCount, tt.hasNext)
			assertAPIError(t, err, ErrorKindInvalidResponse, 0)
		})
	}
}

func customFieldResult(id int, name string) map[string]any {
	label := "Custom field " + name
	return map[string]any{
		"id":           id,
		"url":          fmt.Sprintf("/api/extras/custom-fields/%d/", id),
		"display":      label,
		"object_types": []string{"dcim.device"},
		"type":         map[string]string{"value": "text", "label": "Text"},
		"data_type":    "text",
		"name":         name,
		"label":        label,
		"required":     false,
	}
}

func TestPatchDeviceForwardsETagAndReturnsUpdatedSnapshot(t *testing.T) {
	const expectedETag = `W/"device-revision-42"`
	const updatedETag = `W/"device-revision-43"`
	var calls atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPatch || r.URL.Path != "/api/dcim/devices/42/" {
			t.Errorf("request = %s %s, want PATCH /api/dcim/devices/42/", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("If-Match"); got != expectedETag {
			t.Errorf("If-Match = %q, want %q", got, expectedETag)
		}
		if got := r.Header.Get("Authorization"); got != "Token "+testToken {
			t.Errorf("Authorization = %q, want mounted token", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode PATCH body: %v", err)
		}
		if got := body["status"]; got != "active" {
			t.Errorf("PATCH status = %#v, want active", got)
		}
		customFields, _ := body["custom_fields"].(map[string]any)
		if got := customFields["osac_instance_id"]; got != "instance-42" {
			t.Errorf("PATCH custom field = %#v, want instance-42", got)
		}
		if got := body["changelog_message"]; got != "Claimed by OSAC" {
			t.Errorf("PATCH changelog_message = %#v, want Claim by OSAC", got)
		}
		w.Header().Set("ETag", updatedETag)
		writeJSON(t, w, http.StatusOK, json.RawMessage(deviceResponse(42, "host-42", "active")))
	}))

	status := "active"
	snapshot, err := client.PatchDevice(context.Background(), "42", DevicePatch{
		Status:           &status,
		CustomFields:     map[string]any{"osac_instance_id": "instance-42"},
		ChangelogMessage: "Claimed by OSAC",
	}, expectedETag)
	if err != nil {
		t.Fatalf("PatchDevice() error = %v", err)
	}
	if snapshot.ETag != updatedETag || snapshot.Device.ID != "42" || snapshot.Device.Status != "active" {
		t.Fatalf("PatchDevice() snapshot = %#v", snapshot)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("PATCH calls = %d, want 1", got)
	}
}

func TestPatchDeviceRejectsMissingETagWithoutRequest(t *testing.T) {
	var calls atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	status := "active"

	_, err := client.PatchDevice(context.Background(), "42", DevicePatch{Status: &status}, " \r\n")
	assertAPIError(t, err, ErrorKindInvalidRequest, 0)
	if got := calls.Load(); got != 0 {
		t.Fatalf("server calls = %d, want 0 for missing ETag", got)
	}
}

func TestGetDeviceRetriesTransientFailureAndSanitizesErrors(t *testing.T) {
	var calls atomic.Int32
	server, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writeJSON(t, w, http.StatusServiceUnavailable, map[string]string{
				"detail": "sensitive response body",
			})
			return
		}
		writeJSON(t, w, http.StatusOK, json.RawMessage(deviceResponse(42, "host-42", "active")))
	}))
	_ = server

	snapshot, err := client.GetDevice(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetDevice() after transient failure error = %v", err)
	}
	if snapshot.Device.ID != "42" {
		t.Fatalf("GetDevice() device ID = %q, want 42", snapshot.Device.ID)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("GET calls = %d, want retry once after 503", got)
	}
}

func TestGetDeviceClassifiesAndRedactsPermanentFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		kind       ErrorKind
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, kind: ErrorKindUnauthorized},
		{name: "forbidden", statusCode: http.StatusForbidden, kind: ErrorKindForbidden},
		{name: "not found", statusCode: http.StatusNotFound, kind: ErrorKindNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tt.statusCode)
				_, _ = fmt.Fprintf(w, "private response body; token=%s", testToken)
			}))

			_, err := client.GetDevice(context.Background(), "42")
			assertAPIError(t, err, tt.kind, tt.statusCode)
			if got := calls.Load(); got != 1 {
				t.Fatalf("GET calls = %d, want 1 for non-retryable HTTP %d", got, tt.statusCode)
			}
			assertDoesNotContain(t, err.Error(), testToken, "private response body", server.URL)
		})
	}
}

func TestGetDeviceStopsAfterThreeTransientFailures(t *testing.T) {
	var calls atomic.Int32
	server, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, "private response body; token=%s", testToken)
	}))

	_, err := client.GetDevice(context.Background(), "42")
	assertAPIError(t, err, ErrorKindUnavailable, http.StatusServiceUnavailable)
	if got := calls.Load(); got != maxGETAttempts {
		t.Fatalf("GET calls = %d, want %d", got, maxGETAttempts)
	}
	assertDoesNotContain(t, err.Error(), testToken, "private response body", server.URL)
}

func TestPatchDeviceDoesNotRetryUncertainFailure(t *testing.T) {
	var calls atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("If-Match"); got != `W/"device-revision-42"` {
			t.Errorf("If-Match = %q, want original weak ETag", got)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, "private response body; token=%s", testToken)
	}))
	status := "active"

	_, err := client.PatchDevice(context.Background(), "42", DevicePatch{Status: &status}, `W/"device-revision-42"`)
	assertAPIError(t, err, ErrorKindUnavailable, http.StatusServiceUnavailable)
	if got := calls.Load(); got != 1 {
		t.Fatalf("PATCH calls = %d, want 1 after uncertain failure", got)
	}
	assertDoesNotContain(t, err.Error(), testToken, "private response body")
}

func TestPatchDeviceClassifiesPreconditionFailure(t *testing.T) {
	var calls atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("If-Match"); got != `W/"old-revision"` {
			t.Errorf("If-Match = %q, want old ETag", got)
		}
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte("stale ETag detail"))
	}))
	status := "active"

	_, err := client.PatchDevice(context.Background(), "42", DevicePatch{Status: &status}, `W/"old-revision"`)
	assertAPIError(t, err, ErrorKindConflict, http.StatusPreconditionFailed)
	if got := calls.Load(); got != 1 {
		t.Fatalf("PATCH calls = %d, want 1 on 412", got)
	}
	assertDoesNotContain(t, err.Error(), "stale ETag detail")
}

func TestClientRejectsTLSCertificateNotInConfiguredRoots(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSON(t, w, http.StatusOK, json.RawMessage(deviceResponse(42, "host-42", "active")))
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	client, err := New(Config{Endpoint: server.URL, TokenFile: writeTestToken(t)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.GetDevice(context.Background(), "42")
	assertAPIError(t, err, ErrorKindTransport, 0)
	if got := calls.Load(); got != 0 {
		t.Fatalf("server handler calls = %d, want 0 for untrusted certificate", got)
	}
	assertDoesNotContain(t, err.Error(), server.URL, testToken)
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	source, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/api/dcim/devices/42/", http.StatusFound)
	}))
	_ = source
	_, err := client.GetDevice(context.Background(), "42")
	assertAPIError(t, err, ErrorKindRedirect, http.StatusFound)
	if got := destinationCalls.Load(); got != 0 {
		t.Fatalf("redirect destination calls = %d, want 0", got)
	}
}

func TestGetDeviceRejectsNonNumericIDBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	_, client := newTrustedClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	_, err := client.GetDevice(context.Background(), "42/other")
	assertAPIError(t, err, ErrorKindInvalidRequest, 0)
	if got := calls.Load(); got != 0 {
		t.Fatalf("server calls = %d, want 0 for invalid device ID", got)
	}
}

func newTrustedClient(t *testing.T, handler http.Handler) (*httptest.Server, *Client) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	caFile := filepath.Join(t.TempDir(), "netbox-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{Endpoint: server.URL + "/", TokenFile: writeTestToken(t), CAFile: caFile})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server, client
}

func writeTestToken(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "netbox-token")
	if err := os.WriteFile(path, []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func deviceResponse(id int, name, status string) string {
	return fmt.Sprintf(`{
		"id": %d,
		"url": "/api/dcim/devices/%d/",
		"display": %q,
		"name": %q,
		"device_type": {
			"id": 1,
			"url": "/api/dcim/device-types/1/",
			"display": "Test device type",
			"manufacturer": {"id": 1, "url": "/api/dcim/manufacturers/1/", "display": "Test manufacturer", "name": "Test manufacturer", "slug": "test-manufacturer"},
			"model": "Test model",
			"slug": "test-device-type"
		},
		"role": {"id": 1, "url": "/api/dcim/device-roles/1/", "display": "Server", "name": "Server", "slug": "server", "_depth": 0},
		"site": {"id": 1, "url": "/api/dcim/sites/1/", "display": "Test site", "name": "Test site", "slug": "test-site"},
		"status": {"value": %q, "label": "Active"},
		"custom_fields": {"osac_instance_id": "instance-42"},
		"console_port_count": 0,
		"console_server_port_count": 0,
		"power_port_count": 0,
		"power_outlet_count": 0,
		"front_port_count": 0,
		"rear_port_count": 0,
		"device_bay_count": 0,
		"module_bay_count": 0,
		"inventory_item_count": 0
	}`, id, id, name, name, status)
}

func assertAPIError(t *testing.T, err error, wantKind ErrorKind, wantStatus int) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want API error kind %q", wantKind)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T (%v), want *APIError", err, err)
	}
	if apiErr.Kind != wantKind || apiErr.StatusCode != wantStatus {
		t.Fatalf("API error = %#v, want kind %q and status %d", apiErr, wantKind, wantStatus)
	}
}

func assertDoesNotContain(t *testing.T, value string, forbidden ...string) {
	t.Helper()
	for _, part := range forbidden {
		if part != "" && strings.Contains(value, part) {
			t.Errorf("diagnostic %q contains sensitive value %q", value, part)
		}
	}
}

func TestTLSConfigKeepsCertificateVerificationEnabled(t *testing.T) {
	config, err := newTLSConfig("")
	if err != nil {
		t.Fatalf("newTLSConfig() error = %v", err)
	}
	if config.InsecureSkipVerify {
		t.Fatal("TLS certificate verification is disabled")
	}
	if config.MinVersion < tls.VersionTLS12 {
		t.Fatalf("TLS minimum version = %#x, want TLS 1.2 or newer", config.MinVersion)
	}
	if config.RootCAs == nil {
		t.Fatal("TLS root CA pool is nil")
	}
}

func TestParseDeviceIDRejectsOverflowAndNonPositiveIDs(t *testing.T) {
	for _, id := range []string{"", "0", "-1", "2147483648", "host-42", "42/"} {
		t.Run(url.PathEscape(id), func(t *testing.T) {
			if _, err := parseDeviceID(id); err == nil {
				t.Fatalf("parseDeviceID(%q) succeeded, want error", id)
			}
		})
	}
}
