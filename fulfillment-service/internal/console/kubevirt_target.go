/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package console

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

// Subresource names for the console.osac.openshift.io aggregated API.
const (
	subresourceConsole = "console"
	subresourceVNC     = "vnc"
)

// BuildKubeVirtTarget builds a console Target from a parsed REST config by
// constructing the WebSocket URI and extracting the bearer token. Called at
// session creation time to pre-compute the values that are embedded in the
// encrypted ticket.
func BuildKubeVirtTarget(config *rest.Config, namespace, crName, consoleType string) (*Target, error) {
	uri, err := buildConsoleURL(config, namespace, crName, consoleType)
	if err != nil {
		return nil, err
	}

	token, err := extractBearerToken(config)
	if err != nil {
		return nil, fmt.Errorf("failed to extract bearer token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("no bearer token found in kubeconfig")
	}

	return &Target{
		ResourceType: ResourceTypeComputeInstance,
		BackendURI:   uri,
		BackendToken: token,
	}, nil
}

// buildConsoleURL constructs the WebSocket URL for a console subresource
// from a REST config.
func buildConsoleURL(config *rest.Config, namespace, crName, consoleType string) (string, error) {
	host := config.Host
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	parsed, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("failed to parse host %q: %w", host, err)
	}

	scheme := "wss"
	if parsed.Scheme == "http" || parsed.Scheme == "ws" {
		scheme = "ws"
	}

	var subresource string
	switch consoleType {
	case ConsoleTypeSerial:
		subresource = subresourceConsole
	case ConsoleTypeVNC:
		subresource = subresourceVNC
	default:
		return "", fmt.Errorf("unsupported console type %q", consoleType)
	}

	consolePath := fmt.Sprintf(
		"/apis/console.osac.openshift.io/v1alpha1/namespaces/%s/computeinstances/%s/%s",
		url.PathEscape(namespace),
		url.PathEscape(crName),
		subresource,
	)

	return fmt.Sprintf("%s://%s%s", scheme, parsed.Host, consolePath), nil
}

// extractBearerToken extracts the bearer token from a rest.Config using the
// client-go transport wrapper chain. Supports bearer-token-based auth mechanisms
// (static token, token file, exec credential plugin).
func extractBearerToken(config *rest.Config) (string, error) {
	authHeaders, err := authHeadersFromConfig(config)
	if err != nil {
		return "", err
	}
	authValue := authHeaders.Get("Authorization")
	if strings.HasPrefix(authValue, "Bearer ") {
		return authValue[7:], nil
	}
	return "", nil
}

// authHeadersFromConfig extracts authentication headers from a rest.Config by
// building the same transport wrapper chain that client-go uses internally, then
// capturing the headers it sets on a dummy request.
func authHeadersFromConfig(config *rest.Config) (http.Header, error) {
	transportConfig, err := config.TransportConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get transport config: %w", err)
	}

	capture := &headerCaptureRoundTripper{}
	rt, err := transport.HTTPWrappersForConfig(transportConfig, capture)
	if err != nil {
		return nil, fmt.Errorf("failed to build auth wrappers: %w", err)
	}

	req, err := http.NewRequest(http.MethodGet, "https://placeholder", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// RoundTrip populates the request with auth headers, then our capture
	// round-tripper saves them and returns a sentinel error.
	resp, err := rt.RoundTrip(req)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil && !errors.Is(err, errHeaderCaptureOnly) {
		return nil, fmt.Errorf("failed to apply auth wrappers: %w", err)
	}
	return capture.headers, nil
}

var errHeaderCaptureOnly = errors.New("header capture only")

// headerCaptureRoundTripper is a fake http.RoundTripper that saves the request
// headers set by transport wrappers (auth, user-agent, impersonation) without
// making a network call.
type headerCaptureRoundTripper struct {
	headers http.Header
}

func (h *headerCaptureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	h.headers = req.Header.Clone()
	// Return an error rather than nil response — the RoundTripper contract
	// requires a valid *http.Response or an error, and a nil response would
	// panic any wrapper that tries to access it.
	return nil, errHeaderCaptureOnly
}
