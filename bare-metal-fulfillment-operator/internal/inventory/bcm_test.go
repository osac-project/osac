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

package inventory

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestBCMServer(t *testing.T, handler http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	return server, server.Client()
}

func TestParseBCMConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name: "valid config",
			cfg: &Config{
				Options: map[string]any{
					"bcm": map[string]any{
						"url":      "https://bcm-head:8081",
						"certFile": "/certs/tls.crt",
						"keyFile":  "/certs/tls.key",
					},
				},
			},
		},
		{
			name:    "missing bcm options",
			cfg:     &Config{Options: map[string]any{}},
			wantErr: "bcm options not found",
		},
		{
			name: "missing url",
			cfg: &Config{
				Options: map[string]any{
					"bcm": map[string]any{
						"certFile": "/certs/tls.crt",
						"keyFile":  "/certs/tls.key",
					},
				},
			},
			wantErr: "bcm url is required",
		},
		{
			name: "missing certFile",
			cfg: &Config{
				Options: map[string]any{
					"bcm": map[string]any{
						"url":     "https://bcm-head:8081",
						"keyFile": "/certs/tls.key",
					},
				},
			},
			wantErr: "bcm certFile is required",
		},
		{
			name: "missing keyFile",
			cfg: &Config{
				Options: map[string]any{
					"bcm": map[string]any{
						"url":      "https://bcm-head:8081",
						"certFile": "/certs/tls.crt",
					},
				},
			},
			wantErr: "bcm keyFile is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseBCMConfig(tt.cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.URL != "https://bcm-head:8081" {
				t.Errorf("expected URL %q, got %q", "https://bcm-head:8081", result.URL)
			}
		})
	}
}

func TestCheckVersion(t *testing.T) {
	t.Run("valid version", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != bcmVersionPath {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"cm_version":"11.0","cmd_version":"3.1","build_hash":"abc","build_index":1,"database_version":1}`)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		if err := c.checkVersion(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("version too old", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"cm_version":"9.2","cmd_version":"2.0","build_hash":"old","build_index":1,"database_version":1}`)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		err := c.checkVersion(context.Background())
		if err == nil {
			t.Fatal("expected error for old version, got nil")
		}
		if !errors.Is(err, ErrBCMVersionTooOld) {
			t.Errorf("expected ErrBCMVersionTooOld, got: %v", err)
		}
	})

	t.Run("exact minimum version", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"cm_version":"10.25","cmd_version":"3.0","build_hash":"min","build_index":1,"database_version":1}`)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		if err := c.checkVersion(context.Background()); err != nil {
			t.Fatalf("expected minimum version to pass, got: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		err := c.checkVersion(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrBCMServerError) {
			t.Errorf("expected ErrBCMServerError, got: %v", err)
		}
	})
}

func TestGetDevices(t *testing.T) {
	t.Run("returns list of devices", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertJSONCall(t, r, "cmdevice", "getDevices")
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `[
				{"baseType":"Device","childType":"LiteNode","uuid":"uuid1","hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}},
				{"baseType":"Device","childType":"PhysicalNode","uuid":"uuid2","hostname":"head01","mac":"aa:bb:cc:dd:ee:02","extra_values":null}
			]`)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		devices, err := c.getDevices(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(devices) != 2 {
			t.Fatalf("expected 2 devices, got %d", len(devices))
		}
		if devices[0].Hostname != "node001" {
			t.Errorf("expected hostname node001, got %s", devices[0].Hostname)
		}
		if devices[0].ChildType != "LiteNode" {
			t.Errorf("expected childType LiteNode, got %s", devices[0].ChildType)
		}
		if devices[0].raw == nil {
			t.Error("expected raw JSON to be preserved")
		}
	})

	t.Run("empty list", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `[]`)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		devices, err := c.getDevices(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(devices) != 0 {
			t.Fatalf("expected 0 devices, got %d", len(devices))
		}
	})
}

func TestGetDevice(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := assertJSONCall(t, r, "cmdevice", "getDevice")
			var args []string
			if err := json.Unmarshal(req.Args.(json.RawMessage), &args); err != nil {
				t.Fatalf("failed to parse args: %v", err)
			}
			if args[0] != "node001" {
				t.Errorf("expected hostname node001, got %s", args[0])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"baseType":"Device","childType":"LiteNode","uuid":"uuid1","hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}`)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		device, err := c.getDevice(context.Background(), "node001")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if device == nil {
			t.Fatal("expected device, got nil")
		}
		if device.Hostname != "node001" {
			t.Errorf("expected hostname node001, got %s", device.Hostname)
		}
		if device.ExtraValues["resource_class"] != "h100" {
			t.Errorf("expected resource_class h100, got %v", device.ExtraValues["resource_class"])
		}
	})

	t.Run("not found returns nil", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `null`)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		device, err := c.getDevice(context.Background(), "nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if device != nil {
			t.Errorf("expected nil device for not found, got %+v", device)
		}
	})
}

func TestUpdateDevice(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertJSONCall(t, r, "cmdevice", "updateDevice")
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"success":true,"task_uuid":"00000000-0000-0000-0000-000000000000","updated_entity":null,"validation":[]}`)
		}))

		deviceJSON := json.RawMessage(`{"baseType":"Device","childType":"LiteNode","hostname":"node001"}`)
		c := NewBCMClientForTest(client, server.URL, "test")
		resp, err := c.updateDevice(context.Background(), deviceJSON)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !resp.Success {
			t.Error("expected success=true")
		}
	})

	t.Run("validation error", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"success":false,"task_uuid":"00000000-0000-0000-0000-000000000000","updated_entity":null,"validation":[{"baseType":"Validation","error_code":"NOT_NULL","field":"partition","message":"partition is required","severity":"ERROR"}]}`)
		}))

		deviceJSON := json.RawMessage(`{"baseType":"Device","hostname":"node001"}`)
		c := NewBCMClientForTest(client, server.URL, "test")
		resp, err := c.updateDevice(context.Background(), deviceJSON)
		if err == nil {
			t.Fatal("expected validation error, got nil")
		}

		var valErr *BCMValidationError
		if !errors.As(err, &valErr) {
			t.Fatalf("expected BCMValidationError, got: %T %v", err, err)
		}
		if len(valErr.Validations) != 1 {
			t.Fatalf("expected 1 validation, got %d", len(valErr.Validations))
		}
		if valErr.Validations[0].ErrorCode != "NOT_NULL" {
			t.Errorf("expected error_code NOT_NULL, got %s", valErr.Validations[0].ErrorCode)
		}

		if resp == nil {
			t.Fatal("expected response even on validation error")
		}
	})
}

func TestDoJSONCall(t *testing.T) {
	t.Run("auth error from certificate message", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"errormessage":"Your certificate (profile:) does not allow access to CMDevice::getDevices\n"}`)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		_, err := c.doJSONCall(context.Background(), "cmdevice", "getDevices", []any{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrBCMAuthFailed) {
			t.Errorf("expected ErrBCMAuthFailed, got: %v", err)
		}
	})

	t.Run("generic api error", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"errormessage":"No such service: '(cm)fake'\n"}`)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		_, err := c.doJSONCall(context.Background(), "cmfake", "getDevices", []any{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if errors.Is(err, ErrBCMAuthFailed) {
			t.Error("should not be auth error for service not found")
		}
	})

	t.Run("server error 500", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, "internal error")
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		_, err := c.doJSONCall(context.Background(), "cmdevice", "getDevices", []any{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrBCMServerError) {
			t.Errorf("expected ErrBCMServerError, got: %v", err)
		}
	})

	t.Run("auth error from HTTP 401", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		_, err := c.doJSONCall(context.Background(), "cmdevice", "getDevices", []any{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrBCMAuthFailed) {
			t.Errorf("expected ErrBCMAuthFailed, got: %v", err)
		}
	})

	t.Run("client error 404", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		_, err := c.doJSONCall(context.Background(), "cmdevice", "getDevices", []any{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if errors.Is(err, ErrBCMAuthFailed) || errors.Is(err, ErrBCMServerError) {
			t.Errorf("404 should be generic client error, got: %v", err)
		}
	})

	t.Run("connection error", func(t *testing.T) {
		client := &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}}

		c := NewBCMClientForTest(client, "https://localhost:1", "test")
		_, err := c.doJSONCall(context.Background(), "cmdevice", "getDevices", []any{})
		if err == nil {
			t.Fatal("expected connection error, got nil")
		}
		if !errors.Is(err, ErrBCMConnectionFailed) {
			t.Errorf("expected ErrBCMConnectionFailed, got: %v", err)
		}
	})

	t.Run("nil args defaults to empty array", func(t *testing.T) {
		server, client := newTestBCMServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := assertJSONCall(t, r, "cmdevice", "getDevices")
			raw := req.Args.(json.RawMessage)
			if string(raw) != "[]" {
				t.Errorf("expected args to be [], got %s", string(raw))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `[]`)
		}))

		c := NewBCMClientForTest(client, server.URL, "test")
		_, err := c.doJSONCall(context.Background(), "cmdevice", "getDevices", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestSetExtraValue(t *testing.T) {
	raw := json.RawMessage(`{"hostname":"node001","extra_values":{"resource_class":"h100"}}`)

	updated, err := setExtraValue(raw, "osac_instance_id", "test-uid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(updated, &obj); err != nil {
		t.Fatalf("failed to unmarshal updated JSON: %v", err)
	}

	ev := obj["extra_values"].(map[string]any)
	if ev["osac_instance_id"] != "test-uid-123" {
		t.Errorf("expected osac_instance_id=test-uid-123, got %v", ev["osac_instance_id"])
	}
	if ev["resource_class"] != "h100" {
		t.Errorf("expected resource_class=h100 preserved, got %v", ev["resource_class"])
	}
}

func TestSetExtraValueNilExtraValues(t *testing.T) {
	raw := json.RawMessage(`{"hostname":"node001","extra_values":null}`)

	updated, err := setExtraValue(raw, "osac_instance_id", "test-uid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(updated, &obj); err != nil {
		t.Fatalf("failed to unmarshal updated JSON: %v", err)
	}

	ev := obj["extra_values"].(map[string]any)
	if ev["osac_instance_id"] != "test-uid-123" {
		t.Errorf("expected osac_instance_id=test-uid-123, got %v", ev["osac_instance_id"])
	}
}

func TestRemoveExtraValue(t *testing.T) {
	raw := json.RawMessage(`{"hostname":"node001","extra_values":{"resource_class":"h100","osac_instance_id":"uid-123"}}`)

	updated, err := removeExtraValue(raw, "osac_instance_id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(updated, &obj); err != nil {
		t.Fatalf("failed to unmarshal updated JSON: %v", err)
	}

	ev := obj["extra_values"].(map[string]any)
	if _, ok := ev["osac_instance_id"]; ok {
		t.Error("expected osac_instance_id to be removed")
	}
	if ev["resource_class"] != "h100" {
		t.Errorf("expected resource_class=h100 preserved, got %v", ev["resource_class"])
	}
}

func TestClassifyHTTPError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		target error
	}{
		{
			name:   "nil error",
			err:    nil,
			target: nil,
		},
		{
			name:   "tls keyword in error",
			err:    fmt.Errorf("tls: handshake failure"),
			target: ErrBCMTLSFailed,
		},
		{
			name:   "x509 keyword in error",
			err:    fmt.Errorf("x509: certificate signed by unknown authority"),
			target: ErrBCMTLSFailed,
		},
		{
			name:   "certificate keyword in error",
			err:    fmt.Errorf("remote error: certificate required"),
			target: ErrBCMTLSFailed,
		},
		{
			name:   "generic connection error",
			err:    fmt.Errorf("dial tcp 10.0.0.1:8081: connect: connection refused"),
			target: ErrBCMConnectionFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyHTTPError(tt.err)
			if tt.target == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}
			if !errors.Is(result, tt.target) {
				t.Errorf("expected %v, got %v", tt.target, result)
			}
		})
	}
}

func TestBCMValidationErrorFormat(t *testing.T) {
	err := &BCMValidationError{
		Validations: []bcmValidation{
			{ErrorCode: "NOT_NULL", Field: "partition", Message: "partition is required"},
			{ErrorCode: "BAD_VALUE", Field: "uuid", Message: "invalid UUID"},
		},
	}

	msg := err.Error()
	if !strings.Contains(msg, "partition") || !strings.Contains(msg, "NOT_NULL") {
		t.Errorf("error message should contain field details, got: %s", msg)
	}
	if !strings.Contains(msg, "BAD_VALUE") {
		t.Errorf("error message should contain all validations, got: %s", msg)
	}
}

func TestBCMBackendRegistration(t *testing.T) {
	if _, ok := newClientFuncs["bcm"]; !ok {
		t.Fatal("bcm backend not registered in newClientFuncs")
	}
}

func TestNewBCMClientRequiresBMHLifecycleManager(t *testing.T) {
	cfg := &Config{
		Type: "bcm",
		Options: map[string]any{
			"bcm": map[string]any{
				"url":      "https://bcm-head:8081",
				"certFile": "/certs/tls.crt",
				"keyFile":  "/certs/tls.key",
			},
		},
	}

	_, err := NewBCMClient(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error when BMHLifecycleManager is nil, got nil")
	}
	if !strings.Contains(err.Error(), "BCM inventory backend requires Metal3 management backend") {
		t.Errorf("expected Metal3 requirement error, got: %v", err)
	}
}

// assertJSONCall reads and validates the BCM JSON API request body.
func assertJSONCall(t *testing.T, r *http.Request, expectedService, expectedCall string) *bcmJSONRequest {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Errorf("expected POST, got %s", r.Method)
	}
	if r.URL.Path != bcmJSONPath {
		t.Errorf("expected path %s, got %s", bcmJSONPath, r.URL.Path)
	}

	var req struct {
		Service string          `json:"service"`
		Call    string          `json:"call"`
		Args    json.RawMessage `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("failed to decode request body: %v", err)
	}
	if req.Service != expectedService {
		t.Errorf("expected service %q, got %q", expectedService, req.Service)
	}
	if req.Call != expectedCall {
		t.Errorf("expected call %q, got %q", expectedCall, req.Call)
	}
	return &bcmJSONRequest{Service: req.Service, Call: req.Call, Args: req.Args}
}
