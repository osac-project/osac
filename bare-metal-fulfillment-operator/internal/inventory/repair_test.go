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
	"testing"
)

func TestNetworkRepairRequired(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want bool
	}{
		{name: "string true", v: "true", want: true},
		{name: "string True", v: "True", want: true},
		{name: "string 1", v: "1", want: true},
		{name: "string false", v: "false", want: false},
		{name: "bool true", v: true, want: true},
		{name: "bool false", v: false, want: false},
		{name: "nil", v: nil, want: false},
		{name: "int", v: 1, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := networkRepairRequired(tt.v); got != tt.want {
				t.Errorf("networkRepairRequired(%v) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}
