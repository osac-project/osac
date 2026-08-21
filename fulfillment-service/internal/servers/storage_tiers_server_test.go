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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
)

var _ = Describe("Storage tiers server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			backendsDAO, err := dao.NewGenericDAO[*privatev1.StorageBackend]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			server, err := NewStorageTiersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetStorageBackendsDAO(backendsDAO).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewStorageTiersServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewStorageTiersServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewStorageTiersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if storage backends DAO is not set", func() {
			server, err := NewStorageTiersServer().
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
			publicServer    *StorageTiersServer
			privateServer   *PrivateStorageTiersServer
			backendID       string
			secondBackendID string
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
							Password: testBackendPassword,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			backendID = backendResp.GetObject().GetId()

			// Create a second, distinct StorageBackend for the multi-backend defensive test — the
			// storage_tier_backends materialized table has a unique (storage_tier_id, backend_id)
			// constraint, so two associations on the same tier must reference different backends:
			secondBackendResp, err := backendsServer.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
				Object: privatev1.StorageBackend_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-backend-2",
					}.Build(),
					Spec: privatev1.StorageBackendSpec_builder{
						Provider: "vast",
						Endpoint: "https://storage2.example.com:8443",
						Credentials: privatev1.StorageBackendCredentials_builder{
							Username: "admin",
							Password: testBackendPassword,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			secondBackendID = secondBackendResp.GetObject().GetId()

			backendsDAO, err := dao.NewGenericDAO[*privatev1.StorageBackend]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			privateServer, err = NewPrivateStorageTiersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetStorageBackendsDAO(backendsDAO).
				Build()
			Expect(err).ToNot(HaveOccurred())

			publicServer, err = NewStorageTiersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetStorageBackendsDAO(backendsDAO).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		// defaultBackend returns a BackendAssociation with distinct, non-zero values for every field,
		// so tests can assert each one survived the flatten without ambiguity.
		defaultBackend := func() *privatev1.BackendAssociation {
			return privatev1.BackendAssociation_builder{
				BackendId:            backendID,
				MaxReadBandwidthMbs:  1000,
				MaxWriteBandwidthMbs: 500,
				EncryptionEnabled:    true,
			}.Build()
		}

		// createTier creates a StorageTier via the private server (which enforces exactly one backend
		// association) so tests exercise the public server's delegation and flattening.
		createTier := func(name string, backend *privatev1.BackendAssociation) *privatev1.StorageTier {
			response, err := privateServer.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Description: "A test storage tier",
						Protocol:    privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
						Backends:    []*privatev1.BackendAssociation{backend},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		// createMalformedTier bypasses the private server's Create validation to insert a StorageTier
		// with zero backend associations directly via the DAO, simulating already-corrupted data. It
		// returns the created tier's id.
		createMalformedTier := func(name string) string {
			tierDAO, err := dao.NewGenericDAO[*privatev1.StorageTier]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			response, err := tierDAO.Create().SetObject(privatev1.StorageTier_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   name,
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.StorageTierSpec_builder{
					Description: "no backends",
				}.Build(),
				Status: privatev1.StorageTierStatus_builder{
					State: privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject().GetId()
		}

		It("Get flattens the backend association into the public spec", func() {
			created := createTier("test-tier", defaultBackend())

			response, err := publicServer.Get(ctx, publicv1.StorageTiersGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			obj := response.GetObject()
			Expect(obj.GetId()).To(Equal(created.GetId()))
			Expect(obj.GetMetadata().GetName()).To(Equal("test-tier"))
			Expect(obj.GetSpec().GetDescription()).To(Equal("A test storage tier"))
			Expect(obj.GetSpec().GetProtocol()).To(Equal(publicv1.StorageProtocol_STORAGE_PROTOCOL_NFS))
			Expect(obj.GetSpec().GetMaxReadBandwidthMbs()).To(Equal(int32(1000)))
			Expect(obj.GetSpec().GetMaxWriteBandwidthMbs()).To(Equal(int32(500)))
			Expect(obj.GetSpec().GetEncryptionEnabled()).To(BeTrue())
			Expect(obj.GetStatus().GetState()).To(Equal(publicv1.StorageTierState_STORAGE_TIER_STATE_ACTIVE))
		})

		It("Get returns NOT_FOUND for a non-existent id (delegated from the private server)", func() {
			_, err := publicServer.Get(ctx, publicv1.StorageTiersGetRequest_builder{
				Id: "non-existent-id",
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.NotFound))
		})

		It("List flattens the backend association for every item", func() {
			const count = 3
			for i := range count {
				createTier(fmt.Sprintf("tier-%d", i), defaultBackend())
			}

			response, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
			for _, item := range response.GetItems() {
				Expect(item.GetSpec().GetProtocol()).To(Equal(publicv1.StorageProtocol_STORAGE_PROTOCOL_NFS))
			}
		})

		It("List respects limit", func() {
			const count = 5
			for i := range count {
				createTier(fmt.Sprintf("tier-%d", i), defaultBackend())
			}

			response, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{
				Limit: new(int32(2)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 2))
		})

		It("List respects offset", func() {
			const count = 5
			for i := range count {
				createTier(fmt.Sprintf("tier-%d", i), defaultBackend())
			}

			response, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{
				Offset: new(int32(2)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-2))
		})

		It("List forwards a valid filter on a path shared by both schemas", func() {
			created := createTier("gold-tier", defaultBackend())
			createTier("silver-tier", defaultBackend())

			response, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{
				Filter: new(fmt.Sprintf("this.id == '%s'", created.GetId())),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetId()).To(Equal(created.GetId()))
		})

		It("List rejects a filter referencing a field that doesn't exist in the public schema at all", func() {
			// Seed a tier that WOULD be returned if this filter were forwarded to the private
			// delegate, so the test distinguishes "rejected before delegation" from "delegated but
			// empty":
			createTier("test-tier", defaultBackend())

			response, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{
				Filter: new(fmt.Sprintf("this.spec.backends[0].backend_id == '%s'", backendID)),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.InvalidArgument))
			Expect(response).To(BeNil())
		})

		DescribeTable("List rejects filters on fields that exist publicly but at a different path privately",
			func(filter string) {
				// Seed a tier whose values would match these filters if forwarded, so a
				// delegated-but-empty result can't masquerade as rejection:
				createTier("test-tier", defaultBackend())

				response, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{
					Filter: new(filter),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("not yet supported"))
				Expect(response).To(BeNil())
			},
			Entry("max_read_bandwidth_mbs", "this.spec.max_read_bandwidth_mbs == 1000"),
			Entry("max_write_bandwidth_mbs", "this.spec.max_write_bandwidth_mbs == 500"),
			Entry("encryption_enabled", "this.spec.encryption_enabled == true"),
		)

		It("List forwards a filter on this.spec.protocol now that the path is shared with the private schema", func() {
			nfsTier := createTier("nfs-tier", defaultBackend())

			blockResponse, err := privateServer.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Metadata: privatev1.Metadata_builder{Name: "block-tier"}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Description: "A test storage tier",
						Protocol:    privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
						Backends: []*privatev1.BackendAssociation{
							privatev1.BackendAssociation_builder{
								BackendId: secondBackendID,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			blockID := blockResponse.GetObject().GetId()

			response, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{
				Filter: new("this.spec.protocol == 1"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetId()).To(Equal(nfsTier.GetId()))
			Expect(response.GetItems()[0].GetId()).ToNot(Equal(blockID))
		})

		It("List rejects a filter that fails to compile", func() {
			_, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{
				Filter: new("this.id =="),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Get returns Internal, and List omits, a tier with zero backend associations", func() {
			malformedID := createMalformedTier("malformed-tier")

			_, err := publicServer.Get(ctx, publicv1.StorageTiersGetRequest_builder{
				Id: malformedID,
			}.Build())
			Expect(err).To(HaveOccurred())
			getSt, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(getSt.Code()).To(Equal(codes.Internal))

			// A tenant can't fix a malformed tier — that's a cloud-provider-admin data problem — so
			// List logs and omits it rather than failing the whole listing for every caller:
			listResponse, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{
				Filter: new(fmt.Sprintf("this.id == '%s'", malformedID)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetItems()).To(BeEmpty())
			Expect(listResponse.GetSize()).To(Equal(int32(0)))
			// Total must be adjusted down along with the drop, not left at the raw DB count (which
			// would be 1 here, since the malformed tier itself matches the filter) — otherwise a
			// client would see Total=1, Size=0, Items=[], an inconsistent page.
			Expect(listResponse.GetTotal()).To(Equal(int32(0)))
		})

		It("List adjusts Total to exclude tiers dropped for having an unexpected backend count", func() {
			createTier("valid-tier-1", defaultBackend())
			createTier("valid-tier-2", defaultBackend())
			createMalformedTier("malformed-tier-total")

			// The DB-level total across all three tiers is 3, but the malformed one is dropped from
			// the page, so the client-visible Total must be adjusted down to 2 to match Size and
			// Items — not left at the raw DB count.
			listResponse, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetItems()).To(HaveLen(2))
			Expect(listResponse.GetSize()).To(Equal(int32(2)))
			Expect(listResponse.GetTotal()).To(Equal(int32(2)))
		})

		It("List does not adjust Total when the response does not cover the entire result set", func() {
			createTier("valid-tier-1", defaultBackend())
			createMalformedTier("malformed-tier-paginated")

			// Fetch a single-item page starting at offset 1, out of 2 total matching tiers (one
			// valid, one malformed). Regardless of which tier this page happens to land on (order
			// is by id, which is server-generated and unpredictable), Total must remain the plain
			// DB count (2) — not adjusted — because this page provably does not cover the entire
			// result set, so we cannot know how many malformed rows exist outside it.
			listResponse, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{
				Offset: new(int32(1)),
				Limit:  new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetTotal()).To(Equal(int32(2)))
		})

		It("List does not adjust Total for a truncated first page, even at offset 0", func() {
			createTier("valid-tier-1", defaultBackend())
			createTier("valid-tier-2", defaultBackend())
			createMalformedTier("malformed-tier-first-page")

			// Fetch a 2-item page starting at offset 0, out of 3 total matching tiers (two valid,
			// one malformed). Offset is 0, but the page still doesn't cover the entire result set
			// (limit=2 < total=3), so Total must remain the plain DB count (3) regardless of
			// whether the malformed tier happens to land in this page or be excluded by the limit.
			listResponse, err := publicServer.List(ctx, publicv1.StorageTiersListRequest_builder{
				Limit: new(int32(2)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetTotal()).To(Equal(int32(3)))
		})

		It("Get returns Internal for a tier with more than one backend association", func() {
			tierDAO, err := dao.NewGenericDAO[*privatev1.StorageTier]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			createResponse, err := tierDAO.Create().SetObject(privatev1.StorageTier_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "multi-backend-tier",
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.StorageTierSpec_builder{
					Description: "two backends",
					Protocol:    privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
					Backends: []*privatev1.BackendAssociation{
						defaultBackend(),
						privatev1.BackendAssociation_builder{
							BackendId:            secondBackendID,
							MaxReadBandwidthMbs:  2000,
							MaxWriteBandwidthMbs: 1000,
							EncryptionEnabled:    false,
						}.Build(),
					},
				}.Build(),
				Status: privatev1.StorageTierStatus_builder{
					State: privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			multiID := createResponse.GetObject().GetId()

			_, err = publicServer.Get(ctx, publicv1.StorageTiersGetRequest_builder{
				Id: multiID,
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.Internal))
		})

		It("toPublicStorageProtocol maps an out-of-range private value to UNSPECIFIED", func() {
			// Simulates a future private StorageProtocol value the public enum doesn't know about yet
			// — this must hit the defensive `default` branch rather than silently miscasting:
			result := publicServer.toPublicStorageProtocol(ctx, privatev1.StorageProtocol(99))
			Expect(result).To(Equal(publicv1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED))
		})
	})

	Describe("Schema drift regression", func() {
		It("Every StorageTierSpec field except description and protocol is covered by the Layer 2 rejection list", func() {
			descriptor := (&publicv1.StorageTierSpec{}).ProtoReflect().Descriptor()
			fields := descriptor.Fields()

			rejected := make(map[string]bool, len(storageTierUnforwardableFilterFields))
			for _, field := range storageTierUnforwardableFilterFields {
				rejected[field] = true
			}

			for i := range fields.Len() {
				field := fields.Get(i)
				// description/protocol share the same path publicly and privately, so they're forwardable.
				if field.Name() == "description" || field.Name() == "protocol" {
					continue
				}
				path := fmt.Sprintf("this.spec.%s", field.Name())
				Expect(rejected[path]).To(BeTrue(),
					fmt.Sprintf(
						"field %q is not covered by storageTierUnforwardableFilterFields — update the "+
							"rejection list in storage_tiers_server.go",
						field.Name(),
					))
			}
		})

		It("Public and private StorageProtocol enums have identical named values", func() {
			private := make(map[string]int32)
			privateValues := privatev1.StorageProtocol(0).Descriptor().Values()
			for i := range privateValues.Len() {
				v := privateValues.Get(i)
				private[string(v.Name())] = int32(v.Number())
			}

			public := make(map[string]int32)
			publicValues := publicv1.StorageProtocol(0).Descriptor().Values()
			for i := range publicValues.Len() {
				v := publicValues.Get(i)
				public[string(v.Name())] = int32(v.Number())
			}

			Expect(private).To(Equal(public),
				"private and public StorageProtocol enums have drifted — update toPublicStorageProtocol and this test")
		})
	})
})
