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

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("SecurityGroups server", func() {
	var virtualNetworkID string

	BeforeEach(func() {
		var err error

		// Create a default NetworkClass for tests:
		ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		nc := privatev1.NetworkClass_builder{
			Id:            "default",
			FabricManager: "ovn-kubernetes",
			Metadata: privatev1.Metadata_builder{
				Tenant: testTenant,
			}.Build(),
			Spec: privatev1.NetworkClassSpec_builder{AddressPlan: fixtureAddressPlan()}.Build(),
			Status: privatev1.NetworkClassStatus_builder{
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}.Build(),
		}.Build()

		createNCResponse, err := ncDao.Create().
			SetObject(nc).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(createNCResponse.GetObject().GetId()).To(Equal("default"))

		// Create a default VirtualNetwork for tests:
		vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		vn := privatev1.VirtualNetwork_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: testTenant,
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{
				Ipv4Cidr: new("10.0.0.0/16"),
			}.Build(),
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
			}.Build(),
		}.Build()

		createVNResponse, err := vnDao.Create().
			SetObject(vn).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		virtualNetworkID = createVNResponse.GetObject().GetId()
		Expect(virtualNetworkID).ToNot(BeEmpty())
	})

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewSecurityGroupsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewSecurityGroupsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewSecurityGroupsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *SecurityGroupsServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewSecurityGroupsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						Ingress: []*publicv1.SecurityRule{
							{
								Protocol: publicv1.Protocol_PROTOCOL_TCP,
								PortFrom: new(int32(80)),
								PortTo:   new(int32(80)),
								Ipv4Cidr: new("0.0.0.0/0"),
							},
						},
						Egress: []*publicv1.SecurityRule{
							{
								Protocol: publicv1.Protocol_PROTOCOL_ALL,
								Ipv4Cidr: new("0.0.0.0/0"),
							},
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetSpec().GetIngress()).To(HaveLen(1))
			Expect(object.GetSpec().GetEgress()).To(HaveLen(1))
		})

		It("updates both rule lists while preserving the security group identity and parent", func() {
			name := fmt.Sprintf("test-%s", uuid.NewString()[:8])
			created, err := server.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Metadata: publicv1.Metadata_builder{Name: name}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						Ingress: []*publicv1.SecurityRule{{
							Protocol: publicv1.Protocol_PROTOCOL_TCP,
							PortFrom: new(int32(80)),
							PortTo:   new(int32(80)),
							Ipv4Cidr: new("0.0.0.0/0"),
						}},
						Egress: []*publicv1.SecurityRule{{
							Protocol: publicv1.Protocol_PROTOCOL_ALL,
							Ipv4Cidr: new("0.0.0.0/0"),
						}},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			markSecurityGroupReady(created.GetObject().GetId())

			updated, err := server.Update(ctx, publicv1.SecurityGroupsUpdateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Id:       created.GetObject().GetId(),
					Metadata: publicv1.Metadata_builder{Name: "changed-name"}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: "changed-vnet"}.Build(),
						Ingress: []*publicv1.SecurityRule{{
							Protocol: publicv1.Protocol_PROTOCOL_UDP,
							PortFrom: new(int32(53)),
							PortTo:   new(int32(53)),
							Ipv4Cidr: new("10.0.0.0/24"),
						}},
						Egress: []*publicv1.SecurityRule{{
							Protocol: publicv1.Protocol_PROTOCOL_TCP,
							PortFrom: new(int32(443)),
							PortTo:   new(int32(443)),
							Ipv4Cidr: new("192.168.0.0/16"),
						}},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			object := updated.GetObject()
			Expect(object.GetId()).To(Equal(created.GetObject().GetId()))
			Expect(object.GetMetadata().GetName()).To(Equal(name))
			Expect(object.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]).To(Equal(virtualNetworkID))
			Expect(object.GetSpec().GetVirtualNetwork().GetId()).To(Equal(virtualNetworkID))
			Expect(object.GetSpec().GetIngress()).To(HaveLen(1))
			Expect(object.GetSpec().GetIngress()[0].GetProtocol()).To(Equal(publicv1.Protocol_PROTOCOL_UDP))
			Expect(object.GetSpec().GetIngress()[0].GetPortFrom()).To(Equal(int32(53)))
			Expect(object.GetSpec().GetEgress()).To(HaveLen(1))
			Expect(object.GetSpec().GetEgress()[0].GetPortFrom()).To(Equal(int32(443)))
		})

		It("rejects reserved metadata on update without changing the stored security group", func() {
			created, err := server.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("reserved-metadata-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						Ingress: []*publicv1.SecurityRule{{
							Protocol: publicv1.Protocol_PROTOCOL_TCP,
							PortFrom: new(int32(80)),
							PortTo:   new(int32(80)),
							Ipv4Cidr: new("10.0.0.0/8"),
						}},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			securityGroupsDAO, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			storedResponse, err := securityGroupsDAO.Get().SetId(created.GetObject().GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			before := proto.Clone(storedResponse.GetObject()).(*privatev1.SecurityGroup)

			for _, testCase := range []struct {
				key    string
				labels bool
			}{
				{key: "admin.osac.openshift.io/managed", labels: true},
				{key: "provider.admin.osac.openshift.io/id"},
			} {
				labels := map[string]string{}
				annotations := map[string]string{}
				if testCase.labels {
					labels[testCase.key] = "system"
				} else {
					annotations[testCase.key] = "provider"
				}

				response, err := server.Update(ctx, publicv1.SecurityGroupsUpdateRequest_builder{
					Object: publicv1.SecurityGroup_builder{
						Id:       created.GetObject().GetId(),
						Metadata: publicv1.Metadata_builder{Labels: labels, Annotations: annotations}.Build(),
						Spec:     created.GetObject().GetSpec(),
					}.Build(),
				}.Build())
				Expect(response).To(BeNil())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(grpcstatus.Convert(err).Message()).To(Equal(reservedMetadataError))

				updated, err := securityGroupsDAO.Get().SetId(created.GetObject().GetId()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(updated.GetObject(), before)).To(BeTrue())
			}
		})

		It("List objects", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
					Object: publicv1.SecurityGroup_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("sg-%d", i),
						}.Build(),
						Spec: publicv1.SecurityGroupSpec_builder{
							VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
							Ingress: []*publicv1.SecurityRule{
								{
									Protocol: publicv1.Protocol_PROTOCOL_TCP,
									PortFrom: new(int32(443)),
									PortTo:   new(int32(443)),
									Ipv4Cidr: new("0.0.0.0/0"),
								},
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.SecurityGroupsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects:
			const count = 10
			for range count {
				_, err := server.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
					Object: publicv1.SecurityGroup_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: publicv1.SecurityGroupSpec_builder{
							VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.SecurityGroupsListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			// Create a few objects:
			const count = 10
			for range count {
				_, err := server.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
					Object: publicv1.SecurityGroup_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: publicv1.SecurityGroupSpec_builder{
							VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.SecurityGroupsListRequest_builder{
				Offset: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			// Create a few objects:
			const count = 10
			var objects []*publicv1.SecurityGroup
			for range count {
				response, err := server.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
					Object: publicv1.SecurityGroup_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: publicv1.SecurityGroupSpec_builder{
							VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, response.GetObject())
			}

			// List the objects:
			for _, object := range objects {
				response, err := server.List(ctx, publicv1.SecurityGroupsListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						Ingress: []*publicv1.SecurityRule{
							{
								Protocol: publicv1.Protocol_PROTOCOL_TCP,
								PortFrom: new(int32(22)),
								PortTo:   new(int32(22)),
								Ipv4Cidr: new("10.0.0.0/8"),
							},
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get it:
			getResponse, err := server.Get(ctx, publicv1.SecurityGroupsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Rejects non-canonical rule CIDRs on Create", func() {
			_, err := server.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						Ingress: []*publicv1.SecurityRule{
							{
								Protocol: publicv1.Protocol_PROTOCOL_TCP,
								PortFrom: new(int32(80)),
								PortTo:   new(int32(80)),
								Ipv4Cidr: new("10.0.1.5/24"),
							},
						},
						Egress: []*publicv1.SecurityRule{
							{
								Protocol: publicv1.Protocol_PROTOCOL_ALL,
								Ipv4Cidr: new("0.0.0.0/0"),
							},
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("canonical"))
		})

		It("Delete object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Add a finalizer, as otherwise the object will be immediately deleted and archived and it
			// won't be possible to verify the deletion timestamp. This can't be done using the server
			// because this is a public object, and public objects don't have the finalizers field.
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(
				ctx,
				`update security_groups set finalizers = '{"a"}' where id = $1`,
				object.GetId(),
			)
			Expect(err).ToNot(HaveOccurred())

			// Delete the object:
			_, err = server.Delete(ctx, publicv1.SecurityGroupsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.SecurityGroupsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("blocks deletion of default-labeled SecurityGroup", func() {
			createResponse, err := server.Create(controllerContext(), publicv1.SecurityGroupsCreateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "default-security-group",
						Labels: map[string]string{
							"osac.openshift.io/default": "true",
						},
					}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, publicv1.SecurityGroupsDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("default"))
			Expect(err.Error()).To(ContainSubstring("system-managed"))

			getResponse, err := server.Get(ctx, publicv1.SecurityGroupsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).To(BeNil())
		})

	})

	Describe("Tenant isolation", func() {
		var privateServer *PrivateSecurityGroupsServer

		BeforeEach(func() {
			var err error
			privateServer, err = NewPrivateSecurityGroupsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects private rule edits and allows status updates", func() {
			created, err := privateServer.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
				Object: privatev1.SecurityGroup_builder{
					Metadata: privatev1.Metadata_builder{Name: "immutable-security-group-rules", Tenant: testTenant}.Build(),
					Spec: privatev1.SecurityGroupSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						Ingress:        []*privatev1.SecurityRule{{Protocol: privatev1.Protocol_PROTOCOL_TCP, PortFrom: new(int32(22)), PortTo: new(int32(22)), Ipv4Cidr: new("0.0.0.0/0")}},
						Egress:         []*privatev1.SecurityRule{{Protocol: privatev1.Protocol_PROTOCOL_ALL, Ipv4Cidr: new("0.0.0.0/0")}},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Private updates are reserved for controller status and feedback.
			changed := proto.Clone(created.GetObject()).(*privatev1.SecurityGroup)
			changed.GetSpec().GetIngress()[0].SetIpv4Cidr("10.0.0.0/8")
			_, err = privateServer.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
				Object: changed, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.ingress"}},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err).To(MatchError(ContainSubstring("spec is immutable")))
			stored, getErr := privateServer.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: created.GetObject().GetId()}.Build())
			Expect(getErr).ToNot(HaveOccurred())
			Expect(proto.Equal(stored.GetObject().GetSpec(), created.GetObject().GetSpec())).To(BeTrue())

			_, err = privateServer.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
				Object: privatev1.SecurityGroup_builder{
					Id: created.GetObject().GetId(),
					Status: privatev1.SecurityGroupStatus_builder{
						State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects moving a stored SecurityGroup into a reserved tenant and preserves the stored object", func() {
			created, err := privateServer.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
				Object: privatev1.SecurityGroup_builder{
					Metadata: privatev1.Metadata_builder{Name: "tenant-immutable-security-group", Tenant: testTenant}.Build(),
					Spec: privatev1.SecurityGroupSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := created.GetObject().GetId()

			_, err = privateServer.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
				Object: privatev1.SecurityGroup_builder{
					Id:       id,
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.tenant"}},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))
			Expect(err.Error()).To(ContainSubstring("objects cannot be placed in the 'shared' tenant"))

			stored, getErr := privateServer.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
			Expect(getErr).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
		})

		It("blocks deletion after an update omits the default label", func() {
			createResp, err := privateServer.Create(controllerContext(), privatev1.SecurityGroupsCreateRequest_builder{
				Object: privatev1.SecurityGroup_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "default-security-group-update",
						Tenant: testTenant,
						Labels: map[string]string{"osac.openshift.io/default": "true"},
					}.Build(),
					Spec: privatev1.SecurityGroupSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			object := createResp.GetObject()
			object.GetMetadata().SetLabels(map[string]string{"env": "test"})
			_, err = privateServer.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{Object: object}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))

			_, err = privateServer.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{Id: object.GetId()}.Build())
			Expect(err).To(HaveOccurred())
			status, ok = grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))

			getResponse, err := privateServer.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: object.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/default", "true"))
		})

		It("rejects deletion while a referencing compute instance is being deleted", func() {
			created, err := privateServer.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
				Object: privatev1.SecurityGroup_builder{
					Metadata: privatev1.Metadata_builder{Tenant: testTenant, Name: "group-with-active-dependent"}.Build(),
					Spec: privatev1.SecurityGroupSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			computeInstances, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			instance, err := computeInstances.Create().SetObject(privatev1.ComputeInstance_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: testTenant, Name: "instance-with-deleting-group-ref", Finalizers: []string{"test-cleanup"},
				}.Build(),
				Spec: privatev1.ComputeInstanceSpec_builder{
					NetworkAttachments: []*privatev1.ComputeNetworkAttachment{privatev1.ComputeNetworkAttachment_builder{
						SecurityGroups: []*privatev1.SecurityGroupLocalReference{
							privatev1.SecurityGroupLocalReference_builder{Id: created.GetObject().GetId()}.Build(),
						},
					}.Build()},
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = computeInstances.Delete().SetId(instance.GetObject().GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = privateServer.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("ComputeInstance"))
		})

	})
	Describe("ICMP rule gate", func() {
		const ingressMessage = "ingress rule at index 1: protocol icmp is not supported by this deployment's " +
			"network managers; use protocol all, which includes ICMP"
		const egressMessage = "egress rule at index 0: protocol icmp is not supported by this deployment's " +
			"network managers; use protocol all, which includes ICMP"

		var (
			server  *SecurityGroupsServer
			private *PrivateSecurityGroupsServer
		)

		// setCapability records status.security_group_icmp on the deployment NetworkClass; nil clears it.
		setCapability := func(value *bool) {
			classes := fixtureDAO[*privatev1.NetworkClass]()
			response, err := classes.Get().SetId("default").Do(ctx)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			object := response.GetObject()
			if value == nil {
				object.GetStatus().ClearSecurityGroupIcmp()
			} else {
				object.GetStatus().SetSecurityGroupIcmp(*value)
			}
			_, err = classes.Update().SetObject(object).Do(ctx)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		}

		newPrivate := func(settings NetworkingSettings) *PrivateSecurityGroupsServer {
			result, err := NewPrivateSecurityGroupsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetNetworkingSettings(settings).
				Build()
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			return result
		}

		tcp := func() *publicv1.SecurityRule {
			return publicv1.SecurityRule_builder{
				Protocol: publicv1.Protocol_PROTOCOL_TCP, PortFrom: new(int32(80)), PortTo: new(int32(80)),
				Ipv4Cidr: new("10.0.0.0/8"),
			}.Build()
		}
		icmp := func() *publicv1.SecurityRule {
			return publicv1.SecurityRule_builder{
				Protocol: publicv1.Protocol_PROTOCOL_ICMP, Ipv4Cidr: new("10.0.0.0/8"),
			}.Build()
		}
		all := func() *publicv1.SecurityRule {
			return publicv1.SecurityRule_builder{
				Protocol: publicv1.Protocol_PROTOCOL_ALL, Ipv4Cidr: new("10.0.0.0/8"),
			}.Build()
		}
		publicCreate := func(srv *SecurityGroupsServer, vn string, ingress, egress []*publicv1.SecurityRule) (
			*publicv1.SecurityGroupsCreateResponse, error) {
			return srv.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Metadata: publicv1.Metadata_builder{Name: fmt.Sprintf("icmp-%s", uuid.NewString()[:8])}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: vn}.Build(),
						Ingress:        ingress,
						Egress:         egress,
					}.Build(),
				}.Build(),
			}.Build())
		}
		publicUpdate := func(id string, ingress, egress []*publicv1.SecurityRule) error {
			_, err := server.Update(ctx, publicv1.SecurityGroupsUpdateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Id: id,
					Spec: publicv1.SecurityGroupSpec_builder{
						Ingress: ingress, Egress: egress,
					}.Build(),
				}.Build(),
			}.Build())
			return err
		}
		stored := func(id string) *privatev1.SecurityGroup {
			response, err := private.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			return response.GetObject()
		}
		setState := func(id string, state privatev1.SecurityGroupState) {
			groups := fixtureDAO[*privatev1.SecurityGroup]()
			response, err := groups.Get().SetId(id).Do(ctx)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			object := response.GetObject()
			object.GetStatus().SetState(state)
			_, err = groups.Update().SetObject(object).Do(ctx)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		}

		BeforeEach(func() {
			var err error
			server, err = NewSecurityGroupsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			private = newPrivate(DefaultNetworkingSettings())
		})

		DescribeTable("acceptsICMPRules",
			func(status *privatev1.NetworkClassStatus, provisioningEnabled, want bool) {
				Expect(acceptsICMPRules(status, provisioningEnabled)).To(Equal(want))
			},
			Entry("true with provisioning enabled",
				privatev1.NetworkClassStatus_builder{SecurityGroupIcmp: new(true)}.Build(), true, true),
			Entry("true with provisioning disabled",
				privatev1.NetworkClassStatus_builder{SecurityGroupIcmp: new(true)}.Build(), false, true),
			Entry("false with provisioning enabled",
				privatev1.NetworkClassStatus_builder{SecurityGroupIcmp: new(false)}.Build(), true, false),
			Entry("false with provisioning disabled",
				privatev1.NetworkClassStatus_builder{SecurityGroupIcmp: new(false)}.Build(), false, false),
			Entry("unset with provisioning disabled", &privatev1.NetworkClassStatus{}, false, true),
			Entry("unset with provisioning enabled", &privatev1.NetworkClassStatus{}, true, false),
			Entry("a nil status with provisioning enabled", nil, true, false),
		)

		It("rejects an ingress icmp rule on create with the rule's index", func() {
			setCapability(new(false))
			_, err := publicCreate(server, virtualNetworkID, []*publicv1.SecurityRule{tcp(), icmp()}, nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcstatus.Convert(err).Message()).To(Equal(ingressMessage))
		})

		It("rejects an egress icmp rule on create", func() {
			setCapability(new(false))
			_, err := publicCreate(server, virtualNetworkID, []*publicv1.SecurityRule{tcp()},
				[]*publicv1.SecurityRule{icmp()})
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcstatus.Convert(err).Message()).To(Equal(egressMessage))
		})

		It("rejects through the private Create", func() {
			setCapability(new(false))
			_, err := private.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
				Object: privatev1.SecurityGroup_builder{
					Metadata: privatev1.Metadata_builder{Name: "private-icmp", Tenant: testTenant}.Build(),
					Spec: privatev1.SecurityGroupSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						Egress: []*privatev1.SecurityRule{privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_ICMP, Ipv4Cidr: new("10.0.0.0/8"),
						}.Build()},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcstatus.Convert(err).Message()).To(Equal(egressMessage))
		})

		It("creates an icmp rule when the capability is true", func() {
			setCapability(new(true))
			response, err := publicCreate(server, virtualNetworkID, []*publicv1.SecurityRule{icmp()}, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored(response.GetObject().GetId()).GetSpec().GetIngress()[0].GetProtocol()).
				To(Equal(privatev1.Protocol_PROTOCOL_ICMP))
		})

		It("fails closed on a NetworkClass without the field", func() {
			setCapability(nil)
			_, err := publicCreate(server, virtualNetworkID, []*publicv1.SecurityRule{icmp()}, nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		})

		It("stores icmp rules with provider networking disabled", func() {
			setCapability(nil)
			disabled := NetworkingSettings{ProvisioningEnabled: false}
			disabledServer, err := NewSecurityGroupsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetNetworkingSettings(disabled).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = publicCreate(disabledServer, virtualNetworkID, []*publicv1.SecurityRule{icmp()}, nil)
			Expect(err).ToNot(HaveOccurred())
		})

		It("accepts protocol all without the capability", func() {
			setCapability(new(false))
			_, err := publicCreate(server, virtualNetworkID, []*publicv1.SecurityRule{all()},
				[]*publicv1.SecurityRule{all()})
			Expect(err).ToNot(HaveOccurred())
		})

		It("keeps the parent VirtualNetwork error first", func() {
			setCapability(new(false))
			_, err := publicCreate(server, "missing-network", []*publicv1.SecurityRule{icmp()}, nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("does not exist"))
		})

		It("fails with FailedPrecondition without a NetworkClass", func() {
			_, err := fixtureDAO[*privatev1.NetworkClass]().Delete().SetId("default").Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = publicCreate(server, virtualNetworkID, []*publicv1.SecurityRule{icmp()}, nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(grpcstatus.Convert(err).Message()).To(Equal("no NetworkClass is configured"))
		})

		It("rejects a rule update adding icmp and leaves the group unchanged", func() {
			setCapability(new(false))
			created, err := publicCreate(server, virtualNetworkID, []*publicv1.SecurityRule{all()}, nil)
			Expect(err).ToNot(HaveOccurred())
			id := created.GetObject().GetId()
			markSecurityGroupReady(id)
			before := stored(id)

			err = publicUpdate(id, []*publicv1.SecurityRule{all(), icmp()}, nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcstatus.Convert(err).Message()).To(Equal(ingressMessage))

			after := stored(id)
			Expect(proto.Equal(after.GetSpec(), before.GetSpec())).To(BeTrue())
			Expect(after.GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY))
			Expect(after.GetStatus().GetMessage()).ToNot(Equal(securityGroupUpdatingMessage))
		})

		It("rejects an update keeping a stored icmp rule", func() {
			setCapability(new(true))
			created, err := publicCreate(server, virtualNetworkID, []*publicv1.SecurityRule{icmp()}, nil)
			Expect(err).ToNot(HaveOccurred())
			id := created.GetObject().GetId()
			setState(id, privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED)
			setCapability(new(false))

			err = publicUpdate(id, []*publicv1.SecurityRule{icmp()}, nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(stored(id).GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED))
		})

		It("accepts an update replacing a stored icmp rule with all", func() {
			setCapability(new(true))
			created, err := publicCreate(server, virtualNetworkID, []*publicv1.SecurityRule{icmp()}, nil)
			Expect(err).ToNot(HaveOccurred())
			id := created.GetObject().GetId()
			setState(id, privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED)
			setCapability(new(false))

			Expect(publicUpdate(id, []*publicv1.SecurityRule{all()}, nil)).To(Succeed())
			after := stored(id)
			Expect(after.GetSpec().GetIngress()[0].GetProtocol()).To(Equal(privatev1.Protocol_PROTOCOL_ALL))
			Expect(after.GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
		})

		It("keeps status updates, get and delete working on a stored icmp group", func() {
			setCapability(new(true))
			created, err := publicCreate(server, virtualNetworkID, []*publicv1.SecurityRule{icmp()}, nil)
			Expect(err).ToNot(HaveOccurred())
			id := created.GetObject().GetId()
			setCapability(new(false))

			Expect(stored(id).GetSpec().GetIngress()[0].GetProtocol()).To(Equal(privatev1.Protocol_PROTOCOL_ICMP))
			_, err = private.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
				Object: privatev1.SecurityGroup_builder{
					Id: id,
					Status: privatev1.SecurityGroupStatus_builder{
						State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Delete(ctx, publicv1.SecurityGroupsDeleteRequest_builder{Id: id}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("round-trips false and clears the field through NetworkClasses.Update", func() {
			// The shared fixture class is not valid for a validated private update, so replace it.
			fixtures := fixtureDAO[*privatev1.NetworkClass]()
			_, err := fixtures.Delete().SetId("default").Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			created, err := fixtures.Create().SetObject(privatev1.NetworkClass_builder{
				Id:            "round-trip",
				FabricManager: "netris",
				Metadata:      privatev1.Metadata_builder{Name: "round-trip", Tenant: testTenant}.Build(),
				Spec: privatev1.NetworkClassSpec_builder{
					AddressPlan: fixtureAddressPlan(),
					Defaults: privatev1.NetworkDefaults_builder{
						VirtualNetworkIpv4Cidr: "10.0.0.0/16", SubnetIpv4Cidr: "10.0.0.0/24",
					}.Build(),
				}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			classID := created.GetObject().GetId()

			classes, err := NewPrivateNetworkClassesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			mask := &fieldmaskpb.FieldMask{Paths: []string{"status.security_group_icmp"}}
			update := func(status *privatev1.NetworkClassStatus) {
				_, err := classes.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object:     privatev1.NetworkClass_builder{Id: classID, Status: status}.Build(),
					UpdateMask: mask,
				}.Build())
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
			}
			read := func() *privatev1.NetworkClassStatus {
				response, err := classes.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: classID}.Build())
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				return response.GetObject().GetStatus()
			}

			update(privatev1.NetworkClassStatus_builder{SecurityGroupIcmp: new(false)}.Build())
			Expect(read().HasSecurityGroupIcmp()).To(BeTrue())
			Expect(read().GetSecurityGroupIcmp()).To(BeFalse())

			update(&privatev1.NetworkClassStatus{})
			Expect(read().HasSecurityGroupIcmp()).To(BeFalse())
		})
	})
})
