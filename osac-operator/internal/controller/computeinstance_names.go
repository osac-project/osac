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
)

const (
	defaultComputeInstanceNamespace string = "osac-computeinstance"

	computeInstanceControllerName = "computeinstance-controller"

	eventReasonTenantNotReady      = "TenantNotReady"
	eventReasonProvisioningStorage = "ProvisioningStorage"
	eventReasonInfrastructureReady = "InfrastructureReady"
	eventReasonProvisioningFailed  = "ProvisioningFailed"
	eventReasonReady               = "Ready"

	eventActionReconcile = "Reconcile"
)

var (
	osacComputeInstanceNameLabel                 string = fmt.Sprintf("%s/computeinstance", osacPrefix)
	osacComputeInstanceIDLabel                   string = fmt.Sprintf("%s/computeinstance-uuid", osacPrefix)
	osacComputeInstanceFinalizer                 string = fmt.Sprintf("%s/computeinstance", osacPrefix)
	osacComputeInstanceFeedbackFinalizer         string = fmt.Sprintf("%s/computeinstance-feedback", osacPrefix)
	osacComputeInstanceManagementStateAnnotation string = fmt.Sprintf("%s/management-state", osacPrefix)
	osacSubnetTargetNamespaceAnnotation          string = fmt.Sprintf("%s/subnet-target-namespace", osacPrefix)
)
