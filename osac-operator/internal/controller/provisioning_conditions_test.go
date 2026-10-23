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

package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func Test_classifyProvisioningFailure(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		// State-lock timeout
		{
			name:    "state lock timeout",
			message: "timed out waiting for AgentlessNet lock state.sqlite3.lock",
			want:    v1alpha1.ReasonStateLockTimeout,
		},
		{
			name:    "state lock timeout alternate path",
			message: "timed out waiting for AgentlessNet lock uid-lock-042.lock",
			want:    v1alpha1.ReasonStateLockTimeout,
		},
		// Switch operation failures (Cumulus)
		{
			name:    "switch VLAN discovery malformed",
			message: "Cumulus VLAN discovery returned invalid JSON",
			want:    v1alpha1.ReasonSwitchOperationFailed,
		},
		{
			name:    "switch reserved VLAN range",
			message: "Cumulus internal reserved VLAN range is malformed",
			want:    v1alpha1.ReasonSwitchOperationFailed,
		},
		{
			name:    "switch L3-VNI reserved",
			message: "Cumulus L3-VNI reserved VLAN range is reversed",
			want:    v1alpha1.ReasonSwitchOperationFailed,
		},
		{
			name:    "switch VLAN membership",
			message: "Cumulus VLAN membership entry is malformed",
			want:    v1alpha1.ReasonSwitchOperationFailed,
		},
		{
			name:    "switch interface entry",
			message: "Cumulus VLAN interface entry is malformed",
			want:    v1alpha1.ReasonSwitchOperationFailed,
		},
		{
			name:    "switch switchd range",
			message: "Cumulus switchd internal reserved VLAN range is missing",
			want:    v1alpha1.ReasonSwitchOperationFailed,
		},
		// Namespace operation failures
		{
			name:    "namespace absent after reconciliation",
			message: "VirtualNetwork namespace is not present after reconciliation",
			want:    v1alpha1.ReasonNamespaceOperationFailed,
		},
		{
			name:    "namespace absent during subnet",
			message: "VirtualNetwork namespace is absent during Subnet reconciliation",
			want:    v1alpha1.ReasonNamespaceOperationFailed,
		},
		{
			name:    "namespace uplink absent",
			message: "VirtualNetwork uplink interface is absent",
			want:    v1alpha1.ReasonNamespaceOperationFailed,
		},
		{
			name:    "namespace peer absent",
			message: "namespace uplink exists without its host peer",
			want:    v1alpha1.ReasonNamespaceOperationFailed,
		},
		{
			name:    "namespace or host uplink remains",
			message: "network namespace or host uplink remains after deletion",
			want:    v1alpha1.ReasonNamespaceOperationFailed,
		},
		{
			name:    "host uplink not veth",
			message: "deterministic host uplink exists but is not a veth",
			want:    v1alpha1.ReasonNamespaceOperationFailed,
		},
		{
			name:    "host uplink ownership mismatch",
			message: "deterministic host uplink is not owned by this VirtualNetwork UID",
			want:    v1alpha1.ReasonNamespaceOperationFailed,
		},
		{
			name:    "host forwarding isolation absent",
			message: "host VirtualNetwork forwarding isolation rule is absent",
			want:    v1alpha1.ReasonNamespaceOperationFailed,
		},
		{
			name:    "host forwarding isolation misplaced",
			message: "host VirtualNetwork forwarding isolation rule is below an allow rule",
			want:    v1alpha1.ReasonNamespaceOperationFailed,
		},
		// Net-node command failures
		{
			name:    "command timeout",
			message: "ip timed out after 30 seconds",
			want:    v1alpha1.ReasonNetNodeCommandFailed,
		},
		{
			name:    "command failed with exit status",
			message: "iptables failed (exit status 1)",
			want:    v1alpha1.ReasonNetNodeCommandFailed,
		},
		{
			name:    "command could not run",
			message: "dnsmasq could not run",
			want:    v1alpha1.ReasonNetNodeCommandFailed,
		},
		{
			name:    "required binary unavailable",
			message: "required network-node binary ss is unavailable",
			want:    v1alpha1.ReasonNetNodeCommandFailed,
		},
		// State corruption
		{
			name:    "database corrupt",
			message: "AgentlessNet state database is corrupt: disk I/O error",
			want:    v1alpha1.ReasonStateCorrupt,
		},
		{
			name:    "unsupported schema version",
			message: "unsupported state schema: 99",
			want:    v1alpha1.ReasonStateCorrupt,
		},
		{
			name:    "invalid schema tables",
			message: "AgentlessNet state database has an invalid schema",
			want:    v1alpha1.ReasonStateCorrupt,
		},
		{
			name:    "state payload malformed",
			message: "malformed VirtualNetwork state payload",
			want:    v1alpha1.ReasonStateCorrupt,
		},
		{
			name:    "state payload malformed subnet",
			message: "malformed Subnet state payload",
			want:    v1alpha1.ReasonStateCorrupt,
		},
		{
			name:    "state path not regular file",
			message: "AgentlessNet state path is not a regular file",
			want:    v1alpha1.ReasonStateCorrupt,
		},
		{
			name:    "state file unsafe owner",
			message: "AgentlessNet state file has unsafe owner or mode",
			want:    v1alpha1.ReasonStateCorrupt,
		},
		{
			name:    "state indexed mismatch",
			message: "VirtualNetwork indexed state does not match its payload",
			want:    v1alpha1.ReasonStateCorrupt,
		},
		{
			name:    "subnet indexed mismatch",
			message: "Subnet indexed state does not match its payload",
			want:    v1alpha1.ReasonStateCorrupt,
		},
		// Fallback to generic reason
		{
			name:    "generic provisioning failure",
			message: "Subnet CIDR must be contained by its parent VirtualNetwork",
			want:    v1alpha1.ReasonProvisioningFailed,
		},
		{
			name:    "empty message",
			message: "",
			want:    v1alpha1.ReasonProvisioningFailed,
		},
		{
			name:    "VN generic error message",
			message: "AgentlessNet VirtualNetwork operation failed; inspect the network node and retry.",
			want:    v1alpha1.ReasonProvisioningFailed,
		},
		{
			name:    "unknown error",
			message: "something unexpected happened",
			want:    v1alpha1.ReasonProvisioningFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyProvisioningFailure(tt.message)
			if got != tt.want {
				t.Errorf(
					"classifyProvisioningFailure(%q) = %q, want %q",
					tt.message, got, tt.want)
			}
		})
	}
}

func Test_setReadyConditionFailed_uses_classified_reason(t *testing.T) {
	tests := []struct {
		name       string
		message    string
		wantReason string
	}{
		{
			name:       "switch failure classifies reason",
			message:    "Cumulus VLAN discovery returned invalid JSON",
			wantReason: v1alpha1.ReasonSwitchOperationFailed,
		},
		{
			name:       "lock timeout classifies reason",
			message:    "timed out waiting for AgentlessNet lock state.sqlite3.lock",
			wantReason: v1alpha1.ReasonStateLockTimeout,
		},
		{
			name:       "generic failure keeps default reason",
			message:    "Subnet tenant does not match saved state",
			wantReason: v1alpha1.ReasonProvisioningFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var conditions []metav1.Condition
			setReadyConditionFailed(&conditions, tt.message)
			if len(conditions) != 1 {
				t.Fatalf("expected 1 condition, got %d", len(conditions))
			}
			cond := conditions[0]
			if cond.Type != v1alpha1.ConditionReady {
				t.Errorf("condition type = %q, want %q",
					cond.Type, v1alpha1.ConditionReady)
			}
			if cond.Status != metav1.ConditionFalse {
				t.Errorf("condition status = %q, want %q",
					cond.Status, metav1.ConditionFalse)
			}
			if cond.Reason != tt.wantReason {
				t.Errorf("condition reason = %q, want %q",
					cond.Reason, tt.wantReason)
			}
			if cond.Message != tt.message {
				t.Errorf("condition message = %q, want %q",
					cond.Message, tt.message)
			}
		})
	}
}

func Test_noopMessageFromOutputs(t *testing.T) {
	tests := []struct {
		name    string
		outputs map[string]any
		want    string
	}{
		{
			name:    "nil outputs",
			outputs: nil,
			want:    "",
		},
		{
			name:    "empty outputs",
			outputs: map[string]any{},
			want:    "",
		},
		{
			name:    "osac_noop false",
			outputs: map[string]any{"osac_noop": false},
			want:    "",
		},
		{
			name: "osac_noop true with message",
			outputs: map[string]any{
				"osac_noop":         true,
				"osac_noop_message": "custom explanation",
			},
			want: "custom explanation",
		},
		{
			name:    "osac_noop true without message",
			outputs: map[string]any{"osac_noop": true},
			want: "Resource accepted as a no-op: " +
				"no backend policy was applied.",
		},
		{
			name: "osac_noop true with empty message",
			outputs: map[string]any{
				"osac_noop":         true,
				"osac_noop_message": "",
			},
			want: "Resource accepted as a no-op: " +
				"no backend policy was applied.",
		},
		{
			name: "unrelated outputs without noop key",
			outputs: map[string]any{
				"backend_id": "some-id",
			},
			want: "",
		},
		{
			name: "osac_noop wrong type (string)",
			outputs: map[string]any{
				"osac_noop": "true",
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := noopMessageFromOutputs(tt.outputs)
			if got != tt.want {
				t.Errorf(
					"noopMessageFromOutputs() = %q, want %q",
					got, tt.want)
			}
		})
	}
}
