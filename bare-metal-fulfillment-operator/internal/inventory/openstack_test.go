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
	"strings"
	"testing"

	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/v1/nodes"
)

func TestValidateMatchExpressions(t *testing.T) {
	tests := []struct {
		name             string
		matchExpressions map[string]string
		wantError        bool
		errorContains    string
	}{
		{
			name:             "valid simple labels",
			matchExpressions: map[string]string{"key1": "value1", "key2": "value2"},
		},
		{
			name:             "empty map is invalid",
			matchExpressions: map[string]string{},
			wantError:        true,
			errorContains:    "empty map",
		},
		{
			name:             "nil map is invalid",
			matchExpressions: nil,
			wantError:        true,
			errorContains:    "empty map",
		},
		{
			name:             "empty key is invalid",
			matchExpressions: map[string]string{"": "value1"},
			wantError:        true,
			errorContains:    "empty key",
		},
		{
			name:             "empty value is rejected",
			matchExpressions: map[string]string{"key1": ""},
			wantError:        true,
			errorContains:    "empty value not allowed",
		},
		{
			name:             "key with special characters",
			matchExpressions: map[string]string{"osac.openshift.io/hardware-profile": "gpu-large"},
		},
		{
			name:             "key with spaces is invalid",
			matchExpressions: map[string]string{"key with spaces": "value1"},
			wantError:        true,
			errorContains:    "contains spaces",
		},
		{
			name:             "managedBy key is treated as regular label",
			matchExpressions: map[string]string{"managedBy": "baremetal"},
		},
		{
			name:             "provisionState key is treated as regular label",
			matchExpressions: map[string]string{"provisionState": "available"},
		},
		{
			name: "mix of different label types is valid",
			matchExpressions: map[string]string{
				"hardware-profile": "gpu-large",
				"managedBy":        "baremetal",
			},
		},
		{
			name:             "multiple valid labels",
			matchExpressions: map[string]string{"resourceClass": "gpu-large", "environment": "production"},
		},
		{
			name:             "multiple labels with one invalid key",
			matchExpressions: map[string]string{"validKey": "validValue", "": "invalidEmptyKey"},
			wantError:        true,
			errorContains:    "empty key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMatchExpressions(tt.matchExpressions)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errorContains)
				}
				if !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("error = %v, want containing %q", err, tt.errorContains)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestNodeMatchesLabels(t *testing.T) {
	tests := []struct {
		name             string
		node             *nodes.Node
		matchExpressions map[string]string
		want             bool
	}{
		{
			name: "all labels match",
			node: &nodes.Node{
				Extra: map[string]interface{}{
					"osac_labels": map[string]interface{}{
						"environment": "production",
						"hardware":    "gpu-large",
					},
				},
			},
			matchExpressions: map[string]string{"environment": "production", "hardware": "gpu-large"},
			want:             true,
		},
		{
			name: "value mismatch",
			node: &nodes.Node{
				Extra: map[string]interface{}{
					"osac_labels": map[string]interface{}{
						"environment": "production",
						"hardware":    "gpu-small",
					},
				},
			},
			matchExpressions: map[string]string{"environment": "production", "hardware": "gpu-large"},
			want:             false,
		},
		{
			name: "missing required key",
			node: &nodes.Node{
				Extra: map[string]interface{}{
					"osac_labels": map[string]interface{}{
						"environment": "staging",
					},
				},
			},
			matchExpressions: map[string]string{"environment": "production", "hardware": "gpu-large"},
			want:             false,
		},
		{
			name: "no osac_labels",
			node: &nodes.Node{
				Extra: map[string]interface{}{},
			},
			matchExpressions: map[string]string{"environment": "production"},
			want:             false,
		},
		{
			name: "nil Extra",
			node: &nodes.Node{
				Extra: nil,
			},
			matchExpressions: map[string]string{"environment": "production"},
			want:             false,
		},
		{
			name: "empty match expressions matches any node",
			node: &nodes.Node{
				Extra: map[string]interface{}{
					"osac_labels": map[string]interface{}{"environment": "production"},
				},
			},
			matchExpressions: map[string]string{},
			want:             true,
		},
		{
			name: "nil match expressions matches any node",
			node: &nodes.Node{
				Extra: map[string]interface{}{
					"osac_labels": map[string]interface{}{"environment": "production"},
				},
			},
			matchExpressions: nil,
			want:             true,
		},
		{
			name: "malformed osac_labels type",
			node: &nodes.Node{
				Extra: map[string]interface{}{
					"osac_labels": "not-a-map",
				},
			},
			matchExpressions: map[string]string{"environment": "production"},
			want:             false,
		},
		{
			name: "substring does not match",
			node: &nodes.Node{
				Extra: map[string]interface{}{
					"osac_labels": map[string]interface{}{"version": "1.2.3"},
				},
			},
			matchExpressions: map[string]string{"version": "1.2"},
			want:             false,
		},
		{
			name: "non-string label value does not match",
			node: &nodes.Node{
				Extra: map[string]interface{}{
					"osac_labels": map[string]interface{}{"count": 42},
				},
			},
			matchExpressions: map[string]string{"count": "42"},
			want:             false,
		},
		{
			name: "node has superset of required labels",
			node: &nodes.Node{
				Extra: map[string]interface{}{
					"osac_labels": map[string]interface{}{
						"env":       "prod",
						"hw":        "gpu",
						"unrelated": "value",
					},
				},
			},
			matchExpressions: map[string]string{"env": "prod", "hw": "gpu"},
			want:             true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nodeMatchesLabels(tt.node, tt.matchExpressions)
			if got != tt.want {
				t.Errorf("nodeMatchesLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindFreeHostValidation(t *testing.T) {
	tests := []struct {
		name             string
		matchExpressions map[string]string
		errorContains    string
	}{
		{
			name:             "empty key rejected before Ironic query",
			matchExpressions: map[string]string{"": "value1"},
			errorContains:    "empty key",
		},
		{
			name:             "key with spaces rejected",
			matchExpressions: map[string]string{"key with spaces": "value1"},
			errorContains:    "contains spaces",
		},
	}

	client := &OpenStackClient{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.FindFreeHost(context.Background(), tt.matchExpressions)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.errorContains)
			}
			if !strings.Contains(err.Error(), tt.errorContains) {
				t.Errorf("error = %v, want containing %q", err, tt.errorContains)
			}
		})
	}
}
