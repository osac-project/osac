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
	"errors"
	"reflect"
	"testing"
)

func TestParseLogicalPortMAC(t *testing.T) {
	tests := []struct {
		name    string
		mapping map[string]string
		port    string
		want    string
		wantErr bool
	}{
		{
			name:    "normalizes uppercase colon-delimited MAC",
			mapping: map[string]string{"data-0": "52:54:00:AA:BB:CC"},
			port:    "data-0",
			want:    "52:54:00:aa:bb:cc",
		},
		{
			name:    "normalizes hyphen-delimited MAC",
			mapping: map[string]string{"data-0": "52-54-00-AA-BB-CC"},
			port:    "data-0",
			want:    "52:54:00:aa:bb:cc",
		},
		{
			name:    "trims surrounding whitespace",
			mapping: map[string]string{"data-0": " 52:54:00:AA:BB:CC\n"},
			port:    "data-0",
			want:    "52:54:00:aa:bb:cc",
		},
		{name: "missing selected port", mapping: map[string]string{"other": "52:54:00:aa:bb:cc"}, port: "data-0", wantErr: true},
		{name: "malformed selected port MAC", mapping: map[string]string{"data-0": "not-a-mac"}, port: "data-0", wantErr: true},
		{name: "non-Ethernet address", mapping: map[string]string{"data-0": "52:54:00:aa:bb:cc:dd:ee"}, port: "data-0", wantErr: true},
		{name: "empty port name", mapping: map[string]string{"data-0": "52:54:00:aa:bb:cc"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLogicalPortMAC(tt.mapping, tt.port)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseLogicalPortMAC error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidLogicalPortMAC) {
					t.Fatalf("ParseLogicalPortMAC error = %v, want ErrInvalidLogicalPortMAC", err)
				}
				return
			}
			if got != tt.want {
				t.Errorf("ParseLogicalPortMAC = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeLogicalPortMACMapping(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		want    map[string]string
		wantErr bool
	}{
		{name: "object", value: map[string]any{"data-0": "52:54:00:aa:bb:cc"}, want: map[string]string{"data-0": "52:54:00:aa:bb:cc"}},
		{name: "JSON string", value: `{"data-0":"52:54:00:aa:bb:cc"}`, want: map[string]string{"data-0": "52:54:00:aa:bb:cc"}},
		{name: "empty string", value: "", want: map[string]string{}},
		{name: "nil", want: map[string]string{}},
		{name: "wrong shape", value: []any{"not", "a", "mapping"}, wantErr: true},
		{name: "malformed JSON string", value: "not-json", wantErr: true},
		{name: "non-string MAC", value: map[string]any{"data-0": 42}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeLogicalPortMACMapping(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("decodeLogicalPortMACMapping error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !errors.Is(err, ErrMalformedLogicalPortMACMapping) {
					t.Fatalf("decodeLogicalPortMACMapping error = %v, want ErrMalformedLogicalPortMACMapping", err)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decodeLogicalPortMACMapping = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHostSupportsLogicalPortSkipsHostWithoutInventoryName(t *testing.T) {
	eligible, err := hostSupportsLogicalPort(
		context.Background(),
		&Host{InventoryHostID: "namespace/host-1"},
		"data-0",
		map[string]string{"data-0": "52:54:00:aa:bb:cc"},
		nil,
	)
	if err != nil {
		t.Fatalf("hostSupportsLogicalPort error = %v", err)
	}
	if eligible {
		t.Fatal("host without an inventory name was eligible")
	}
}
