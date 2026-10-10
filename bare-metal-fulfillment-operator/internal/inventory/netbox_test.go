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
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

func TestNetBoxClientGetHostLogicalPortMACsNotImplemented(t *testing.T) {
	macs, err := (&NetBoxClient{}).GetHostLogicalPortMACs(context.Background(), "baremetal/netbox-42")
	if !errors.Is(err, errNetBoxClientNotImplemented) {
		t.Fatalf("GetHostLogicalPortMACs error = %v, want %v", err, errNetBoxClientNotImplemented)
	}
	if macs != nil {
		t.Fatalf("GetHostLogicalPortMACs returned %v, want nil", macs)
	}
}

func TestNewNetBoxHostIdentity(t *testing.T) {
	tests := []struct {
		name        string
		namespace   string
		deviceID    string
		wantName    string
		wantHostID  string
		wantEncoded bool
		wantErr     bool
	}{
		{name: "numeric ID", namespace: "baremetal", deviceID: "42", wantName: "netbox-42", wantHostID: "baremetal/netbox-42"},
		{name: "leading zeroes are preserved", namespace: "baremetal", deviceID: "0042", wantName: "netbox-0042", wantHostID: "baremetal/netbox-0042"},
		{name: "UUID ID", namespace: "baremetal", deviceID: "5f8d0f9c-3e2b-4a11-8f61-a1e6b7c2d3e4", wantName: "netbox-5f8d0f9c-3e2b-4a11-8f61-a1e6b7c2d3e4", wantHostID: "baremetal/netbox-5f8d0f9c-3e2b-4a11-8f61-a1e6b7c2d3e4"},
		{name: "opaque ID containing a slash", namespace: "baremetal", deviceID: "rack/1", wantEncoded: true},
		{name: "opaque ID containing uppercase characters", namespace: "baremetal", deviceID: "Rack-1", wantEncoded: true},
		{name: "opaque ID valid as a Kubernetes name", namespace: "baremetal", deviceID: "rack-1", wantName: "netbox-rack-1", wantHostID: "baremetal/netbox-rack-1"},
		{name: "empty ID", namespace: "baremetal", wantErr: true},
		{name: "empty namespace", deviceID: "42", wantErr: true},
		{name: "namespace containing a path separator", namespace: "bare/metal", deviceID: "42", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newNetBoxHostIdentity(tt.namespace, tt.deviceID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("newNetBoxHostIdentity(%q, %q) error = %v, wantErr %t", tt.namespace, tt.deviceID, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.namespace != tt.namespace || got.deviceID != tt.deviceID || (tt.wantName != "" && got.name != tt.wantName) {
				t.Fatalf("newNetBoxHostIdentity(%q, %q) = %#v", tt.namespace, tt.deviceID, got)
			}
			if tt.wantEncoded && !strings.HasPrefix(got.name, netBoxEncodedHostNamePrefix) {
				t.Fatalf("opaque ID produced name %q, want encoded name", got.name)
			}
			if tt.wantHostID != "" && got.inventoryHostID != tt.wantHostID {
				t.Fatalf("inventory host ID = %q, want %q", got.inventoryHostID, tt.wantHostID)
			}
			if got.inventoryHostID != tt.namespace+"/"+got.name {
				t.Fatalf("inventory host ID %q does not identify Metal3 host %q/%q", got.inventoryHostID, tt.namespace, got.name)
			}
			if problems := validation.IsDNS1035Label(got.name); len(problems) != 0 {
				t.Fatalf("generated name %q is not a DNS-1035 label: %v", got.name, problems)
			}

			parsed, err := parseNetBoxHostIdentity(got.inventoryHostID, tt.namespace)
			if err != nil || parsed != got {
				t.Fatalf("generated inventory host ID %q did not round-trip: (%#v, %v)", got.inventoryHostID, parsed, err)
			}
		})
	}
}

func TestParseNetBoxHostIdentity(t *testing.T) {
	tests := []struct {
		name            string
		inventoryHostID string
		configuredNS    string
		wantDeviceID    string
		wantName        string
		wantErr         bool
	}{
		{name: "persisted numeric ID", inventoryHostID: "baremetal/netbox-42", configuredNS: "baremetal", wantDeviceID: "42", wantName: "netbox-42"},
		{name: "persisted leading zeroes", inventoryHostID: "baremetal/netbox-0042", configuredNS: "baremetal", wantDeviceID: "0042", wantName: "netbox-0042"},
		{name: "persisted UUID", inventoryHostID: "baremetal/netbox-5f8d0f9c-3e2b-4a11-8f61-a1e6b7c2d3e4", configuredNS: "baremetal", wantDeviceID: "5f8d0f9c-3e2b-4a11-8f61-a1e6b7c2d3e4", wantName: "netbox-5f8d0f9c-3e2b-4a11-8f61-a1e6b7c2d3e4"},
		{name: "wrong namespace", inventoryHostID: "other/netbox-42", configuredNS: "baremetal", wantErr: true},
		{name: "missing namespace", inventoryHostID: "netbox-42", configuredNS: "baremetal", wantErr: true},
		{name: "empty namespace", inventoryHostID: "/netbox-42", configuredNS: "", wantErr: true},
		{name: "extra path component", inventoryHostID: "baremetal/netbox-42/other", configuredNS: "baremetal", wantErr: true},
		{name: "wrong name prefix", inventoryHostID: "baremetal/worker-42", configuredNS: "baremetal", wantErr: true},
		{name: "invalid encoded ID", inventoryHostID: "baremetal/nb-!!!!", configuredNS: "baremetal", wantErr: true},
		{name: "empty ID", inventoryHostID: "baremetal/netbox-", configuredNS: "baremetal", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseNetBoxHostIdentity(tt.inventoryHostID, tt.configuredNS)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseNetBoxHostIdentity(%q, %q) error = %v, wantErr %t", tt.inventoryHostID, tt.configuredNS, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.namespace != tt.configuredNS || got.deviceID != tt.wantDeviceID || got.name != tt.wantName || got.inventoryHostID != tt.inventoryHostID {
				t.Fatalf("parseNetBoxHostIdentity(%q, %q) = %#v", tt.inventoryHostID, tt.configuredNS, got)
			}
		})
	}
}
