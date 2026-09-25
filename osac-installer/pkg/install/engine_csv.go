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
	"strings"

	"github.com/Masterminds/semver/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// clusterServiceVersionGVR identifies OLM's ClusterServiceVersion resource,
// used to check whether an Operator is installed and its CSV has reached
// the Succeeded phase.
var clusterServiceVersionGVR = schema.GroupVersionResource{
	Group:    "operators.coreos.com",
	Version:  "v1alpha1",
	Resource: "clusterserviceversions",
}

// buildCSVSucceededCheck builds the "csv-succeeded" engine: an Operator
// whose OLM package is entry.PackagePrefix must have a ClusterServiceVersion
// in entry.Namespace with status.phase Succeeded, and, if entry.MinVersion
// is set, spec.version must be at least that version.
func buildCSVSucceededCheck(entry matrixEntry) (Check, error) {
	if entry.Namespace == "" || entry.PackagePrefix == "" {
		return Check{}, fmt.Errorf(`"csv-succeeded" requires "namespace" and "packagePrefix"`)
	}
	severity, err := parseSeverity(entry.Severity)
	if err != nil {
		return Check{}, err
	}
	category, err := parseCategory(entry.Category)
	if err != nil {
		return Check{}, err
	}
	var minVersion *semver.Version
	if entry.MinVersion != "" {
		minVersion, err = semver.NewVersion(entry.MinVersion)
		if err != nil {
			return Check{}, fmt.Errorf("invalid minVersion %q: %w", entry.MinVersion, err)
		}
	}
	name := entry.DisplayName
	if name == "" {
		name = entry.ID
	}
	docsSuffix := ""
	if entry.DocsURL != "" {
		docsSuffix = " (" + entry.DocsURL + ")"
	}
	return Check{
		Name:        entry.ID,
		Description: fmt.Sprintf("%s is installed and ready", name),
		Severity:    severity,
		Category:    category,
		Run: func(ctx context.Context, clients *Clients) (Status, string) {
			list, err := clients.Dynamic.Resource(clusterServiceVersionGVR).Namespace(entry.Namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				return Failed, fmt.Sprintf("failed to list ClusterServiceVersions in namespace %q: %v", entry.Namespace, err)
			}
			var found *unstructured.Unstructured
			for i := range list.Items {
				if strings.HasPrefix(list.Items[i].GetName(), entry.PackagePrefix+".") {
					found = &list.Items[i]
					break
				}
			}
			if found == nil {
				return Failed, fmt.Sprintf(
					"%s is not installed: no ClusterServiceVersion named %q.* found in namespace %q; "+
						"install it before deploying OSAC%s", name, entry.PackagePrefix, entry.Namespace, docsSuffix)
			}
			phase, _, _ := unstructured.NestedString(found.Object, "status", "phase")
			if phase != "Succeeded" {
				return Failed, fmt.Sprintf("%s CSV %q is not ready yet (status.phase=%q, want Succeeded)", name, found.GetName(), phase)
			}
			if minVersion == nil {
				return Pass, fmt.Sprintf("%s is installed and ready (%s)", name, found.GetName())
			}
			versionStr, _, _ := unstructured.NestedString(found.Object, "spec", "version")
			actual, err := semver.NewVersion(versionStr)
			if err != nil {
				return Failed, fmt.Sprintf("%s CSV %q has an unparseable spec.version %q: %v", name, found.GetName(), versionStr, err)
			}
			if actual.LessThan(minVersion) {
				return Failed, fmt.Sprintf("%s is version %s, older than the minimum supported version %s", name, actual, minVersion)
			}
			return Pass, fmt.Sprintf("%s is installed and ready (%s, version %s)", name, found.GetName(), actual)
		},
	}, nil
}
