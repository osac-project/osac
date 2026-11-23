// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package writeproxy

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// ReceivePath is the only path the proxy serves.
	ReceivePath = "/api/v1/receive"
	// maxBodyBytes caps a single remote-write request.
	maxBodyBytes = 32 << 20
)

// Config configures a write-proxy handler.
type Config struct {
	// Upstream is Thanos Receive's remote-write URL.
	Upstream *url.URL
	// Roots verifies client certificates; it holds the write CA.
	Roots *x509.CertPool
	// Revocation is the shared denylist.
	Revocation RevocationList
	// Rule drops metric families the store must never hold.
	Rule DropRule
	// Client sends forwarded requests. Optional.
	Client *http.Client
	// Logger receives rejections. Optional.
	Logger *slog.Logger
	// Now is the clock used for certificate validity. Optional.
	Now func() time.Time
}

// NewHandler returns the HTTP handler for the managed remote-write endpoint.
func NewHandler(cfg Config) (http.Handler, error) {
	if cfg.Upstream == nil {
		return nil, errors.New("write proxy: upstream URL is required")
	}
	if cfg.Roots == nil {
		return nil, errors.New("write proxy: write CA pool is required")
	}
	if cfg.Revocation == nil {
		return nil, errors.New("write proxy: revocation list is required")
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: time.Minute}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	server := &server{cfg: cfg}
	mux := http.NewServeMux()
	mux.Handle("POST "+ReceivePath, server)
	return mux, nil
}

// NewProbeHandler serves only the health endpoints. It is meant for a plain
// listener because the kubelet has no client certificate for the mTLS port.
// Readiness follows the denylist, so a replica that has not observed the
// revocation ConfigMap is taken out of the Route instead of silently rejecting
// every write.
func NewProbeHandler(revocation RevocationList) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if revocation == nil || !revocation.Synced() {
			http.Error(w, "revocation list unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

type server struct {
	cfg Config
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var certs []*x509.Certificate
	if r.TLS != nil {
		certs = r.TLS.PeerCertificates
	}
	identity, err := VerifyPeer(certs, s.cfg.Roots, s.cfg.Revocation, s.cfg.Now())
	if err != nil {
		// Every identity rejection is a 401 and the samples are discarded.
		s.cfg.Logger.Warn("rejected managed remote write", "remote_addr", r.RemoteAddr, "reason", err)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	encoding := strings.TrimSpace(strings.ToLower(r.Header.Get("Content-Encoding")))
	if encoding != "snappy" {
		http.Error(w, "unsupported Content-Encoding: remote write requires snappy", http.StatusUnsupportedMediaType)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "remote-write request is too large", http.StatusRequestEntityTooLarge)
			return
		}
		s.cfg.Logger.Warn("reading managed remote write", "cluster", identity.Cluster, "err", err)
		http.Error(w, "could not read request body", http.StatusBadRequest)
		return
	}

	transformed, dropAll, err := Transform(body, identity, s.cfg.Rule)
	if err != nil {
		s.cfg.Logger.Warn("rejecting malformed managed remote write", "cluster", identity.Cluster, "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if dropAll {
		// Nothing survived the drop rule. The client's write was accepted; there is nothing
		// to forward.
		s.cfg.Logger.Debug("dropped an entire managed remote-write batch", "cluster", identity.Cluster)
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := s.forward(r.Context(), transformed); err != nil {
		var statusError *upstreamStatusError
		if errors.As(err, &statusError) {
			s.cfg.Logger.Warn("upstream rejected managed remote write", "cluster", identity.Cluster, "status", statusError.status)
			http.Error(w, fmt.Sprintf("upstream rejected the write with status %d", statusError.status), http.StatusBadGateway)
			return
		}
		s.cfg.Logger.Warn("forwarding managed remote write", "cluster", identity.Cluster, "err", err)
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *server) forward(ctx context.Context, body []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Upstream.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building upstream request: %w", err)
	}
	request.Header.Set("Content-Encoding", "snappy")
	request.Header.Set("Content-Type", "application/x-protobuf")
	request.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	response, err := s.cfg.Client.Do(request)
	if err != nil {
		return fmt.Errorf("calling upstream: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &upstreamStatusError{status: response.StatusCode}
	}
	return nil
}

type upstreamStatusError struct {
	status int
}

func (e *upstreamStatusError) Error() string {
	return fmt.Sprintf("upstream returned status %d", e.status)
}
