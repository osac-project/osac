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
	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/v1/ports"
	th "github.com/gophercloud/gophercloud/v2/testhelper"
	fakeclient "github.com/gophercloud/gophercloud/v2/testhelper/client"
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

func TestGetHostNICs_OpenStack(t *testing.T) {
	t.Run("returns lowercased MACs from port records", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("node_uuid") != "test-node-uuid" {
				t.Errorf("unexpected node_uuid: %s", r.URL.Query().Get("node_uuid"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"ports": [{"address": "AA:BB:CC:DD:EE:01"}, {"address": "ff:00:11:22:33:44"}]}`)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{
			client: sc,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) {
				return nil, fmt.Errorf("should not reconnect")
			},
		}

		nics, err := c.GetHostNICs(context.Background(), "test-node-uuid")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(nics) != 2 {
			t.Fatalf("expected 2 NICs, got %d", len(nics))
		}
		if nics[0].MAC != "aa:bb:cc:dd:ee:01" {
			t.Errorf("expected lowercased MAC, got %q", nics[0].MAC)
		}
		if nics[1].MAC != "ff:00:11:22:33:44" {
			t.Errorf("expected lowercased MAC, got %q", nics[1].MAC)
		}
	})

	t.Run("returns error when port list is empty", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"ports": []}`)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{
			client: sc,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) {
				return nil, fmt.Errorf("should not reconnect")
			},
		}

		_, err := c.GetHostNICs(context.Background(), "test-node-uuid")
		if err == nil {
			t.Fatal("expected error for empty port list, got nil")
		}
	})

	t.Run("retries on auth error and returns NICs on success", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		callCount := 0
		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			callCount++
			if callCount == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"ports": [{"address": "aa:bb:cc:dd:ee:01"}]}`)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{
			client:           sc,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) { return sc, nil },
		}

		nics, err := c.GetHostNICs(context.Background(), "test-node-uuid")
		if err != nil {
			t.Fatalf("unexpected error after reconnect: %v", err)
		}
		if len(nics) != 1 || nics[0].MAC != "aa:bb:cc:dd:ee:01" {
			t.Errorf("expected 1 NIC with correct MAC, got %v", nics)
		}
		if callCount != 2 {
			t.Errorf("expected 2 calls (auth retry), got %d", callCount)
		}
	})

	t.Run("returns error on non-auth API failure", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{
			client: sc,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) {
				return nil, fmt.Errorf("should not reconnect")
			},
		}

		_, err := c.GetHostNICs(context.Background(), "test-node-uuid")
		if err == nil {
			t.Fatal("expected error for 500, got nil")
		}
	})
}

func TestFindFreeHost_PortFilter(t *testing.T) {
	const nodeListResponse = `{"nodes": [{"uuid": "node-1", "name": "host-1", "resource_class": "gpu-node", "provision_state": "available", "extra": {}}]}`

	t.Run("selects node with ports", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		fakeServer.Mux.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, nodeListResponse)
		})
		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"ports": [{"address": "aa:bb:cc:dd:ee:01"}]}`)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{
			client:           sc,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) { return sc, nil },
			HostClass:        "openstack",
		}

		host, err := c.FindFreeHost(context.Background(), map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected a host, got nil")
		}
		if host.InventoryHostID != "node-1" {
			t.Errorf("expected node-1, got %q", host.InventoryHostID)
		}
	})

	t.Run("skips node with no registered ports", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		fakeServer.Mux.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, nodeListResponse)
		})
		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"ports": []}`)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{
			client:           sc,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) { return sc, nil },
			HostClass:        "openstack",
		}

		host, err := c.FindFreeHost(context.Background(), map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (portless node skipped), got %+v", host)
		}
	})

	t.Run("skips node when port API returns error", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		fakeServer.Mux.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, nodeListResponse)
		})
		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{
			client:           sc,
			newServiceClient: func(context.Context) (*gophercloud.ServiceClient, error) { return sc, nil },
			HostClass:        "openstack",
		}

		host, err := c.FindFreeHost(context.Background(), map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (port API error → skip), got %+v", host)
		}
	})
}

// compile-time check that ports package is used
var _ = ports.ListOpts{}

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
