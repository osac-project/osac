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
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Storage backend ownership API", func() {
	var client privatev1.StorageBackendsClient
	BeforeEach(func() {
		client = privatev1.NewStorageBackendsClient(tool.InternalView().AdminConn())
	})

	DescribeTable("rejects OSAC-managed NetApp without storing a backend",
		func(ctx context.Context, provider string) {
			name := "it-osac-netapp-" + uuid.New()
			response, err := client.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
				Object: privatev1.StorageBackend_builder{
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec: privatev1.StorageBackendSpec_builder{
						Provider: provider,
						Endpoint: "https://storage.example.com",
						Credentials: privatev1.StorageBackendCredentials_builder{
							Username: "admin", Password: testBackendPassword,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			if response.GetObject().GetId() != "" {
				DeferCleanup(func(ctx context.Context) {
					_, err := client.Delete(ctx, privatev1.StorageBackendsDeleteRequest_builder{
						Id: response.GetObject().GetId(),
					}.Build())
					Expect(err).NotTo(HaveOccurred())
				})
			}
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(err).Message()).To(ContainSubstring(
				"OSAC-managed NetApp storage is not supported; set cspManaged to true"))
			Expect(response).To(BeNil())
			listed, err := client.List(ctx, privatev1.StorageBackendsListRequest_builder{
				Filter: new(`this.metadata.name == "` + name + `"`),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(BeEmpty())
		},
		Entry("NetApp", "netapp"),
	)

	registerWithCLI := func(ctx context.Context, name string, flags ...string) *privatev1.StorageBackend {
		GinkgoHelper()
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, stderr, code := tool.RunCLI(ctx, homeDir,
			"login", "https://"+internalServiceAddr, "--private", "--insecure",
			"--flow=password", "--user="+adminUsername, "--password="+adminsPassword,
		)
		Expect(code).To(Equal(0), "login failed: %s", stderr)

		DeferCleanup(func(ctx context.Context) {
			listed, err := client.List(ctx, privatev1.StorageBackendsListRequest_builder{
				Filter: new(`this.metadata.name == "` + name + `"`),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			for _, backend := range listed.GetItems() {
				_, err := client.Delete(ctx, privatev1.StorageBackendsDeleteRequest_builder{Id: backend.GetId()}.Build())
				Expect(err).NotTo(HaveOccurred())
			}
		})
		args := append([]string{"create", "storagebackend", "--name", name}, flags...)
		_, stderr, code = tool.RunCLI(ctx, homeDir, args...)
		Expect(code).To(Equal(0), "create failed: %s", stderr)
		output, stderr, code := tool.RunCLI(ctx, homeDir, "get", "storagebackend", name, "-o", "json")
		Expect(code).To(Equal(0), "get failed: %s", stderr)
		backend := &privatev1.StorageBackend{}
		Expect((protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(output), backend)).To(Succeed())
		return backend
	}

	It("creates a credential-free CSP-managed backend through the private CLI", func(ctx context.Context) {
		backend := registerWithCLI(ctx, "it-csp-cli-"+uuid.New(), "--provider", "netapp", "--csp-managed")
		Expect(backend.GetSpec().GetCspManaged()).To(BeTrue())
		Expect(backend.GetSpec().GetEndpoint()).To(BeEmpty())
		Expect(backend.GetSpec().GetCredentials()).To(BeNil())
	})

	It("registers endpoint-only VAST through the CLI and admits only NFS tiers", func(ctx context.Context) {
		const endpoint = "https://vast.example.com"
		backend := registerWithCLI(ctx, "it-vast-nfs-"+uuid.New(), "--provider", "vast", "--endpoint", endpoint)
		got, err := client.Get(ctx, privatev1.StorageBackendsGetRequest_builder{Id: backend.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.GetObject().GetStatus().GetState()).To(Equal(privatev1.StorageBackendState_STORAGE_BACKEND_STATE_READY))
		Expect(got.GetObject().GetSpec().GetEndpoint()).To(Equal(endpoint))
		Expect(got.GetObject().GetSpec().GetCredentials()).To(BeNil())
		Expect(got.GetObject().GetSpec().GetCspManaged()).To(BeFalse())

		tiers := privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())
		createTier := func(name string, protocol privatev1.StorageProtocol) (*privatev1.StorageTiersCreateResponse, error) {
			DeferCleanup(func(ctx context.Context) {
				_, err := tiers.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{Id: name}.Build())
				if status.Code(err) != codes.NotFound {
					Expect(err).NotTo(HaveOccurred())
				}
			})
			return tiers.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Protocol: protocol,
						Backends: []*privatev1.BackendAssociation{privatev1.BackendAssociation_builder{BackendId: backend.GetId()}.Build()},
					}.Build(),
				}.Build(),
			}.Build())
		}
		nfs, err := createTier("it-nfs-"+uuid.New(), privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)
		Expect(err).NotTo(HaveOccurred())
		Expect(nfs.GetObject().GetStatus().GetState()).To(Equal(privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE))
		blockName := "it-block-" + uuid.New()
		_, err = createTier(blockName, privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("credentials"))
		_, err = tiers.Get(ctx, privatev1.StorageTiersGetRequest_builder{Id: blockName}.Build())
		Expect(status.Code(err)).To(Equal(codes.NotFound))
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

	It("requires an endpoint for VAST registration with default OSAC ownership", func(ctx context.Context) {
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
