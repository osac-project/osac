/*
Copyright (c) 2025 Red Hat Inc.

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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Network classes server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewNetworkClassesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewNetworkClassesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewNetworkClassesServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewNetworkClassesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			publicServer  *NetworkClassesServer
			privateServer *PrivateNetworkClassesServer
		)

		BeforeEach(func() {
			var err error

			// Create the public server:
			publicServer, err = NewNetworkClassesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a private server for test data setup (private API requires
			// implementation_strategy which is not exposed in public API):
			privateServer, err = NewPrivateNetworkClassesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		// createNetworkClass creates a NetworkClass via the private server (which accepts
		// implementation_strategy) and returns the created object.
		createNetworkClass := func() *privatev1.NetworkClass {
			response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Title:                  "Test Network Class",
					ImplementationStrategy: "ovn-kubernetes",
					FabricManager:          "netris",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		// createDefaultNetworkClass creates a NetworkClass with is_default=true via the private server.
		createDefaultNetworkClass := func() *privatev1.NetworkClass {
			response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Title:                  "Default Network Class",
					ImplementationStrategy: "ovn-kubernetes",
					FabricManager:          "netris",
					IsDefault:              new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		It("List objects", func() {
			// Create a few objects via the private server:
			const count = 10
			for range count {
				createNetworkClass()
			}

			// List the objects via public server:
			response, err := publicServer.List(ctx, publicv1.NetworkClassesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects via the private server:
			const count = 10
			for range count {
				createNetworkClass()
			}

			// List the objects via public server:
			response, err := publicServer.List(ctx, publicv1.NetworkClassesListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			// Create a few objects via the private server:
			const count = 10
			for range count {
				createNetworkClass()
			}

			// List the objects via public server:
			response, err := publicServer.List(ctx, publicv1.NetworkClassesListRequest_builder{
				Offset: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			// Create a few objects via the private server:
			const count = 10
			var ids []string
			for range count {
				obj := createNetworkClass()
				ids = append(ids, obj.GetId())
			}

			// List the objects via public server:
			for _, id := range ids {
				response, err := publicServer.List(ctx, publicv1.NetworkClassesListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", id)),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(id))
			}
		})

		It("Get object", func() {
			// Create the object via the private server:
			privateObj := createNetworkClass()

			// Get it via public server:
			getResponse, err := publicServer.Get(ctx, publicv1.NetworkClassesGetRequest_builder{
				Id: privateObj.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			publicObj := getResponse.GetObject()
			Expect(publicObj.GetId()).To(Equal(privateObj.GetId()))
			Expect(publicObj.GetTitle()).To(Equal(privateObj.GetTitle()))
		})

		It("Update object", func() {
			// Create the object via the private server:
			privateObj := createNetworkClass()

			// Update the object via public server:
			updateResponse, err := publicServer.Update(ctx, publicv1.NetworkClassesUpdateRequest_builder{
				Object: publicv1.NetworkClass_builder{
					Id:          privateObj.GetId(),
					Title:       "Your title",
					Description: "Your description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Your title"))
			Expect(updateResponse.GetObject().GetDescription()).To(Equal("Your description."))

			// Get and verify via public server:
			getResponse, err := publicServer.Get(ctx, publicv1.NetworkClassesGetRequest_builder{
				Id: privateObj.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Your title"))
			Expect(getResponse.GetObject().GetDescription()).To(Equal("Your description."))
		})

		It("Delete object", func() {
			// Create the object via the private server:
			privateObj := createNetworkClass()

			// Add a finalizer, as otherwise the object will be immediately deleted and archived and it
			// won't be possible to verify the deletion timestamp. This can't be done using the server
			// because this is a public object, and public objects don't have the finalizers field.
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(
				ctx,
				`update network_classes set finalizers = '{"a"}' where id = $1`,
				privateObj.GetId(),
			)
			Expect(err).ToNot(HaveOccurred())

			// Delete the object via public server:
			_, err = publicServer.Delete(ctx, publicv1.NetworkClassesDeleteRequest_builder{
				Id: privateObj.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify via public server:
			getResponse, err := publicServer.Get(ctx, publicv1.NetworkClassesGetRequest_builder{
				Id: privateObj.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := getResponse.GetObject()
			Expect(object.GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Generates UUID for id ignoring caller-provided value", func() {
			callerProvidedId := "my-custom-id"
			response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Id:                     callerProvidedId,
					Title:                  "Test Network Class",
					ImplementationStrategy: "ovn-kubernetes",
					FabricManager:          "netris",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).ToNot(Equal(callerProvidedId))
			_, err = uuid.Parse(response.GetObject().GetId())
			Expect(err).ToNot(HaveOccurred())
		})

		Describe("Default NetworkClass", func() {
			It("Create NC with is_default=true is visible via public Get", func() {
				// Create via private server with is_default=true:
				ncA := createDefaultNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeTrue())

				// Get via public server and verify is_default is visible:
				getResponse, err := publicServer.Get(ctx, publicv1.NetworkClassesGetRequest_builder{
					Id: ncA.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetIsDefault()).To(BeTrue())
			})

			It("Auto-swap on second default: first NC loses its default flag", func() {
				// Create NC-A as default:
				ncA := createDefaultNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeTrue())

				// Create NC-B as default: NC-A should lose its default flag:
				ncB := createDefaultNetworkClass()
				Expect(ncB.GetIsDefault()).To(BeTrue())

				// Verify NC-A is no longer the default:
				getResponseA, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: ncA.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponseA.GetObject().GetIsDefault()).To(BeFalse())

				// Verify NC-B is still the default:
				getResponseB, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: ncB.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponseB.GetObject().GetIsDefault()).To(BeTrue())
			})

			It("Update to set is_default triggers swap", func() {
				// Create NC-A as default:
				ncA := createDefaultNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeTrue())

				// Create NC-B not as default:
				ncB := createNetworkClass()
				Expect(ncB.GetIsDefault()).To(BeFalse())

				// Update NC-B with field mask setting is_default=true:
				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:        ncB.GetId(),
						IsDefault: new(true),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"is_default"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetIsDefault()).To(BeTrue())

				// Verify NC-A lost its default:
				getResponseA, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: ncA.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponseA.GetObject().GetIsDefault()).To(BeFalse())
			})

			It("Update NC-B with is_default=false does not clear NC-A default", func() {
				// Create NC-A as default:
				ncA := createDefaultNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeTrue())

				// Create NC-B (non-default):
				ncB := createNetworkClass()
				Expect(ncB.GetIsDefault()).To(BeFalse())

				// Explicitly set NC-B's is_default=false via masked Update.
				// The swap guard (HasIsDefault=true, GetIsDefault=false) should NOT fire.
				_, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:        ncB.GetId(),
						IsDefault: new(false),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"is_default"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// NC-A should still be the default (swap was not triggered):
				getResponse, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: ncA.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetIsDefault()).To(BeTrue())
			})

			It("Update to unset is_default: no defaults remain", func() {
				// Create NC-A as default:
				ncA := createDefaultNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeTrue())

				// Update NC-A setting is_default=false:
				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:        ncA.GetId(),
						IsDefault: new(false),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"is_default"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetIsDefault()).To(BeFalse())

				// Verify no defaults remain by listing:
				listResponse, err := privateServer.List(ctx, privatev1.NetworkClassesListRequest_builder{
					Filter: new("this.is_default == true"),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(listResponse.GetItems()).To(BeEmpty())
			})

			It("Setting same NC as default again is idempotent", func() {
				// Create NC-A as default:
				ncA := createDefaultNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeTrue())

				// Update NC-A with is_default=true again (idempotent):
				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:        ncA.GetId(),
						IsDefault: new(true),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"is_default"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetIsDefault()).To(BeTrue())

				// Verify via Get that it's still true:
				getResponse, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: ncA.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetIsDefault()).To(BeTrue())
			})

			It("Public Update preserves is_default when changing other fields", func() {
				// Create NC-A as default via private server:
				ncA := createDefaultNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeTrue())

				// Do a public Update changing only the title (not touching is_default):
				_, err := publicServer.Update(ctx, publicv1.NetworkClassesUpdateRequest_builder{
					Object: publicv1.NetworkClass_builder{
						Id:    ncA.GetId(),
						Title: "Updated Title",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Verify is_default is still true via public Get:
				getResponse, err := publicServer.Get(ctx, publicv1.NetworkClassesGetRequest_builder{
					Id: ncA.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetIsDefault()).To(BeTrue())
				Expect(getResponse.GetObject().GetTitle()).To(Equal("Updated Title"))
			})

			It("Public API cannot clear is_default via Update", func() {
				// Create NC-A as default via private server:
				ncA := createDefaultNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeTrue())

				// Attempt public Update with is_default=false (AddIgnoredFields should prevent it):
				_, err := publicServer.Update(ctx, publicv1.NetworkClassesUpdateRequest_builder{
					Object: publicv1.NetworkClass_builder{
						Id:        ncA.GetId(),
						Title:     ncA.GetTitle(),
						IsDefault: new(false),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Verify is_default is still true (the public inMapper ignores is_default):
				getResponse, err := publicServer.Get(ctx, publicv1.NetworkClassesGetRequest_builder{
					Id: ncA.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetIsDefault()).To(BeTrue())
			})

			It("Multiple defaults fallback: newest by creation_timestamp wins", func() {
				// Drop the unique index to simulate a race condition where creation of two default
				// network classes succeed.
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx, "drop index if exists network_classes_single_default")
				Expect(err).ToNot(HaveOccurred())

				// Create two default network classes:
				ncDao, ncErr := dao.NewGenericDAO[*privatev1.NetworkClass]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(ncErr).ToNot(HaveOccurred())

				createResponseA, ncErr := ncDao.Create().
					SetObject(privatev1.NetworkClass_builder{
						Title:                  "NC-A",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						IsDefault:              new(true),
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Status: privatev1.NetworkClassStatus_builder{
							State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
						}.Build(),
					}.Build()).
					Do(ctx)
				Expect(ncErr).ToNot(HaveOccurred())
				ncAId := createResponseA.GetObject().GetId()

				createResponseB, ncErr := ncDao.Create().SetObject(
					privatev1.NetworkClass_builder{
						Title:                  "NC-B",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						IsDefault:              new(true),
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Status: privatev1.NetworkClassStatus_builder{
							State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
						}.Build(),
					}.Build()).
					Do(ctx)
				Expect(ncErr).ToNot(HaveOccurred())
				ncBId := createResponseB.GetObject().GetId()

				// Verify: both NCs have is_default=true (invariant violation):
				listResponse, err := privateServer.List(ctx, privatev1.NetworkClassesListRequest_builder{
					Filter: new("this.is_default == true"),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(listResponse.GetItems()).To(HaveLen(2))

				// The default-swap on a new Create should clear all existing defaults:
				ncC := createDefaultNetworkClass()
				Expect(ncC.GetIsDefault()).To(BeTrue())

				// NC-B should have been unset by the swap:
				getResponseB, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: ncBId,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponseB.GetObject().GetIsDefault()).To(BeFalse())

				// NC-A should also have been unset (clearExistingDefaults clears all):
				getResponseA, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: ncAId,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponseA.GetObject().GetIsDefault()).To(BeFalse())

				// NC-C is the new default:
				getResponseC, err := publicServer.Get(ctx, publicv1.NetworkClassesGetRequest_builder{
					Id: ncC.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponseC.GetObject().GetIsDefault()).To(BeTrue())
			})

			It("Update without UpdateMask and IsDefault=true applies via proto.Merge", func() {
				// Create NC-A not as default:
				ncA := createNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeFalse())

				// Update without a field mask, setting is_default=true (proto.Merge path):
				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:        ncA.GetId(),
						Title:     ncA.GetTitle(),
						IsDefault: new(true),
					}.Build(),
					// No UpdateMask — triggers proto.Merge branch in applyNetworkClassUpdate
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetIsDefault()).To(BeTrue())

				// Verify persisted via Get:
				getResponse, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: ncA.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetIsDefault()).To(BeTrue())
			})

			It("Update without UpdateMask validates the fully-replaced capabilities, not a stale merge", func() {
				// Create a NC where dual_stack is validly supported (both ipv4 and ipv6 true):
				response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC with capabilities",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						Capabilities: privatev1.NetworkClassCapabilities_builder{
							SupportsIpv4:      true,
							SupportsIpv6:      true,
							SupportsDualStack: true,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				nc := response.GetObject()

				// Update without a field mask (full replacement), dropping supports_ipv4/ipv6 but
				// keeping supports_dual_stack=true. The persisted object (via generic.Update, which
				// replaces wholesale) would violate NC-VAL-03 (dual_stack requires ipv4 and ipv6).
				// applyNetworkClassUpdate's nil-mask path builds a "merged" preview solely for this
				// validation step; if it used proto.Merge's recursive semantics on capabilities
				// unchanged, the preview would incorrectly retain the old ipv4/ipv6=true flags and
				// let this invalid update pass validation.
				_, err = privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:                     nc.GetId(),
						Title:                  nc.GetTitle(),
						ImplementationStrategy: nc.GetImplementationStrategy(),
						FabricManager:          nc.GetFabricManager(),
						Capabilities: privatev1.NetworkClassCapabilities_builder{
							SupportsDualStack: true,
						}.Build(),
					}.Build(),
					// No UpdateMask — triggers the proto.Merge branch in applyNetworkClassUpdate.
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("supports_ipv4"))
			})

			It("Update without UpdateMask and IsDefault absent clears is_default via full replacement", func() {
				// Create NC-A as default:
				ncA := createDefaultNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeTrue())

				// Update without a field mask, with is_default absent in the update object.
				// generic.Update replaces the entire object, so absent fields are cleared.
				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:    ncA.GetId(),
						Title: ncA.GetTitle(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetIsDefault()).To(BeFalse())
			})

			It("No-mask Update omitting is_default does not clear other defaults", func() {
				// Create NC-A as default:
				ncA := createDefaultNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeTrue())

				// Create NC-B (non-default):
				ncB := createNetworkClass()

				// Update NC-B with no mask and is_default absent.
				// The swap guard uses HasIsDefault() on the request object,
				// so it should NOT fire (is_default was not explicitly set).
				_, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:    ncB.GetId(),
						Title: "updated title",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// NC-A should still be the default (clearExistingDefaults was not triggered):
				getResponse, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: ncA.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetIsDefault()).To(BeTrue())
			})

			It("clearExistingDefaults skips soft-deleted NCs", func() {
				// Create NC-A as default via DAO:
				ncDao, ncErr := dao.NewGenericDAO[*privatev1.NetworkClass]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(ncErr).ToNot(HaveOccurred())

				ncA := privatev1.NetworkClass_builder{
					Title:                  "NC-A",
					ImplementationStrategy: "ovn-kubernetes",
					FabricManager:          "netris",
					IsDefault:              new(true),
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Status: privatev1.NetworkClassStatus_builder{
						State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
					}.Build(),
				}.Build()
				createResponseA, ncErr := ncDao.Create().SetObject(ncA).Do(ctx)
				Expect(ncErr).ToNot(HaveOccurred())
				ncAId := createResponseA.GetObject().GetId()

				// Soft-delete NC-A by setting deletion_timestamp via SQL:
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, ncErr = tx.Exec(ctx,
					"UPDATE network_classes SET deletion_timestamp = now() WHERE id = $1",
					ncAId,
				)
				Expect(ncErr).ToNot(HaveOccurred())

				// Create NC-B as the new default (triggers clearExistingDefaults):
				ncB := createDefaultNetworkClass()
				Expect(ncB.GetIsDefault()).To(BeTrue())

				// Verify NC-A still exists (was not archived by clearExistingDefaults):
				getResponse, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: ncAId,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				// NC-A is soft-deleted but not archived — it still has is_default=true
				// because clearExistingDefaults skipped it.
				Expect(getResponse.GetObject().GetIsDefault()).To(BeTrue())
			})

			It("Unique partial index prevents second default NC via DAO", func() {
				// Create first default NC via DAO (bypassing server swap logic):
				ncDao, ncErr := dao.NewGenericDAO[*privatev1.NetworkClass]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(ncErr).ToNot(HaveOccurred())

				ncA := privatev1.NetworkClass_builder{
					Title:                  "NC-A",
					ImplementationStrategy: "ovn-kubernetes",
					FabricManager:          "netris",
					IsDefault:              new(true),
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Status: privatev1.NetworkClassStatus_builder{
						State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
					}.Build(),
				}.Build()
				_, ncErr = ncDao.Create().SetObject(ncA).Do(ctx)
				Expect(ncErr).ToNot(HaveOccurred())

				// Second default NC via DAO should fail with unique violation:
				ncB := privatev1.NetworkClass_builder{
					Title:                  "NC-B",
					ImplementationStrategy: "ovn-kubernetes",
					FabricManager:          "netris",
					IsDefault:              new(true),
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Status: privatev1.NetworkClassStatus_builder{
						State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
					}.Build(),
				}.Build()
				_, ncErr = ncDao.Create().SetObject(ncB).Do(ctx)
				Expect(ncErr).To(HaveOccurred())
				Expect(ncErr.Error()).To(ContainSubstring("already exists"))
			})

			It("findDefaultNetworkClass excludes soft-deleted records", func() {
				// Create a DAO for direct data setup:
				ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				// Create a network default network class, and then delete it. It has finalizer to
				// to ensure that it stays in the table, so that we can verify that the partial index
				/// works correctly.
				ncDeletedResponse, err := ncDao.Create().
					SetObject(privatev1.NetworkClass_builder{
						Title:                  "Deleted Default",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						IsDefault:              new(true),
						Metadata: privatev1.Metadata_builder{
							Finalizers: []string{"a"},
							Tenant:     auth.SharedTenant,
						}.Build(),
						Status: privatev1.NetworkClassStatus_builder{
							State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
						}.Build(),
					}.Build()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				ncDeleted := ncDeletedResponse.GetObject()
				ncDeletedID := ncDeleted.GetId()
				_, err = ncDao.Delete().SetId(ncDeletedID).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create another default network class:
				ncActiveResponse, err := ncDao.Create().
					SetObject(privatev1.NetworkClass_builder{
						Title:                  "Active Default",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						IsDefault:              new(true),
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Status: privatev1.NetworkClassStatus_builder{
							State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
						}.Build(),
					}.Build()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				ncActive := ncActiveResponse.GetObject()
				ncActiveID := ncActive.GetId()

				// Call findDefaultNetworkClass directly — should return only the active one:
				result, err := findDefaultNetworkClass(ctx, logger, ncDao)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).ToNot(BeNil())
				Expect(result.GetId()).To(Equal(ncActiveID))
			})

			It("Delete the default NC: no defaults remain in List", func() {
				// Create NC-A as default:
				ncA := createDefaultNetworkClass()
				Expect(ncA.GetIsDefault()).To(BeTrue())

				// Delete NC-A immediately (no finalizers so it is hard-deleted):
				_, err := publicServer.Delete(ctx, publicv1.NetworkClassesDeleteRequest_builder{
					Id: ncA.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Verify no defaults remain in List:
				listResponse, err := privateServer.List(ctx, privatev1.NetworkClassesListRequest_builder{
					Filter: new("this.is_default == true"),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(listResponse.GetItems()).To(BeEmpty())
			})
		})

		Describe("Manager fields", func() {
			It("Create with fabric_manager persists the value", func() {
				response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC with fabric manager",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetFabricManager()).To(Equal("netris"))

				getResponse, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: response.GetObject().GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetFabricManager()).To(Equal("netris"))
			})

			It("Create without fabric_manager fails", func() {
				_, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC without fabric manager",
						ImplementationStrategy: "ovn-kubernetes",
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("fabric_manager"))
			})

			It("Create with k8s_manager persists the value", func() {
				response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC with k8s manager",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						K8SManager:             new("cudn_localnet"),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetK8SManager()).To(Equal("cudn_localnet"))

				getResponse, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: response.GetObject().GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetK8SManager()).To(Equal("cudn_localnet"))
			})

			It("Create without k8s_manager succeeds", func() {
				response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC without k8s manager",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().HasK8SManager()).To(BeFalse())
			})

			It("Update changing fabric_manager fails with immutability error", func() {
				nc := createNetworkClass()
				Expect(nc.GetFabricManager()).To(Equal("netris"))

				_, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:            nc.GetId(),
						FabricManager: "neutron",
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"fabric_manager"}},
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("fabric_manager"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("Update setting k8s_manager for the first time succeeds", func() {
				// Create NC without k8s_manager (BM-only region):
				nc := createNetworkClass()
				Expect(nc.HasK8SManager()).To(BeFalse())

				// Set k8s_manager for the first time (adding VM support):
				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:         nc.GetId(),
						K8SManager: new("cudn_localnet"),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"k8s_manager"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetK8SManager()).To(Equal("cudn_localnet"))
			})

			It("Update changing k8s_manager fails with immutability error", func() {
				response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC for k8s update",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						K8SManager:             new("cudn_localnet"),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				nc := response.GetObject()

				_, err = privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:         nc.GetId(),
						K8SManager: new("ovn_evpn"),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"k8s_manager"}},
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("k8s_manager"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("Update with field mask preserves unmasked manager fields", func() {
				response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC for mask test",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						K8SManager:             new("cudn_localnet"),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				nc := response.GetObject()

				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:    nc.GetId(),
						Title: "Updated title",
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetFabricManager()).To(Equal("netris"))
				Expect(updateResponse.GetObject().GetK8SManager()).To(Equal("cudn_localnet"))
			})

			It("Full replacement update with same fabric_manager succeeds", func() {
				nc := createNetworkClass()
				Expect(nc.GetFabricManager()).To(Equal("netris"))

				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:            nc.GetId(),
						Title:         "Updated",
						FabricManager: "netris",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetFabricManager()).To(Equal("netris"))
			})

			It("Full replacement update changing fabric_manager fails", func() {
				nc := createNetworkClass()

				_, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:            nc.GetId(),
						Title:         nc.GetTitle(),
						FabricManager: "neutron",
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("fabric_manager"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})
		})

		Describe("Defaults", func() {
			validDefaults := func() *privatev1.NetworkDefaults {
				return privatev1.NetworkDefaults_builder{
					VirtualNetworkIpv4Cidr: "10.0.0.0/16",
					SubnetIpv4Cidr:         "10.0.1.0/24",
					IngressRules: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_TCP,
							PortFrom: new(int32(22)),
							PortTo:   new(int32(22)),
							Ipv4Cidr: new("0.0.0.0/0"),
						}.Build(),
					},
					EgressRules: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_ALL,
							Ipv4Cidr: new("0.0.0.0/0"),
						}.Build(),
					},
				}.Build()
			}

			createNetworkClassWithDefaults := func(defaults *privatev1.NetworkDefaults) *privatev1.NetworkClass {
				response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC with defaults",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						Spec:                   privatev1.NetworkClassSpec_builder{Defaults: defaults}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject()
			}

			It("Create with valid defaults persists and returns them", func() {
				nc := createNetworkClassWithDefaults(validDefaults())

				Expect(nc.GetSpec().GetDefaults()).ToNot(BeNil())
				Expect(nc.GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()).To(Equal("10.0.0.0/16"))
				Expect(nc.GetSpec().GetDefaults().GetSubnetIpv4Cidr()).To(Equal("10.0.1.0/24"))
				Expect(nc.GetSpec().GetDefaults().GetIngressRules()).To(HaveLen(1))
				Expect(nc.GetSpec().GetDefaults().GetEgressRules()).To(HaveLen(1))
			})

			It("Get after create returns defaults", func() {
				nc := createNetworkClassWithDefaults(validDefaults())

				getResponse, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: nc.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				retrieved := getResponse.GetObject()
				Expect(retrieved.GetSpec().GetDefaults()).ToNot(BeNil())
				Expect(retrieved.GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()).To(Equal("10.0.0.0/16"))
				Expect(retrieved.GetSpec().GetDefaults().GetSubnetIpv4Cidr()).To(Equal("10.0.1.0/24"))
				Expect(retrieved.GetSpec().GetDefaults().GetIngressRules()).To(HaveLen(1))
				Expect(retrieved.GetSpec().GetDefaults().GetIngressRules()[0].GetProtocol()).To(Equal(privatev1.Protocol_PROTOCOL_TCP))
				Expect(retrieved.GetSpec().GetDefaults().GetIngressRules()[0].GetPortFrom()).To(BeNumerically("==", 22))
			})

			It("List after create returns defaults in items", func() {
				createNetworkClassWithDefaults(validDefaults())

				listResponse, err := privateServer.List(ctx, privatev1.NetworkClassesListRequest_builder{}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(listResponse.GetItems()).To(HaveLen(1))
				Expect(listResponse.GetItems()[0].GetSpec().GetDefaults()).ToNot(BeNil())
				Expect(listResponse.GetItems()[0].GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()).To(Equal("10.0.0.0/16"))
			})

			It("Update defaults via field mask replaces entire defaults", func() {
				nc := createNetworkClassWithDefaults(validDefaults())

				newDefaults := privatev1.NetworkDefaults_builder{
					VirtualNetworkIpv4Cidr: "172.16.0.0/12",
					SubnetIpv4Cidr:         "172.16.1.0/24",
				}.Build()

				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:   nc.GetId(),
						Spec: privatev1.NetworkClassSpec_builder{Defaults: newDefaults}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.defaults"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()).To(Equal("172.16.0.0/12"))
				Expect(updated.GetSpec().GetDefaults().GetSubnetIpv4Cidr()).To(Equal("172.16.1.0/24"))
				Expect(updated.GetSpec().GetDefaults().GetIngressRules()).To(BeEmpty())
				Expect(updated.GetSpec().GetDefaults().GetEgressRules()).To(BeEmpty())
			})

			It("Update defaults to nil clears them", func() {
				nc := createNetworkClassWithDefaults(validDefaults())

				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id: nc.GetId(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.defaults"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetSpec().GetDefaults()).To(BeNil())
			})

			It("Create without defaults succeeds", func() {
				nc := createNetworkClass()
				Expect(nc.GetSpec().GetDefaults()).To(BeNil())
			})

			It("Defaults with CIDRs only succeeds", func() {
				defaults := privatev1.NetworkDefaults_builder{
					VirtualNetworkIpv4Cidr: "10.0.0.0/16",
					SubnetIpv4Cidr:         "10.0.1.0/24",
				}.Build()
				nc := createNetworkClassWithDefaults(defaults)
				Expect(nc.GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()).To(Equal("10.0.0.0/16"))
				Expect(nc.GetSpec().GetDefaults().GetIngressRules()).To(BeEmpty())
			})

			It("Defaults with rules only succeeds", func() {
				defaults := privatev1.NetworkDefaults_builder{
					IngressRules: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_TCP,
							PortFrom: new(int32(443)),
							PortTo:   new(int32(443)),
							Ipv4Cidr: new("0.0.0.0/0"),
						}.Build(),
					},
				}.Build()
				nc := createNetworkClassWithDefaults(defaults)
				Expect(nc.GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()).To(BeEmpty())
				Expect(nc.GetSpec().GetDefaults().GetIngressRules()).To(HaveLen(1))
			})

			It("Invalid virtual_network_ipv4_cidr fails validation", func() {
				defaults := privatev1.NetworkDefaults_builder{
					VirtualNetworkIpv4Cidr: "not-a-cidr",
				}.Build()
				_, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC invalid VN CIDR",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						Spec:                   privatev1.NetworkClassSpec_builder{Defaults: defaults}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("virtual_network_ipv4_cidr"))
			})

			It("Invalid subnet_ipv4_cidr fails validation", func() {
				defaults := privatev1.NetworkDefaults_builder{
					VirtualNetworkIpv4Cidr: "10.0.0.0/16",
					SubnetIpv4Cidr:         "invalid",
				}.Build()
				_, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC invalid subnet CIDR",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						Spec:                   privatev1.NetworkClassSpec_builder{Defaults: defaults}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("subnet_ipv4_cidr"))
			})

			It("Subnet CIDR not within virtual_network_ipv4_cidr fails", func() {
				defaults := privatev1.NetworkDefaults_builder{
					VirtualNetworkIpv4Cidr: "10.0.0.0/16",
					SubnetIpv4Cidr:         "192.168.1.0/24",
				}.Build()
				_, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC subnet outside VN",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						Spec:                   privatev1.NetworkClassSpec_builder{Defaults: defaults}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not within"))
			})

			It("Subnet CIDR without virtual_network_ipv4_cidr fails", func() {
				defaults := privatev1.NetworkDefaults_builder{
					SubnetIpv4Cidr: "10.0.1.0/24",
				}.Build()
				_, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC subnet without VN",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						Spec:                   privatev1.NetworkClassSpec_builder{Defaults: defaults}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("subnet_ipv4_cidr requires"))
				Expect(err.Error()).To(ContainSubstring("virtual_network_ipv4_cidr"))
			})

			It("Ingress rule with invalid protocol fails", func() {
				defaults := privatev1.NetworkDefaults_builder{
					IngressRules: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_UNSPECIFIED,
							Ipv4Cidr: new("0.0.0.0/0"),
						}.Build(),
					},
				}.Build()
				_, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC invalid rule protocol",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						Spec:                   privatev1.NetworkClassSpec_builder{Defaults: defaults}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("protocol is required"))
			})

			It("TCP rule without port range fails", func() {
				defaults := privatev1.NetworkDefaults_builder{
					IngressRules: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_TCP,
							PortFrom: new(int32(22)),
							Ipv4Cidr: new("0.0.0.0/0"),
						}.Build(),
					},
				}.Build()
				_, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC TCP missing port_to",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						Spec:                   privatev1.NetworkClassSpec_builder{Defaults: defaults}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("port"))
			})

			It("Rule with invalid CIDR fails", func() {
				defaults := privatev1.NetworkDefaults_builder{
					EgressRules: []*privatev1.SecurityRule{
						privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_ALL,
							Ipv4Cidr: new("not-a-cidr"),
						}.Build(),
					},
				}.Build()
				_, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC invalid rule CIDR",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						Spec:                   privatev1.NetworkClassSpec_builder{Defaults: defaults}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("CIDR"))
			})

			It("Update with invalid defaults via field mask fails validation", func() {
				nc := createNetworkClassWithDefaults(validDefaults())

				invalidDefaults := privatev1.NetworkDefaults_builder{
					VirtualNetworkIpv4Cidr: "not-a-cidr",
				}.Build()
				_, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:   nc.GetId(),
						Spec: privatev1.NetworkClassSpec_builder{Defaults: invalidDefaults}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.defaults"}},
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("virtual_network_ipv4_cidr"))
			})

			It("Update with an unknown field mask path fails", func() {
				nc := createNetworkClass()

				_, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:    nc.GetId(),
						Title: "New Title",
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"not_a_real_field"}},
				}.Build())
				Expect(err).To(HaveOccurred())
			})

			It("Public API returns defaults as OUTPUT_ONLY", func() {
				nc := createNetworkClassWithDefaults(validDefaults())

				getResponse, err := publicServer.Get(ctx, publicv1.NetworkClassesGetRequest_builder{
					Id: nc.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				publicNC := getResponse.GetObject()
				Expect(publicNC.GetSpec().GetDefaults()).ToNot(BeNil())
				Expect(publicNC.GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()).To(Equal("10.0.0.0/16"))
				Expect(publicNC.GetSpec().GetDefaults().GetSubnetIpv4Cidr()).To(Equal("10.0.1.0/24"))
				Expect(publicNC.GetSpec().GetDefaults().GetIngressRules()).To(HaveLen(1))
			})

			It("Public API cannot set defaults via Update", func() {
				// Create NC via private API (no defaults):
				nc := createNetworkClass()
				Expect(nc.GetSpec().GetDefaults()).To(BeNil())

				// Attempt to set defaults via public Update — inMapper should ignore the field:
				publicDefaults := publicv1.NetworkDefaults_builder{
					VirtualNetworkIpv4Cidr: "10.0.0.0/16",
					SubnetIpv4Cidr:         "10.0.1.0/24",
				}.Build()
				_, err := publicServer.Update(ctx, publicv1.NetworkClassesUpdateRequest_builder{
					Object: publicv1.NetworkClass_builder{
						Id:    nc.GetId(),
						Title: nc.GetTitle(),
						Spec:  publicv1.NetworkClassSpec_builder{Defaults: publicDefaults}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Get via private API to confirm defaults were not persisted:
				getResponse, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: nc.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetSpec().GetDefaults()).To(BeNil())
			})

			It("Public Update preserves defaults set via private API", func() {
				nc := createNetworkClassWithDefaults(validDefaults())

				_, err := publicServer.Update(ctx, publicv1.NetworkClassesUpdateRequest_builder{
					Object: publicv1.NetworkClass_builder{
						Id:    nc.GetId(),
						Title: "Updated title",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				getResponse, err := publicServer.Get(ctx, publicv1.NetworkClassesGetRequest_builder{
					Id: nc.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetSpec().GetDefaults()).ToNot(BeNil())
				Expect(getResponse.GetObject().GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()).To(Equal("10.0.0.0/16"))
				Expect(getResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			})
		})

		Describe("DisableCapabilities", func() {
			createWithDisableCapabilities := func(caps *privatev1.NetworkClassCapabilities) *privatev1.NetworkClass {
				response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC with disable_capabilities",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						Spec: privatev1.NetworkClassSpec_builder{
							DisableCapabilities: caps,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject()
			}

			It("Create with valid disable_capabilities persists and returns them", func() {
				nc := createWithDisableCapabilities(privatev1.NetworkClassCapabilities_builder{
					SupportsIpv6: true,
					DpuSupport:   true,
				}.Build())

				Expect(nc.GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeTrue())
				Expect(nc.GetSpec().GetDisableCapabilities().GetDpuSupport()).To(BeTrue())
				Expect(nc.GetSpec().GetDisableCapabilities().GetSupportsIpv4()).To(BeFalse())
				Expect(nc.GetSpec().GetDisableCapabilities().GetSupportsDualStack()).To(BeFalse())

				getResponse, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: nc.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeTrue())
				Expect(getResponse.GetObject().GetSpec().GetDisableCapabilities().GetDpuSupport()).To(BeTrue())
			})

			It("Create with all capabilities disabled succeeds", func() {
				nc := createWithDisableCapabilities(privatev1.NetworkClassCapabilities_builder{
					SupportsIpv4:      true,
					SupportsIpv6:      true,
					SupportsDualStack: true,
					DpuSupport:        true,
				}.Build())
				Expect(nc.GetSpec().GetDisableCapabilities().GetSupportsIpv4()).To(BeTrue())
				Expect(nc.GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeTrue())
				Expect(nc.GetSpec().GetDisableCapabilities().GetSupportsDualStack()).To(BeTrue())
				Expect(nc.GetSpec().GetDisableCapabilities().GetDpuSupport()).To(BeTrue())
			})

			It("Create with empty disable_capabilities succeeds", func() {
				nc := createWithDisableCapabilities(nil)
				Expect(nc.GetSpec().GetDisableCapabilities()).To(BeNil())
			})

			It("Create without spec succeeds with nil disable_capabilities", func() {
				nc := createNetworkClass()
				Expect(nc.GetSpec().GetDisableCapabilities()).To(BeNil())
			})

			It("Update via spec.disable_capabilities field mask replaces only that sub-field", func() {
				nc := createWithDisableCapabilities(privatev1.NetworkClassCapabilities_builder{
					SupportsIpv6: true,
				}.Build())

				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id: nc.GetId(),
						Spec: privatev1.NetworkClassSpec_builder{
							DisableCapabilities: privatev1.NetworkClassCapabilities_builder{
								DpuSupport: true,
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.disable_capabilities"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetDpuSupport()).To(BeTrue())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeFalse())
			})

			It("Update without UpdateMask replaces disable_capabilities instead of merging", func() {
				nc := createWithDisableCapabilities(privatev1.NetworkClassCapabilities_builder{
					SupportsIpv6: true,
				}.Build())

				// Update without a field mask, setting a different disable_capabilities value
				// (proto.Merge path in applyNetworkClassUpdate). proto.Merge recursively merges
				// nested messages instead of replacing them, so if that semantic leaked through,
				// supports_ipv6 would still be true here alongside dpu_support.
				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:                     nc.GetId(),
						Title:                  nc.GetTitle(),
						ImplementationStrategy: nc.GetImplementationStrategy(),
						FabricManager:          nc.GetFabricManager(),
						Spec: privatev1.NetworkClassSpec_builder{
							DisableCapabilities: privatev1.NetworkClassCapabilities_builder{
								DpuSupport: true,
							}.Build(),
						}.Build(),
					}.Build(),
					// No UpdateMask — triggers proto.Merge branch in applyNetworkClassUpdate.
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetDpuSupport()).To(BeTrue())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeFalse())
			})

			It("Update via spec field mask replaces entire spec including disable_capabilities", func() {
				nc := createWithDisableCapabilities(privatev1.NetworkClassCapabilities_builder{
					SupportsIpv6: true,
				}.Build())

				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id: nc.GetId(),
						Spec: privatev1.NetworkClassSpec_builder{
							DisableCapabilities: privatev1.NetworkClassCapabilities_builder{
								DpuSupport:        true,
								SupportsDualStack: true,
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetDpuSupport()).To(BeTrue())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetSupportsDualStack()).To(BeTrue())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeFalse())
				Expect(updateResponse.GetObject().GetSpec().GetDefaults()).To(BeNil())
			})

			It("Update to empty disable_capabilities clears it", func() {
				nc := createWithDisableCapabilities(privatev1.NetworkClassCapabilities_builder{
					SupportsIpv6: true,
					DpuSupport:   true,
				}.Build())

				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id: nc.GetId(),
						Spec: privatev1.NetworkClassSpec_builder{
							DisableCapabilities: privatev1.NetworkClassCapabilities_builder{}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.disable_capabilities"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeFalse())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetDpuSupport()).To(BeFalse())
			})

			It("Update via spec.defaults mask preserves disable_capabilities", func() {
				nc := createWithDisableCapabilities(privatev1.NetworkClassCapabilities_builder{
					SupportsIpv6: true,
				}.Build())

				defaults := privatev1.NetworkDefaults_builder{
					VirtualNetworkIpv4Cidr: "10.0.0.0/16",
					SubnetIpv4Cidr:         "10.0.1.0/24",
				}.Build()
				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id:   nc.GetId(),
						Spec: privatev1.NetworkClassSpec_builder{Defaults: defaults}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.defaults"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeTrue())
				Expect(updateResponse.GetObject().GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()).To(
					Equal("10.0.0.0/16"))
			})

			It("Update via spec.disable_capabilities mask preserves defaults", func() {
				response, err := privateServer.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Title:                  "NC with both",
						ImplementationStrategy: "ovn-kubernetes",
						FabricManager:          "netris",
						Spec: privatev1.NetworkClassSpec_builder{
							Defaults: privatev1.NetworkDefaults_builder{
								VirtualNetworkIpv4Cidr: "10.0.0.0/16",
								SubnetIpv4Cidr:         "10.0.1.0/24",
							}.Build(),
							DisableCapabilities: privatev1.NetworkClassCapabilities_builder{
								SupportsIpv6: true,
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				nc := response.GetObject()

				updateResponse, err := privateServer.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object: privatev1.NetworkClass_builder{
						Id: nc.GetId(),
						Spec: privatev1.NetworkClassSpec_builder{
							DisableCapabilities: privatev1.NetworkClassCapabilities_builder{
								DpuSupport: true,
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.disable_capabilities"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetDpuSupport()).To(BeTrue())
				Expect(updateResponse.GetObject().GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeFalse())
				Expect(updateResponse.GetObject().GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()).To(
					Equal("10.0.0.0/16"))
			})

			It("Public API returns disable_capabilities as OUTPUT_ONLY", func() {
				nc := createWithDisableCapabilities(privatev1.NetworkClassCapabilities_builder{
					SupportsIpv6: true,
					DpuSupport:   true,
				}.Build())

				getResponse, err := publicServer.Get(ctx, publicv1.NetworkClassesGetRequest_builder{
					Id: nc.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				publicNC := getResponse.GetObject()
				Expect(publicNC.GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeTrue())
				Expect(publicNC.GetSpec().GetDisableCapabilities().GetDpuSupport()).To(BeTrue())
			})

			It("Public Update does not overwrite disable_capabilities set via private API", func() {
				nc := createWithDisableCapabilities(privatev1.NetworkClassCapabilities_builder{
					SupportsIpv6: true,
				}.Build())

				_, err := publicServer.Update(ctx, publicv1.NetworkClassesUpdateRequest_builder{
					Object: publicv1.NetworkClass_builder{
						Id:    nc.GetId(),
						Title: nc.GetTitle(),
						Spec: publicv1.NetworkClassSpec_builder{
							DisableCapabilities: publicv1.NetworkClassCapabilities_builder{
								DpuSupport: true,
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				getResponse, err := privateServer.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
					Id: nc.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeTrue())
				Expect(getResponse.GetObject().GetSpec().GetDisableCapabilities().GetDpuSupport()).To(BeFalse())
			})

			It("Public Update preserves disable_capabilities when changing other fields", func() {
				nc := createWithDisableCapabilities(privatev1.NetworkClassCapabilities_builder{
					SupportsIpv6: true,
				}.Build())

				_, err := publicServer.Update(ctx, publicv1.NetworkClassesUpdateRequest_builder{
					Object: publicv1.NetworkClass_builder{
						Id:    nc.GetId(),
						Title: "Updated title",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				getResponse, err := publicServer.Get(ctx, publicv1.NetworkClassesGetRequest_builder{
					Id: nc.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetSpec().GetDisableCapabilities().GetSupportsIpv6()).To(BeTrue())
				Expect(getResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			})
		})
	})
})
