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

package it

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestContainsPullSecretPayload(t *testing.T) {
	escaped, err := json.Marshal(dockerConfigJSON)
	if err != nil {
		t.Fatal("cannot encode synthetic fixture")
	}
	cases := []struct {
		name     string
		encoded  []byte
		contains bool
	}{
		{
			name:    "reference only",
			encoded: []byte(`{"spec":{"pullSecretRef":{"name":"fixture-secret"},"pullSecretRevision":"fixture-revision"}}`),
		},
		{
			name:     "raw payload",
			encoded:  []byte(dockerConfigJSON),
			contains: true,
		},
		{
			name:     "escaped payload in spec",
			encoded:  append(append([]byte(`{"spec":{"pullSecret":`), escaped...), []byte(`}}`)...),
			contains: true,
		},
		{
			name:     "escaped payload outside spec",
			encoded:  append(append([]byte(`{"metadata":{"annotations":{"fixture":`), escaped...), []byte(`}}}`)...),
			contains: true,
		},
		{
			name:     "base64 payload outside spec",
			encoded:  []byte(`{"metadata":{"annotations":{"fixture":"` + base64.StdEncoding.EncodeToString([]byte(dockerConfigJSON)) + `"}}}`),
			contains: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if containsPullSecretPayload(testCase.encoded, dockerConfigJSON) != testCase.contains {
				t.Fatal("unexpected synthetic credential detection result")
			}
		})
	}
}
