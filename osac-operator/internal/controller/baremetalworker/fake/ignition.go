/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package fake

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
)

// defaultIgnition is the small, valid-looking ignition body served before any override.
var defaultIgnition = []byte(`{"ignition":{"version":"3.2.0"}}`)

// IgnitionServer is a configurable HTTP endpoint that stands in for an InfraEnv's discovery
// ignition URL. It serves a settable body (SetContent) or a body of a settable size (SetSize,
// for the discovery-ignition size-warning path). Point the real IgnitionFetcher at URL().
type IgnitionServer struct {
	server  *httptest.Server
	mu      sync.Mutex
	content []byte
}

// NewIgnitionServer starts a fake ignition endpoint serving a small default body. Call Close
// when done.
func NewIgnitionServer() *IgnitionServer {
	s := &IgnitionServer{content: append([]byte(nil), defaultIgnition...)}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		body := s.content
		s.mu.Unlock()
		_, _ = w.Write(body)
	}))
	return s
}

// URL returns the endpoint's base URL (use as an InfraEnv discoveryIgnitionURL).
func (s *IgnitionServer) URL() string { return s.server.URL }

// SetContent sets the exact body the endpoint serves.
func (s *IgnitionServer) SetContent(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.content = append([]byte(nil), b...)
}

// SetSize makes the endpoint serve a body of exactly n bytes, for exercising the discovery
// ignition size-warning/limit paths. Note the real IgnitionFetcher caps reads at 1 MiB, so a
// body fetched through it will be truncated above that.
func (s *IgnitionServer) SetSize(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := []byte(`{"ignition":{"version":"3.2.0"},"padding":"`)
	suffix := []byte(`"}`)
	if n <= len(prefix)+len(suffix) {
		s.content = bytes.Repeat([]byte("a"), n)
		return
	}
	paddingLen := n - len(prefix) - len(suffix)
	s.content = append(prefix, append(bytes.Repeat([]byte("a"), paddingLen), suffix...)...)
}

// Close shuts down the endpoint.
func (s *IgnitionServer) Close() { s.server.Close() }
