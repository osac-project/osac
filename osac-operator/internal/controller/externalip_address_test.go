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

import "testing"

func TestValidateAllocatedAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		cidrs   []string
		wantErr bool
	}{
		{name: "canonical IPv4 in pool", address: "192.0.2.8", cidrs: []string{"192.0.2.0/24"}},
		{name: "address outside pool", address: "198.51.100.8", cidrs: []string{"192.0.2.0/24"}, wantErr: true},
		{name: "IPv6 address", address: "2001:db8::8", cidrs: []string{"2001:db8::/64"}, wantErr: true},
		{name: "non-canonical IPv4", address: "192.000.002.008", cidrs: []string{"192.0.2.0/24"}, wantErr: true},
		{name: "invalid address", address: "not-an-ip", cidrs: []string{"192.0.2.0/24"}, wantErr: true},
		{name: "invalid pool CIDR", address: "192.0.2.8", cidrs: []string{"bad-cidr"}, wantErr: true},
		{name: "empty pool", address: "192.0.2.8", cidrs: nil, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAllocatedAddress(test.address, test.cidrs)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateAllocatedAddress(%q, %v) error = %v, wantErr %t", test.address, test.cidrs, err, test.wantErr)
			}
		})
	}
}
