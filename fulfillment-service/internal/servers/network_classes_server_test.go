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
	"net/netip"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Network classes server", func() {
	var server *PrivateNetworkClassesServer

	newServer := func(settings NetworkingSettings) *PrivateNetworkClassesServer {
		result, err := NewPrivateNetworkClassesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetNetworkingSettings(settings).
			Build()
		Expect(err).ToNot(HaveOccurred())
		return result
	}

	// installerObject is the object the installer sends.
	installerObject := func() *privatev1.NetworkClass {
		return privatev1.NetworkClass_builder{
			Title:         "OSAC NetworkClass",
			Description:   "Deployment networking",
			FabricManager: "netris",
			K8SManager:    new("cudn"),
			Spec: privatev1.NetworkClassSpec_builder{
				Defaults: privatev1.NetworkDefaults_builder{
					VirtualNetworkIpv4Cidr: "10.0.0.0/16",
					SubnetIpv4Cidr:         "10.0.0.0/24",
				}.Build(),
				EastWestCapabilities: privatev1.NetworkClassEastWestCapabilities_builder{
					SupportsEastWestEthernet: true,
				}.Build(),
				AddressPlan: fixtureAddressPlan(),
			}.Build(),
		}.Build()
	}

	create := func(object *privatev1.NetworkClass) (*privatev1.NetworkClass, error) {
		response, err := server.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{Object: object}.Build())
		return response.GetObject(), err
	}

	BeforeEach(func() {
		server = newServer(DefaultNetworkingSettings())
	})

	Describe("Create", func() {
		It("creates the installer NetworkClass in PENDING state, named after the fabric manager", func() {
			object, err := create(installerObject())
			Expect(err).ToNot(HaveOccurred())
			Expect(object.GetStatus().GetState()).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING))
			Expect(object.GetMetadata().GetName()).To(Equal("netris"))
			Expect(object.GetSpec().GetAddressPlan().GetOverlay()).To(BeNumerically("==", 0.75))
		})

		It("allows only one NetworkClass", func() {
			_, err := create(installerObject())
			Expect(err).ToNot(HaveOccurred())
			_, err = create(installerObject())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("only one NetworkClass per deployment is allowed"))
		})

		It("rejects status on create", func() {
			object := installerObject()
			object.SetStatus(privatev1.NetworkClassStatus_builder{
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}.Build())
			_, err := create(object)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		})

		DescribeTable("rejects invalid objects",
			func(mutate func(*privatev1.NetworkClass), message string) {
				object := installerObject()
				mutate(object)
				_, err := create(object)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring(message))
			},
			Entry("missing title", func(o *privatev1.NetworkClass) { o.SetTitle("") }, "title"),
			Entry("missing fabric manager", func(o *privatev1.NetworkClass) { o.SetFabricManager("") },
				"fabric_manager"),
			Entry("empty Kubernetes manager", func(o *privatev1.NetworkClass) { o.SetK8SManager("") },
				"k8s_manager"),
			Entry("missing defaults", func(o *privatev1.NetworkClass) { o.GetSpec().ClearDefaults() },
				"spec.defaults"),
			Entry("missing address plan", func(o *privatev1.NetworkClass) { o.GetSpec().ClearAddressPlan() },
				"address_plan"),
			Entry("Subnet outside the VirtualNetwork", func(o *privatev1.NetworkClass) {
				o.GetSpec().GetDefaults().SetSubnetIpv4Cidr("10.9.0.0/24")
			}, "subnet_ipv4_cidr"),
			Entry("share not a multiple of 1/16", func(o *privatev1.NetworkClass) {
				o.GetSpec().SetAddressPlan(privatev1.AddressPlan_builder{
					Overlay: 0.7, Fabric: 0.15, LbVip: 0.15,
				}.Build())
			}, "address"),
			Entry("default Subnet beyond the prefix limit", func(o *privatev1.NetworkClass) {
				o.GetSpec().GetDefaults().SetSubnetIpv4Cidr("10.0.0.0/27")
			}, "prefix limit"),
		)

		It("rejects a default range that overlaps an excluded range", func() {
			server = newServer(NetworkingSettings{
				ProvisioningEnabled: true,
				ExcludedIPv4CIDRs:   []netip.Prefix{netip.MustParsePrefix("10.0.128.0/17")},
			})
			_, err := create(installerObject())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("10.0.128.0/17"))
		})
	})

	Describe("Update", func() {
		var object *privatev1.NetworkClass

		patchPlan := func(plan *privatev1.AddressPlan) error {
			_, err := server.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Id:   object.GetId(),
					Spec: privatev1.NetworkClassSpec_builder{AddressPlan: plan}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.address_plan"}},
			}.Build())
			return err
		}

		BeforeEach(func() {
			var err error
			object, err = create(installerObject())
			Expect(err).ToNot(HaveOccurred())
		})

		It("accepts writing the stored address plan again", func() {
			Expect(patchPlan(object.GetSpec().GetAddressPlan())).To(Succeed())
		})

		It("rejects an address plan change even when no VirtualNetwork exists", func() {
			err := patchPlan(privatev1.AddressPlan_builder{Overlay: 0.5, Fabric: 0.25, LbVip: 0.25}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec is immutable"))
			response, err := server.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: object.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(response.GetObject().GetSpec().GetAddressPlan(),
				object.GetSpec().GetAddressPlan())).To(BeTrue())
		})

		It("rejects an address plan change while a VirtualNetwork exists", func() {
			createTenant("tenant-a")
			seedVirtualNetwork("tenant-a", "10.1.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
			err := patchPlan(privatev1.AddressPlan_builder{Overlay: 0.5, Fabric: 0.25, LbVip: 0.25}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec is immutable"))
		})

		It("rejects other spec changes", func() {
			_, err := server.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Id:            object.GetId(),
					FabricManager: "other",
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"fabric_manager"}},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec is immutable"))
		})

		It("accepts the operator status fields", func() {
			_, err := server.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Id: object.GetId(),
					Status: privatev1.NetworkClassStatus_builder{
						State:       privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
						Message:     new("ready"),
						NetworkType: "evpn",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					"status.state", "status.message", "status.network_managers", "status.network_type",
				}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Delete", func() {
		var object *privatev1.NetworkClass

		BeforeEach(func() {
			var err error
			object, err = create(installerObject())
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects the delete while a VirtualNetwork exists", func() {
			createTenant("tenant-a")
			vn := seedVirtualNetwork("tenant-a", "10.1.0.0/16",
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
			_, err := server.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: object.GetId()}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring(vn.GetId()))
		})

		It("rejects the delete while an ExternalIPPool exists", func() {
			pool := seedExternalIPPool("192.0.2.0/24", 256)
			_, err := server.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: object.GetId()}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring(pool.GetId()))
		})

		It("deletes the NetworkClass and accepts a second delete", func() {
			_, err := server.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: object.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: object.GetId()}.Build())
			Expect(grpcstatus.Code(err)).To(BeElementOf(grpccodes.OK, grpccodes.NotFound))
		})
	})
})
