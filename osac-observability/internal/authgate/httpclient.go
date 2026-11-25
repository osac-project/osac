// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package authgate

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"
)

const providerTimeout = 10 * time.Second

// NewHTTPClient returns the client used to reach the identity provider. caFile
// names roots to trust beside the system pool; an empty value uses the system
// pool alone. The provider is normally the Keycloak Route, whose certificate a
// stock cluster signs with a cluster CA instead of a public one.
func NewHTTPClient(caFile string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caFile == "" {
		return &http.Client{Timeout: providerTimeout, Transport: transport}, nil
	}
	// #nosec G304 -- the path comes from the pod spec, not from a request.
	contents, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA bundle: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(contents) {
		return nil, fmt.Errorf("CA bundle %s holds no certificate", caFile)
	}
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Timeout: providerTimeout, Transport: transport}, nil
}
