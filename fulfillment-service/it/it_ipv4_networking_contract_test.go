/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// ipv4NetworkingContractFixture creates the READY VirtualNetwork required by
// Subnet and SecurityGroup handlers. The fixture deliberately reaches READY
// through the same gRPC Update RPC used by clients, so these tests exercise the
// handler path rather than constructing database state directly.
type ipv4NetworkingContractFixture struct {
	networkClasses  privatev1.NetworkClassesClient
	virtualNetworks privatev1.VirtualNetworksClient
	subnets         privatev1.SubnetsClient
	securityGroups  privatev1.SecurityGroupsClient

	networkClassID   string
	virtualNetworkID string
	subnetID         string
	securityGroupID  string
}

func newIPv4NetworkingContractFixture(ctx context.Context) *ipv4NetworkingContractFixture {
	adminConn := tool.InternalView().AdminConn()
	fixture := &ipv4NetworkingContractFixture{
		networkClasses:  privatev1.NewNetworkClassesClient(adminConn),
		virtualNetworks: privatev1.NewVirtualNetworksClient(adminConn),
		subnets:         privatev1.NewSubnetsClient(adminConn),
		securityGroups:  privatev1.NewSecurityGroupsClient(adminConn),
	}

	networkClassName := fmt.Sprintf("ipv4-contract-nc-%s", uuid.New()[24:])
	networkClassResponse, err := fixture.networkClasses.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
		Object: privatev1.NetworkClass_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   networkClassName,
				Tenant: usersGroup,
			}.Build(),
			Title:         "IPv4 networking contract test class",
			FabricManager: new("netris"),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	fixture.networkClassID = networkClassResponse.GetObject().GetId()

	fixture.virtualNetworkID = fmt.Sprintf("ipv4-contract-vn-%s", uuid.New())
	virtualNetworkName := fmt.Sprintf("ipv4-contract-vn-%s", uuid.New()[24:])
	_, err = fixture.virtualNetworks.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
		Object: privatev1.VirtualNetwork_builder{
			Id: fixture.virtualNetworkID,
			Metadata: privatev1.Metadata_builder{
				Name:   virtualNetworkName,
				Tenant: usersGroup,
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{
				NetworkClass: privatev1.NetworkClassReference_builder{Id: fixture.networkClassID}.Build(),
				Region:       "us-east-1",
				Ipv4Cidr:     new("10.240.0.0/16"),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())

	// The fulfillment service starts the resource in PENDING. The integration
	// environment does not run the operator feedback loop, so promote it via the
	// private handler after the initial reconciliation pass.
	Eventually(func(g Gomega) {
		response, getErr := fixture.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
			Id: fixture.virtualNetworkID,
		}.Build())
		g.Expect(getErr).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetStatus().GetState()).To(
			Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
	}, time.Minute, time.Second).Should(Succeed())

	virtualNetworkResponse, err := fixture.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
		Id: fixture.virtualNetworkID,
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	virtualNetwork := virtualNetworkResponse.GetObject()
	virtualNetwork.SetStatus(privatev1.VirtualNetworkStatus_builder{
		State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
	}.Build())
	_, err = fixture.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
		Object:     virtualNetwork,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
	}.Build())
	Expect(err).ToNot(HaveOccurred())

	DeferCleanup(func() {
		if fixture.securityGroupID != "" {
			_, _ = fixture.securityGroups.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{
				Id: fixture.securityGroupID,
			}.Build())
		}
		if fixture.subnetID != "" {
			_, _ = fixture.subnets.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
				Id: fixture.subnetID,
			}.Build())
		}
		if fixture.virtualNetworkID != "" {
			_, _ = fixture.virtualNetworks.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: fixture.virtualNetworkID,
			}.Build())
		}
		if fixture.networkClassID != "" {
			_, _ = fixture.networkClasses.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{
				Id: fixture.networkClassID,
			}.Build())
		}
	})

	return fixture
}

func (f *ipv4NetworkingContractFixture) createSubnet(ctx context.Context) *privatev1.Subnet {
	f.subnetID = fmt.Sprintf("ipv4-contract-subnet-%s", uuid.New())
	name := fmt.Sprintf("ipv4-contract-subnet-%s", uuid.New()[24:])
	_, err := f.subnets.Create(ctx, privatev1.SubnetsCreateRequest_builder{
		Object: privatev1.Subnet_builder{
			Id: f.subnetID,
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: usersGroup,
			}.Build(),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: f.virtualNetworkID}.Build(),
				Ipv4Cidr:       new("10.240.1.0/24"),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())

	Eventually(func(g Gomega) {
		getResponse, getErr := f.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: f.subnetID}.Build())
		g.Expect(getErr).ToNot(HaveOccurred())
		g.Expect(getResponse.GetObject().GetStatus().GetState()).To(
			Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
	}, time.Minute, time.Second).Should(Succeed())

	getResponse, err := f.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: f.subnetID}.Build())
	Expect(err).ToNot(HaveOccurred())
	subnet := getResponse.GetObject()
	subnet.SetStatus(privatev1.SubnetStatus_builder{
		State: privatev1.SubnetState_SUBNET_STATE_READY,
	}.Build())
	_, err = f.subnets.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
		Object:     subnet,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	return subnet
}

func expectIPv4ContractError(err error, fragments ...string) {
	Expect(err).To(HaveOccurred())
	Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	for _, fragment := range fragments {
		Expect(err.Error()).To(ContainSubstring(fragment))
	}
}

func expectNoVirtualNetworkNamed(ctx context.Context, client privatev1.VirtualNetworksClient, name string) {
	response, err := client.List(ctx, privatev1.VirtualNetworksListRequest_builder{}.Build())
	Expect(err).ToNot(HaveOccurred())
	for _, object := range response.GetItems() {
		Expect(object.GetMetadata().GetName()).ToNot(Equal(name))
	}
}

func expectNoSubnetNamed(ctx context.Context, client privatev1.SubnetsClient, name string) {
	response, err := client.List(ctx, privatev1.SubnetsListRequest_builder{}.Build())
	Expect(err).ToNot(HaveOccurred())
	for _, object := range response.GetItems() {
		Expect(object.GetMetadata().GetName()).ToNot(Equal(name))
	}
}

func expectNoSecurityGroupNamed(ctx context.Context, client privatev1.SecurityGroupsClient, name string) {
	response, err := client.List(ctx, privatev1.SecurityGroupsListRequest_builder{}.Build())
	Expect(err).ToNot(HaveOccurred())
	for _, object := range response.GetItems() {
		Expect(object.GetMetadata().GetName()).ToNot(Equal(name))
	}
}

func expectNoExternalIPPoolNamed(ctx context.Context, client privatev1.ExternalIPPoolsClient, name string) {
	response, err := client.List(ctx, privatev1.ExternalIPPoolsListRequest_builder{}.Build())
	Expect(err).ToNot(HaveOccurred())
	for _, object := range response.GetItems() {
		Expect(object.GetMetadata().GetName()).ToNot(Equal(name))
	}
}

var _ = Describe("IPv4-only VirtualNetwork gRPC contract", func() {
	var (
		ctx    context.Context
		client privatev1.VirtualNetworksClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
	})

	It("supports canonical IPv4 CRUD and preserves the CIDR during a metadata-only update", func() {
		fixture := newIPv4NetworkingContractFixture(ctx)
		name := fmt.Sprintf("ipv4-vn-crud-%s", uuid.New()[24:])
		response, err := client.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: fixture.networkClassID}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.241.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		Expect(response.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.241.0.0/16"))

		getResponse, err := client.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.241.0.0/16"))

		listResponse, err := client.List(ctx, privatev1.VirtualNetworksListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(ContainElement(WithTransform(
			func(object *privatev1.VirtualNetwork) string { return object.GetId() }, Equal(id))))

		updatedLabels := map[string]string{"contract": "ipv4-vn"}
		_, err = client.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id:       id,
				Metadata: privatev1.Metadata_builder{Labels: updatedLabels}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		getResponse, err = client.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("contract", "ipv4-vn"))
		Expect(getResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.241.0.0/16"))

		_, err = client.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	DescribeTable("rejects invalid IPv4, IPv6, and dual-stack creates before persistence",
		func(spec *privatev1.VirtualNetworkSpec, expected ...string) {
			name := fmt.Sprintf("invalid-vn-%s", uuid.New()[24:])
			_, err := client.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			expectIPv4ContractError(err, expected...)
			expectNoVirtualNetworkNamed(ctx, client, name)
		},
		Entry("missing IPv4 CIDR", privatev1.VirtualNetworkSpec_builder{Region: "us-east-1"}.Build(), "spec.ipv4_cidr"),
		Entry("empty IPv4 CIDR", privatev1.VirtualNetworkSpec_builder{Region: "us-east-1", Ipv4Cidr: new("")}.Build(), "spec.ipv4_cidr"),
		Entry("malformed IPv4 CIDR", privatev1.VirtualNetworkSpec_builder{Region: "us-east-1", Ipv4Cidr: new("not-a-cidr")}.Build(), "ipv4_cidr"),
		Entry("non-canonical IPv4 CIDR", privatev1.VirtualNetworkSpec_builder{Region: "us-east-1", Ipv4Cidr: new("10.242.0.1/16")}.Build(), "canonical"),
		Entry("IPv6-only CIDR", privatev1.VirtualNetworkSpec_builder{Region: "us-east-1", Ipv6Cidr: new("2001:db8::/32")}.Build(), "IPv6 and dual-stack"),
		Entry("dual-stack CIDRs", privatev1.VirtualNetworkSpec_builder{Region: "us-east-1", Ipv4Cidr: new("10.242.0.0/16"), Ipv6Cidr: new("2001:db8::/32")}.Build(), "IPv6 and dual-stack"),
	)

	It("rejects non-canonical, IPv6, dual-stack, and clearing updates through field masks", func() {
		fixture := newIPv4NetworkingContractFixture(ctx)
		name := fmt.Sprintf("ipv4-vn-update-%s", uuid.New()[24:])
		response, err := client.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: fixture.networkClassID}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.243.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		DeferCleanup(func() { _, _ = client.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: id}.Build()) })

		cases := []struct {
			name  string
			field string
			value *string
			want  string
		}{
			{name: "non-canonical IPv4", field: "spec.ipv4_cidr", value: new("10.243.0.1/16"), want: "canonical"},
			{name: "IPv6", field: "spec.ipv6_cidr", value: new("2001:db8::/32"), want: "IPv6 and dual-stack"},
			{name: "cleared IPv4", field: "spec.ipv4_cidr", value: new(""), want: "immutable"},
		}
		for _, testCase := range cases {
			By("rejecting " + testCase.name)
			object := privatev1.VirtualNetwork_builder{Id: id}.Build()
			if strings.HasPrefix(testCase.field, "spec.ipv4") {
				object.SetSpec(privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: testCase.value}.Build())
			} else {
				object.SetSpec(privatev1.VirtualNetworkSpec_builder{Ipv6Cidr: testCase.value}.Build())
			}
			_, err = client.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{testCase.field}},
			}.Build())
			expectIPv4ContractError(err, testCase.want)
		}

		By("rejecting an IPv4 and IPv6 dual-stack update")
		_, err = client.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id:   id,
				Spec: privatev1.VirtualNetworkSpec_builder{Ipv6Cidr: new("2001:db8::/32")}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.ipv6_cidr"}},
		}.Build())
		expectIPv4ContractError(err, "IPv6 and dual-stack")
	})
})

var _ = Describe("IPv4-only Subnet gRPC contract", func() {
	var (
		ctx    context.Context
		client privatev1.SubnetsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
	})

	It("supports canonical IPv4 CRUD and preserves the CIDR during a metadata-only update", func() {
		fixture := newIPv4NetworkingContractFixture(ctx)
		subnet := fixture.createSubnet(ctx)
		id := subnet.GetId()
		getResponse, err := client.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.240.1.0/24"))

		listResponse, err := client.List(ctx, privatev1.SubnetsListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(ContainElement(WithTransform(
			func(object *privatev1.Subnet) string { return object.GetId() }, Equal(id))))

		updatedLabels := map[string]string{"contract": "ipv4-subnet"}
		_, err = client.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
			Object: privatev1.Subnet_builder{
				Id:       id,
				Metadata: privatev1.Metadata_builder{Labels: updatedLabels}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		getResponse, err = client.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("contract", "ipv4-subnet"))
		Expect(getResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.240.1.0/24"))

		_, err = client.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	DescribeTable("rejects invalid IPv4, IPv6, and dual-stack creates before persistence",
		func(spec *privatev1.SubnetSpec, expected ...string) {
			fixture := newIPv4NetworkingContractFixture(ctx)
			name := fmt.Sprintf("invalid-subnet-%s", uuid.New()[24:])
			spec.SetVirtualNetwork(privatev1.VirtualNetworkLocalReference_builder{Id: fixture.virtualNetworkID}.Build())
			_, err := client.Create(ctx, privatev1.SubnetsCreateRequest_builder{
				Object: privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			expectIPv4ContractError(err, expected...)
			expectNoSubnetNamed(ctx, client, name)
		},
		Entry("missing IPv4 CIDR", privatev1.SubnetSpec_builder{}.Build(), "spec.ipv4_cidr"),
		Entry("empty IPv4 CIDR", privatev1.SubnetSpec_builder{Ipv4Cidr: new("")}.Build(), "spec.ipv4_cidr"),
		Entry("malformed IPv4 CIDR", privatev1.SubnetSpec_builder{Ipv4Cidr: new("not-a-cidr")}.Build(), "ipv4_cidr"),
		Entry("non-canonical IPv4 CIDR", privatev1.SubnetSpec_builder{Ipv4Cidr: new("10.244.0.1/24")}.Build(), "canonical"),
		Entry("IPv6-only CIDR", privatev1.SubnetSpec_builder{Ipv6Cidr: new("2001:db8::/64")}.Build(), "IPv6 and dual-stack"),
		Entry("dual-stack CIDRs", privatev1.SubnetSpec_builder{Ipv4Cidr: new("10.244.0.0/24"), Ipv6Cidr: new("2001:db8::/64")}.Build(), "IPv6 and dual-stack"),
	)

	It("rejects invalid field-masked updates and never changes the stored CIDR", func() {
		fixture := newIPv4NetworkingContractFixture(ctx)
		subnet := fixture.createSubnet(ctx)
		id := subnet.GetId()

		cases := []struct {
			name  string
			field string
			value *string
			want  string
		}{
			{name: "non-canonical IPv4", field: "spec.ipv4_cidr", value: new("10.240.1.1/24"), want: "canonical"},
			{name: "IPv6", field: "spec.ipv6_cidr", value: new("2001:db8::/64"), want: "IPv6 and dual-stack"},
			{name: "cleared IPv4", field: "spec.ipv4_cidr", value: new(""), want: "immutable"},
		}
		for _, testCase := range cases {
			By("rejecting " + testCase.name)
			object := privatev1.Subnet_builder{Id: id}.Build()
			if strings.HasPrefix(testCase.field, "spec.ipv4") {
				object.SetSpec(privatev1.SubnetSpec_builder{Ipv4Cidr: testCase.value}.Build())
			} else {
				object.SetSpec(privatev1.SubnetSpec_builder{Ipv6Cidr: testCase.value}.Build())
			}
			_, err := client.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{testCase.field}},
			}.Build())
			expectIPv4ContractError(err, testCase.want)
		}
	})
})

var _ = Describe("IPv4-only SecurityGroup gRPC contract", func() {
	var (
		ctx    context.Context
		client privatev1.SecurityGroupsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn())
	})

	It("supports canonical IPv4 ingress and egress rules through CRUD", func() {
		fixture := newIPv4NetworkingContractFixture(ctx)
		name := fmt.Sprintf("ipv4-sg-crud-%s", uuid.New()[24:])
		rule := privatev1.SecurityRule_builder{
			Protocol: privatev1.Protocol_PROTOCOL_ALL,
			Ipv4Cidr: new("0.0.0.0/0"),
		}.Build()
		response, err := client.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: fixture.virtualNetworkIDRef(),
					Ingress:        []*privatev1.SecurityRule{rule},
					Egress:         []*privatev1.SecurityRule{rule},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		fixture.securityGroupID = response.GetObject().GetId()
		Expect(response.GetObject().GetSpec().GetIngress()[0].GetIpv4Cidr()).To(Equal("0.0.0.0/0"))
		Expect(response.GetObject().GetSpec().GetEgress()[0].GetIpv4Cidr()).To(Equal("0.0.0.0/0"))

		getResponse, err := client.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: fixture.securityGroupID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetIngress()).To(HaveLen(1))
		Expect(getResponse.GetObject().GetSpec().GetEgress()).To(HaveLen(1))

		updatedLabels := map[string]string{"contract": "ipv4-security-group"}
		_, err = client.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Id:       fixture.securityGroupID,
				Metadata: privatev1.Metadata_builder{Labels: updatedLabels}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		getResponse, err = client.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: fixture.securityGroupID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("contract", "ipv4-security-group"))
		Expect(getResponse.GetObject().GetSpec().GetIngress()[0].GetIpv4Cidr()).To(Equal("0.0.0.0/0"))

		_, err = client.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{Id: fixture.securityGroupID}.Build())
		Expect(err).ToNot(HaveOccurred())
		fixture.securityGroupID = ""
	})

	DescribeTable("rejects missing, malformed, host-bit, IPv6-only, and dual-stack rules",
		func(ingress bool, rule *privatev1.SecurityRule, expected string) {
			fixture := newIPv4NetworkingContractFixture(ctx)
			name := fmt.Sprintf("invalid-sg-%s", uuid.New()[24:])
			spec := privatev1.SecurityGroupSpec_builder{VirtualNetwork: fixture.virtualNetworkIDRef()}
			if ingress {
				spec.Ingress = []*privatev1.SecurityRule{rule}
			} else {
				spec.Egress = []*privatev1.SecurityRule{rule}
			}
			_, err := client.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
				Object: privatev1.SecurityGroup_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
					Spec:     spec.Build(),
				}.Build(),
			}.Build())
			expectIPv4ContractError(err, expected)
			expectNoSecurityGroupNamed(ctx, client, name)
		},
		Entry("missing ingress IPv4", true, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL}.Build(), "ipv4_cidr"),
		Entry("empty ingress IPv4", true, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL, Ipv4Cidr: new("")}.Build(), "ipv4_cidr"),
		Entry("malformed ingress IPv4", true, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL, Ipv4Cidr: new("not-a-cidr")}.Build(), "ipv4_cidr"),
		Entry("host-bit ingress IPv4", true, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL, Ipv4Cidr: new("10.245.0.1/24")}.Build(), "canonical"),
		Entry("IPv6-only ingress", true, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL, Ipv6Cidr: new("2001:db8::/64")}.Build(), "IPv6 and dual-stack"),
		Entry("dual-stack ingress", true, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL, Ipv4Cidr: new("10.245.0.0/24"), Ipv6Cidr: new("2001:db8::/64")}.Build(), "IPv6 and dual-stack"),
		Entry("missing egress IPv4", false, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL}.Build(), "ipv4_cidr"),
		Entry("host-bit egress IPv4", false, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL, Ipv4Cidr: new("10.246.0.1/24")}.Build(), "canonical"),
		Entry("IPv6-only egress", false, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL, Ipv6Cidr: new("2001:db8::/64")}.Build(), "IPv6 and dual-stack"),
		Entry("dual-stack egress", false, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL, Ipv4Cidr: new("10.246.0.0/24"), Ipv6Cidr: new("2001:db8::/64")}.Build(), "IPv6 and dual-stack"),
	)

	It("rejects an invalid field-masked ingress update without changing the stored rule", func() {
		fixture := newIPv4NetworkingContractFixture(ctx)
		rule := privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL, Ipv4Cidr: new("0.0.0.0/0")}.Build()
		response, err := client.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("ipv4-sg-update-%s", uuid.New()[24:]), Tenant: usersGroup}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: fixture.virtualNetworkIDRef(),
					Ingress:        []*privatev1.SecurityRule{rule},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		fixture.securityGroupID = response.GetObject().GetId()
		_, err = client.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Id: fixture.securityGroupID,
				Spec: privatev1.SecurityGroupSpec_builder{
					Ingress: []*privatev1.SecurityRule{{
						Protocol: privatev1.Protocol_PROTOCOL_ALL,
						Ipv4Cidr: new("10.247.0.1/24"),
					}},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.ingress"}},
		}.Build())
		expectIPv4ContractError(err, "canonical")
	})
})

func (f *ipv4NetworkingContractFixture) virtualNetworkIDRef() *privatev1.VirtualNetworkLocalReference {
	return privatev1.VirtualNetworkLocalReference_builder{Id: f.virtualNetworkID}.Build()
}

var _ = Describe("IPv4-only ExternalIPPool gRPC contract", func() {
	var (
		ctx    context.Context
		client privatev1.ExternalIPPoolsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
	})

	It("supports one canonical IPv4 CIDR through CRUD and preserves it on metadata update", func() {
		name := fmt.Sprintf("ipv4-pool-crud-%s", uuid.New()[24:])
		cidr := "10.248.0.0/28"
		response, err := client.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{Name: name}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{cidr},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		DeferCleanup(func() { _, _ = client.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: id}.Build()) })
		Expect(response.GetObject().GetSpec().GetCidrs()).To(Equal([]string{cidr}))
		Expect(response.GetObject().GetSpec().GetIpFamily()).To(Equal(privatev1.IPFamily_IP_FAMILY_IPV4))

		getResponse, err := client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetCidrs()).To(Equal([]string{cidr}))
		listResponse, err := client.List(ctx, privatev1.ExternalIPPoolsListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(ContainElement(WithTransform(
			func(object *privatev1.ExternalIPPool) string { return object.GetId() }, Equal(id))))

		updatedLabels := map[string]string{"contract": "ipv4-pool"}
		_, err = client.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id:       id,
				Metadata: privatev1.Metadata_builder{Labels: updatedLabels}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		getResponse, err = client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("contract", "ipv4-pool"))
		Expect(getResponse.GetObject().GetSpec().GetCidrs()).To(Equal([]string{cidr}))
	})

	DescribeTable("rejects invalid family and CIDR create shapes before persistence",
		func(spec *privatev1.ExternalIPPoolSpec, expected ...string) {
			name := fmt.Sprintf("invalid-pool-%s", uuid.New()[24:])
			_, err := client.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			expectIPv4ContractError(err, expected...)
			expectNoExternalIPPoolNamed(ctx, client, name)
		},
		Entry("unspecified family", privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"10.249.0.0/28"}}.Build(), "spec.ip_family"),
		Entry("IPv6 family", privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"2001:db8::/64"}, IpFamily: privatev1.IPFamily_IP_FAMILY_IPV6}.Build(), "spec.ip_family"),
		Entry("zero CIDRs", privatev1.ExternalIPPoolSpec_builder{IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4}.Build(), "spec.cidrs"),
		Entry("multiple CIDRs", privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"10.249.1.0/28", "10.249.2.0/28"}, IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4}.Build(), "spec.cidrs"),
		Entry("malformed CIDR", privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"not-a-cidr"}, IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4}.Build(), "cidr"),
		Entry("host-bit CIDR", privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"10.249.3.1/28"}, IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4}.Build(), "canonical"),
		Entry("IPv6 CIDR with IPv4 family", privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"2001:db8::/64"}, IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4}.Build(), "IPv4 CIDR"),
	)

	It("rejects IPv6, unspecified-family, non-canonical, and multiple-CIDR updates", func() {
		cidr := "10.250.0.0/28"
		response, err := client.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("ipv4-pool-update-%s", uuid.New()[24:])}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs: []string{cidr}, IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		DeferCleanup(func() { _, _ = client.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: id}.Build()) })

		cases := []struct {
			name string
			obj  *privatev1.ExternalIPPool
			mask string
			want string
		}{
			{name: "IPv6 family", obj: privatev1.ExternalIPPool_builder{Id: id, Spec: privatev1.ExternalIPPoolSpec_builder{IpFamily: privatev1.IPFamily_IP_FAMILY_IPV6}.Build()}.Build(), mask: "spec.ip_family", want: "IP_FAMILY_IPV4"},
			{name: "unspecified family", obj: privatev1.ExternalIPPool_builder{Id: id, Spec: privatev1.ExternalIPPoolSpec_builder{IpFamily: privatev1.IPFamily_IP_FAMILY_UNSPECIFIED}.Build()}.Build(), mask: "spec.ip_family", want: "validation failed"},
			{name: "non-canonical CIDR", obj: privatev1.ExternalIPPool_builder{Id: id, Spec: privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"10.250.0.1/28"}}.Build()}.Build(), mask: "spec.cidrs", want: "canonical"},
			{name: "multiple CIDRs", obj: privatev1.ExternalIPPool_builder{Id: id, Spec: privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"10.250.0.0/28", "10.250.1.0/28"}}.Build()}.Build(), mask: "spec.cidrs", want: "spec.cidrs"},
		}
		for _, testCase := range cases {
			By("rejecting " + testCase.name)
			_, err = client.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
				Object:     testCase.obj,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{testCase.mask}},
			}.Build())
			expectIPv4ContractError(err, testCase.want)
		}
	})
})
