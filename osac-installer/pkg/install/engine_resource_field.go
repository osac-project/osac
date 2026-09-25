/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	"context"
	"fmt"
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// buildResourceFieldEqualsCheck builds the "resource-field-equals" engine:
// the first resource found for the given GVR must have entry.Field set to
// entry.Expected. Used today for Metal3's cluster-scoped Provisioning CR
// (spec.watchAllNamespaces), but not specific to it -- any single,
// effectively-singleton custom resource with a field to assert on fits.
func buildResourceFieldEqualsCheck(entry matrixEntry) (Check, error) {
	if entry.Group == "" || entry.Version == "" || entry.Resource == "" || len(entry.Field) == 0 {
		return Check{}, fmt.Errorf(`"resource-field-equals" requires "group", "version", "resource", and "field"`)
	}
	severity, err := parseSeverity(entry.Severity)
	if err != nil {
		return Check{}, err
	}
	category, err := parseCategory(entry.Category)
	if err != nil {
		return Check{}, err
	}
	gvr := schema.GroupVersionResource{Group: entry.Group, Version: entry.Version, Resource: entry.Resource}
	notFoundMessage := entry.NotFoundMessage
	if notFoundMessage == "" {
		notFoundMessage = fmt.Sprintf("no %s resource found", entry.Resource)
	}
	mismatchMessage := entry.MismatchMessage
	if mismatchMessage == "" {
		mismatchMessage = fmt.Sprintf("%s does not have %v set to %v", entry.Resource, entry.Field, entry.Expected)
	}
	return Check{
		Name:        entry.ID,
		Description: fmt.Sprintf("%s has %v set to %v", entry.Resource, entry.Field, entry.Expected),
		Severity:    severity,
		Category:    category,
		Run: func(ctx context.Context, clients *Clients) (Status, string) {
			list, err := clients.Dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
			if err != nil {
				return Failed, fmt.Sprintf("failed to list %s: %v", entry.Resource, err)
			}
			if len(list.Items) == 0 {
				return Failed, notFoundMessage
			}
			actual, found, err := unstructured.NestedFieldNoCopy(list.Items[0].Object, entry.Field...)
			if err != nil {
				return Failed, fmt.Sprintf("failed to read %v on %s: %v", entry.Field, entry.Resource, err)
			}
			if !found || !reflect.DeepEqual(actual, entry.Expected) {
				return Failed, mismatchMessage
			}
			return Pass, fmt.Sprintf("%s has %v set to %v", entry.Resource, entry.Field, entry.Expected)
		},
	}, nil
}
