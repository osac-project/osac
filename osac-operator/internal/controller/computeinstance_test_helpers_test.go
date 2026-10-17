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

package controller

import (
	"time"

	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mc "sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// Existing ComputeInstance tests use the manual storage-controller path unless
// a test explicitly opts into the storage readiness prerequisite.
func newTestComputeInstanceReconciler(
	mgr mcmanager.Manager,
	computeInstanceNamespace string,
	tenantNamespace string,
	networkingNamespace string,
	provisioningProvider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration,
	maxJobHistory int,
	targetCluster mc.ClusterName,
) *ComputeInstanceReconciler {
	return NewComputeInstanceReconciler(
		mgr,
		computeInstanceNamespace,
		tenantNamespace,
		networkingNamespace,
		provisioningProvider,
		statusPollInterval,
		maxJobHistory,
		targetCluster,
		NewTenantReadinessEvaluator(false),
	)
}
