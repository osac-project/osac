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

	"github.com/Masterminds/semver/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// clusterVersionGVR identifies OpenShift's cluster-scoped ClusterVersion
// resource (the singleton object named "version"), used to read the
// cluster's OpenShift version.
var clusterVersionGVR = schema.GroupVersionResource{
	Group:    "config.openshift.io",
	Version:  "v1",
	Resource: "clusterversions",
}

// buildOCPVersionRangeCheck builds the "ocp-version-range" engine: the
// cluster's status.desired.version must be at least entry.MinVersion.
func buildOCPVersionRangeCheck(entry matrixEntry) (Check, error) {
	if entry.MinVersion == "" {
		return Check{}, fmt.Errorf(`"ocp-version-range" requires "minVersion"`)
	}
	severity, err := parseSeverity(entry.Severity)
	if err != nil {
		return Check{}, err
	}
	category, err := parseCategory(entry.Category)
	if err != nil {
		return Check{}, err
	}
	minVersion, err := semver.NewVersion(entry.MinVersion)
	if err != nil {
		return Check{}, fmt.Errorf("invalid minVersion %q: %w", entry.MinVersion, err)
	}
	return Check{
		Name:        entry.ID,
		Category:    category,
		Description: fmt.Sprintf("the cluster is running OpenShift %s or later", minVersion),
		Severity:    severity,
		Run: func(ctx context.Context, clients *Clients) (Status, string) {
			obj, err := clients.Dynamic.Resource(clusterVersionGVR).Get(ctx, "version", metav1.GetOptions{})
			if err != nil {
				return Failed, fmt.Sprintf("failed to read the cluster's ClusterVersion: %v", err)
			}
			versionStr, _, _ := unstructured.NestedString(obj.Object, "status", "desired", "version")
			actual, err := semver.NewVersion(versionStr)
			if err != nil {
				return Failed, fmt.Sprintf("cluster reports an unparseable OpenShift version %q: %v", versionStr, err)
			}
			if actual.LessThan(minVersion) {
				return Failed, fmt.Sprintf("cluster is running OpenShift %s, older than the validated minimum %s", actual, minVersion)
			}
			return Pass, fmt.Sprintf("cluster is running OpenShift %s", actual)
		},
	}, nil
}
