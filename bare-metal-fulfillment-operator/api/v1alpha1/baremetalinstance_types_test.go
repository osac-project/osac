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

package v1alpha1

import (
	"testing"
)

func TestHostConditionTypeValues(t *testing.T) {
	if HostConditionAllocated != "Allocated" {
		t.Errorf("HostConditionAllocated = %q, want %q", HostConditionAllocated, "Allocated")
	}
	if HostConditionPowerSynced != "PowerSynced" {
		t.Errorf("HostConditionPowerSynced = %q, want %q", HostConditionPowerSynced, "PowerSynced")
	}
	if HostConditionProvisionTemplateComplete != "ProvisionTemplateComplete" {
		t.Errorf("HostConditionProvisionTemplateComplete = %q, want %q", HostConditionProvisionTemplateComplete, "ProvisionTemplateComplete")
	}
	if HostConditionDeprovisionTemplateComplete != "DeprovisionTemplateComplete" {
		t.Errorf("HostConditionDeprovisionTemplateComplete = %q, want %q", HostConditionDeprovisionTemplateComplete, "DeprovisionTemplateComplete")
	}
}

func TestHostConditionReasonValues(t *testing.T) {
	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"Progressing", HostConditionReasonProgressing, "Progressing"},
		{"TemplateFailed", HostConditionReasonTemplateFailed, "TemplateFailed"},
		{"PowerOn", HostConditionReasonPowerOn, "PowerOn"},
		{"PowerOff", HostConditionReasonPowerOff, "PowerOff"},
		{"IronicAPIFailure", HostConditionReasonIronicAPIFailure, "IronicAPIFailure"},
		{"PowerSyncFailed", HostConditionReasonPowerSyncFailed, "PowerSyncFailed"},
		{"PowerSyncRequired", HostConditionReasonPowerSyncRequired, "PowerSyncRequired"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %q, want %q", tt.got, tt.expected)
			}
		})
	}
}
