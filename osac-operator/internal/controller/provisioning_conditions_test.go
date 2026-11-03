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
)

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
