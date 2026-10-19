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

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// TestFormatStorageBackendReadinessMessage covers sorted readiness summaries and
// the legacy fallback used when backend details are unavailable.
func TestFormatStorageBackendReadinessMessage(t *testing.T) {
	tests := []struct {
		name                 string
		statuses             []v1alpha1.StorageBackendStatus
		legacyHubSecretReady bool
		want                 string
	}{
		{
			name: "partial readiness lists ready and not-ready backends in stable order",
			statuses: []v1alpha1.StorageBackendStatus{
				{Name: "vast", Provider: "ceph", Ready: false},
				{Name: "local", Provider: "lvms", Ready: true},
				{Name: "block", Provider: "csi", Ready: true},
			},
			want: "Hub Secret readiness by backend: ready (2): block: csi, local: lvms; not ready (1): vast: ceph",
		},
		{
			name: "no backend Hub Secrets are ready",
			statuses: []v1alpha1.StorageBackendStatus{
				{Name: "vast", Provider: "vast", Ready: false},
				{Name: "lvms", Provider: "lvms", Ready: false},
			},
			want: "Hub Secret readiness by backend: ready (0): none; not ready (2): lvms: lvms, vast: vast; no backend Hub Secrets are ready",
		},
		{
			name: "all backend Hub Secrets are ready",
			statuses: []v1alpha1.StorageBackendStatus{
				{Name: "vast", Provider: "vast", Ready: true},
				{Name: "lvms", Provider: "lvms", Ready: true},
			},
			want: "Hub Secret readiness by backend: ready (2): lvms: lvms, vast: vast; not ready (0): none",
		},
		{
			name:                 "no backend entries and legacy Hub Secret found",
			legacyHubSecretReady: true,
			want:                 "No storage backend entries are available to evaluate (ready: 0; not ready: 0); legacy tenant-wide Hub Secret check: found a Secret",
		},
		{
			name: "no backend entries and legacy Hub Secret absent",
			want: "No storage backend entries are available to evaluate (ready: 0; not ready: 0); legacy tenant-wide Hub Secret check: no Secret found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatStorageBackendReadinessMessage(tt.statuses, tt.legacyHubSecretReady); got != tt.want {
				t.Fatalf("formatStorageBackendReadinessMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}
