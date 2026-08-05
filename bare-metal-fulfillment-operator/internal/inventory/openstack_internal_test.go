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

package inventory

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "generic error",
			err:  fmt.Errorf("some random error"),
			want: false,
		},
		{
			name: "401 ErrUnexpectedResponseCode",
			err: gophercloud.ErrUnexpectedResponseCode{
				Actual:   http.StatusUnauthorized,
				Expected: []int{http.StatusOK},
			},
			want: true,
		},
		{
			name: "404 ErrUnexpectedResponseCode",
			err: gophercloud.ErrUnexpectedResponseCode{
				Actual:   http.StatusNotFound,
				Expected: []int{http.StatusOK},
			},
			want: false,
		},
		{
			name: "500 ErrUnexpectedResponseCode",
			err: gophercloud.ErrUnexpectedResponseCode{
				Actual:   http.StatusInternalServerError,
				Expected: []int{http.StatusOK},
			},
			want: false,
		},
		{
			name: "ErrUnableToReauthenticate pointer",
			err: &gophercloud.ErrUnableToReauthenticate{
				ErrOriginal: fmt.Errorf("original"),
				ErrReauth:   fmt.Errorf("reauth failed"),
			},
			want: true,
		},
		{
			name: "ErrErrorAfterReauthentication pointer",
			err: &gophercloud.ErrErrorAfterReauthentication{
				ErrOriginal: fmt.Errorf("still failing"),
			},
			want: true,
		},
		{
			name: "wrapped ErrUnableToReauthenticate",
			err: fmt.Errorf("operation failed: %w", &gophercloud.ErrUnableToReauthenticate{
				ErrOriginal: fmt.Errorf("original"),
				ErrReauth:   fmt.Errorf("reauth failed"),
			}),
			want: true,
		},
		{
			name: "wrapped ErrErrorAfterReauthentication",
			err: fmt.Errorf("operation failed: %w", &gophercloud.ErrErrorAfterReauthentication{
				ErrOriginal: fmt.Errorf("still failing"),
			}),
			want: true,
		},
		{
			name: "wrapped 401 error",
			err: fmt.Errorf("operation failed: %w", gophercloud.ErrUnexpectedResponseCode{
				Actual:   http.StatusUnauthorized,
				Expected: []int{http.StatusOK},
			}),
			want: true,
		},
		{
			name: "wrapped non-auth error",
			err: fmt.Errorf("operation failed: %w", gophercloud.ErrUnexpectedResponseCode{
				Actual:   http.StatusNotFound,
				Expected: []int{http.StatusOK},
			}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAuthError(tt.err); got != tt.want {
				t.Errorf("isAuthError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReconnect(t *testing.T) {
	const (
		oldEndpoint = "http://old:6385/v1/"
		newEndpoint = "http://new:6385/v1/"
	)

	t.Run("swaps the service client on success", func(t *testing.T) {
		oldSC := &gophercloud.ServiceClient{Endpoint: oldEndpoint}
		newSC := &gophercloud.ServiceClient{Endpoint: newEndpoint}

		c := &OpenStackClient{
			client: oldSC,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) {
				return newSC, nil
			},
		}

		if err := c.reconnect(context.Background()); err != nil {
			t.Fatalf("reconnect() unexpected error: %v", err)
		}
		if c.client != newSC {
			t.Errorf("expected client to be swapped to newSC")
		}
		if c.client.Endpoint != newEndpoint {
			t.Errorf("expected endpoint %q, got %q", newEndpoint, c.client.Endpoint)
		}
	})

	t.Run("returns error when factory fails", func(t *testing.T) {
		oldSC := &gophercloud.ServiceClient{Endpoint: oldEndpoint}

		c := &OpenStackClient{
			client: oldSC,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) {
				return nil, fmt.Errorf("keystone is down")
			},
		}

		err := c.reconnect(context.Background())
		if err == nil {
			t.Fatal("reconnect() expected error, got nil")
		}
		if c.client != oldSC {
			t.Error("should keep old client on failure")
		}
	})
}
