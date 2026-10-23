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
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// classifyProvisioningFailure maps an AAP job failure message to a stable
// condition reason.  The patterns are derived from the real error messages
// produced by the AgentlessNet provider's Python exception hierarchy:
//
//   - StateError / StateCorrupt  (agentless_net_state.py)
//   - SubnetProviderError        (agentless_net_subnet.py)
//   - NetworkCommandError        (agentless_net_network.py)
//
// The classifier is intentionally conservative: only well-known, stable
// prefixes and substrings are matched.  Unrecognised messages fall through
// to ReasonProvisioningFailed so that new provider messages do not require
// a synchronised operator release.
func classifyProvisioningFailure(message string) string {
	// State-lock timeout — StateError raised by _locked_path().
	if strings.Contains(message, "timed out waiting for AgentlessNet lock") {
		return v1alpha1.ReasonStateLockTimeout
	}

	// Switch (Cumulus) failures — SubnetProviderError from
	// parse_switch_vlan_exclusions() and related helpers.
	if strings.HasPrefix(message, "Cumulus ") {
		return v1alpha1.ReasonSwitchOperationFailed
	}

	// State corruption — StateCorrupt exceptions.
	for _, pattern := range []string{
		"AgentlessNet state database is corrupt",
		"AgentlessNet state database has an invalid schema",
		"AgentlessNet state path is not a regular file",
		"AgentlessNet state file has unsafe owner or mode",
		"unsupported state schema",
		"malformed VirtualNetwork state payload",
		"malformed Subnet state payload",
		"malformed ExternalIPPool state",
		"malformed ExternalIP reservation state",
		"indexed state does not match its payload",
	} {
		if strings.Contains(message, pattern) {
			return v1alpha1.ReasonStateCorrupt
		}
	}

	// Namespace / uplink / forwarding-isolation failures —
	// NetworkCommandError and StateError from reconcile/delete paths.
	for _, pattern := range []string{
		"namespace is not present after reconciliation",
		"namespace is absent",
		"uplink interface is absent",
		"uplink exists without its host peer",
		"namespace or host uplink remains after deletion",
		"host uplink exists but is not a veth",
		"host uplink is not owned by this VirtualNetwork UID",
		"host uplink alias does not match",
		"forwarding isolation rule is absent",
		"forwarding isolation rule is below an allow rule",
	} {
		if strings.Contains(message, pattern) {
			return v1alpha1.ReasonNamespaceOperationFailed
		}
	}

	// Net-node command failures — NetworkCommandError from
	// run_command().
	for _, pattern := range []string{
		"timed out after ",
		"failed (exit status ",
		"could not run",
		"required network-node binary ",
	} {
		if strings.Contains(message, pattern) {
			return v1alpha1.ReasonNetNodeCommandFailed
		}
	}

	return v1alpha1.ReasonProvisioningFailed
}

func setReadyConditionFailed(conditions *[]metav1.Condition, message string) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:    v1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  classifyProvisioningFailure(message),
		Message: message,
	})
}

func setReadyConditionTrue(conditions *[]metav1.Condition) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:   v1alpha1.ConditionReady,
		Status: metav1.ConditionTrue,
		Reason: v1alpha1.ReasonAsExpected,
	})
}

// setReadyConditionNoOp marks Ready=True with ReasonNoOp for resources
// whose provisioning backend reports that no real policy or backend
// effect was applied. The message carries the backend's explanation.
func setReadyConditionNoOp(conditions *[]metav1.Condition, message string) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:    v1alpha1.ConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  v1alpha1.ReasonNoOp,
		Message: message,
	})
}

// noopMessageFromOutputs returns the human-readable no-op message
// from provisioning job outputs when the backend signals that no real
// effect was applied (osac_noop: true). Returns "" when the outputs
// carry no no-op signal.
func noopMessageFromOutputs(outputs map[string]any) string {
	if outputs == nil {
		return ""
	}
	noop, _ := outputs["osac_noop"].(bool)
	if !noop {
		return ""
	}
	msg, _ := outputs["osac_noop_message"].(string)
	if msg == "" {
		msg = "Resource accepted as a no-op: " +
			"no backend policy was applied."
	}
	return msg
}

// setReadyConditionBlocked marks Ready=False for a precondition that is expected to
// self-heal once satisfied (e.g. no dispatcher-resolvable manager yet), as opposed to
// setReadyConditionFailed's terminal provisioning failure.
func setReadyConditionBlocked(conditions *[]metav1.Condition, reason, message string) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:    v1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}
