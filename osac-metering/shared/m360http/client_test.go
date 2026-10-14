/*
Copyright (c) 2026 Red Hat Inc.

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

package m360http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientBuildsVersionedAuthenticatedRequest(t *testing.T) {
	var (
		gotPath string
		gotAuth string
		gotType string
		gotBody string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL + "/tenant/root/", APIVersion: "v2", APIKey: "secret", AllowInsecureHTTP: true})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	endpoint := client.Endpoint("external", "accounts", "account/one")
	request, err := client.NewRequest(context.Background(), http.MethodPut, endpoint, strings.NewReader(`{"externalId":"tenant"}`), "application/json")
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_ = response.Body.Close()

	if gotPath != "/tenant/root/api/v2/external/accounts/account%2Fone" {
		t.Fatalf("request path = %q, want escaped versioned endpoint", gotPath)
	}
	if gotAuth != "Bearer secret" || gotType != "application/json" || gotBody != `{"externalId":"tenant"}` {
		t.Fatalf("request headers/body = %q, %q, %q", gotAuth, gotType, gotBody)
	}
}

func TestClientUsesSharedDefaultTimeout(t *testing.T) {
	client, err := NewClient(Config{BaseURL: "https://m360.example.test", APIVersion: "v1", APIKey: "secret"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if got := client.httpClient.Timeout; got != DefaultTimeout {
		t.Fatalf("HTTP timeout = %s, want %s", got, DefaultTimeout)
	}
}

func TestClientHealthCheckUsesBaseURLAndAcceptsAnyHTTPStatus(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, APIVersion: "v1", APIKey: "secret", AllowInsecureHTTP: true})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck() error = %v, want HTTP response to count as reachable", err)
	}
	if gotMethod != http.MethodHead || gotPath != "/" {
		t.Fatalf("health check request = %s %s, want HEAD /", gotMethod, gotPath)
	}
}

func TestNewClientRejectsIncompleteConfiguration(t *testing.T) {
	for name, config := range map[string]Config{
		"relative URL":    {BaseURL: "m360", APIVersion: "v1", APIKey: "secret"},
		"plain HTTP URL":  {BaseURL: "http://m360.example.test", APIVersion: "v1", APIKey: "secret"},
		"invalid version": {BaseURL: "https://m360.example.test", APIVersion: "v1/path", APIKey: "secret"},
		"missing key":     {BaseURL: "https://m360.example.test", APIVersion: "v1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewClient(config); err == nil {
				t.Fatal("NewClient() succeeded with invalid configuration")
			}
		})
	}
}

func TestNewClientAllowsHTTPOnlyWhenExplicitlyEnabled(t *testing.T) {
	config := Config{BaseURL: "http://m360.example.test", APIVersion: "v1", APIKey: "secret"}
	if _, err := NewClient(config); err == nil {
		t.Fatal("NewClient() accepted plain HTTP without the explicit opt-in")
	}

	config.AllowInsecureHTTP = true
	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient() with AllowInsecureHTTP error = %v", err)
	}
	if got := client.baseURL.Scheme; got != "http" {
		t.Fatalf("base URL scheme = %q, want http for explicit test opt-in", got)
	}
}

func TestClientHealthCheckHonorsContextDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, APIVersion: "v1", APIKey: "secret", AllowInsecureHTTP: true})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := client.HealthCheck(ctx); err == nil {
		t.Fatal("HealthCheck() succeeded after the context deadline")
	}
}
