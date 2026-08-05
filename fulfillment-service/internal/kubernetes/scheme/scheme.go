/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package scheme

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	bmfov1alpha1 "github.com/osac-project/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
)

// NewHub creates a runtime.Scheme with OSAC CRD types and core Kubernetes types
// registered. Use this for any Kubernetes client that interacts with hub clusters.
func NewHub() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := osacv1alpha1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := bmfov1alpha1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := corev1.AddToScheme(s); err != nil {
		return nil, err
	}
	return s, nil
}
