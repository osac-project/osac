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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Storage backend ownership API", func() {
	var client privatev1.StorageBackendsClient
	BeforeEach(func() {
		client = privatev1.NewStorageBackendsClient(tool.InternalView().AdminConn())
	})

	It("persists a credential-free CSP-managed backend and enforces immutable ownership", func(ctx context.Context) {
		created, err := client.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Metadata: privatev1.Metadata_builder{Name: "it-csp-backend-" + uuid.New()}.Build(),
				Spec:     privatev1.StorageBackendSpec_builder{Provider: "netapp", CspManaged: true}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		id := created.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := client.Delete(ctx, privatev1.StorageBackendsDeleteRequest_builder{Id: id}.Build())
			Expect(err).NotTo(HaveOccurred())
		})

		updated, err := client.Update(ctx, privatev1.StorageBackendsUpdateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Id:   id,
				Spec: privatev1.StorageBackendSpec_builder{Description: "CSP owns storage provisioning"}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.description"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetCspManaged()).To(BeTrue())

		_, err = client.Update(ctx, privatev1.StorageBackendsUpdateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Id: id,
				Spec: privatev1.StorageBackendSpec_builder{
					Endpoint: "https://storage.example.com",
					Credentials: privatev1.StorageBackendCredentials_builder{
						Username: "admin", Password: testBackendPassword,
					}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.csp_managed", "spec.endpoint", "spec.credentials"}},
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("spec.csp_managed"))

		got, err := client.Get(ctx, privatev1.StorageBackendsGetRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.GetObject().GetSpec().GetCspManaged()).To(BeTrue())
		Expect(got.GetObject().GetSpec().GetEndpoint()).To(BeEmpty())
		Expect(got.GetObject().GetSpec().GetCredentials()).To(BeNil())
		Expect(got.GetObject().GetSpec().GetDescription()).To(Equal("CSP owns storage provisioning"))
		Expect(got.GetObject().GetMetadata().GetTenant()).To(Equal("shared"))
	})

	It("rejects credential-free registration with default OSAC ownership", func(ctx context.Context) {
		_, err := client.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Metadata: privatev1.Metadata_builder{Name: "it-osac-backend-" + uuid.New()}.Build(),
				Spec:     privatev1.StorageBackendSpec_builder{Provider: "vast"}.Build(),
			}.Build(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("does not grant tenant users private backend access", func(ctx context.Context) {
		userClient := privatev1.NewStorageBackendsClient(tool.InternalView().UserConn())
		_, err := userClient.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Metadata: privatev1.Metadata_builder{Name: "it-forbidden-backend-" + uuid.New()}.Build(),
				Spec:     privatev1.StorageBackendSpec_builder{Provider: "netapp", CspManaged: true}.Build(),
			}.Build(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
	})
})
