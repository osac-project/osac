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
	"reflect"
	"testing"

	th "github.com/gophercloud/gophercloud/v2/testhelper"
	fakeclient "github.com/gophercloud/gophercloud/v2/testhelper/client"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

func TestGetHostLogicalPortMACs_OpenStack(t *testing.T) {
	tests := []struct {
		name    string
		extra   string
		status  int
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "logical port names and MACs are preserved",
			extra: `{"osac_interface_macs":{"data-0":"52:54:00:16:04:83","data-1":"52:54:00:AA:BB:CC"}}`,
			want:  map[string]string{"data-0": "52:54:00:16:04:83", "data-1": "52:54:00:AA:BB:CC"},
		},
		{name: "absent mapping", extra: `{}`, want: map[string]string{}},
		{name: "empty mapping", extra: `{"osac_interface_macs":{}}`, want: map[string]string{}},
		{name: "null mapping", extra: `{"osac_interface_macs":null}`, want: map[string]string{}},
		{name: "null extra", extra: `null`, want: map[string]string{}},
		{name: "wrong shape", extra: `{"osac_interface_macs":[]}`, wantErr: true},
		{name: "non-string MAC", extra: `{"osac_interface_macs":{"data-0":42}}`, wantErr: true},
		{name: "missing node", extra: `{}`, status: http.StatusNotFound, wantErr: true},
		{name: "auth failure", extra: `{}`, status: http.StatusUnauthorized, wantErr: true},
		{name: "API failure", extra: `{}`, status: http.StatusInternalServerError, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeServer := th.SetupHTTP()
			defer fakeServer.Teardown()
			fakeServer.Mux.HandleFunc("/nodes/test-node-uuid", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				status := tt.status
				if status == 0 {
					status = http.StatusOK
				}
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `{"uuid":"test-node-uuid","extra":%s}`, tt.extra)
			})
			c := &OpenStackClient{client: fakeclient.ServiceClient(fakeServer)}
			got, err := c.GetHostLogicalPortMACs(context.Background(), "test-node-uuid")
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetHostLogicalPortMACs error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetHostLogicalPortMACs = %v, want %v", got, tt.want)
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
		c := &OpenStackClient{client: sc}

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
		c := &OpenStackClient{client: sc}

		_, err := c.GetHostNICs(context.Background(), "test-node-uuid")
		if err == nil {
			t.Fatal("expected error for empty port list, got nil")
		}
	})

	t.Run("returns error on auth failure", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{client: sc}

		_, err := c.GetHostNICs(context.Background(), "test-node-uuid")
		if err == nil {
			t.Fatal("expected error for 401, got nil")
		}
	})

	t.Run("returns error on non-auth API failure", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{client: sc}

		_, err := c.GetHostNICs(context.Background(), "test-node-uuid")
		if err == nil {
			t.Fatal("expected error for 500, got nil")
		}
	})
}

func TestFindFreeHost_PortFilter(t *testing.T) {
	// osac_labels carries the host selector labels; matchExpressions are now matched
	// against these rather than resource_class (see OSAC-3578 label filtering).
	const nodeListResponse = `{"nodes": [{"uuid": "node-1", "name": "host-1", "resource_class": "gpu-node", "provision_state": "available", "extra": {"osac_labels": {"hostType": "gpu-node"}}}]}`

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
			client:    sc,
			hostClass: "openstack",
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
			client:    sc,
			hostClass: "openstack",
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
			client:    sc,
			hostClass: "openstack",
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

func TestFindFreeHost_ManagedByGuard(t *testing.T) {
	// Ownership guard mirrors the Metal3 backend: a node whose managedBy osac_label
	// belongs to another system is skipped; a missing/empty managedBy defaults to the
	// osac-owned value and is selectable. managedBy is never matched as a host label.
	portsResponse := `{"ports": [{"address": "aa:bb:cc:dd:ee:01"}]}`

	t.Run("skips node owned by another system", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		fakeServer.Mux.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"nodes": [{"uuid": "node-1", "name": "host-1", "provision_state": "available", "extra": {"osac_labels": {"hostType": "gpu-node", "managedBy": "someone-else"}}}]}`)
		})
		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, portsResponse)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{
			client:    sc,
			hostClass: "openstack",
		}

		host, err := c.FindFreeHost(context.Background(), map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (foreign managedBy skipped), got %+v", host)
		}
	})

	t.Run("selects node with no managedBy label (defaults to owned)", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		fakeServer.Mux.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"nodes": [{"uuid": "node-1", "name": "host-1", "provision_state": "available", "extra": {"osac_labels": {"hostType": "gpu-node"}}}]}`)
		})
		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, portsResponse)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{
			client:    sc,
			hostClass: "openstack",
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
		if host.ManagedBy != shared.OsacDefaultManagedByValue {
			t.Errorf("expected managedBy %q, got %q", shared.OsacDefaultManagedByValue, host.ManagedBy)
		}
	})

	t.Run("selects node explicitly owned by osac default", func(t *testing.T) {
		fakeServer := th.SetupHTTP()
		defer fakeServer.Teardown()

		fakeServer.Mux.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{"nodes": [{"uuid": "node-1", "name": "host-1", "provision_state": "available", "extra": {"osac_labels": {"hostType": "gpu-node", "managedBy": %q}}}]}`, shared.OsacDefaultManagedByValue)
		})
		fakeServer.Mux.HandleFunc("/ports/detail", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, portsResponse)
		})

		sc := fakeclient.ServiceClient(fakeServer)
		c := &OpenStackClient{
			client:    sc,
			hostClass: "openstack",
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
}
