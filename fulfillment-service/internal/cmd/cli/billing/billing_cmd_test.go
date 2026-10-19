//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package billing

import (
	"strings"
	"testing"
)

func TestBillingCommandHelp(t *testing.T) {
	cmd := Cmd()
	if cmd.Name() != "billing" {
		t.Fatalf("unexpected command name %q", cmd.Name())
	}
	if strings.HasSuffix(cmd.Short, ".") {
		t.Fatalf("short help must not end with a period: %q", cmd.Short)
	}
	for _, name := range []string{"status", "costs"} {
		subcommand, _, err := cmd.Find([]string{name})
		if err != nil || subcommand.Name() != name {
			t.Fatalf("missing %s subcommand: %v", name, err)
		}
		if strings.HasSuffix(subcommand.Short, ".") {
			t.Fatalf("short help must not end with a period: %q", subcommand.Short)
		}
	}
	for _, name := range []string{"from", "to", "limit"} {
		costs, _, _ := cmd.Find([]string{"costs"})
		flag := costs.Flags().Lookup(name)
		if flag == nil || !strings.HasPrefix(flag.Usage, "_[") {
			t.Errorf("flag %s is missing typed Markdown help", name)
		}
	}
}

func TestCostsOptionsValidate(t *testing.T) {
	for _, test := range []struct {
		name    string
		options costsOptions
		valid   bool
	}{
		{"valid", costsOptions{from: "2026-10-01", to: "2026-10-31", limit: 100}, true},
		{"missing from", costsOptions{to: "2026-10-31", limit: 100}, false},
		{"invalid day", costsOptions{from: "2026-02-30", to: "2026-10-31", limit: 100}, false},
		{"reverse range", costsOptions{from: "2026-10-31", to: "2026-10-01", limit: 100}, false},
		{"zero limit", costsOptions{from: "2026-10-01", to: "2026-10-31", limit: 0}, false},
		{"excessive limit", costsOptions{from: "2026-10-01", to: "2026-10-31", limit: 101}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.options.validate()
			if (err == nil) != test.valid {
				t.Fatalf("validate() error = %v, valid = %t", err, test.valid)
			}
		})
	}
}
