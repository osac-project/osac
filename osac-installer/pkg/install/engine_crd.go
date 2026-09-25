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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// customResourceDefinitionGVR identifies the built-in
// CustomResourceDefinition resource itself (apiextensions.k8s.io/v1), used
// to check whether a given CRD is registered on the target cluster.
var customResourceDefinitionGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

// checkCRDExists reports whether the named CustomResourceDefinition is
// registered on the cluster. remediation is included in the failure message
// verbatim, so it should read naturally after "CRD %q not found: ".
func checkCRDExists(ctx context.Context, clients *Clients, name, remediation string) (Status, string) {
	_, err := clients.Dynamic.Resource(customResourceDefinitionGVR).Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return Failed, fmt.Sprintf("CRD %q not found: %s", name, remediation)
	case err != nil:
		return Failed, fmt.Sprintf("failed to check for CRD %q: %v", name, err)
	default:
		return Pass, fmt.Sprintf("CRD %q found", name)
	}
}

// buildCRDExistsCheck builds the "crd-exists" engine: entry.CRD must be
// registered on the cluster.
func buildCRDExistsCheck(entry matrixEntry) (Check, error) {
	if entry.CRD == "" {
		return Check{}, fmt.Errorf(`"crd-exists" requires "crd"`)
	}
	severity, err := parseSeverity(entry.Severity)
	if err != nil {
		return Check{}, err
	}
	category, err := parseCategory(entry.Category)
	if err != nil {
		return Check{}, err
	}
	return Check{
		Name:        entry.ID,
		Description: fmt.Sprintf("CRD %q is registered", entry.CRD),
		Severity:    severity,
		Category:    category,
		Run: func(ctx context.Context, clients *Clients) (Status, string) {
			return checkCRDExists(ctx, clients, entry.CRD, entry.Remediation)
		},
	}, nil
}
