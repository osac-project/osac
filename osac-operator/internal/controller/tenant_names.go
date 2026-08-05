/*
Copyright 2025.

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

package controller

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// defaultStorageClassSentinel is the label value that marks a shared StorageClass
	// available to all tenants. No Tenant CR can be named "Default" because uppercase
	// is forbidden in Kubernetes resource names.
	defaultStorageClassSentinel = "Default"

	// tenantControllerName is the name used when creating the event recorder
	tenantControllerName = "tenant-controller"

	// eventReasonDuplicateStorageClass is the event reason emitted when multiple
	// StorageClasses match a tenant
	eventReasonDuplicateStorageClass = "DuplicateStorageClass"

	// eventActionDetectDuplicate is the event action for duplicate StorageClass detection
	eventActionDetectDuplicate = "DetectDuplicate"

	// eventReasonMissingStorageTier is the event reason emitted when a Tier API-defined
	// tier has no matching resolved StorageClass
	eventReasonMissingStorageTier = "MissingStorageTier"

	// eventActionDetectMissingTier is the event action for missing storage tier detection
	eventActionDetectMissingTier = "DetectMissingTier"
)

var (
	// osacTenantRefLabel the label used to reference the tenant object
	osacTenantRefLabel string = fmt.Sprintf("%s/tenant-ref", osacPrefix)

	// osacProjectRefLabel is the label used to reference the project in which the tenant obehct lives
	osacProjectRefLabel string = fmt.Sprintf("%s/project", osacPrefix)

	// osacTenantKey is the key used to associate resources with a tenant.
	// Used as a label on StorageClasses/Secrets and as an annotation on ComputeInstances.
	osacTenantKey string = fmt.Sprintf("%s/tenant", osacPrefix)

	// osacStorageTierLabel is the label key that identifies the storage tier of a StorageClass
	osacStorageTierLabel string = fmt.Sprintf("%s/storage-tier", osacPrefix)
)

func tenantNamespacePredicate(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(
		func(obj client.Object) bool {
			return obj.GetNamespace() == namespace
		},
	)
}

func tenantLabelSelector(project string) metav1.LabelSelector {
	return metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      osacTenantRefLabel,
				Operator: metav1.LabelSelectorOpExists,
			},
			{
				Key:      osacProjectRefLabel,
				Operator: metav1.LabelSelectorOpIn,
				Values:   []string{project},
			},
		},
	}
}

func storageClassTenantPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		_, exists := obj.GetLabels()[osacTenantKey]
		return exists
	})
}
