/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package secretref parses the `--require-secret` flag value shared by
// `osac install discover` and `osac install validate` into
// install.SecretRef values.
package secretref

import (
	"fmt"
	"strings"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

// Parse parses one `--require-secret` value: "namespace/name" or
// "namespace/name:key1,key2".
func Parse(raw string) (install.SecretRef, error) {
	namespace, rest, found := strings.Cut(raw, "/")
	if !found || namespace == "" {
		return install.SecretRef{}, fmt.Errorf("%q: want NAMESPACE/NAME[:KEY,...]", raw)
	}
	name, keysPart, _ := strings.Cut(rest, ":")
	if name == "" {
		return install.SecretRef{}, fmt.Errorf("%q: want NAMESPACE/NAME[:KEY,...]", raw)
	}
	var keys []string
	if keysPart != "" {
		keys = strings.Split(keysPart, ",")
	}
	return install.SecretRef{Namespace: namespace, Name: name, Keys: keys}, nil
}

// ParseAll parses every `--require-secret` value, stopping at the first
// invalid one.
func ParseAll(raw []string) ([]install.SecretRef, error) {
	refs := make([]install.SecretRef, 0, len(raw))
	for _, value := range raw {
		ref, err := Parse(value)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}
