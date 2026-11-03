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

package networktask

import (
	"testing"
)

func TestResourceGenerationUsesStoredSnapshot(t *testing.T) {
	input := map[string]any{
		"resource": map[string]any{
			"metadata": map[string]any{"generation": float64(3)},
		},
	}

	generation, err := ResourceGeneration(input)
	if err != nil {
		t.Fatalf("ResourceGeneration() error = %v", err)
	}
	if generation != 3 {
		t.Fatalf("ResourceGeneration() = %d, want 3", generation)
	}
}

func TestResourceGenerationRejectsMissingOrInvalidGeneration(t *testing.T) {
	tests := map[string]map[string]any{
		"missing resource":       {},
		"missing metadata":       {"resource": map[string]any{}},
		"missing generation":     {"resource": map[string]any{"metadata": map[string]any{}}},
		"non-integer generation": {"resource": map[string]any{"metadata": map[string]any{"generation": 1.5}}},
		"negative generation":    {"resource": map[string]any{"metadata": map[string]any{"generation": -1}}},
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ResourceGeneration(input)
			if err == nil {
				t.Fatal("ResourceGeneration() succeeded; want an error")
			}
		})
	}
}
