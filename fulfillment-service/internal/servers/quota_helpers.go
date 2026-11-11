/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
)

// jsonToMap reads a jsonb document of string values, as the labels and annotations columns hold.
func jsonToMap(data []byte) map[string]string {
	if len(data) == 0 {
		return nil
	}
	result := map[string]string{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	return result
}

// fnv32a returns the FNV-32a hash of the value, printed as hex.
func fnv32a(value string) uint32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum32()
}

// quotaNameFromDimension converts a dimension to a DNS-1123 name: lowercased, non-alphanumerics
// dashed, trimmed, and suffixed with a short hash of the original dimension so two dimensions that
// sanitize to the same base still get different names.
func quotaNameFromDimension(dimension string) string {
	var sanitized strings.Builder
	for _, r := range strings.ToLower(dimension) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sanitized.WriteRune(r)
		} else {
			sanitized.WriteRune('-')
		}
	}
	base := strings.Trim(sanitized.String(), "-")
	if len(base) > 54 {
		base = base[:54]
	}
	return fmt.Sprintf("%s-%x", base, fnv32a(dimension))
}
