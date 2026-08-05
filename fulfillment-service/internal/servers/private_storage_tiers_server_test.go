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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Private storage tiers server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			backendsDAO, err := dao.NewGenericDAO[*privatev1.StorageBackend]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			server, err := NewPrivateStorageTiersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetStorageBackendsDAO(backendsDAO).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateStorageTiersServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateStorageTiersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if storage backends DAO is not set", func() {
			server, err := NewPrivateStorageTiersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("storage backends DAO is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			server    *PrivateStorageTiersServer
			backendID string
		)

		BeforeEach(func() {
			var err error

			// Create a real StorageBackend so that backend reference validation passes:
			backendsServer, err := NewPrivateStorageBackendsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			backendResp, err := backendsServer.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
				Object: privatev1.StorageBackend_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-backend",
					}.Build(),
					Spec: privatev1.StorageBackendSpec_builder{
						Provider: "vast",
						Endpoint: "https://storage.example.com:8443",
						Credentials: privatev1.StorageBackendCredentials_builder{
							Username: "admin",
							Password: "secret",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			backendID = backendResp.GetObject().GetId()

			// Create the StorageBackends DAO for cross-resource validation:
			backendsDAO, err := dao.NewGenericDAO[*privatev1.StorageBackend]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Build the server with the backends DAO injected:
			server, err = NewPrivateStorageTiersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetStorageBackendsDAO(backendsDAO).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createStorageTier := func() *privatev1.StorageTier {
			response, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-tier",
					}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Description: "A test storage tier",
						Backends: []*privatev1.BackendAssociation{
							privatev1.BackendAssociation_builder{
								BackendId:            backendID,
								Protocol:             privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
								MaxReadBandwidthMbs:  1000,
								MaxWriteBandwidthMbs: 500,
								QuotaGib:             1024,
								EncryptionEnabled:    true,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		createStorageTierWithName := func(name string) *privatev1.StorageTier {
			response, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Metadata: privatev1.Metadata_builder{
						Name: name,
					}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Description: "A test storage tier",
						Backends: []*privatev1.BackendAssociation{
							privatev1.BackendAssociation_builder{
								BackendId:            backendID,
								Protocol:             privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
								MaxReadBandwidthMbs:  1000,
								MaxWriteBandwidthMbs: 500,
								QuotaGib:             1024,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		It("Creates and gets a storage tier", func() {
			created := createStorageTier()

			Expect(created.GetId()).ToNot(BeEmpty())
			Expect(created.GetSpec().GetDescription()).To(Equal("A test storage tier"))
			Expect(created.GetSpec().GetBackends()).To(HaveLen(1))
			Expect(created.GetSpec().GetBackends()[0].GetBackendId()).To(Equal(backendID))
			Expect(created.GetSpec().GetBackends()[0].GetProtocol()).To(Equal(
				privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS))
			Expect(created.GetSpec().GetBackends()[0].GetMaxReadBandwidthMbs()).To(Equal(int32(1000)))
			Expect(created.GetSpec().GetBackends()[0].GetMaxWriteBandwidthMbs()).To(Equal(int32(500)))
			Expect(created.GetSpec().GetBackends()[0].GetQuotaGib()).To(Equal(int64(1024)))
			Expect(created.GetSpec().GetBackends()[0].GetEncryptionEnabled()).To(BeTrue())
			Expect(created.GetStatus().GetState()).To(Equal(
				privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE))
			Expect(created.GetMetadata().GetTenant()).To(Equal(auth.SharedTenant))

			getResponse, err := server.Get(ctx, privatev1.StorageTiersGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			obj := getResponse.GetObject()
			Expect(obj.GetId()).To(Equal(created.GetId()))
			Expect(obj.GetSpec().GetDescription()).To(Equal("A test storage tier"))
			Expect(obj.GetSpec().GetBackends()).To(HaveLen(1))
			Expect(obj.GetSpec().GetBackends()[0].GetBackendId()).To(Equal(backendID))
		})

		It("Get returns NOT_FOUND for non-existent ID", func() {
			_, err := server.Get(ctx, privatev1.StorageTiersGetRequest_builder{
				Id: "non-existent-id",
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.NotFound))
		})

		It("List objects", func() {
			const count = 5
			for i := range count {
				createStorageTierWithName(fmt.Sprintf("tier-%d", i))
			}

			response, err := server.List(ctx, privatev1.StorageTiersListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			const count = 5
			for i := range count {
				createStorageTierWithName(fmt.Sprintf("tier-%d", i))
			}

			response, err := server.List(ctx, privatev1.StorageTiersListRequest_builder{
				Limit: new(int32(2)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 2))
		})

		It("List objects with offset", func() {
			const count = 5
			for i := range count {
				createStorageTierWithName(fmt.Sprintf("tier-%d", i))
			}

			response, err := server.List(ctx, privatev1.StorageTiersListRequest_builder{
				Offset: new(int32(2)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-2))
		})

		It("List objects with filter", func() {
			const count = 3
			var ids []string
			for i := range count {
				obj := createStorageTierWithName(fmt.Sprintf("tier-%d", i))
				ids = append(ids, obj.GetId())
			}

			for _, id := range ids {
				response, err := server.List(ctx, privatev1.StorageTiersListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", id)),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(id))
			}
		})

		It("List objects with order", func() {
			createStorageTierWithName("aaa-tier")
			createStorageTierWithName("zzz-tier")

			response, err := server.List(ctx, privatev1.StorageTiersListRequest_builder{
				Order: new("metadata.name asc"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 2))
			Expect(response.GetItems()[0].GetMetadata().GetName()).To(Equal("aaa-tier"))
			Expect(response.GetItems()[1].GetMetadata().GetName()).To(Equal("zzz-tier"))
		})

		It("Update applies partial changes via field mask", func() {
			created := createStorageTier()

			updateResponse, err := server.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Id: created.GetId(),
					Spec: privatev1.StorageTierSpec_builder{
						Description: "Updated description",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.description"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetDescription()).To(Equal("Updated description"))
			Expect(updateResponse.GetObject().GetSpec().GetBackends()).To(HaveLen(1))
			Expect(updateResponse.GetObject().GetSpec().GetBackends()[0].GetBackendId()).To(Equal(backendID))
		})

		It("Update backends replaces the backend association", func() {
			created := createStorageTier()

			updateResponse, err := server.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Id: created.GetId(),
					Spec: privatev1.StorageTierSpec_builder{
						Backends: []*privatev1.BackendAssociation{
							privatev1.BackendAssociation_builder{
								BackendId:            backendID,
								Protocol:             privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
								MaxReadBandwidthMbs:  2000,
								MaxWriteBandwidthMbs: 1000,
								QuotaGib:             2048,
								EncryptionEnabled:    false,
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.backends"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetBackends()).To(HaveLen(1))
			Expect(updateResponse.GetObject().GetSpec().GetBackends()[0].GetProtocol()).To(Equal(
				privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK))
			Expect(updateResponse.GetObject().GetSpec().GetBackends()[0].GetMaxReadBandwidthMbs()).To(Equal(int32(2000)))
			Expect(updateResponse.GetObject().GetSpec().GetBackends()[0].GetQuotaGib()).To(Equal(int64(2048)))
		})

		It("Delete removes the object", func() {
			created := createStorageTier()

			_, err := server.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Get(ctx, privatev1.StorageTiersGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.NotFound))
		})

		It("Generates UUID for id ignoring caller-provided value", func() {
			callerProvidedId := "my-custom-id"
			response, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Id: callerProvidedId,
					Metadata: privatev1.Metadata_builder{
						Name: "test-tier",
					}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Backends: []*privatev1.BackendAssociation{
							privatev1.BackendAssociation_builder{
								BackendId: backendID,
								Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).ToNot(Equal(callerProvidedId))
			_, err = uuid.Parse(response.GetObject().GetId())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Create always sets state to ACTIVE regardless of caller-provided state", func() {
			response, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-tier",
					}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Backends: []*privatev1.BackendAssociation{
							privatev1.BackendAssociation_builder{
								BackendId: backendID,
								Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
							}.Build(),
						},
					}.Build(),
					Status: privatev1.StorageTierStatus_builder{
						State: privatev1.StorageTierState_STORAGE_TIER_STATE_UNSPECIFIED,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE))
		})

		It("Create forces tenant to shared", func() {
			response, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-tier",
						Tenant: "some-other-tenant",
					}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Backends: []*privatev1.BackendAssociation{
							privatev1.BackendAssociation_builder{
								BackendId: backendID,
								Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetMetadata().GetTenant()).To(Equal(auth.SharedTenant))
		})

		Describe("Validation", func() {
			It("Create without name fails", func() {
				_, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Spec: privatev1.StorageTierSpec_builder{
							Backends: []*privatev1.BackendAssociation{
								privatev1.BackendAssociation_builder{
									BackendId: backendID,
									Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("metadata.name"))
			})

			It("Create without backends fails", func() {
				_, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-tier",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("backends"))
			})

			It("Create with empty backend_id fails", func() {
				_, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-tier",
						}.Build(),
						Spec: privatev1.StorageTierSpec_builder{
							Backends: []*privatev1.BackendAssociation{
								privatev1.BackendAssociation_builder{
									Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("backend_id"))
			})

			It("Create with nil object fails", func() {
				_, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("storage tier is mandatory"))
			})

			It("Create with non-existent backend_id fails", func() {
				_, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-tier",
						}.Build(),
						Spec: privatev1.StorageTierSpec_builder{
							Backends: []*privatev1.BackendAssociation{
								privatev1.BackendAssociation_builder{
									BackendId: "no-such-backend",
									Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.NotFound))
				Expect(st.Message()).To(ContainSubstring("no-such-backend"))
			})

			It("Create with more than one backend fails in v0.1", func() {
				_, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-tier",
						}.Build(),
						Spec: privatev1.StorageTierSpec_builder{
							Backends: []*privatev1.BackendAssociation{
								privatev1.BackendAssociation_builder{
									BackendId: backendID,
									Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
								}.Build(),
								privatev1.BackendAssociation_builder{
									BackendId: backendID,
									Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("one backend"))
			})

			It("Update with non-existent backend_id fails", func() {
				created := createStorageTier()

				_, err := server.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Id: created.GetId(),
						Spec: privatev1.StorageTierSpec_builder{
							Backends: []*privatev1.BackendAssociation{
								privatev1.BackendAssociation_builder{
									BackendId: "no-such-backend",
									Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.backends"}},
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.NotFound))
				Expect(st.Message()).To(ContainSubstring("no-such-backend"))
			})

			It("Update with more than one backend fails in v0.1", func() {
				created := createStorageTier()

				_, err := server.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Id: created.GetId(),
						Spec: privatev1.StorageTierSpec_builder{
							Backends: []*privatev1.BackendAssociation{
								privatev1.BackendAssociation_builder{
									BackendId: backendID,
									Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
								}.Build(),
								privatev1.BackendAssociation_builder{
									BackendId: backendID,
									Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.backends"}},
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("one backend"))
			})

			It("Update clearing backends fails", func() {
				created := createStorageTier()

				_, err := server.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Id: created.GetId(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.backends"}},
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("backends"))
			})

			It("Update without mask and without backends fails", func() {
				created := createStorageTier()

				_, err := server.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Id: created.GetId(),
						Spec: privatev1.StorageTierSpec_builder{
							Description: "no backends",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("backends"))
			})
		})

		Describe("Immutability", func() {
			It("Update changing metadata.name fails", func() {
				created := createStorageTier()

				_, err := server.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Id: created.GetId(),
						Metadata: privatev1.Metadata_builder{
							Name: "new-name",
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.name"}},
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("immutable"))
			})

			It("Update with metadata.tenant in update_mask fails", func() {
				created := createStorageTier()

				_, err := server.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Id: created.GetId(),
						Metadata: privatev1.Metadata_builder{
							Tenant: "other-tenant",
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.tenant"}},
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("immutable"))
			})
		})

		Describe("Name uniqueness", func() {
			It("Create with duplicate active name fails", func() {
				createStorageTierWithName("unique-name")

				_, err := server.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "unique-name",
						}.Build(),
						Spec: privatev1.StorageTierSpec_builder{
							Backends: []*privatev1.BackendAssociation{
								privatev1.BackendAssociation_builder{
									BackendId: backendID,
									Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.AlreadyExists))
			})

			It("Create after delete of same name succeeds", func() {
				created := createStorageTierWithName("reusable-name")

				_, err := server.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{
					Id: created.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				second := createStorageTierWithName("reusable-name")
				Expect(second.GetId()).ToNot(Equal(created.GetId()))
				Expect(second.GetMetadata().GetName()).To(Equal("reusable-name"))
			})
		})

		Describe("Optimistic locking", func() {
			It("Update with stale version and lock=true fails", func() {
				created := createStorageTier()

				_, err := server.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Id: created.GetId(),
						Spec: privatev1.StorageTierSpec_builder{
							Description: "first update",
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.description"}},
					Lock:       true,
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Id: created.GetId(),
						Spec: privatev1.StorageTierSpec_builder{
							Description: "second update",
						}.Build(),
						Metadata: privatev1.Metadata_builder{
							Version: created.GetMetadata().GetVersion(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.description"}},
					Lock:       true,
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.Aborted))
			})
		})

		It("Update without id fails", func() {
			_, err := server.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Spec: privatev1.StorageTierSpec_builder{
						Description: "updated",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("identifier"))
		})
	})
})
