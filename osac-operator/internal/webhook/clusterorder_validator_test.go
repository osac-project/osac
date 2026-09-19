/*
Copyright 2025.

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

package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestClusterOrderValidator_Handle(t *testing.T) {
	tests := []struct {
		name      string
		operation admissionv1.Operation
		object    map[string]any
		allowed   bool
		contains  string
	}{
		{
			name:      "allows valid spec on create",
			operation: admissionv1.Create,
			object: map[string]any{
				"spec": map[string]any{
					"templateID":         "ocp_ci_small",
					"templateParameters": `{"key":"val"}`,
					"pullSecret":         "secret-data",
					"sshPublicKey":       "ssh-ed25519 AAAA...",
				},
			},
			allowed: true,
		},
		{
			name:      "allows valid spec with all known fields",
			operation: admissionv1.Create,
			object: map[string]any{
				"spec": map[string]any{
					"templateID":         "ocp_ci_small",
					"templateParameters": `{}`,
					"pullSecret":         "s",
					"sshPublicKey":       "k",
					"releaseImage":       "quay.io/img:tag",
					"nodeRequests":       []map[string]any{{"resourceClass": "gpu", "numberOfNodes": 1}},
					"network":            map[string]any{"podCIDR": "10.0.0.0/16"},
					"networkAttachment":  map[string]any{"subnetRef": "my-subnet"},
				},
			},
			allowed: true,
		},
		{
			name:      "rejects spec.parameters",
			operation: admissionv1.Create,
			object: map[string]any{
				"spec": map[string]any{
					"templateID": "ocp_ci_small",
					"parameters": map[string]any{
						"pull_secret":    "secret",
						"ssh_public_key": "key",
					},
				},
			},
			allowed:  false,
			contains: "parameters",
		},
		{
			name:      "rejects multiple unknown fields",
			operation: admissionv1.Create,
			object: map[string]any{
				"spec": map[string]any{
					"templateID": "ocp_ci_small",
					"parameters": map[string]any{},
					"credentials": map[string]any{},
				},
			},
			allowed:  false,
			contains: "credentials, parameters",
		},
		{
			name:      "rejects unknown fields on update",
			operation: admissionv1.Update,
			object: map[string]any{
				"spec": map[string]any{
					"templateID": "ocp_ci_small",
					"parameters": map[string]any{},
				},
			},
			allowed:  false,
			contains: "parameters",
		},
		{
			name:      "allows delete operations",
			operation: admissionv1.Delete,
			object: map[string]any{
				"spec": map[string]any{
					"templateID": "ocp_ci_small",
					"parameters": map[string]any{},
				},
			},
			allowed: true,
		},
		{
			name:      "allows empty spec",
			operation: admissionv1.Create,
			object: map[string]any{
				"spec": map[string]any{},
			},
			allowed: true,
		},
		{
			name:      "allows missing spec",
			operation: admissionv1.Create,
			object:    map[string]any{},
			allowed:   true,
		},
		{
			name:      "includes valid field names in denial message",
			operation: admissionv1.Create,
			object: map[string]any{
				"spec": map[string]any{
					"templateID": "ocp_ci_small",
					"parameters": map[string]any{},
				},
			},
			allowed:  false,
			contains: "templateParameters, pullSecret, sshPublicKey",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.object)
			if err != nil {
				t.Fatalf("failed to marshal object: %v", err)
			}

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Operation: tt.operation,
					Object:    runtime.RawExtension{Raw: raw},
				},
			}

			v := &ClusterOrderValidator{}
			resp := v.Handle(context.Background(), req)

			if resp.Allowed != tt.allowed {
				t.Errorf("expected allowed=%v, got allowed=%v, result=%+v", tt.allowed, resp.Allowed, resp.Result)
			}
			if !tt.allowed && tt.contains != "" {
				if resp.Result == nil || resp.Result.Message == "" {
					t.Fatalf("expected denial message containing %q, got nil result", tt.contains)
				}
				if !strings.Contains(resp.Result.Message, tt.contains) {
					t.Errorf("expected denial message to contain %q, got %q", tt.contains, resp.Result.Message)
				}
			}
			if !tt.allowed && resp.Result != nil && resp.Result.Code != http.StatusForbidden {
				t.Errorf("expected status code %d, got %d", http.StatusForbidden, resp.Result.Code)
			}
		})
	}
}
