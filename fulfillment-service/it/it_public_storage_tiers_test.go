/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

// testBackendPassword is a dummy credential for a StorageBackend that only ever exists for the
// duration of a test and is never used to reach a real system.
const testBackendPassword = "secret"

var _ = Describe("Public storage tiers", func() {
	var (
		ctx              context.Context
		publicClient     publicv1.StorageTiersClient
		privateClient    privatev1.StorageTiersClient
		privateBackendID string
	)

	BeforeEach(func() {
		ctx = context.Background()
		publicClient = publicv1.NewStorageTiersClient(tool.ExternalView().UserConn())
		privateClient = privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())

		// Seed one real StorageBackend for backend reference validation:
		backendsClient := privatev1.NewStorageBackendsClient(tool.InternalView().AdminConn())
		backendResp, err := backendsClient.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("it-pub-st-backend-%s", uuid.New()),
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
		privateBackendID = backendResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = backendsClient.Delete(ctx, privatev1.StorageBackendsDeleteRequest_builder{
				Id: privateBackendID,
			}.Build())
		})
	})

	// createViaPrivate seeds a StorageTier through the private admin API with a single backend
	// association, and returns its ID. Registers a DeferCleanup to delete it afterwards.
	createViaPrivate := func(suffix string, protocol privatev1.StorageProtocol) string {
		name := fmt.Sprintf("it-pub-st-%s-%s", suffix, uuid.New())
		response, err := privateClient.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
			Object: privatev1.StorageTier_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.StorageTierSpec_builder{
					Description: "Public IT test storage tier.",
					Protocol:    protocol,
					Backends: []*privatev1.BackendAssociation{
						privatev1.BackendAssociation_builder{
							BackendId:            privateBackendID,
							MaxReadBandwidthMbs:  1000,
							MaxWriteBandwidthMbs: 500,
							EncryptionEnabled:    true,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = privateClient.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{
				Id: id,
			}.Build())
		})
		return id
	}

	It("Can list storage tiers with the correctly flattened public shape", func() {
		createViaPrivate("list", privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)

		response, err := publicClient.List(ctx, publicv1.StorageTiersListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.GetItems()).ToNot(BeEmpty())
	})

	It("Can get a storage tier with the correctly flattened public shape", func() {
		id := createViaPrivate("get", privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)

		response, err := publicClient.Get(ctx, publicv1.StorageTiersGetRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		object := response.GetObject()
		Expect(object).ToNot(BeNil())
		Expect(object.GetId()).To(Equal(id))
		Expect(object.GetSpec().GetDescription()).To(Equal("Public IT test storage tier."))
		Expect(object.GetSpec().GetProtocol()).To(Equal(publicv1.StorageProtocol_STORAGE_PROTOCOL_NFS))
		Expect(object.GetSpec().GetMaxReadBandwidthMbs()).To(Equal(int32(1000)))
		Expect(object.GetSpec().GetMaxWriteBandwidthMbs()).To(Equal(int32(500)))
		Expect(object.GetSpec().GetEncryptionEnabled()).To(BeTrue())
		Expect(object.GetStatus().GetState()).To(Equal(publicv1.StorageTierState_STORAGE_TIER_STATE_ACTIVE))
	})

	It("Can filter storage tiers by a shared-path field", func() {
		id1 := createViaPrivate("filter1", privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)
		id2 := createViaPrivate("filter2", privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK)

		idFilter := fmt.Sprintf("this.id == '%s'", id1)
		response, err := publicClient.List(ctx, publicv1.StorageTiersListRequest_builder{
			Filter: &idFilter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetItems()[0].GetId()).To(Equal(id1))

		nameFilter := "this.metadata.name.contains('filter')"
		response, err = publicClient.List(ctx, publicv1.StorageTiersListRequest_builder{
			Filter: &nameFilter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(2))
		ids := []string{response.GetItems()[0].GetId(), response.GetItems()[1].GetId()}
		Expect(ids).To(ContainElements(id1, id2))
	})

	It("Supports pagination", func() {
		createViaPrivate("page1", privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)
		createViaPrivate("page2", privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)
		createViaPrivate("page3", privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)

		pageFilter := "this.metadata.name.contains('page')"
		limit2 := int32(2)
		response, err := publicClient.List(ctx, publicv1.StorageTiersListRequest_builder{
			Filter: &pageFilter,
			Limit:  &limit2,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(2))
		Expect(response.GetTotal()).To(BeNumerically(">=", 3))
		Expect(response.GetSize()).To(Equal(int32(2)))

		offset2 := int32(2)
		response, err = publicClient.List(ctx, publicv1.StorageTiersListRequest_builder{
			Filter: &pageFilter,
			Offset: &offset2,
			Limit:  &limit2,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetTotal()).To(BeNumerically(">=", 3))
	})

	It("Denies a tenant calling the private StorageTiers API", func() {
		client := privatev1.NewStorageTiersClient(tool.InternalView().UserConn())
		_, err := client.List(ctx, privatev1.StorageTiersListRequest_builder{}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
	})

	It("Rejects a filter referencing a field excluded from the public schema", func() {
		createViaPrivate("excluded", privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)

		filter := fmt.Sprintf("this.spec.backends[0].backend_id == '%s'", privateBackendID)
		_, err := publicClient.List(ctx, publicv1.StorageTiersListRequest_builder{
			Filter: &filter,
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects a filter referencing a field at a private-mismatched path", func() {
		createViaPrivate("mismatch", privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)

		filter := "this.spec.max_read_bandwidth_mbs == 1000"
		_, err := publicClient.List(ctx, publicv1.StorageTiersListRequest_builder{
			Filter: &filter,
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
	})

	It("Filters storage tiers by protocol now that the path is shared with the private schema", func() {
		id1 := createViaPrivate("protocol1", privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)
		createViaPrivate("protocol2", privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK)

		filter := "this.metadata.name.contains('protocol') && this.spec.protocol == 1"
		response, err := publicClient.List(ctx, publicv1.StorageTiersListRequest_builder{
			Filter: &filter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetItems()[0].GetId()).To(Equal(id1))
	})

	// A StorageTier's protocol must be an explicit NFS or BLOCK; the proto constraint
	// (enum not_in [0]) rejects UNSPECIFIED at the protovalidate interceptor, before the handler.
	It("Rejects creating a tier with an unset (UNSPECIFIED) protocol", func() {
		name := fmt.Sprintf("it-pub-st-unspecified-%s", uuid.New())
		_, err := privateClient.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
			Object: privatev1.StorageTier_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.StorageTierSpec_builder{
					Description: "Public IT test storage tier.",
					Backends: []*privatev1.BackendAssociation{
						privatev1.BackendAssociation_builder{
							BackendId: privateBackendID,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(st.Message()).To(ContainSubstring("protocol"))
	})
})
