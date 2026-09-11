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
	"fmt"
)

// ErrProviderNotImplemented identifies a provider that has no registered
// VendorProvisioner implementation.
var ErrProviderNotImplemented = errors.New("provider not implemented")

// ProviderNotImplementedError reports the provider selected for a volume when
// no implementation is registered for it.
type ProviderNotImplementedError struct {
	Provider string
}

func (e *ProviderNotImplementedError) Error() string {
	return fmt.Sprintf("provider %q is not implemented", e.Provider)
}

func (e *ProviderNotImplementedError) Unwrap() error {
	return ErrProviderNotImplemented
}

// VendorProvisionerRegistry selects a provider-specific implementation by the
// provider resolved by the fulfillment-service.
type VendorProvisionerRegistry map[string]VendorProvisioner

// Lookup returns the implementation registered for provider. A nil or absent
// entry is treated as unimplemented so a partially populated registry cannot
// result in a nil-interface panic during reconciliation.
func (r VendorProvisionerRegistry) Lookup(provider string) (VendorProvisioner, error) {
	provisioner, ok := r[provider]
	if !ok || provisioner == nil {
		return nil, &ProviderNotImplementedError{Provider: provider}
	}
	return provisioner, nil
}
