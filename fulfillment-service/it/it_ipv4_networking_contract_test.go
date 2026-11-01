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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
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
	DeferCleanup(func(cleanupCtx context.Context) {
		var cleanupErrors []error
		// This suite deploys Fulfillment without the networking operator. Clear any controller
		// finalizer after Delete so each dependency is archived before its parent is removed.
		if fixture.securityGroupID != "" {
			cleanupErrors = appendFixtureCleanupError(cleanupErrors, "security group", fixture.securityGroupID,
				cleanupNetworkFixtureResource(cleanupCtx,
					func(ctx context.Context) error {
						_, err := fixture.securityGroups.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{Id: fixture.securityGroupID}.Build())
						return err
					},
					func(ctx context.Context) (*privatev1.Metadata, error) {
						response, err := fixture.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: fixture.securityGroupID}.Build())
						if err != nil {
							return nil, err
						}
						return response.GetObject().GetMetadata(), nil
					},
					func(ctx context.Context) error {
						response, err := fixture.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: fixture.securityGroupID}.Build())
						if err != nil {
							return err
						}
						object := response.GetObject()
						object.GetMetadata().SetFinalizers(nil)
						_, err = fixture.securityGroups.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
							Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
						}.Build())
						return err
					}))
		}
		if fixture.subnetID != "" {
			cleanupErrors = appendFixtureCleanupError(cleanupErrors, "subnet", fixture.subnetID,
				cleanupNetworkFixtureResource(cleanupCtx,
					func(ctx context.Context) error {
						_, err := fixture.subnets.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: fixture.subnetID}.Build())
						return err
					},
					func(ctx context.Context) (*privatev1.Metadata, error) {
						response, err := fixture.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: fixture.subnetID}.Build())
						if err != nil {
							return nil, err
						}
						return response.GetObject().GetMetadata(), nil
					},
					func(ctx context.Context) error {
						response, err := fixture.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: fixture.subnetID}.Build())
						if err != nil {
							return err
						}
						object := response.GetObject()
						object.GetMetadata().SetFinalizers(nil)
						_, err = fixture.subnets.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
							Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
						}.Build())
						return err
					}))
		}
		if fixture.virtualNetworkID != "" {
			cleanupErrors = appendFixtureCleanupError(cleanupErrors, "virtual network", fixture.virtualNetworkID,
				cleanupNetworkFixtureResource(cleanupCtx,
					func(ctx context.Context) error {
						_, err := fixture.virtualNetworks.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: fixture.virtualNetworkID}.Build())
						return err
					},
					func(ctx context.Context) (*privatev1.Metadata, error) {
						response, err := fixture.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: fixture.virtualNetworkID}.Build())
						if err != nil {
							return nil, err
						}
						return response.GetObject().GetMetadata(), nil
					},
					func(ctx context.Context) error {
						response, err := fixture.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: fixture.virtualNetworkID}.Build())
						if err != nil {
							return err
						}
						object := response.GetObject()
						object.GetMetadata().SetFinalizers(nil)
						_, err = fixture.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
							Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
						}.Build())
						return err
					}))
		}
		// The NetworkClass is the installer's deployment singleton: the fixture never deletes it.
		Expect(cleanupErrors).To(BeEmpty())
	})

	networkClassResponse, err := sharedNetworkClass(ctx, fixture.networkClasses)
	Expect(err).ToNot(HaveOccurred())
	fixture.networkClassID = networkClassResponse.GetObject().GetId()
	err = updateNetworkClassStatusWithRetry(ctx, fixture.networkClasses, fixture.networkClassID, privatev1.NetworkClassStatus_builder{
		State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
		Hub:   hubId,
	}.Build(), &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.hub"}})
	Expect(err).ToNot(HaveOccurred())

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
				Ipv4Cidr: new("10.240.0.0/16"),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	virtualNetworkResponse, err := fixture.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
		Id: fixture.virtualNetworkID,
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	virtualNetwork := virtualNetworkResponse.GetObject()
	// This contract test exercises SecurityGroup validation, not asynchronous Hub selection.
	// Seed the resource states directly so controller queue delay cannot consume the suite timeout.
	virtualNetwork.SetStatus(privatev1.VirtualNetworkStatus_builder{
		State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
		Hub:   hubId,
	}.Build())
	_, err = fixture.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
		Object:     virtualNetwork,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.hub"}},
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	Eventually(func() privatev1.VirtualNetworkState {
		response, getErr := fixture.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
			Id: fixture.virtualNetworkID,
		}.Build())
		Expect(getErr).ToNot(HaveOccurred())
		return response.GetObject().GetStatus().GetState()
	}, time.Minute, time.Second).Should(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
	waitForNetworkingCR(ctx, listNetworkingCR(
		tool.KubeClient(),
		labels.VirtualNetworkUuid,
		fixture.virtualNetworkID,
		&osacv1alpha1.VirtualNetworkList{},
		func(objects *osacv1alpha1.VirtualNetworkList) int { return len(objects.Items) },
	))
	virtualNetworkResponse, err = fixture.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
		Id: fixture.virtualNetworkID,
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	virtualNetwork = virtualNetworkResponse.GetObject()
	virtualNetwork.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)
	_, err = fixture.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
		Object:     virtualNetwork,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
	}.Build())
	Expect(err).ToNot(HaveOccurred())

	return fixture
}

func appendFixtureCleanupError(errors []error, kind, id string, err error) []error {
	if err != nil {
		return append(errors, fmt.Errorf("delete %s %q: %w", kind, id, err))
	}
	return errors
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
		_, getErr := f.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: f.subnetID}.Build())
		g.Expect(getErr).ToNot(HaveOccurred())
	}, time.Minute, time.Second).Should(Succeed())

	getResponse, err := f.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: f.subnetID}.Build())
	Expect(err).ToNot(HaveOccurred())
	subnet := getResponse.GetObject()
	expectNetworkingResourceHub(ctx, hubId, func(getCtx context.Context) (string, error) {
		response, getErr := f.subnets.Get(getCtx, privatev1.SubnetsGetRequest_builder{Id: f.subnetID}.Build())
		if getErr != nil {
			return "", getErr
		}
		return response.GetObject().GetStatus().GetHub(), nil
	})
	subnet.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_READY)
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

func expectFailedPrecondition(err error, fragments ...string) {
	Expect(err).To(HaveOccurred())
	Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
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
		_ = newIPv4NetworkingContractFixture(ctx)
		name := fmt.Sprintf("ipv4-vn-crud-%s", uuid.New()[24:])
		response, err := client.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr: new("10.241.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupVirtualNetworkFixture(cleanupCtx, client, id)).To(Succeed())
		})
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
		// Networking resources are update-less: names and labels can\'t change.
		expectIPv4ContractError(err, "immutable")
		getResponse, err = client.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.241.0.0/16"))

		_, err = client.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	It("requires a READY VirtualNetwork for Subnet creation and protects referenced parents from deletion", func() {
		fixture := newIPv4NetworkingContractFixture(ctx)
		virtualNetworkResponse, err := fixture.virtualNetworks.Get(ctx,
			privatev1.VirtualNetworksGetRequest_builder{Id: fixture.virtualNetworkID}.Build())
		Expect(err).ToNot(HaveOccurred())
		virtualNetwork := virtualNetworkResponse.GetObject()
		virtualNetwork.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING)
		_, err = fixture.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     virtualNetwork,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		subnetName := fmt.Sprintf("networking-ready-gate-subnet-%s", uuid.New()[24:])
		_, err = fixture.subnets.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{Name: subnetName, Tenant: usersGroup}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: fixture.virtualNetworkID}.Build(),
					Ipv4Cidr:       new("10.240.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		expectFailedPrecondition(err, "VirtualNetwork", "READY")
		expectNoSubnetNamed(ctx, fixture.subnets, subnetName)

		virtualNetwork.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)
		_, err = fixture.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     virtualNetwork,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		subnet := fixture.createSubnet(ctx)
		_, err = fixture.networkClasses.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{
			Id: fixture.networkClassID,
		}.Build())
		expectFailedPrecondition(err, "VirtualNetwork")
		networkClassResponse, err := fixture.networkClasses.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
			Id: fixture.networkClassID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(networkClassResponse.GetObject().GetMetadata().GetDeletionTimestamp()).To(BeNil())

		_, err = fixture.virtualNetworks.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
			Id: fixture.virtualNetworkID,
		}.Build())
		expectFailedPrecondition(err, "Subnet", subnet.GetMetadata().GetName())
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
		Entry("missing IPv4 CIDR", privatev1.VirtualNetworkSpec_builder{}.Build(), "spec.ipv4_cidr"),
		Entry("empty IPv4 CIDR", privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new("")}.Build(), "spec.ipv4_cidr"),
		Entry("malformed IPv4 CIDR", privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new("not-a-cidr")}.Build(), "ipv4_cidr"),
		Entry("non-canonical IPv4 CIDR", privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new("10.242.0.1/16")}.Build(), "canonical"),
	)

	It("rejects invalid and immutable CIDR changes through field masks", func() {
		_ = newIPv4NetworkingContractFixture(ctx)
		name := fmt.Sprintf("ipv4-vn-update-%s", uuid.New()[24:])
		response, err := client.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr: new("10.243.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupVirtualNetworkFixture(cleanupCtx, client, id)).To(Succeed())
		})

		cases := []struct {
			name  string
			field string
			value *string
			want  string
		}{
			{name: "non-canonical IPv4", field: "spec.ipv4_cidr", value: new("10.243.0.1/16"), want: "canonical"},
			{name: "changed IPv4", field: "spec.ipv4_cidr", value: new("10.244.0.0/16"), want: "immutable"},
		}
		for _, testCase := range cases {
			By("rejecting " + testCase.name)
			object := privatev1.VirtualNetwork_builder{Id: id}.Build()
			object.SetSpec(privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: testCase.value}.Build())
			_, err = client.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{testCase.field}},
			}.Build())
			expectIPv4ContractError(err, testCase.want)
			stored, getErr := client.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
			Expect(getErr).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.243.0.0/16"))
		}
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
		// Networking resources are update-less: names and labels can\'t change.
		expectIPv4ContractError(err, "immutable")
		getResponse, err = client.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
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
			{name: "changed IPv4", field: "spec.ipv4_cidr", value: new("10.240.2.0/24"), want: "immutable"},
		}
		for _, testCase := range cases {
			By("rejecting " + testCase.name)
			object := privatev1.Subnet_builder{Id: id}.Build()
			object.SetSpec(privatev1.SubnetSpec_builder{Ipv4Cidr: testCase.value}.Build())
			_, err := client.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{testCase.field}},
			}.Build())
			expectIPv4ContractError(err, testCase.want)
			stored, getErr := client.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
			Expect(getErr).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.240.1.0/24"))
		}
	})
})

var _ = Describe("IPv4-only SecurityGroup gRPC contract", func() {
	var (
		ctx          context.Context
		client       privatev1.SecurityGroupsClient
		publicClient publicv1.SecurityGroupsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn())
		publicClient = publicv1.NewSecurityGroupsClient(tool.ExternalView().UserConn())
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
		// Networking resources are update-less: names and labels can\'t change.
		expectIPv4ContractError(err, "immutable")
		getResponse, err = client.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: fixture.securityGroupID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetIngress()[0].GetIpv4Cidr()).To(Equal("0.0.0.0/0"))

		Expect(cleanupSecurityGroupFixture(ctx, fixture.securityGroups, fixture.securityGroupID)).To(Succeed())
		fixture.securityGroupID = ""
	})

	It("allows tenant users to update rules and reconciles the SecurityGroup CR", func() {
		fixture := newIPv4NetworkingContractFixture(ctx)
		name := fmt.Sprintf("ipv4-sg-public-update-%s", uuid.New()[24:])
		initialRule := privatev1.SecurityRule_builder{
			Protocol: privatev1.Protocol_PROTOCOL_ALL,
			Ipv4Cidr: new("0.0.0.0/0"),
		}.Build()
		created, err := client.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: fixture.virtualNetworkIDRef(),
					Ingress:        []*privatev1.SecurityRule{initialRule},
					Egress:         []*privatev1.SecurityRule{initialRule},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		fixture.securityGroupID = created.GetObject().GetId()

		By("waiting for the initial SecurityGroup CR")
		Eventually(func(g Gomega) {
			securityGroups := &osacv1alpha1.SecurityGroupList{}
			g.Expect(tool.KubeClient().List(ctx, securityGroups, crclient.MatchingLabels{
				labels.SecurityGroupUuid: fixture.securityGroupID,
			})).To(Succeed())
			g.Expect(securityGroups.Items).To(HaveLen(1))
		}, time.Minute, time.Second).Should(Succeed())

		By("marking the initial SecurityGroup READY for a rule update")
		initial, err := fixture.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{
			Id: fixture.securityGroupID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		initial.GetObject().GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY)
		_, err = fixture.securityGroups.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
			Object:     initial.GetObject(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		updatedIngress := publicv1.SecurityRule_builder{
			Protocol: publicv1.Protocol_PROTOCOL_UDP,
			PortFrom: new(int32(53)),
			PortTo:   new(int32(53)),
			Ipv4Cidr: new("10.251.0.0/16"),
		}.Build()
		updatedEgress := publicv1.SecurityRule_builder{
			Protocol: publicv1.Protocol_PROTOCOL_TCP,
			PortFrom: new(int32(443)),
			PortTo:   new(int32(443)),
			Ipv4Cidr: new("192.168.0.0/16"),
		}.Build()

		By("updating both rule lists through the public API as a tenant user")
		updated, err := publicClient.Update(ctx, publicv1.SecurityGroupsUpdateRequest_builder{
			Object: publicv1.SecurityGroup_builder{
				Id: fixture.securityGroupID,
				Metadata: publicv1.Metadata_builder{
					Name: "ignored-name-change",
				}.Build(),
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: fixture.virtualNetworkID}.Build(),
					Ingress:        []*publicv1.SecurityRule{updatedIngress},
					Egress:         []*publicv1.SecurityRule{updatedEgress},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetId()).To(Equal(fixture.securityGroupID))
		Expect(updated.GetObject().GetMetadata().GetName()).To(Equal(name))
		Expect(updated.GetObject().GetMetadata().GetAnnotations()).To(HaveKeyWithValue(
			"osac.openshift.io/owner-reference", fixture.virtualNetworkID,
		))
		Expect(updated.GetObject().GetSpec().GetVirtualNetwork().GetId()).To(Equal(fixture.virtualNetworkID))
		Expect(updated.GetObject().GetSpec().GetIngress()).To(HaveLen(1))
		Expect(proto.Equal(updated.GetObject().GetSpec().GetIngress()[0], updatedIngress)).To(BeTrue())
		Expect(updated.GetObject().GetSpec().GetEgress()).To(HaveLen(1))
		Expect(proto.Equal(updated.GetObject().GetSpec().GetEgress()[0], updatedEgress)).To(BeTrue())

		By("reading the stored rule lists and immutable parent through the public API")
		stored, err := publicClient.Get(ctx, publicv1.SecurityGroupsGetRequest_builder{
			Id: fixture.securityGroupID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(stored.GetObject().GetMetadata().GetName()).To(Equal(name))
		Expect(stored.GetObject().GetMetadata().GetAnnotations()).To(HaveKeyWithValue(
			"osac.openshift.io/owner-reference", fixture.virtualNetworkID,
		))
		Expect(stored.GetObject().GetSpec().GetVirtualNetwork().GetId()).To(Equal(fixture.virtualNetworkID))
		Expect(stored.GetObject().GetSpec().GetIngress()).To(HaveLen(1))
		Expect(proto.Equal(stored.GetObject().GetSpec().GetIngress()[0], updatedIngress)).To(BeTrue())
		Expect(stored.GetObject().GetSpec().GetEgress()).To(HaveLen(1))
		Expect(proto.Equal(stored.GetObject().GetSpec().GetEgress()[0], updatedEgress)).To(BeTrue())

		By("waiting for Fulfillment to patch the existing Kubernetes CR with the new rules")
		Eventually(func(g Gomega) {
			securityGroups := &osacv1alpha1.SecurityGroupList{}
			g.Expect(tool.KubeClient().List(ctx, securityGroups, crclient.MatchingLabels{
				labels.SecurityGroupUuid: fixture.securityGroupID,
			})).To(Succeed())
			g.Expect(securityGroups.Items).To(HaveLen(1))
			if len(securityGroups.Items) != 1 {
				return
			}

			securityGroup := securityGroups.Items[0]
			g.Expect(securityGroup.Spec.VirtualNetwork).To(Equal(fixture.virtualNetworkID))
			g.Expect(securityGroup.Spec.IngressRules).To(HaveLen(1))
			g.Expect(securityGroup.Spec.EgressRules).To(HaveLen(1))
			if len(securityGroup.Spec.IngressRules) != 1 || len(securityGroup.Spec.EgressRules) != 1 {
				return
			}
			g.Expect(securityGroup.Spec.IngressRules[0].Protocol).To(Equal(osacv1alpha1.SecurityGroupProtocolUDP))
			g.Expect(securityGroup.Spec.IngressRules[0].PortFrom).ToNot(BeNil())
			g.Expect(securityGroup.Spec.IngressRules[0].PortTo).ToNot(BeNil())
			if securityGroup.Spec.IngressRules[0].PortFrom != nil {
				g.Expect(*securityGroup.Spec.IngressRules[0].PortFrom).To(Equal(int32(53)))
			}
			if securityGroup.Spec.IngressRules[0].PortTo != nil {
				g.Expect(*securityGroup.Spec.IngressRules[0].PortTo).To(Equal(int32(53)))
			}
			g.Expect(securityGroup.Spec.IngressRules[0].SourceCIDR).To(Equal("10.251.0.0/16"))
			g.Expect(securityGroup.Spec.EgressRules[0].Protocol).To(Equal(osacv1alpha1.SecurityGroupProtocolTCP))
			g.Expect(securityGroup.Spec.EgressRules[0].PortFrom).ToNot(BeNil())
			g.Expect(securityGroup.Spec.EgressRules[0].PortTo).ToNot(BeNil())
			if securityGroup.Spec.EgressRules[0].PortFrom != nil {
				g.Expect(*securityGroup.Spec.EgressRules[0].PortFrom).To(Equal(int32(443)))
			}
			if securityGroup.Spec.EgressRules[0].PortTo != nil {
				g.Expect(*securityGroup.Spec.EgressRules[0].PortTo).To(Equal(int32(443)))
			}
			g.Expect(securityGroup.Spec.EgressRules[0].DestinationCIDR).To(Equal("192.168.0.0/16"))
		}, time.Minute, time.Second).Should(Succeed())
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
		Entry("missing egress IPv4", false, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL}.Build(), "ipv4_cidr"),
		Entry("host-bit egress IPv4", false, privatev1.SecurityRule_builder{Protocol: privatev1.Protocol_PROTOCOL_ALL, Ipv4Cidr: new("10.246.0.1/24")}.Build(), "canonical"),
	)

	It("rejects a private field-masked ingress change without changing the stored rule", func() {
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
						Ipv4Cidr: new("10.247.0.0/24"),
					}},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.ingress"}},
		}.Build())
		expectIPv4ContractError(err, "spec is immutable")
		stored, err := client.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: fixture.securityGroupID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(stored.GetObject().GetSpec().GetIngress()[0].GetIpv4Cidr()).To(Equal("0.0.0.0/0"))
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
		// Networking resources are update-less: names and labels can\'t change.
		expectIPv4ContractError(err, "immutable")
		getResponse, err = client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
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

	It("rejects invalid and immutable field-masked updates", func() {
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
			{name: "IPv6 family", obj: privatev1.ExternalIPPool_builder{Id: id, Spec: privatev1.ExternalIPPoolSpec_builder{IpFamily: privatev1.IPFamily_IP_FAMILY_IPV6}.Build()}.Build(), mask: "spec.ip_family", want: "spec.ip_family"},
			{name: "unspecified family", obj: privatev1.ExternalIPPool_builder{Id: id, Spec: privatev1.ExternalIPPoolSpec_builder{IpFamily: privatev1.IPFamily_IP_FAMILY_UNSPECIFIED}.Build()}.Build(), mask: "spec.ip_family", want: "spec.ip_family"},
			{name: "non-canonical CIDR", obj: privatev1.ExternalIPPool_builder{Id: id, Spec: privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"10.250.0.1/28"}}.Build()}.Build(), mask: "spec.cidrs", want: "canonical"},
			{name: "multiple CIDRs", obj: privatev1.ExternalIPPool_builder{Id: id, Spec: privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"10.250.0.0/28", "10.250.1.0/28"}}.Build()}.Build(), mask: "spec.cidrs", want: "spec.cidrs"},
			{name: "changed canonical CIDR", obj: privatev1.ExternalIPPool_builder{Id: id, Spec: privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"10.250.1.0/28"}}.Build()}.Build(), mask: "spec.cidrs", want: "immutable"},
		}
		for _, testCase := range cases {
			By("rejecting " + testCase.name)
			_, err = client.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
				Object:     testCase.obj,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{testCase.mask}},
			}.Build())
			expectIPv4ContractError(err, testCase.want)
			stored, getErr := client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
			Expect(getErr).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetSpec().GetCidrs()).To(Equal([]string{cidr}))
			Expect(stored.GetObject().GetSpec().GetIpFamily()).To(Equal(privatev1.IPFamily_IP_FAMILY_IPV4))
		}
	})
})

var _ = Describe("IPv4-only controller endpoint gRPC contract", Label("ipv4-networking"), func() {
	It("validates endpoint addresses before persistence and preserves accepted status", func(ctx context.Context) {
		internal := privatev1.NewClustersClient(tool.InternalView().AdminConn())
		fields := []struct {
			path     string
			set      func(*privatev1.ClusterStatus, string)
			get      func(*privatev1.ClusterStatus) string
			accepted string
		}{
			{
				path:     "status.api_endpoint",
				set:      func(status *privatev1.ClusterStatus, value string) { status.SetApiEndpoint(value) },
				get:      func(status *privatev1.ClusterStatus) string { return status.GetApiEndpoint() },
				accepted: "192.0.2.20",
			},
			{
				path:     "status.ingress_endpoint",
				set:      func(status *privatev1.ClusterStatus, value string) { status.SetIngressEndpoint(value) },
				get:      func(status *privatev1.ClusterStatus) string { return status.GetIngressEndpoint() },
				accepted: "198.51.100.21",
			},
		}
		invalidCreateValues := []string{"2001:db8::1", "192.000.2.1", "192.0.2.1/32"}
		invalidUpdateValues := []string{"2001:db8::2", "::ffff:192.0.2.2", "not-an-ip", "192.000.2.2", "192.0.2.2/32"}

		By("rejecting invalid controller addresses before Create persists a cluster")
		for _, field := range fields {
			for _, value := range invalidCreateValues {
				object := privatev1.Cluster_builder{
					Id:       uuid.New(),
					Metadata: privatev1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "missing-template"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{}.Build(),
				}.Build()
				field.set(object.GetStatus(), value)
				_, err := internal.Create(ctx, privatev1.ClustersCreateRequest_builder{Object: object}.Build())
				if err == nil {
					_, deleteErr := internal.Delete(ctx, privatev1.ClustersDeleteRequest_builder{Id: object.GetId()}.Build())
					Expect(deleteErr).ToNot(HaveOccurred())
					Fail("Create accepted " + value + " for " + field.path)
				}
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring(field.path))
				Expect(err.Error()).To(ContainSubstring("canonical IPv4"))
				_, getErr := internal.Get(ctx, privatev1.ClustersGetRequest_builder{Id: object.GetId()}.Build())
				Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
			}
		}

		By("creating a real cluster and accepting canonical IPv4 controller updates")
		bmit := createCatalogItemBareMetalInstanceTypeFixture(ctx, "shared")
		template := createCatalogItemClusterTemplateFixture(ctx, nil, nil)
		network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
		cluster, err := createClusterFixture(ctx, tool.ExternalView().UserConn(), publicv1.ClusterSpec_builder{
			Template: publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
			NodeSets: map[string]*publicv1.ClusterNodeSet{
				"workers": publicv1.ClusterNodeSet_builder{
					Size:                  new(int32(1)),
					BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bmit, Shared: true}.Build(),
				}.Build(),
			},
			NetworkAttachment: network.clusterAttachment(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())

		for _, field := range fields {
			status := privatev1.ClusterStatus_builder{}.Build()
			field.set(status, field.accepted)
			_, err := internal.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object:     privatev1.Cluster_builder{Id: cluster.GetId(), Status: status}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{field.path}},
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			stored, err := internal.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(field.get(stored.GetObject().GetStatus())).To(Equal(field.accepted))

			By("rejecting malformed or non-IPv4 values for " + field.path + " without changing stored status")
			for _, value := range invalidUpdateValues {
				updateStatus := privatev1.ClusterStatus_builder{}.Build()
				field.set(updateStatus, value)
				_, err = internal.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object:     privatev1.Cluster_builder{Id: cluster.GetId(), Status: updateStatus}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{field.path}},
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring(field.path))
				Expect(err.Error()).To(ContainSubstring("canonical IPv4"))

				stored, err = internal.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(field.get(stored.GetObject().GetStatus())).To(Equal(field.accepted))
			}
		}
	})
})
