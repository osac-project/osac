/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package gvks

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var HostedCluster = schema.GroupVersionKind{
	Group:   "hypershift.openshift.io",
	Version: "v1beta1",
	Kind:    "HostedCluster",
}

var HostedClusterList = listGVK(HostedCluster)

var Secret = schema.GroupVersionKind{
	Version: "v1",
	Kind:    "Secret",
}

func listGVK(gvk schema.GroupVersionKind) schema.GroupVersionKind {
	gvk.Kind = gvk.Kind + "List"
	return gvk
}
