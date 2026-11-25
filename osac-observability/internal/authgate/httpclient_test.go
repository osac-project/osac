// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package authgate

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeCAM(t *testing.T, server *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(path, certificate, 0o600); err != nil {
		t.Fatalf("write CA bundle: %v", err)
	}
	return path
}

func TestNewHTTPClientTrustsTheCAFileItIsGiven(t *testing.T) {
	// The identity provider is usually the Keycloak Route, which a stock
	// cluster signs with its own CA rather than a public one, so the gate has
	// to trust the distributed bundle.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// The test server's certificate is self-signed: the system pool rejects it,
	// which is what makes the CA file the reason the call below succeeds.
	if _, err := http.Get(server.URL); err == nil {
		t.Fatal("the test certificate is trusted by the system pool")
	}

	client, err := NewHTTPClient(writeCAM(t, server))
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get with the CA bundle: %v", err)
	}
	_ = response.Body.Close()
}

func TestNewHTTPClientUsesTheSystemPoolWithoutACAFile(t *testing.T) {
	client, err := NewHTTPClient("")
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	if client == nil {
		t.Fatal("NewHTTPClient returned no client")
	}
}

func TestNewHTTPClientRejectsAnUnreadableOrEmptyBundle(t *testing.T) {
	if _, err := NewHTTPClient(filepath.Join(t.TempDir(), "absent.pem")); err == nil {
		t.Error("NewHTTPClient accepted a missing CA bundle")
	}
	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewHTTPClient(empty); err == nil {
		t.Error("NewHTTPClient accepted a bundle with no certificate")
	}
}
