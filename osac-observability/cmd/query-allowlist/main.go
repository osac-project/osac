// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

// Command query-allowlist sits between the admin OAuth proxies and Thanos
// Query. It forwards the five documented read-only endpoints unchanged and
// answers every other path with 501 in Prometheus's error format, so an admin
// login never reaches an unimplemented or administrative API.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/osac-project/osac/osac-observability/internal/allowlist"
)

const (
	defaultListenAddr    = ":9091"
	defaultUpstreamURL   = "http://127.0.0.1:4180"
	defaultAllowlistFile = "/etc/query-allowlist/endpoints.txt"
)

type config struct {
	ListenAddr    string
	UpstreamURL   string
	AllowlistFile string
}

type errorBody struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	upstream, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "UPSTREAM_URL: %v\n", err)
		os.Exit(1)
	}
	matcher, err := allowlist.Load(cfg.AllowlistFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           newHandler(upstream, matcher, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}
	logger.Info("serving the admin query allowlist", "listen", cfg.ListenAddr, "upstream", cfg.UpstreamURL)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "listen on %s: %v\n", cfg.ListenAddr, err)
		os.Exit(1)
	}
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		ListenAddr:    valueOr(getenv, "LISTEN_ADDR", defaultListenAddr),
		UpstreamURL:   valueOr(getenv, "UPSTREAM_URL", defaultUpstreamURL),
		AllowlistFile: valueOr(getenv, "ALLOWLIST_FILE", defaultAllowlistFile),
	}
	upstream, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return config{}, fmt.Errorf("UPSTREAM_URL: %w", err)
	}
	if upstream.Scheme != "http" && upstream.Scheme != "https" {
		return config{}, fmt.Errorf("UPSTREAM_URL must use http or https, got %q", cfg.UpstreamURL)
	}
	if upstream.Host == "" {
		return config{}, fmt.Errorf("UPSTREAM_URL must include a host, got %q", cfg.UpstreamURL)
	}
	return cfg, nil
}

func valueOr(getenv func(string) string, name, fallback string) string {
	if value := getenv(name); value != "" {
		return value
	}
	return fallback
}

// newHandler returns the allowlist in front of a reverse proxy to upstream.
func newHandler(upstream *url.URL, matcher *allowlist.Matcher, logger *slog.Logger) http.Handler {
	proxy := &httputil.ReverseProxy{
		Director: func(request *http.Request) {
			// Only the destination changes: the client's path, query string
			// and body reach Thanos exactly as the admin sent them.
			request.URL.Scheme = upstream.Scheme
			request.URL.Host = upstream.Host
			request.Host = upstream.Host
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !matcher.Allowed(r.Method, r.URL.Path) {
			// The path only: the query string can carry tenant data.
			logger.Info("rejecting an unsupported endpoint", "path", r.URL.Path)
			writeNotImplemented(w, r.URL.Path)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// writeNotImplemented answers in the shape Prometheus clients already parse.
func writeNotImplemented(w http.ResponseWriter, path string) {
	payload, err := json.Marshal(errorBody{
		Status:    "error",
		ErrorType: "not_implemented",
		Error:     fmt.Sprintf("the endpoint %s is not supported by this proxy", path),
	})
	if err != nil {
		http.Error(w, "not implemented", http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write(payload); err != nil {
		return
	}
}
