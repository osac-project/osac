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
	"errors"
	"testing"
)

func TestVendorProvisionerRegistryLookup(t *testing.T) {
	provisioner := NewMockVendorProvisioner()
	registry := VendorProvisionerRegistry{"vast": provisioner}

	got, err := registry.Lookup("vast")
	if err != nil {
		t.Fatalf("Lookup(vast) error: %v", err)
	}
	if got != provisioner {
		t.Fatalf("Lookup(vast) returned %p, want %p", got, provisioner)
	}

	_, err = registry.Lookup("netapp")
	if err == nil {
		t.Fatal("Lookup(netapp) returned nil error")
	}
	if !errors.Is(err, ErrProviderNotImplemented) {
		t.Fatalf("Lookup(netapp) error = %v, want ErrProviderNotImplemented", err)
	}
	var notImplemented *ProviderNotImplementedError
	if !errors.As(err, &notImplemented) {
		t.Fatalf("Lookup(netapp) error = %T, want ProviderNotImplementedError", err)
	}
	if notImplemented.Provider != "netapp" {
		t.Errorf("ProviderNotImplementedError.Provider = %q, want netapp", notImplemented.Provider)
	}

	if _, err := (VendorProvisionerRegistry{}).Lookup("vast"); !errors.Is(err, ErrProviderNotImplemented) {
		t.Fatalf("empty registry Lookup error = %v, want ErrProviderNotImplemented", err)
	}
}
