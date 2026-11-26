// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/osac-project/osac/osac-observability/internal/allowlist"
)

// recorded is one request as the upstream saw it.
type recorded struct {
	method string
	path   string
	query  string
	header http.Header
	body   string
}

func newUpstream(t *testing.T, calls *[]recorded) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		*calls = append(*calls, recorded{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			header: r.Header.Clone(),
			body:   string(body),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func newTestHandler(t *testing.T, upstream *httptest.Server, logger *slog.Logger) http.Handler {
	t.Helper()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	return newHandler(target, allowlist.Default(), logger)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHandlerRejectsAnUnsupportedPath(t *testing.T) {
	var calls []recorded
	upstream := newUpstream(t, &calls)
	handler := newTestHandler(t, upstream, discardLogger())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil))

	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotImplemented)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	want := `{"status":"error","errorType":"not_implemented",` +
		`"error":"the endpoint /api/v1/targets is not supported by this proxy"}`
	if got := recorder.Body.String(); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if len(calls) != 0 {
		t.Errorf("the denied request reached the upstream %d times", len(calls))
	}
}

func TestHandlerRejectsANonGetPostMethodOnAnAllowedPath(t *testing.T) {
	var calls []recorded
	upstream := newUpstream(t, &calls)
	handler := newTestHandler(t, upstream, discardLogger())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/query", nil))

	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotImplemented)
	}
	want := `{"status":"error","errorType":"not_implemented",` +
		`"error":"the endpoint /api/v1/query is not supported by this proxy"}`
	if got := recorder.Body.String(); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if len(calls) != 0 {
		t.Errorf("the denied request reached the upstream %d times", len(calls))
	}
}

func TestHandlerForwardsAnAllowedQueryUnchanged(t *testing.T) {
	var calls []recorded
	upstream := newUpstream(t, &calls)
	handler := newTestHandler(t, upstream, discardLogger())

	request := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=up&time=1700000000", nil)
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Body.String(); got != `{"status":"success","data":{}}` {
		t.Errorf("body = %s, want the upstream response", got)
	}
	if len(calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(calls))
	}
	got := calls[0]
	if got.method != http.MethodGet || got.path != "/api/v1/query" {
		t.Errorf("upstream saw %s %s, want GET /api/v1/query", got.method, got.path)
	}
	if got.query != "query=up&time=1700000000" {
		t.Errorf("query = %q, want the client query unchanged", got.query)
	}
	if got.header.Get("Authorization") != "Bearer token" {
		t.Errorf("Authorization header was not preserved")
	}
	// Nothing may narrow or widen the query on the client's behalf: a filter
	// here would silently change what the admin sees.
	values, err := url.ParseQuery(got.query)
	if err != nil {
		t.Fatalf("parse forwarded query: %v", err)
	}
	for _, key := range []string{"match[]", "match", "start", "end"} {
		if _, ok := values[key]; ok {
			t.Errorf("the proxy added a %q parameter", key)
		}
	}
	if got.body != "" {
		t.Errorf("body = %q, want empty", got.body)
	}
}

func TestHandlerForwardsAPostBodyUnchanged(t *testing.T) {
	var calls []recorded
	upstream := newUpstream(t, &calls)
	handler := newTestHandler(t, upstream, discardLogger())

	const body = "query=sum(rate(http_requests_total[5m]))&time=1700000000"
	request := httptest.NewRequest(http.MethodPost, "/api/v1/query", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if len(calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(calls))
	}
	if calls[0].method != http.MethodPost || calls[0].body != body {
		t.Errorf("upstream saw %s with body %q, want POST with the client body", calls[0].method, calls[0].body)
	}
	if got := calls[0].header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want it preserved", got)
	}
}

func TestHandlerForwardsLabelValues(t *testing.T) {
	var calls []recorded
	upstream := newUpstream(t, &calls)
	handler := newTestHandler(t, upstream, discardLogger())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/label/__name__/values", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if len(calls) != 1 || calls[0].path != "/api/v1/label/__name__/values" {
		t.Errorf("upstream calls = %+v, want the label values path", calls)
	}
}

func TestHandlerLogsADenialWithoutTheQueryString(t *testing.T) {
	var calls []recorded
	upstream := newUpstream(t, &calls)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := newTestHandler(t, upstream, logger)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/targets?match[]=tenant_secret", nil)
	handler.ServeHTTP(recorder, request)

	output := logs.String()
	if !strings.Contains(output, "/api/v1/targets") {
		t.Errorf("the denial log should name the path: %s", output)
	}
	if strings.Contains(output, "tenant_secret") {
		t.Errorf("the denial log leaked the query string: %s", output)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := loadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.ListenAddr != ":9091" {
		t.Errorf("ListenAddr = %q, want :9091", cfg.ListenAddr)
	}
	if cfg.UpstreamURL != "http://127.0.0.1:4180" {
		t.Errorf("UpstreamURL = %q, want http://127.0.0.1:4180", cfg.UpstreamURL)
	}
	if cfg.AllowlistFile != "/etc/query-allowlist/endpoints.txt" {
		t.Errorf("AllowlistFile = %q, want /etc/query-allowlist/endpoints.txt", cfg.AllowlistFile)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	env := map[string]string{
		"LISTEN_ADDR":    "127.0.0.1:9191",
		"UPSTREAM_URL":   "http://127.0.0.1:4181",
		"ALLOWLIST_FILE": "/tmp/endpoints.txt",
	}
	cfg, err := loadConfig(func(name string) string { return env[name] })
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.ListenAddr != env["LISTEN_ADDR"] {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, env["LISTEN_ADDR"])
	}
	if cfg.UpstreamURL != env["UPSTREAM_URL"] {
		t.Errorf("UpstreamURL = %q, want %q", cfg.UpstreamURL, env["UPSTREAM_URL"])
	}
	if cfg.AllowlistFile != env["ALLOWLIST_FILE"] {
		t.Errorf("AllowlistFile = %q, want %q", cfg.AllowlistFile, env["ALLOWLIST_FILE"])
	}
}

func TestLoadConfigRejectsABadUpstream(t *testing.T) {
	for _, value := range []string{
		"not a url",
		"file:///etc/passwd",
		"ftp://127.0.0.1:4180",
		"http://",
	} {
		_, err := loadConfig(func(name string) string {
			if name == "UPSTREAM_URL" {
				return value
			}
			return ""
		})
		if err == nil {
			t.Errorf("loadConfig accepted UPSTREAM_URL=%q", value)
		}
	}
}
