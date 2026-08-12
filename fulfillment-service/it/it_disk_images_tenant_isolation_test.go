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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("DiskImage tenant isolation", func() {
	var (
		tenantsClient privatev1.TenantsClient
		diAdminClient privatev1.DiskImagesClient
	)

	BeforeEach(func() {
		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		diAdminClient = privatev1.NewDiskImagesClient(tool.InternalView().AdminConn())
	})

	It("Global DiskImage visible to all tenants", func(ctx context.Context) {
		By("Creating a global DiskImage via admin API")
		globalName := fmt.Sprintf("global-di-%s", uuid.New())
		createResp, err := diAdminClient.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
			Object: privatev1.DiskImage_builder{
				Metadata: privatev1.Metadata_builder{
					Name: globalName,
				}.Build(),
				Spec: privatev1.DiskImageSpec_builder{
					SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
					SourceRef:     "quay.io/containerdisks/fedora:41",
					GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
					Architecture: []privatev1.Architecture{
						privatev1.Architecture_ARCHITECTURE_AMD64,
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		globalId := createResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = diAdminClient.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{
				Id: globalId,
			}.Build())
		})

		By("Creating tenant-A and tenant-B")
		nameA := fmt.Sprintf("test-%s", uuid.New())
		idA := createTenant(ctx, tenantsClient, nameA)
		nameB := fmt.Sprintf("test-%s", uuid.New())
		idB := createTenant(ctx, tenantsClient, nameB)

		By("Logging in as break-glass users")
		_, tokenSourceA := loginAsBreakGlass(ctx, tenantsClient, nameA, idA)
		extConnA, err := tool.makeGrpcConn(externalServiceAddr, tokenSourceA)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = extConnA.Close() })

		_, tokenSourceB := loginAsBreakGlass(ctx, tenantsClient, nameB, idB)
		extConnB, err := tool.makeGrpcConn(externalServiceAddr, tokenSourceB)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = extConnB.Close() })

		By("Verifying tenant-A sees the global DiskImage")
		diClientA := publicv1.NewDiskImagesClient(extConnA)
		listLimit := int32(1000)
		listResp, err := diClientA.List(ctx, publicv1.DiskImagesListRequest_builder{
			Limit: &listLimit,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listResp.GetItems(), globalId)).To(BeTrue(),
			"tenant-A should see global DiskImage")

		By("Verifying tenant-B sees the global DiskImage")
		diClientB := publicv1.NewDiskImagesClient(extConnB)
		listResp, err = diClientB.List(ctx, publicv1.DiskImagesListRequest_builder{
			Limit: &listLimit,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listResp.GetItems(), globalId)).To(BeTrue(),
			"tenant-B should see global DiskImage")
	})

	It("Tenant-scoped DiskImage isolated to its tenant", func(ctx context.Context) {
		By("Creating tenant-A and tenant-B")
		nameA := fmt.Sprintf("test-%s", uuid.New())
		idA := createTenant(ctx, tenantsClient, nameA)
		nameB := fmt.Sprintf("test-%s", uuid.New())
		idB := createTenant(ctx, tenantsClient, nameB)

		By("Logging in as break-glass users")
		_, tokenSourceA := loginAsBreakGlass(ctx, tenantsClient, nameA, idA)
		extConnA, err := tool.makeGrpcConn(externalServiceAddr, tokenSourceA)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = extConnA.Close() })

		_, tokenSourceB := loginAsBreakGlass(ctx, tenantsClient, nameB, idB)
		extConnB, err := tool.makeGrpcConn(externalServiceAddr, tokenSourceB)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = extConnB.Close() })

		By("Tenant-A creates a tenant-scoped DiskImage")
		diClientA := publicv1.NewDiskImagesClient(extConnA)
		createResp, err := diClientA.Create(ctx, publicv1.DiskImagesCreateRequest_builder{
			Object: publicv1.DiskImage_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("tenant-di-%s", uuid.New()),
				}.Build(),
				Spec: publicv1.DiskImageSpec_builder{
					SourceType:    publicv1.SourceType_SOURCE_TYPE_REGISTRY,
					SourceRef:     "quay.io/containerdisks/centos:stream9",
					GuestOsFamily: publicv1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
					Architecture: []publicv1.Architecture{
						publicv1.Architecture_ARCHITECTURE_AMD64,
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		diId := createResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = diAdminClient.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{
				Id: diId,
			}.Build())
		})

		By("Verifying tenant-A can see their DiskImage")
		getResp, err := diClientA.Get(ctx, publicv1.DiskImagesGetRequest_builder{
			Id: diId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.GetObject().GetId()).To(Equal(diId))

		By("Verifying tenant-B cannot see tenant-A's DiskImage via List")
		diClientB := publicv1.NewDiskImagesClient(extConnB)
		listLimit := int32(1000)
		listResp, err := diClientB.List(ctx, publicv1.DiskImagesListRequest_builder{
			Limit: &listLimit,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listResp.GetItems(), diId)).To(BeFalse(),
			"tenant-B should not see tenant-A's DiskImage in list")

		By("Verifying tenant-B cannot Get tenant-A's DiskImage by ID")
		_, err = diClientB.Get(ctx, publicv1.DiskImagesGetRequest_builder{
			Id: diId,
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.NotFound))
		name := getResp.GetObject().GetMetadata().GetName()

		By("Verifying tenant-B cannot Update tenant-A's DiskImage")
		_, err = diClientB.Update(ctx, publicv1.DiskImagesUpdateRequest_builder{
			Object: publicv1.DiskImage_builder{
				Id: diId,
				Metadata: publicv1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: publicv1.DiskImageSpec_builder{
					Architecture: []publicv1.Architecture{
						publicv1.Architecture_ARCHITECTURE_ARM64,
					},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.architecture"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok = grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.NotFound))

		By("Verifying tenant-B cannot Delete tenant-A's DiskImage")
		_, err = diClientB.Delete(ctx, publicv1.DiskImagesDeleteRequest_builder{
			Id: diId,
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok = grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.NotFound))
	})

	It("Tenant User can CRUD DiskImages within own tenant", func(ctx context.Context) {
		By("Creating a tenant")
		name := fmt.Sprintf("test-%s", uuid.New())
		id := createTenant(ctx, tenantsClient, name)

		By("Logging in as break-glass user")
		_, tokenSource := loginAsBreakGlass(ctx, tenantsClient, name, id)
		extConn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = extConn.Close() })

		diClient := publicv1.NewDiskImagesClient(extConn)

		By("Creating a tenant-scoped DiskImage")
		diName := fmt.Sprintf("crud-di-%s", uuid.New())
		createResp, err := diClient.Create(ctx, publicv1.DiskImagesCreateRequest_builder{
			Object: publicv1.DiskImage_builder{
				Metadata: publicv1.Metadata_builder{
					Name: diName,
				}.Build(),
				Spec: publicv1.DiskImageSpec_builder{
					SourceType:    publicv1.SourceType_SOURCE_TYPE_REGISTRY,
					SourceRef:     "quay.io/containerdisks/ubuntu:24.04",
					GuestOsFamily: publicv1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
					Architecture: []publicv1.Architecture{
						publicv1.Architecture_ARCHITECTURE_AMD64,
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		diId := createResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = diAdminClient.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{
				Id: diId,
			}.Build())
		})

		By("Verifying the DiskImage has the correct tenant")
		adminGetResp, err := diAdminClient.Get(ctx, privatev1.DiskImagesGetRequest_builder{
			Id: diId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(adminGetResp.GetObject().GetMetadata().GetTenant()).To(Equal(name))

		By("Updating the DiskImage architecture")
		_, err = diClient.Update(ctx, publicv1.DiskImagesUpdateRequest_builder{
			Object: publicv1.DiskImage_builder{
				Id: diId,
				Metadata: publicv1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: publicv1.DiskImageSpec_builder{
					Architecture: []publicv1.Architecture{
						publicv1.Architecture_ARCHITECTURE_AMD64,
						publicv1.Architecture_ARCHITECTURE_ARM64,
					},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.architecture"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Verifying the update persisted")
		getResp, err := diClient.Get(ctx, publicv1.DiskImagesGetRequest_builder{
			Id: diId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		arches := getResp.GetObject().GetSpec().GetArchitecture()
		Expect(arches).To(HaveLen(2))

		By("Deleting the DiskImage")
		_, err = diClient.Delete(ctx, publicv1.DiskImagesDeleteRequest_builder{
			Id: diId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Verifying deletion")
		_, err = diClient.Get(ctx, publicv1.DiskImagesGetRequest_builder{
			Id: diId,
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.NotFound))
	})
})

type diskImageIdentifiable interface {
	GetId() string
}

func containsId[T diskImageIdentifiable](items []T, id string) bool {
	for _, item := range items {
		if item.GetId() == id {
			return true
		}
	}
	return false
}
