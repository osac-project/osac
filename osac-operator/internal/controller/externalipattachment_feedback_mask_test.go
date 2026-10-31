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
	"reflect"
	"testing"
)

// TestExternalIPAttachmentFeedbackMaskOmitsAddress guards the feedback Save mask.
// The fulfillment service rejects status.external_ip_address in a status update
// mask (validatePrivateLifecycleUpdateMask), so one stray path fails every Save.
func TestExternalIPAttachmentFeedbackMaskOmitsAddress(t *testing.T) {
	want := []string{"status.state", "status.message", "status.state_transition_time"}
	if !reflect.DeepEqual(externalIPAttachmentFeedbackPaths, want) {
		t.Fatalf("feedback mask = %v, want %v", externalIPAttachmentFeedbackPaths, want)
	}
	for _, path := range externalIPAttachmentFeedbackPaths {
		if path == "status.external_ip_address" {
			t.Fatalf("feedback mask must not contain %s", path)
		}
	}
}
