/*
Copyright 2026.

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

package netboxclient

import (
	"net/http"
	"strings"
)

// NewClientForTest creates a Client with an injected HTTP client and API token.
func NewClientForTest(httpClient *http.Client, endpoint, token string) *Client {
	testClient := *httpClient
	transport := testClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	testClient.Transport = &requestExtensionTransport{base: transport}
	testClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Client{api: newAPIClient(strings.TrimRight(endpoint, "/"), token, &testClient)}
}
