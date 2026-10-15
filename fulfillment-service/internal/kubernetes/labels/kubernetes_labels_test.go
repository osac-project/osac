/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package labels

import "testing"

func TestMergeTrustedExternalIPLifecycleLabels(t *testing.T) {
	current := map[string]string{
		"osac.openshift.io/externalip-uuid": "eip-1",
		"example.com/operator-label":        "preserve",
		"osac.openshift.io/auto-created":    "stale",
	}
	metadata := map[string]string{
		"osac.openshift.io/auto-created":     "true",
		"osac.openshift.io/auto-created-for": "cluster-1",
		"example.com/user-label":             "do-not-copy",
	}

	untrusted := MergeTrustedExternalIPLifecycleLabels(current, metadata, false)
	if untrusted["osac.openshift.io/externalip-uuid"] != "eip-1" {
		t.Fatalf("existing resource identifier label was not preserved: %v", untrusted)
	}
	if untrusted["example.com/operator-label"] != "preserve" {
		t.Fatalf("unrelated Kubernetes label was not preserved: %v", untrusted)
	}
	if _, ok := untrusted["osac.openshift.io/auto-created"]; ok {
		t.Fatalf("untrusted lifecycle marker was retained: %v", untrusted)
	}
	if _, ok := untrusted["osac.openshift.io/auto-created-for"]; ok {
		t.Fatalf("untrusted lifecycle owner was retained: %v", untrusted)
	}
	if _, ok := untrusted["example.com/user-label"]; ok {
		t.Fatalf("arbitrary metadata label was copied: %v", untrusted)
	}

	trusted := MergeTrustedExternalIPLifecycleLabels(current, metadata, true)
	if trusted["osac.openshift.io/auto-created"] != "true" || trusted["osac.openshift.io/auto-created-for"] != "cluster-1" {
		t.Fatalf("trusted lifecycle labels were not projected: %v", trusted)
	}
	if _, ok := trusted["example.com/user-label"]; ok {
		t.Fatalf("arbitrary metadata label was copied: %v", trusted)
	}
}
