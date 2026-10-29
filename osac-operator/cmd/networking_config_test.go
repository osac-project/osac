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

// Main entrypoint for the operator
package main

import (
	"os"
	"testing"
)

func TestNetworkingProvisioningStartup(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		unset, want bool
	}{
		{name: "unset", unset: true, want: true},
		{name: "empty", want: true},
		{name: "true", value: "true", want: true},
		{name: "false", value: "false"},
		{name: "invalid", value: "invalid", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envEnableNetworkingProvisioning, tc.value)
			if tc.unset {
				if err := os.Unsetenv(envEnableNetworkingProvisioning); err != nil {
					t.Fatal(err)
				}
			}
			loaded := loadNetworkingProvisioning()
			if loaded != tc.want {
				t.Fatalf("got %t, want %t", loaded, tc.want)
			}
			t.Setenv(envEnableNetworkingProvisioning, "false")
			if loaded != tc.want {
				t.Fatal("startup value changed after environment update")
			}
		})
	}
}
