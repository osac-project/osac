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
	"context"
	"errors"
	"net/http"
	"strings"
)

var errUnexpectedRequestScope = errors.New("unexpected NetBox SDK request scope")

type requestOptionsContextKey struct{}

type requestOptions struct {
	customFieldQuery map[string]string
	ifMatch          string
}

func withRequestOptions(ctx context.Context, options requestOptions) context.Context {
	return context.WithValue(ctx, requestOptionsContextKey{}, options)
}

// requestExtensionTransport adds only the two request features absent from the
// generated SDK: dynamic custom-field list filters and conditional device PATCH.
type requestExtensionTransport struct {
	base http.RoundTripper
}

func (t *requestExtensionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	options, ok := request.Context().Value(requestOptionsContextKey{}).(requestOptions)
	if !ok || (len(options.customFieldQuery) == 0 && options.ifMatch == "") {
		return t.base.RoundTrip(request)
	}

	extended := request.Clone(request.Context())
	if extended.Header == nil {
		extended.Header = make(http.Header)
	}
	if options.ifMatch != "" {
		if extended.Method != http.MethodPatch || !isDeviceDetailPath(extended.URL.Path) {
			return nil, errUnexpectedRequestScope
		}
		extended.Header.Set("If-Match", options.ifMatch)
	}

	if len(options.customFieldQuery) > 0 {
		if extended.Method != http.MethodGet || !isDeviceListPath(extended.URL.Path) {
			return nil, errUnexpectedRequestScope
		}
		query := extended.URL.Query()
		for key, value := range options.customFieldQuery {
			query.Set(key, value)
		}
		extended.URL.RawQuery = query.Encode()
	}

	return t.base.RoundTrip(extended)
}

func isDeviceListPath(path string) bool {
	return strings.TrimSuffix(path, "/") == "/api/dcim/devices"
}

func isDeviceDetailPath(path string) bool {
	const prefix = "/api/dcim/devices/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}

	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/")
	if id == "" || strings.Contains(id, "/") {
		return false
	}
	for _, character := range id {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
