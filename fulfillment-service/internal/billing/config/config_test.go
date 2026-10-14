/*
Copyright (c) 2026 Red Hat Inc.

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

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osac-project/osac/fulfillment-service/internal/billing/providers/m360"
)

func TestBuildReturnsNoProviderWhenBillingIsDisabled(t *testing.T) {
	key, provider, err := (Config{}).Build()
	if err != nil {
		t.Fatal(err)
	}
	if key != "" || provider != nil {
		t.Fatalf("Build() = (%q, %T), want empty provider", key, provider)
	}
}

func TestBuildRejectsProviderSettingsWithoutProvider(t *testing.T) {
	_, _, err := (Config{M360APIURL: "https://billing.example"}).Build()
	if err == nil {
		t.Fatal("Build() succeeded with M360 settings and no provider")
	}
}

func TestBuildLoadsM360APIKeyFromFile(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "api-key")
	if err := os.WriteFile(keyFile, []byte("test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, provider, err := (Config{
		ProviderKey:    "m360",
		M360APIURL:     "https://billing.example",
		M360APIVersion: "v1",
		M360APIKeyFile: keyFile,
	}).Build()
	if err != nil {
		t.Fatal(err)
	}
	if key != "m360" {
		t.Fatalf("provider key = %q, want m360", key)
	}
	if _, ok := provider.(*m360.Client); !ok {
		t.Fatalf("provider type = %T, want *m360.Client", provider)
	}
}
