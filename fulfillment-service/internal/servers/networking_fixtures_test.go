/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Fixtures shared by the networking server tests. They write records directly through DAOs, so the create gates of
// the servers don't apply to them.

func fixtureDAO[O dao.Object]() *dao.GenericDAO[O] {
	result, err := dao.NewGenericDAO[O]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return result
}

// controllerContext returns the suite context as seen by the fulfillment controller service account.
func controllerContext() context.Context {
	return auth.ContextWithSubject(ctx, &auth.Subject{User: "service-account-osac-controller"})
}

// fixtureAddressPlan is the default address plan: shares 12/16, 2/16 and 2/16, so the prefix limit is /25.
func fixtureAddressPlan() *privatev1.AddressPlan {
	return privatev1.AddressPlan_builder{Overlay: 0.75, Fabric: 0.125, LbVip: 0.125}.Build()
}

// seedNetworkClass creates the deployment NetworkClass in the given state.
func seedNetworkClass(state privatev1.NetworkClassState) *privatev1.NetworkClass {
	response, err := fixtureDAO[*privatev1.NetworkClass]().Create().SetObject(privatev1.NetworkClass_builder{
		Title:         "OSAC NetworkClass",
		FabricManager: "netris",
		Metadata: privatev1.Metadata_builder{
			Name:   "netris",
			Tenant: auth.SharedTenant,
		}.Build(),
		Spec: privatev1.NetworkClassSpec_builder{
			Defaults: privatev1.NetworkDefaults_builder{
				VirtualNetworkIpv4Cidr: "10.250.0.0/16",
				SubnetIpv4Cidr:         "10.250.0.0/24",
			}.Build(),
			EastWestCapabilities: privatev1.NetworkClassEastWestCapabilities_builder{
				SupportsEastWestEthernet: true,
			}.Build(),
			AddressPlan: fixtureAddressPlan(),
		}.Build(),
		Status: privatev1.NetworkClassStatus_builder{
			State: state,
			Hub:   "network-hub-a",
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return response.GetObject()
}

// seedVirtualNetwork creates a VirtualNetwork in the given tenant and state.
func seedVirtualNetwork(tenant, cidr string, state privatev1.VirtualNetworkState,
	labels map[string]string) *privatev1.VirtualNetwork {
	name := fmt.Sprintf("vn-%s", uuid.NewString()[:8])
	if labels[defaultLabel] == "true" {
		name = "default"
	}
	response, err := fixtureDAO[*privatev1.VirtualNetwork]().Create().SetObject(privatev1.VirtualNetwork_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   name,
			Tenant: tenant,
			Labels: labels,
		}.Build(),
		Spec: privatev1.VirtualNetworkSpec_builder{
			Ipv4Cidr: new(cidr),
		}.Build(),
		Status: privatev1.VirtualNetworkStatus_builder{
			State: state,
			Hub:   "network-hub-a",
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return response.GetObject()
}

// seedSubnet creates a Subnet of a VirtualNetwork in the given state.
func seedSubnet(vn *privatev1.VirtualNetwork, name, cidr string, state privatev1.SubnetState,
	labels map[string]string) *privatev1.Subnet {
	if name == "" {
		name = fmt.Sprintf("subnet-%s", uuid.NewString()[:8])
	}
	response, err := fixtureDAO[*privatev1.Subnet]().Create().SetObject(privatev1.Subnet_builder{
		Metadata: privatev1.Metadata_builder{
			Name:        name,
			Tenant:      vn.GetMetadata().GetTenant(),
			Labels:      labels,
			Annotations: map[string]string{"osac.openshift.io/owner-reference": vn.GetId()},
		}.Build(),
		Spec: privatev1.SubnetSpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
			Ipv4Cidr:       new(cidr),
		}.Build(),
		Status: privatev1.SubnetStatus_builder{
			State: state,
			Hub:   "network-hub-a",
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return response.GetObject()
}

// seedSecurityGroup creates a SecurityGroup of a VirtualNetwork in the given state.
func seedSecurityGroup(vn *privatev1.VirtualNetwork, name string, state privatev1.SecurityGroupState,
	labels map[string]string) *privatev1.SecurityGroup {
	if name == "" {
		name = fmt.Sprintf("sg-%s", uuid.NewString()[:8])
	}
	response, err := fixtureDAO[*privatev1.SecurityGroup]().Create().SetObject(privatev1.SecurityGroup_builder{
		Metadata: privatev1.Metadata_builder{
			Name:        name,
			Tenant:      vn.GetMetadata().GetTenant(),
			Labels:      labels,
			Annotations: map[string]string{"osac.openshift.io/owner-reference": vn.GetId()},
		}.Build(),
		Spec: privatev1.SecurityGroupSpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
		}.Build(),
		Status: privatev1.SecurityGroupStatus_builder{
			State: state,
			Hub:   "network-hub-a",
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return response.GetObject()
}

// seedExternalIPPool creates a READY IPv4 ExternalIPPool with the given CIDR.
func seedExternalIPPool(cidr string, available int64) *privatev1.ExternalIPPool {
	response, err := fixtureDAO[*privatev1.ExternalIPPool]().Create().SetObject(privatev1.ExternalIPPool_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   fmt.Sprintf("pool-%s", uuid.NewString()[:8]),
			Tenant: auth.SharedTenant,
		}.Build(),
		Spec: privatev1.ExternalIPPoolSpec_builder{
			Cidrs:    []string{cidr},
			IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
		}.Build(),
		Status: privatev1.ExternalIPPoolStatus_builder{
			State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
			Hub:       "network-hub-a",
			Total:     available,
			Available: available,
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return response.GetObject()
}

// defaultLabels marks a resource as a tenant default.
func defaultLabels() map[string]string {
	return map[string]string{defaultLabel: "true"}
}

// markTenantDeleting gives a tenant a finalizer and deletes it, so that it stays with a deletion timestamp.
func markTenantDeleting(id string) {
	tenants := fixtureDAO[*privatev1.Tenant]()
	response, err := tenants.Get().SetId(id).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	object := response.GetObject()
	object.GetMetadata().SetFinalizers([]string{"test"})
	_, err = tenants.Update().SetObject(object).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	_, err = tenants.Delete().SetId(id).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}

// defaultNetworkingFixture holds the IDs of the default networking that seedReadyDefaultNetworking creates.
type defaultNetworkingFixture struct {
	networkClassID   string
	virtualNetworkID string
	subnetID         string
	securityGroupID  string
	natGatewayID     string
	poolID           string
}

// seedReadyDefaultNetworking gives a tenant READY default networking, as the tenant controller would: the
// deployment NetworkClass (created when missing), the default VirtualNetwork 10.200.0.0/16, Subnet default-ipv4 and
// SecurityGroup default, and the tenant condition DEFAULT_NETWORKING_READY. When capacity is positive it also creates a
// READY NATGateway with its ExternalIP from a full ExternalIPPool (so that pool is never selected for automatic
// ExternalIPs), and a READY ExternalIPPool with that many available addresses for automatic ExternalIPs. It is
// idempotent.
func seedReadyDefaultNetworking(tenant string, capacity int64) defaultNetworkingFixture {
	var result defaultNetworkingFixture
	ensureFixtureTenant(tenant)
	networkClasses, err := fixtureDAO[*privatev1.NetworkClass]().List().SetLimit(1).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	if len(networkClasses.GetItems()) > 0 {
		result.networkClassID = networkClasses.GetItems()[0].GetId()
	} else {
		result.networkClassID = seedNetworkClass(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY).GetId()
	}

	vnDao := fixtureDAO[*privatev1.VirtualNetwork]()
	existing, err := vnDao.List().SetFilter(fmt.Sprintf(
		`this.metadata.tenant == %q && this.metadata.labels["%s"] == "true"`, tenant, defaultLabel)).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	var vn *privatev1.VirtualNetwork
	if len(existing.GetItems()) > 0 {
		vn = existing.GetItems()[0]
	} else {
		vn = seedVirtualNetwork(tenant, "10.200.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
			defaultLabels())
		seedSubnet(vn, defaultSubnetName, "10.200.0.0/24", privatev1.SubnetState_SUBNET_STATE_READY, defaultLabels())
		seedSecurityGroup(vn, "default", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, defaultLabels())

		if capacity <= 0 {
			markDefaultNetworkingReady(tenant)
			return seedReadyDefaultNetworkingIDs(tenant, result, vn)
		}
		result.natGatewayID = seedReadyNATGateway(tenant, vn.GetId(), true)
		result.poolID = seedExternalIPPool("203.0.113.0/24", capacity).GetId()
		markDefaultNetworkingReady(tenant)
	}
	return seedReadyDefaultNetworkingIDs(tenant, result, vn)
}

func seedReadyDefaultNetworkingIDs(tenant string, result defaultNetworkingFixture,
	vn *privatev1.VirtualNetwork) defaultNetworkingFixture {
	result.virtualNetworkID = vn.GetId()
	subnets, err := fixtureDAO[*privatev1.Subnet]().List().SetFilter(fmt.Sprintf(
		`this.spec.virtual_network.id == %q && this.metadata.name == %q`, vn.GetId(), defaultSubnetName)).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	if len(subnets.GetItems()) > 0 {
		result.subnetID = subnets.GetItems()[0].GetId()
	}
	groups, err := fixtureDAO[*privatev1.SecurityGroup]().List().SetFilter(fmt.Sprintf(
		`this.spec.virtual_network.id == %q && this.metadata.name == "default"`, vn.GetId())).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	if len(groups.GetItems()) > 0 {
		result.securityGroupID = groups.GetItems()[0].GetId()
	}
	return result
}

// markDefaultNetworkingReady sets the tenant condition DEFAULT_NETWORKING_READY to true. The tenant record is created
// when it doesn't exist.
func markDefaultNetworkingReady(tenant string) {
	tenants := fixtureDAO[*privatev1.Tenant]()
	existing, err := tenants.List().SetFilter(fmt.Sprintf("this.id == %q", tenant)).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	condition := privatev1.TenantCondition_builder{
		Type:   privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY,
		Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
	}.Build()
	if len(existing.GetItems()) == 0 {
		_, err = tenants.Create().SetObject(privatev1.Tenant_builder{
			Id:       tenant,
			Metadata: privatev1.Metadata_builder{Name: tenant, Tenant: tenant}.Build(),
			Status: privatev1.TenantStatus_builder{
				Conditions: []*privatev1.TenantCondition{condition},
			}.Build(),
		}.Build()).Do(ctx)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		return
	}
	object := existing.GetItems()[0]
	if !object.HasStatus() {
		object.SetStatus(&privatev1.TenantStatus{})
	}
	object.GetStatus().SetConditions(append(object.GetStatus().GetConditions(), condition))
	_, err = tenants.Update().SetObject(object).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}

// markSecurityGroupReady sets a SecurityGroup READY, as the operator would.
func markSecurityGroupReady(id string) {
	groups := fixtureDAO[*privatev1.SecurityGroup]()
	response, err := groups.Get().SetId(id).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	object := response.GetObject()
	object.GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY)
	_, err = groups.Update().SetObject(object).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}

// reuseNetworkClassDAO stands in for a NetworkClass DAO in fixtures that predate the deployment NetworkClass
// singleton: its create returns the existing NetworkClass when there is one, and creates the given one otherwise.
var reuseNetworkClassDAO networkClassReuser

type networkClassReuser struct {
	object *privatev1.NetworkClass
}

type networkClassReuseResponse struct {
	object *privatev1.NetworkClass
}

func (r networkClassReuseResponse) GetObject() *privatev1.NetworkClass {
	return r.object
}

func (networkClassReuser) Create() networkClassReuser {
	return networkClassReuser{}
}

func (r networkClassReuser) SetObject(object *privatev1.NetworkClass) networkClassReuser {
	r.object = object
	return r
}

func (r networkClassReuser) Do(ctx context.Context) (networkClassReuseResponse, error) {
	classes := fixtureDAO[*privatev1.NetworkClass]()
	existing, err := classes.List().SetLimit(1).Do(ctx)
	if err != nil {
		return networkClassReuseResponse{}, err
	}
	if len(existing.GetItems()) > 0 {
		return networkClassReuseResponse{object: existing.GetItems()[0]}, nil
	}
	if !r.object.GetSpec().HasAddressPlan() {
		if !r.object.HasSpec() {
			r.object.SetSpec(&privatev1.NetworkClassSpec{})
		}
		r.object.GetSpec().SetAddressPlan(fixtureAddressPlan())
	}
	created, err := classes.Create().SetObject(r.object).Do(ctx)
	if err != nil {
		return networkClassReuseResponse{}, err
	}
	return networkClassReuseResponse{object: created.GetObject()}, nil
}

// clearSeededDefaultNetworking removes the default VirtualNetwork that seedReadyDefaultNetworking created for a tenant,
// with its NATGateway, SecurityGroups and Subnets, for tests that build their own defaults. The tenant condition stays.
func clearSeededDefaultNetworking(tenant string) {
	vns, err := fixtureDAO[*privatev1.VirtualNetwork]().List().SetFilter(fmt.Sprintf(
		`this.metadata.tenant == %q && this.metadata.name == "default" && this.metadata.labels["%s"] == "true"`,
		tenant, defaultLabel)).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	for _, vn := range vns.GetItems() {
		child := fmt.Sprintf("this.spec.virtual_network.id == %q", vn.GetId())
		gateways, err := fixtureDAO[*privatev1.NATGateway]().List().SetFilter(child).Do(ctx)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		for _, item := range gateways.GetItems() {
			_, err = fixtureDAO[*privatev1.NATGateway]().Delete().SetId(item.GetId()).Do(ctx)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		}
		groups, err := fixtureDAO[*privatev1.SecurityGroup]().List().SetFilter(child).Do(ctx)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		for _, item := range groups.GetItems() {
			_, err = fixtureDAO[*privatev1.SecurityGroup]().Delete().SetId(item.GetId()).Do(ctx)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		}
		subnets, err := fixtureDAO[*privatev1.Subnet]().List().SetFilter(child).Do(ctx)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		for _, item := range subnets.GetItems() {
			_, err = fixtureDAO[*privatev1.Subnet]().Delete().SetId(item.GetId()).Do(ctx)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		}
		_, err = fixtureDAO[*privatev1.VirtualNetwork]().Delete().SetId(vn.GetId()).Do(ctx)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
	}
}

// seedReadyNATGateway creates a READY NATGateway on a VirtualNetwork, with its ALLOCATED ExternalIP from a full
// ExternalIPPool (so that pool is never selected for automatic ExternalIPs).
func seedReadyNATGateway(tenant, virtualNetworkID string, isDefault bool) string {
	var labels map[string]string
	name := fmt.Sprintf("nat-%s", uuid.NewString()[:8])
	eipName := fmt.Sprintf("nat-eip-%s", uuid.NewString()[:8])
	if isDefault {
		labels = defaultLabels()
		name = "default"
		eipName = "default-nat"
	}
	natPool := seedExternalIPPool(fmt.Sprintf("198.18.%d.0/30", len(tenant)%250), 0)
	eip, err := fixtureDAO[*privatev1.ExternalIP]().Create().SetObject(privatev1.ExternalIP_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   eipName,
			Tenant: tenant,
			Labels: labels,
		}.Build(),
		Spec: privatev1.ExternalIPSpec_builder{
			Pool: privatev1.ExternalIPPoolReference_builder{Id: natPool.GetId()}.Build(),
		}.Build(),
		Status: privatev1.ExternalIPStatus_builder{
			State:   privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
			Address: "198.18.0.1",
			Hub:     "network-hub-a",
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	gateway, err := fixtureDAO[*privatev1.NATGateway]().Create().SetObject(privatev1.NATGateway_builder{
		Metadata: privatev1.Metadata_builder{
			Name:        name,
			Tenant:      tenant,
			Labels:      labels,
			Annotations: map[string]string{"osac.openshift.io/owner-reference": virtualNetworkID},
		}.Build(),
		Spec: privatev1.NATGatewaySpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
			ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eip.GetObject().GetId()}.Build(),
		}.Build(),
		Status: privatev1.NATGatewayStatus_builder{
			State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY,
			Hub:   "network-hub-a",
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return gateway.GetObject().GetId()
}

// ensureFixtureTenant creates the tenant record when it doesn't exist.
func ensureFixtureTenant(tenant string) {
	tenants := fixtureDAO[*privatev1.Tenant]()
	existing, err := tenants.List().SetFilter(fmt.Sprintf("this.id == %q", tenant)).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	if len(existing.GetItems()) > 0 {
		return
	}
	_, err = tenants.Create().SetObject(privatev1.Tenant_builder{
		Id:       tenant,
		Metadata: privatev1.Metadata_builder{Name: tenant, Tenant: tenant}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}

// seedTenantProject creates a project of the given tenant, so records can be stored in it.
func seedTenantProject(tenant, name string) {
	_, err := fixtureDAO[*privatev1.Project]().Create().SetObject(privatev1.Project_builder{
		Id:       name,
		Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}
