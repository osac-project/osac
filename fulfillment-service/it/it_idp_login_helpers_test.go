/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package it

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForOIDCIssuerRetriesTransientResponses(t *testing.T) {
	var requests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "route not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": server.URL + "/realms/master"})
	}))
	defer server.Close()

	issuer := server.URL + "/realms/master"
	endpoint := issuer + "/.well-known/openid-configuration"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := waitForOIDCIssuer(ctx, server.Client(), endpoint, issuer, time.Millisecond); err != nil {
		t.Fatalf("waitForOIDCIssuer() error = %v", err)
	}
	if got := requests.Load(); got < 2 {
		t.Fatalf("discovery requests = %d, want at least 2", got)
	}
}
