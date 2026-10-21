/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package labels

import (
	"fmt"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var group = osacv1alpha1.GroupVersion.Group

// AutoCreated marks resources created by OSAC as part of another resource's lifecycle.
const AutoCreated = "osac.openshift.io/auto-created"

// AutoCreatedFor identifies the resource that caused an automatically created resource.
const AutoCreatedFor = "osac.openshift.io/auto-created-for"

// ClusterOrderUuid is the label where the fulfillment API will write the identifier of the order.
var ClusterOrderUuid = fmt.Sprintf("%s/%s", group, "clusterorder-uuid")

// ComputeInstanceUuid is the label where the fulfillment API will write the identifier of the compute instance.
var ComputeInstanceUuid = fmt.Sprintf("%s/%s", group, "computeinstance-uuid")

// InstanceTypeName is the label where the fulfillment API will write the name of the instance type.
var InstanceTypeName = fmt.Sprintf("%s/%s", group, "instance-type-name")

// SubnetUuid is the label where the fulfillment API will write the identifier of the subnet.
var SubnetUuid = fmt.Sprintf("%s/%s", group, "subnet-uuid")

// VirtualNetworkUuid is the label where the fulfillment API will write the identifier of the virtual network.
var VirtualNetworkUuid = fmt.Sprintf("%s/%s", group, "virtualnetwork-uuid")

// FabricDomainUuid is the label where the fulfillment API writes the fabric domain identifier.
var FabricDomainUuid = fmt.Sprintf("%s/%s", group, "fabricdomain-uuid")

// NetworkClassUuid is the label where the fulfillment API will write the identifier of the network class.
var NetworkClassUuid = fmt.Sprintf("%s/%s", group, "networkclass-uuid")

// SecurityGroupUuid is the label where the fulfillment API will write the identifier of the security group.
var SecurityGroupUuid = fmt.Sprintf("%s/%s", group, "securitygroup-uuid")

// BareMetalInstanceUuid is the label where the fulfillment API will write the identifier of the bare metal instance.
var BareMetalInstanceUuid = fmt.Sprintf("%s/%s", group, "baremetalinstance-uuid")

// ExternalIPPoolUuid is the label where the fulfillment API will write the identifier of the external IP pool.
var ExternalIPPoolUuid = fmt.Sprintf("%s/%s", group, "externalippool-uuid")

// ExternalIPUuid is the label where the fulfillment API will write the identifier of the external IP.
var ExternalIPUuid = fmt.Sprintf("%s/%s", group, "externalip-uuid")

// ExternalIPAttachmentUuid is the label where the fulfillment API will write the identifier of the external IP attachment.
var ExternalIPAttachmentUuid = fmt.Sprintf("%s/%s", group, "externalipattachment-uuid")

// NATGatewayUuid is the label where the fulfillment API will write the identifier of the NAT gateway.
var NATGatewayUuid = fmt.Sprintf("%s/%s", group, "natgateway-uuid")

// TenantUuid is the legacy label where the fulfillment API writes the tenant name.
var TenantUuid = fmt.Sprintf("%s/%s", group, "tenant-uuid")

// TenantID is the label where the fulfillment API writes the immutable tenant identifier.
var TenantID = fmt.Sprintf("%s/%s", group, "tenant-id")

// VolumeUuid is the label where the fulfillment API will write the identifier of the volume.
var VolumeUuid = fmt.Sprintf("%s/%s", group, "volume-uuid")

// TenantRef is the label used to reference the tenant object from associated resources (e.g., namespaces).
var TenantRef = fmt.Sprintf("%s/%s", group, "tenant-ref")

// Project is the label used to reference the project (namespace) in which the tenant object lives.
var Project = fmt.Sprintf("%s/%s", group, "project")

// SecretType is the label used to indicate the type of a system-created secret.
var SecretType = fmt.Sprintf("%s/%s", group, "secret-type")

const (
	autoCreatedLabel            = "osac.openshift.io/auto-created"
	autoCreatedForLabel         = "osac.openshift.io/auto-created-for"
	autoProvisionedLabel        = "osac.openshift.io/auto-provisioned"
	autoProvisionedForLabel     = "osac.openshift.io/auto-provisioned-for"
	autoAttachmentDeferredLabel = "osac.openshift.io/auto-attachment-deferred"
	autoCreatedKindLabel        = "osac.openshift.io/auto-created-kind"
	autoCreatedEndpointLabel    = "osac.openshift.io/auto-created-endpoint"
)

var externalIPLifecycleLabels = []string{
	autoCreatedLabel,
	autoCreatedForLabel,
	autoProvisionedLabel,
	autoProvisionedForLabel,
	autoAttachmentDeferredLabel,
	autoCreatedKindLabel,
	autoCreatedEndpointLabel,
}

// MergeTrustedExternalIPLifecycleLabels updates only the recognized auto-network lifecycle labels.
// It preserves unrelated labels already placed on the Kubernetes object, but never copies arbitrary
// fulfillment metadata labels. The caller must set trusted only for server-created resources.
func MergeTrustedExternalIPLifecycleLabels(current, metadataLabels map[string]string, trusted bool) map[string]string {
	merged := make(map[string]string, len(current)+len(externalIPLifecycleLabels))
	for key, value := range current {
		merged[key] = value
	}
	for _, key := range externalIPLifecycleLabels {
		delete(merged, key)
	}
	if !trusted {
		return merged
	}
	for _, key := range externalIPLifecycleLabels {
		if value, ok := metadataLabels[key]; ok {
			merged[key] = value
		}
	}
	return merged
}
