/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package rendering

import (
	"fmt"
)

// The output formats every read command accepts on its --output flag.
const (
	Table = "table"
	JSON  = "json"
	YAML  = "yaml"
)

// ValidateFormat rejects an unknown output format. Commands call it before doing any work.
func ValidateFormat(format string) error {
	if format != Table && format != JSON && format != YAML {
		return fmt.Errorf("unknown output format '%s', should be '%s', '%s' or '%s'",
			format, Table, JSON, YAML)
	}
	return nil
}
