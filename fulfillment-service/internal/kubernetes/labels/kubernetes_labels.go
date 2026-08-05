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

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
)

var group = osacv1alpha1.GroupVersion.Group

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

// TenantUuid is the label where the fulfillment API will write the identifier of the tenant.
var TenantUuid = fmt.Sprintf("%s/%s", group, "tenant-uuid")

// TenantRef is the label used to reference the tenant object from associated resources (e.g., namespaces).
var TenantRef = fmt.Sprintf("%s/%s", group, "tenant-ref")

// Project is the label used to reference the project (namespace) in which the tenant object lives.
var Project = fmt.Sprintf("%s/%s", group, "project")
