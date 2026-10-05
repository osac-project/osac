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

package feedback

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// FieldIssue describes billing source fields that must not be included in a
// feedback update. Other status fields can still be saved.
type FieldIssue struct {
	Paths []string
	Err   error
}

// FieldIssues is a non-fatal result from syncing one or more source fields.
type FieldIssues []FieldIssue

func (issues FieldIssues) Error() string {
	messages := make([]string, 0, len(issues))
	for _, issue := range issues {
		messages = append(messages, fmt.Sprintf("%s: %v", strings.Join(issue.Paths, ", "), issue.Err))
	}
	return strings.Join(messages, "; ")
}

// Err returns nil when there are no issues, otherwise issues itself.
func (issues FieldIssues) Err() error {
	if len(issues) == 0 {
		return nil
	}
	return issues
}

// AsFieldIssues recognizes a non-fatal field-sync result, including wrapped
// results.
func AsFieldIssues(err error) (FieldIssues, bool) {
	var issues FieldIssues
	if !errors.As(err, &issues) {
		return nil, false
	}
	return issues, true
}

// ExcludeUpdateMaskPaths omits only the requested paths from a field mask.
func ExcludeUpdateMaskPaths(paths, excluded []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if !slices.Contains(excluded, path) {
			result = append(result, path)
		}
	}
	return result
}
