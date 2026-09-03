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

// testVolBackendPassword is a dummy credential for a StorageBackend that only ever exists for the
// duration of a test and is never used to reach a real system.
const testVolBackendPassword = "secret"

var _ = Describe("Public volumes", func() {
	var (
		ctx             context.Context
		publicClient    publicv1.VolumesClient
		privateClient   privatev1.VolumesClient
		storageTierName string
	)

	BeforeEach(func() {
		ctx = context.Background()
		publicClient = publicv1.NewVolumesClient(tool.ExternalView().UserConn())
		privateClient = privatev1.NewVolumesClient(tool.InternalView().AdminConn())

		// Seed a StorageTier and StorageBackend via the private API so that volume creation
		// through the public API can resolve the tier.
		backendsClient := privatev1.NewStorageBackendsClient(tool.InternalView().AdminConn())
		backendResp, err := backendsClient.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("it-pub-vol-backend-%s", uuid.New()),
				}.Build(),
				Spec: privatev1.StorageBackendSpec_builder{
					Provider: "vast",
					Endpoint: "https://storage.example.com:8443",
					Credentials: privatev1.StorageBackendCredentials_builder{
						Username: "admin",
						Password: testVolBackendPassword,
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		backendID := backendResp.GetObject().GetId()
		DeferCleanup(func() {
			_, err := backendsClient.Delete(ctx, privatev1.StorageBackendsDeleteRequest_builder{
				Id: backendID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		tiersClient := privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())
		storageTierName = fmt.Sprintf("it-pub-vol-tier-%s", uuid.New())
		tierResp, err := tiersClient.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
			Object: privatev1.StorageTier_builder{
				Metadata: privatev1.Metadata_builder{
					Name: storageTierName,
				}.Build(),
				Spec: privatev1.StorageTierSpec_builder{
					Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					Backends: []*privatev1.BackendAssociation{
						privatev1.BackendAssociation_builder{
							BackendId: backendID,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tierID := tierResp.GetObject().GetId()
		DeferCleanup(func() {
			tiersClient := privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())
			_, err := tiersClient.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{
				Id: tierID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	// createViaPrivate seeds a Volume through the private admin API and returns it.
	createViaPrivate := func(suffix string) *privatev1.Volume {
		name := fmt.Sprintf("it-pub-vol-%s-%s", suffix, uuid.New())
		response, err := privateClient.Create(ctx, privatev1.VolumesCreateRequest_builder{
			Object: privatev1.Volume_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   name,
					Tenant: "default",
				}.Build(),
				Spec: privatev1.VolumeSpec_builder{
					StorageTier: storageTierName,
					SizeGib:     10,
					AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := privateClient.Delete(ctx, privatev1.VolumesDeleteRequest_builder{
				Id: response.GetObject().GetId(),
			}.Build())
			// Accept NotFound since the delete test may have already removed the volume.
			if err != nil {
				st, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(grpccodes.NotFound))
			}
		})
		return response.GetObject()
	}

	Describe("Get", func() {
		It("Returns volume without private-only fields", func() {
			privateVol := createViaPrivate("get")

			// Fetch through the public API:
			response, err := publicClient.Get(ctx, publicv1.VolumesGetRequest_builder{
				Id: privateVol.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			publicVol := response.GetObject()

			// Core fields should be present:
			Expect(publicVol.GetId()).To(Equal(privateVol.GetId()))
			Expect(publicVol.GetSpec().GetStorageTier()).To(Equal(storageTierName))
			Expect(publicVol.GetSpec().GetSizeGib()).To(Equal(int64(10)))
			Expect(publicVol.GetSpec().GetAccessMode()).To(Equal(
				publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE))

			// Status should be present:
			Expect(publicVol.GetStatus().GetState()).To(Equal(
				publicv1.VolumeState_VOLUME_STATE_CREATING))

			// Verify private-only fields are not in the public proto:
			statusDesc := publicVol.GetStatus().ProtoReflect().Descriptor()
			Expect(statusDesc.Fields().ByName("backend")).To(BeNil())
			Expect(statusDesc.Fields().ByName("protocol")).To(BeNil())
			Expect(statusDesc.Fields().ByName("hub")).To(BeNil())
		})

		It("Returns not found for non-existent volume", func() {
			_, err := publicClient.Get(ctx, publicv1.VolumesGetRequest_builder{
				Id: "non-existent-id",
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.NotFound))
		})
	})

	Describe("List", func() {
		It("Lists volumes created via private API", func() {
			createViaPrivate("list-1")
			createViaPrivate("list-2")

			response, err := publicClient.List(ctx, publicv1.VolumesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(BeNumerically(">=", 2))
		})

		It("Supports limit parameter", func() {
			createViaPrivate("limit-1")
			createViaPrivate("limit-2")
			createViaPrivate("limit-3")

			response, err := publicClient.List(ctx, publicv1.VolumesListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
			Expect(response.GetTotal()).To(BeNumerically(">=", 3))
		})
	})

	Describe("Delete", func() {
		It("Deletes a volume through the public API", func() {
			privateVol := createViaPrivate("delete")

			_, err := publicClient.Delete(ctx, publicv1.VolumesDeleteRequest_builder{
				Id: privateVol.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Verify it's gone:
			_, err = publicClient.Get(ctx, publicv1.VolumesGetRequest_builder{
				Id: privateVol.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.NotFound))
		})
	})
})
