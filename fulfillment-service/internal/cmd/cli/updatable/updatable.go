/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package updatable contains shared validation for CLI commands that mutate resources.
package updatable

import (
	"fmt"

	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
)

// Ensure rejects resources that don't expose an Update operation.
func Ensure(helper reflection.ObjectHelper) error {
	if helper.IsUpdatable() {
		return nil
	}
	return fmt.Errorf("object type %q is immutable; updates are not supported", helper.FullName())
}
