/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Private subnets server", func() {
	var (
		server *PrivateSubnetsServer
		vn     *privatev1.VirtualNetwork
	)

	create := func(parent, name, cidr string, labels map[string]string) (*privatev1.Subnet, error) {
		response, err := server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   name,
					Tenant: "tenant-a",
					Labels: labels,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: parent}.Build(),
					Ipv4Cidr:       new(cidr),
				}.Build(),
			}.Build(),
		}.Build())
		return response.GetObject(), err
	}

	BeforeEach(func() {
		createTenant("tenant-a")
		createTenant("tenant-b")
		seedNetworkClass(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)
		vn = seedVirtualNetwork("tenant-a", "10.1.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
			nil)
		var err error
		server, err = NewPrivateSubnetsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Create", func() {
		It("creates a PENDING Subnet owned by its VirtualNetwork", func() {
			object, err := create(vn.GetId(), "s1", "10.1.1.0/24", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(object.GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
			Expect(object.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(ownerReferenceAnnotation, vn.GetId()))
			Expect(object.GetSpec().GetVirtualNetwork().GetId()).To(Equal(vn.GetId()))
		})

		It("resolves the parent VirtualNetwork by name", func() {
			object, err := create(vn.GetMetadata().GetName(), "s1", "10.1.1.0/24", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(object.GetSpec().GetVirtualNetwork().GetId()).To(Equal(vn.GetId()))
			Expect(object.GetSpec().GetVirtualNetwork().GetName()).To(Equal(vn.GetMetadata().GetName()))

			filter := fmt.Sprintf("this.spec.virtual_network.name == %q", vn.GetMetadata().GetName())
			list, err := server.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(list.GetItems()).To(HaveLen(1))
			Expect(list.GetItems()[0].GetId()).To(Equal(object.GetId()))
		})

		It("rejects a missing parent", func() {
			_, err := create("missing", "s1", "10.1.1.0/24", nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})

		It("rejects a parent of another tenant like a missing one", func() {
			other := seedVirtualNetwork("tenant-b", "10.2.0.0/16",
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
			_, err := create(other.GetId(), "s1", "10.2.1.0/24", nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})

		It("rejects a parent in another project of the tenant like a missing one", func() {
			seedTenantProject("tenant-a", "other-project")
			response, err := fixtureDAO[*privatev1.VirtualNetwork]().Create().SetObject(privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "other-project-vn",
					Tenant:  "tenant-a",
					Project: "other-project",
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new("10.4.0.0/16")}.Build(),
				Status: privatev1.VirtualNetworkStatus_builder{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			other := response.GetObject()
			for _, key := range []string{other.GetId(), other.GetMetadata().GetName()} {
				_, err = create(key, "s1", "10.4.1.0/24", nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("parent VirtualNetwork '" + key + "' does not exist"))
			}
		})

		It("rejects a parent that isn't READY", func() {
			pending := seedVirtualNetwork("tenant-a", "10.3.0.0/16",
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING, nil)
			_, err := create(pending.GetId(), "s1", "10.3.1.0/24", nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("not in READY state"))
		})

		It("rejects a CIDR outside the parent", func() {
			_, err := create(vn.GetId(), "s1", "10.9.1.0/24", nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("not within parent"))
		})

		It("rejects a prefix beyond the address plan prefix limit", func() {
			_, err := create(vn.GetId(), "s1", "10.1.1.0/26", nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("prefix limit /25"))
		})

		It("rejects a CIDR that overlaps a sibling, also one being deleted", func() {
			sibling := seedSubnet(vn, "", "10.1.1.0/24", privatev1.SubnetState_SUBNET_STATE_DELETING, nil)
			_, err := create(vn.GetId(), "s1", "10.1.1.0/25", nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists))
			Expect(err.Error()).To(ContainSubstring(sibling.GetId()))
		})

		It("checks every sibling, beyond the default list limit", func() {
			// More siblings than the DAO's default list limit (100); the overlapping one sorts last by ID.
			for i := range 101 {
				seedSubnet(vn, "", fmt.Sprintf("10.1.%d.0/28", i), privatev1.SubnetState_SUBNET_STATE_READY, nil)
			}
			last, err := fixtureDAO[*privatev1.Subnet]().Create().SetObject(privatev1.Subnet_builder{
				Id: "ffffffff-ffff-ffff-ffff-ffffffffffff",
				Metadata: privatev1.Metadata_builder{
					Name:   "last-sibling",
					Tenant: "tenant-a",
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
					Ipv4Cidr:       new("10.1.200.0/24"),
				}.Build(),
				Status: privatev1.SubnetStatus_builder{State: privatev1.SubnetState_SUBNET_STATE_READY}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = create(vn.GetId(), "s1", "10.1.200.0/25", nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists))
			Expect(err.Error()).To(ContainSubstring(last.GetObject().GetId()))
		})

		It("waits for the default Subnet in the tenant default VirtualNetwork", func() {
			defaultVN := seedVirtualNetwork("tenant-a", "10.0.0.0/16",
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, defaultLabels())
			defaultSubnet := seedSubnet(defaultVN, defaultSubnetName, "10.0.0.0/24",
				privatev1.SubnetState_SUBNET_STATE_PENDING, defaultLabels())
			_, err := create(defaultVN.GetId(), "s1", "10.0.1.0/24", nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("the default Subnet of this VirtualNetwork is not READY yet"))

			defaultSubnet.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_READY)
			_, err = fixtureDAO[*privatev1.Subnet]().Update().SetObject(defaultSubnet).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = create(defaultVN.GetId(), "s1", "10.0.1.0/24", nil)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Update and Delete", func() {
		var subnet *privatev1.Subnet

		BeforeEach(func() {
			subnet = seedSubnet(vn, "", "10.1.1.0/24", privatev1.SubnetState_SUBNET_STATE_READY, nil)
		})

		It("rejects a spec change", func() {
			subnet.GetSpec().SetIpv4Cidr("10.1.2.0/24")
			_, err := server.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
				Object:     subnet,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.ipv4_cidr"}},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec is immutable; delete and recreate the Subnet"))
		})

		It("rejects the delete while a ComputeInstance uses the Subnet", func() {
			instance, err := fixtureDAO[*privatev1.ComputeInstance]().Create().SetObject(privatev1.ComputeInstance_builder{
				Metadata: privatev1.Metadata_builder{Name: "vm", Tenant: "tenant-a"}.Build(),
				Spec: privatev1.ComputeInstanceSpec_builder{
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{
							Subnet: privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
						}.Build(),
					},
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: subnet.GetId()}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring(instance.GetObject().GetId()))
		})

		It("names the owning Cluster for a CaaS worker", func() {
			cluster, err := fixtureDAO[*privatev1.Cluster]().Create().SetObject(privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{Name: "cluster-1", Tenant: "system"}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = fixtureDAO[*privatev1.BareMetalInstance]().Create().SetObject(privatev1.BareMetalInstance_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "worker",
					Tenant: "system",
				}.Build(),
				Status: privatev1.BareMetalInstanceStatus_builder{
					Cluster: privatev1.ClusterLocalReference_builder{Id: cluster.GetObject().GetId()}.Build(),
				}.Build(),
				Spec: privatev1.BareMetalInstanceSpec_builder{
					NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
						privatev1.BareMetalNetworkAttachment_builder{
							Subnet: privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
						}.Build(),
					},
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: subnet.GetId()}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("Cluster '" + cluster.GetObject().GetId() + "'"))
		})

		It("accepts a second delete", func() {
			_, err := server.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: subnet.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: subnet.GetId()}.Build())
			Expect(grpcstatus.Code(err)).To(BeElementOf(grpccodes.OK, grpccodes.NotFound))
		})

		It("lets the controller delete a default Subnet but not a tenant", func() {
			defaultSubnet := seedSubnet(vn, defaultSubnetName, "10.1.2.0/24", privatev1.SubnetState_SUBNET_STATE_READY,
				defaultLabels())
			_, err := server.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: defaultSubnet.GetId()}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			_, err = server.Delete(controllerContext(),
				privatev1.SubnetsDeleteRequest_builder{Id: defaultSubnet.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
