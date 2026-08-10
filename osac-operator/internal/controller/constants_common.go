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

const (
	// ManagementStateManual indicates that the resource should not be managed by the controller
	ManagementStateManual = "manual"

	// ManagementStateUnmanaged indicates that the resource should be ignored by the controller
	ManagementStateUnmanaged = "unmanaged"

	// osacAppName is the name of the osac application
	osacAppName string = "osac-operator"

	// osacPrefix is the prefix used to identify osac resources
	osacPrefix string = "osac.openshift.io"

	// osacImplementationStrategyAnnotation is the annotation key for the network implementation strategy.
	// This is derived from the NetworkClass and used by AAP playbooks to select the appropriate role
	// (e.g., "cudn" -> cudn_virtual_network or cudn_subnet role).
	osacImplementationStrategyAnnotation = osacPrefix + "/implementation-strategy"

	// osacK8sImplementationStrategyAnnotation persists the k8s manager's name for
	// dual-dispatch resources (currently only Subnet). Set in handleUpdate alongside
	// osacImplementationStrategyAnnotation (which always holds the fabric manager's
	// name for these resources) whenever the resolved DispatchPlan has a k8s target,
	// and read directly by handleDeprovisioning to build the k8s DeprovisionTarget
	// without re-resolving the DispatchPlan against a parent VirtualNetwork that may
	// already be gone at delete time.
	osacK8sImplementationStrategyAnnotation = osacPrefix + "/k8s-implementation-strategy"

	// osacExternalIPPoolNameAnnotation is the K8s resource name of the parent ExternalIPPool.
	// set on ExternalIP CRs
	osacExternalIPPoolNameAnnotation = osacPrefix + "/externalippool-name"

	// defaultExternalIPPoolImplementationStrategy is the fallback strategy when none is specified.
	// Used by ExternalIPPool (from its own spec) and ExternalIP (inherited from parent pool).
	defaultExternalIPPoolImplementationStrategy = "metallb-l2"

	// defaultSecurityGroupImplementationStrategy is the fallback implementation strategy
	// for SecurityGroup (standard Kubernetes NetworkPolicy) when neither the SecurityGroup
	// spec nor the parent VirtualNetwork's NetworkClass (via the dispatcher path) resolve
	// one.
	defaultSecurityGroupImplementationStrategy = "network_policy"

	conditionReasonConfigurationApplied  = "ConfigurationApplied"
	conditionMessageConfigurationApplied = "Controller has processed the current spec"
)
