// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package writeproxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type capturedRequest struct {
	body   []byte
	header http.Header
}

type fakeUpstream struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []capturedRequest
	status   int
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	upstream := &fakeUpstream{status: http.StatusNoContent}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstream.mu.Lock()
		upstream.requests = append(upstream.requests, capturedRequest{body: body, header: r.Header.Clone()})
		status := upstream.status
		upstream.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *fakeUpstream) url(t *testing.T) *url.URL {
	t.Helper()
	parsed, err := url.Parse(u.server.URL + "/api/v1/receive")
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}
	return parsed
}

func (u *fakeUpstream) captured() []capturedRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]capturedRequest(nil), u.requests...)
}

type testHandler struct {
	handler  http.Handler
	store    *RevocationStore
	upstream *fakeUpstream
}

func newTestHandler(t *testing.T) *testHandler {
	t.Helper()
	ca := newTestCA(t)
	upstream := newFakeUpstream(t)
	store := syncedStore()
	handler, err := NewHandler(Config{
		Upstream:   upstream.url(t),
		Roots:      ca.pool(),
		Revocation: store,
		Rule:       DefaultDropRule(),
		Now:        func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return &testHandler{handler: handler, store: store, upstream: upstream}
}

func (h *testHandler) do(t *testing.T, certificate *x509.Certificate, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/receive", strings.NewReader(string(body)))
	request.Header.Set("Content-Encoding", "snappy")
	request.Header.Set("Content-Type", "application/x-protobuf")
	if certificate != nil {
		request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}}
	}
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	return recorder
}

func validBody(t *testing.T) []byte {
	t.Helper()
	raw := encodeRequest([][]byte{
		encodeTimeSeries(labelsOf("__name__", "kube_node_info", "cluster", "forged"), encodeSample(1, 1)),
		encodeTimeSeries(labelsOf("__name__", "kube_pod_info"), encodeSample(1, 1)),
	}, nil)
	return mustEncode(t, raw)
}

func TestHandlerForwardsTransformedPayload(t *testing.T) {
	h := newTestHandler(t)
	ca := newTestCA(t)
	leaf := ca.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))

	// The handler was built with a different CA; rebuild it with this one so the leaf is trusted.
	handler, err := NewHandler(Config{
		Upstream:   h.upstream.url(t),
		Roots:      ca.pool(),
		Revocation: h.store,
		Rule:       DefaultDropRule(),
		Now:        func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/receive", strings.NewReader(string(validBody(t))))
	request.Header.Set("Content-Encoding", "snappy")
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	requests := h.upstream.captured()
	if len(requests) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(requests))
	}
	if got := requests[0].header.Get("Content-Encoding"); got != "snappy" {
		t.Errorf("upstream Content-Encoding = %q", got)
	}
	series, _, _ := decodeRequest(t, requests[0].body)
	if len(series) != 1 {
		t.Fatalf("upstream series = %d, want only the allowed one", len(series))
	}
	if series[0]["cluster"] != "cluster-a" || series[0]["managed_by"] != "osac" {
		t.Errorf("identity labels = %v", series[0])
	}
}

// The write CA can be rotated under a running pod. The handler must verify each
// request against the pool the source currently reports, not the startup value.
func TestHandlerVerifiesWithTheSuppliedRoots(t *testing.T) {
	startupCA := newTestCA(t)
	rotatedCA := newTestCA(t)
	upstream := newFakeUpstream(t)
	handler, err := NewHandler(Config{
		Upstream:    upstream.url(t),
		Roots:       startupCA.pool(),
		RootsSource: func() *x509.CertPool { return rotatedCA.pool() },
		Revocation:  syncedStore(),
		Rule:        DefaultDropRule(),
		Now:         func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	status := func(certificate *x509.Certificate) int {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/receive", strings.NewReader(string(validBody(t))))
		request.Header.Set("Content-Encoding", "snappy")
		request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}

	rotatedLeaf := rotatedCA.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	if got := status(rotatedLeaf); got != http.StatusOK {
		t.Fatalf("status for a write signed by the rotated CA = %d, want 200", got)
	}
	startupLeaf := startupCA.issue(t, "cluster-b", 5678, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	if got := status(startupLeaf); got != http.StatusUnauthorized {
		t.Fatalf("status for a write signed only by the previous CA = %d, want 401", got)
	}
}

func TestHandlerRejectsBeforeForwarding(t *testing.T) {
	ca := newTestCA(t)
	otherCA := newTestCA(t)
	expired := ca.issue(t, "cluster-a", 11, testNow.Add(-2*time.Hour), testNow.Add(-time.Hour))
	untrusted := otherCA.issue(t, "cluster-a", 12, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	revoked := ca.issue(t, "cluster-a", 4321, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	management := ca.issue(t, ManagementCluster, 13, testNow.Add(-time.Minute), testNow.Add(time.Hour))

	cases := []struct {
		name        string
		prepare     func(*testHandler)
		certificate *x509.Certificate
		wantBody    string
	}{
		{name: "no certificate", certificate: nil, wantBody: ErrNoCertificate.Error()},
		{name: "expired", certificate: expired, wantBody: ErrExpired.Error()},
		{name: "untrusted", certificate: untrusted, wantBody: ErrUntrustedChain.Error()},
		{name: "revoked", prepare: func(h *testHandler) {
			h.store.Apply(map[string]string{RevocationKey: "10e1"})
		}, certificate: revoked, wantBody: ErrRevoked.Error()},
		{name: "management", certificate: management, wantBody: ErrManagementIdentity.Error()},
		{name: "unsynced", prepare: func(h *testHandler) {
			h.store = NewRevocationStore()
		}, certificate: nil, wantBody: ErrRevocationUnsynced.Error()},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newTestHandler(t)
			// Rebuild with the CA these certificates were issued by.
			handler, err := NewHandler(Config{
				Upstream:   h.upstream.url(t),
				Roots:      ca.pool(),
				Revocation: h.store,
				Rule:       DefaultDropRule(),
				Now:        func() time.Time { return testNow },
			})
			if err != nil {
				t.Fatalf("NewHandler: %v", err)
			}
			h.handler = handler
			if testCase.prepare != nil {
				testCase.prepare(h)
				handler, err = NewHandler(Config{
					Upstream:   h.upstream.url(t),
					Roots:      ca.pool(),
					Revocation: h.store,
					Rule:       DefaultDropRule(),
					Now:        func() time.Time { return testNow },
				})
				if err != nil {
					t.Fatalf("NewHandler: %v", err)
				}
				h.handler = handler
			}
			recorder := h.do(t, testCase.certificate, validBody(t))
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", recorder.Code)
			}
			if !strings.Contains(recorder.Body.String(), testCase.wantBody) {
				t.Errorf("body = %q, want it to name %q", recorder.Body.String(), testCase.wantBody)
			}
			if got := len(h.upstream.captured()); got != 0 {
				t.Errorf("upstream requests = %d; a rejected write must be discarded", got)
			}
		})
	}
}

func TestHandlerClosesGateWhenTheWatchFails(t *testing.T) {
	ca := newTestCA(t)
	upstream := newFakeUpstream(t)
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: ""})
	store.Fail()
	handler, err := NewHandler(Config{
		Upstream:   upstream.url(t),
		Roots:      ca.pool(),
		Revocation: store,
		Rule:       DefaultDropRule(),
		Now:        func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	leaf := ca.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/receive", strings.NewReader(string(validBody(t))))
	request.Header.Set("Content-Encoding", "snappy")
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if got := len(upstream.captured()); got != 0 {
		t.Errorf("upstream requests = %d", got)
	}
}

func TestHandlerAcceptsBatchWhereEverythingIsDropped(t *testing.T) {
	ca := newTestCA(t)
	upstream := newFakeUpstream(t)
	handler, err := NewHandler(Config{
		Upstream:   upstream.url(t),
		Roots:      ca.pool(),
		Revocation: syncedStore(),
		Rule:       DefaultDropRule(),
		Now:        func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	leaf := ca.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	raw := encodeRequest([][]byte{
		encodeTimeSeries(labelsOf("__name__", "kube_pod_info"), encodeSample(1, 1)),
	}, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/receive", strings.NewReader(string(mustEncode(t, raw))))
	request.Header.Set("Content-Encoding", "snappy")
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := len(upstream.captured()); got != 0 {
		t.Errorf("upstream requests = %d; nothing survived, so nothing to forward", got)
	}
}

func TestHandlerRejectsMalformedPayload(t *testing.T) {
	ca := newTestCA(t)
	upstream := newFakeUpstream(t)
	handler, err := NewHandler(Config{
		Upstream:   upstream.url(t),
		Roots:      ca.pool(),
		Revocation: syncedStore(),
		Rule:       DefaultDropRule(),
		Now:        func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	leaf := ca.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/receive", strings.NewReader("not snappy"))
	request.Header.Set("Content-Encoding", "snappy")
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestHandlerRejectsUnsupportedEncoding(t *testing.T) {
	ca := newTestCA(t)
	upstream := newFakeUpstream(t)
	handler, err := NewHandler(Config{
		Upstream:   upstream.url(t),
		Roots:      ca.pool(),
		Revocation: syncedStore(),
		Rule:       DefaultDropRule(),
		Now:        func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	leaf := ca.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/receive", strings.NewReader("{}"))
	request.Header.Set("Content-Encoding", "gzip")
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", recorder.Code)
	}
}

func TestHandlerRejectsOtherMethods(t *testing.T) {
	h := newTestHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/receive", nil)
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
}

func TestHandlerReportsUpstreamFailure(t *testing.T) {
	ca := newTestCA(t)
	upstream := newFakeUpstream(t)
	upstream.status = http.StatusInternalServerError
	handler, err := NewHandler(Config{
		Upstream:   upstream.url(t),
		Roots:      ca.pool(),
		Revocation: syncedStore(),
		Rule:       DefaultDropRule(),
		Now:        func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	leaf := ca.issue(t, "cluster-a", 1234, testNow.Add(-time.Minute), testNow.Add(time.Hour))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/receive", strings.NewReader(string(validBody(t))))
	request.Header.Set("Content-Encoding", "snappy")
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", recorder.Code)
	}
}

func TestProbeHandlerReadinessFollowsTheRevocationList(t *testing.T) {
	store := NewRevocationStore()
	handler := NewProbeHandler(store)

	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz before sync = %d, want 503", ready.Code)
	}
	live := httptest.NewRecorder()
	handler.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if live.Code != http.StatusOK {
		t.Errorf("livez = %d, want 200", live.Code)
	}

	store.Apply(map[string]string{RevocationKey: ""})
	ready = httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Errorf("readyz after sync = %d, want 200", ready.Code)
	}

	write := httptest.NewRecorder()
	handler.ServeHTTP(write, httptest.NewRequest(http.MethodPost, ReceivePath, nil))
	if write.Code != http.StatusNotFound {
		t.Errorf("probe port write = %d, want 404; the probe listener must not serve writes", write.Code)
	}
}

func TestNewHandlerRejectsMissingConfiguration(t *testing.T) {
	if _, err := NewHandler(Config{}); err == nil {
		t.Fatal("expected an error without an upstream")
	}
}
