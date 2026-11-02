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
	"errors"
	"testing"
)

func TestNormalizeMACAddress(t *testing.T) {
	tests := []struct {
		input string
		want  string
		bad   bool
	}{
		{input: "AA:BB:CC:DD:EE:FF", want: "aa:bb:cc:dd:ee:ff"},
		{input: "AA-BB-CC-DD-EE-FF", want: "aa:bb:cc:dd:ee:ff"},
		{input: "aa:bb:cc:dd:ee", bad: true},
		{input: "aa:bb:cc:dd:ee:ff:00", bad: true},
		{input: "aabb.ccdd.eeff", bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := NormalizeMACAddress(tt.input)
			if tt.bad {
				if err == nil {
					t.Fatalf("NormalizeMACAddress(%q) succeeded; want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeMACAddress(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeMACAddress(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLogicalPortMACsFromValue(t *testing.T) {
	for name, value := range map[string]any{
		"object":      map[string]any{"data-0": "AA:BB:CC:DD:EE:FF"},
		"json string": `{"data-0":"AA:BB:CC:DD:EE:FF"}`,
	} {
		t.Run(name, func(t *testing.T) {
			mapping, err := LogicalPortMACsFromValue(value)
			if err != nil {
				t.Fatalf("LogicalPortMACsFromValue: %v", err)
			}
			if !HasValidNetworkPortMAC(mapping, "data-0") {
				t.Fatalf("expected valid data-0 MAC in %v", mapping)
			}
			if HasValidNetworkPortMAC(mapping, "data-1") {
				t.Fatalf("unexpected data-1 mapping in %v", mapping)
			}
		})
	}

	_, err := LogicalPortMACsFromValue(`not-json`)
	if !errors.Is(err, ErrMalformedLogicalPortMACs) {
		t.Fatalf("malformed mapping error = %v, want ErrMalformedLogicalPortMACs", err)
	}
	if mapping, err := LogicalPortMACsFromValue(nil); err != nil || len(mapping) != 0 {
		t.Fatalf("nil mapping = %v, %v; want empty map and no error", mapping, err)
	}
}
