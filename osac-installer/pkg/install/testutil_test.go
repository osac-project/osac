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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

// provisioningGVR identifies Metal3's cluster-scoped Provisioning custom
// resource, which configures BareMetalOperator. Production code doesn't
// need a package-level var for it (the GVR comes from the matrix entry
// instead), but the fake dynamic client's listKinds map needs it either way.
var provisioningGVR = schema.GroupVersionResource{
	Group:    "metal3.io",
	Version:  "v1alpha1",
	Resource: "provisionings",
}

// newFakeClients builds Clients backed by fake typed and dynamic clientsets.
// Every check in this package reads cluster state exclusively through
// Clients, never a concrete client type, so these fakes exercise the exact
// same code path a real cluster would.
func newFakeClients(typedObjects, dynamicObjects []runtime.Object) *Clients {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		customResourceDefinitionGVR: "CustomResourceDefinitionList",
		provisioningGVR:             "ProvisioningList",
		clusterServiceVersionGVR:    "ClusterServiceVersionList",
		clusterVersionGVR:           "ClusterVersionList",
	}
	return &Clients{
		Typed:   fake.NewSimpleClientset(typedObjects...),
		Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, dynamicObjects...),
	}
}

func newUnstructuredCRD(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata": map[string]any{
				"name": name,
			},
		},
	}
}

func newUnstructuredProvisioning(name string, watchAllNamespaces bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "metal3.io/v1alpha1",
			"kind":       "Provisioning",
			"metadata": map[string]any{
				"name": name,
			},
			"spec": map[string]any{
				"watchAllNamespaces": watchAllNamespaces,
			},
		},
	}
}

// newUnstructuredCSV builds a namespaced ClusterServiceVersion with the
// given name (conventionally "<packagePrefix>.v<version>"), phase, and
// spec.version.
func newUnstructuredCSV(namespace, name, phase, version string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "ClusterServiceVersion",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]any{
				"version": version,
			},
			"status": map[string]any{
				"phase": phase,
			},
		},
	}
}

// newUnstructuredClusterVersion builds OpenShift's cluster-scoped
// ClusterVersion singleton (name "version") reporting the given desired
// version.
func newUnstructuredClusterVersion(desiredVersion string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "ClusterVersion",
			"metadata": map[string]any{
				"name": "version",
			},
			"status": map[string]any{
				"desired": map[string]any{
					"version": desiredVersion,
				},
			},
		},
	}
}
