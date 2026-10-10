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
	"net/url"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestRequestExtensionTransportInitializesNilHeaderForIfMatch(t *testing.T) {
	var forwarded *http.Request
	transport := &requestExtensionTransport{base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		forwarded = request
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    request,
		}, nil
	})}

	request := (&http.Request{
		Method: http.MethodPatch,
		URL:    &url.URL{Scheme: "https", Host: "netbox.example.test", Path: "/api/dcim/devices/42/"},
	}).WithContext(withRequestOptions(context.Background(), requestOptions{ifMatch: "etag-42"}))

	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	if response == nil {
		t.Fatal("RoundTrip() response = nil, want response from base transport")
	}
	if forwarded == nil {
		t.Fatal("base transport was not called")
	}
	if forwarded == request {
		t.Fatal("base transport received the original request, want a cloned request")
	}
	if request.Header != nil {
		t.Fatalf("original request Header = %#v, want nil", request.Header)
	}
	if got := forwarded.Header.Get("If-Match"); got != "etag-42" {
		t.Fatalf("forwarded If-Match = %q, want etag-42", got)
	}
}

func TestRequestExtensionTransportRejectsOutOfScopeOptions(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		options requestOptions
	}{
		{
			name:    "If-Match with wrong method",
			method:  http.MethodGet,
			path:    "/api/dcim/devices/42/",
			options: requestOptions{ifMatch: "etag-42"},
		},
		{
			name:    "If-Match with wrong path",
			method:  http.MethodPatch,
			path:    "/api/dcim/sites/42/",
			options: requestOptions{ifMatch: "etag-42"},
		},
		{
			name:   "custom filter with wrong method",
			method: http.MethodPatch,
			path:   "/api/dcim/devices/",
			options: requestOptions{
				customFieldQuery: map[string]string{"cf_owner": "instance-42"},
			},
		},
		{
			name:   "custom filter with wrong path",
			method: http.MethodGet,
			path:   "/api/dcim/devices/42/",
			options: requestOptions{
				customFieldQuery: map[string]string{"cf_owner": "instance-42"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseCalls := 0
			transport := &requestExtensionTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
				baseCalls++
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
			})}
			request := (&http.Request{
				Method: tt.method,
				URL:    &url.URL{Scheme: "https", Host: "netbox.example.test", Path: tt.path},
			}).WithContext(withRequestOptions(context.Background(), tt.options))

			response, err := transport.RoundTrip(request)
			if !errors.Is(err, errUnexpectedRequestScope) {
				t.Fatalf("RoundTrip() error = %v, want %v", err, errUnexpectedRequestScope)
			}
			if response != nil {
				t.Fatalf("RoundTrip() response = %#v, want nil on rejected request", response)
			}
			if baseCalls != 0 {
				t.Fatalf("base transport calls = %d, want 0 for rejected request", baseCalls)
			}
		})
	}
}

func TestIsDeviceDetailPathRejectsInvalidPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "wrong prefix", path: "/api/dcim/sites/42/"},
		{name: "empty ID", path: "/api/dcim/devices/"},
		{name: "nested path", path: "/api/dcim/devices/42/interfaces/1/"},
		{name: "nonnumeric ID", path: "/api/dcim/devices/host-42/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if isDeviceDetailPath(tt.path) {
				t.Fatalf("isDeviceDetailPath(%q) = true, want false", tt.path)
			}
		})
	}
}
