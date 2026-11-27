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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Quota APIs", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("REST gateway", func() {
		It("Round-trips tenant overrides, project limits and the usage report", func() {
			// A fresh tenant keeps the limits and the usage this test writes away from every
			// other spec's state. The project holds the project limit and the disk image
			// gives the tenant images.count usage of one:
			tenant, _ := createCatalogItemTenantAdminFixture(ctx)
			project := createCatalogItemProjectFixture(ctx, tenant)
			createDiskImageInProjectFixture(ctx, tenant, project, resourceFixtureName())
			adminClient := tool.ExternalView().AdminClient()

			// Set the tenant override through the REST gateway:
			code, override := quotaRESTRequest(ctx, adminClient, http.MethodPost,
				"/api/fulfillment/v1/quota_limits", map[string]any{
					"metadata": map[string]any{"tenant": tenant, "name": "limit-images-count"},
					"spec":     map[string]any{"dimension": "images.count", "value": 5, "scope": "QUOTA_SCOPE_TENANT"},
				})
			Expect(code).To(Equal(http.StatusOK), "POST quota_limits returned %d: %v", code, override)
			Expect(override["spec"]).To(HaveKeyWithValue("dimension", "images.count"))
			Expect(override["spec"]).To(HaveKeyWithValue("value", "5"))
			Expect(override["metadata"]).To(HaveKeyWithValue("tenant", tenant))
			overrideID, ok := override["id"].(string)
			Expect(ok).To(BeTrue(), "created limit carries no id: %v", override)
			Expect(overrideID).ToNot(BeEmpty())

			// Set the project limit through the REST gateway:
			code, projectLimit := quotaRESTRequest(ctx, adminClient, http.MethodPost,
				"/api/fulfillment/v1/quota_limits", map[string]any{
					"metadata": map[string]any{"tenant": tenant, "project": project, "name": "limit-images-count-team"},
					"spec":     map[string]any{"dimension": "images.count", "value": 3, "scope": "QUOTA_SCOPE_PROJECT"},
				})
			Expect(code).To(Equal(http.StatusOK))
			Expect(projectLimit["spec"]).To(HaveKeyWithValue("dimension", "images.count"))
			Expect(projectLimit["spec"]).To(HaveKeyWithValue("value", "3"))
			Expect(projectLimit["metadata"]).To(HaveKeyWithValue("tenant", tenant))
			Expect(projectLimit["metadata"]).To(HaveKeyWithValue("project", project))
			projectLimitID, ok := projectLimit["id"].(string)
			Expect(ok).To(BeTrue(), "created limit carries no id: %v", projectLimit)
			Expect(projectLimitID).ToNot(BeEmpty())

			DeferCleanup(func(ctx context.Context) {
				for _, id := range []string{overrideID, projectLimitID} {
					code, _ := quotaRESTRequest(ctx, adminClient, http.MethodDelete,
						"/api/fulfillment/v1/quota_limits/"+id, nil)
					Expect(code).To(Equal(http.StatusOK))
				}
			})

			// Read both limits back through the REST gateway:
			code, readOverride := quotaRESTRequest(ctx, adminClient, http.MethodGet,
				"/api/fulfillment/v1/quota_limits/"+overrideID, nil)
			Expect(code).To(Equal(http.StatusOK))
			Expect(readOverride["id"]).To(Equal(overrideID))
			Expect(readOverride["spec"]).To(HaveKeyWithValue("dimension", "images.count"))
			Expect(readOverride["spec"]).To(HaveKeyWithValue("value", "5"))
			Expect(readOverride["metadata"]).To(HaveKeyWithValue("tenant", tenant))

			code, readProjectLimit := quotaRESTRequest(ctx, adminClient, http.MethodGet,
				"/api/fulfillment/v1/quota_limits/"+projectLimitID, nil)
			Expect(code).To(Equal(http.StatusOK))
			Expect(readProjectLimit["id"]).To(Equal(projectLimitID))
			Expect(readProjectLimit["spec"]).To(HaveKeyWithValue("value", "3"))
			Expect(readProjectLimit["metadata"]).To(HaveKeyWithValue("project", project))

			// Read the usage report through the REST gateway. The tenant's entry carries
			// the override of 5 over the usage of 1, and the project's entry carries the
			// tighter project limit of 3:
			code, report := quotaRESTRequest(ctx, adminClient, http.MethodGet,
				"/api/fulfillment/v1/quota_usage/"+tenant, nil)
			Expect(code).To(Equal(http.StatusOK))
			Expect(report).To(HaveKeyWithValue("tenant", tenant))

			images := findQuotaUsageDimension(report["dimensions"], "images.count")
			Expect(images).ToNot(BeNil(), "images.count missing from the report: %v", report)
			Expect(images).To(HaveKeyWithValue("limit", "5"))
			Expect(images).To(HaveKeyWithValue("usage", "1"))

			projectEntry := findQuotaUsageProject(report["projects"], project)
			Expect(projectEntry).ToNot(BeNil(), "project '%s' missing from the report: %v", project, report)
			projectImages := findQuotaUsageDimension(projectEntry["dimensions"], "images.count")
			Expect(projectImages).ToNot(BeNil(), "images.count missing from the project: %v", projectEntry)
			Expect(projectImages).To(HaveKeyWithValue("limit", "3"))
			Expect(projectImages).To(HaveKeyWithValue("usage", "1"))
		})
	})

	Describe("Typed references in a checked request", func() {
		It("prices a compute instance's id-only storage tier reference", func(ctx context.Context) {
			// The request names the tier by id alone. The deployed reference interceptor
			// fills its name before the quota check prices the boot disk, so the check
			// charges "storage.<tier>.gib" instead of failing on the missing name. This is
			// the deployed counterpart of the unit path the deleted reference-name helper
			// used to cover.
			tenantAdmin := createCatalogItemTenantAdminIdentityFixture(ctx)
			network := createNetworkFixture(ctx, tenantAdmin.TenantID, "")
			instanceType := createComputeInstanceTypeFixture(ctx)
			image := createDiskImageFixture(ctx, "shared", resourceFixtureName())
			tier := createCatalogItemStorageTierFixture(ctx)

			// The tier's name is the dimension the boot disk prices under:
			storageTiers := privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())
			tierResponse, err := storageTiers.Get(ctx, privatev1.StorageTiersGetRequest_builder{
				Id: tier,
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			tierName := tierResponse.GetObject().GetMetadata().GetName()

			template := createComputeInstanceTemplateFixture(ctx,
				privatev1.ComputeInstanceTemplateSpecDefaults_builder{
					RunStrategy: new(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS),
				}.Build(), nil)

			created, err := createComputeInstanceFixture(ctx, tenantAdmin.Conn,
				publicv1.ComputeInstanceSpec_builder{
					Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
					InstanceType: publicv1.InstanceTypeReference_builder{Id: instanceType}.Build(),
					DiskImage:    publicv1.DiskImageReference_builder{Id: image.GetId()}.Build(),
					RunStrategy:  publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
					BootDisk: publicv1.ComputeInstanceDisk_builder{
						SizeGib:     new(int32(11)),
						StorageTier: publicv1.StorageTierReference_builder{Id: tier}.Build(),
					}.Build(),
					NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
						network.computeInstanceAttachment(),
					},
				}.Build())
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				client := publicv1.NewComputeInstancesClient(tenantAdmin.Conn)
				_, deleteErr := client.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{
					Id: created.GetId(),
				}.Build())
				Expect(deleteErr == nil || status.Code(deleteErr) == codes.NotFound).To(BeTrue())
			})

			code, report := quotaRESTRequest(ctx, tenantAdmin.HTTPClient, http.MethodGet,
				"/api/fulfillment/v1/quota_usage/"+tenantAdmin.TenantID, nil)
			Expect(code).To(Equal(http.StatusOK))
			disk := findQuotaUsageDimension(report["dimensions"], "storage."+tierName+".gib")
			Expect(disk).ToNot(BeNil(),
				"storage.%s.gib missing from the usage report: %v", tierName, report)
			Expect(disk).To(HaveKeyWithValue("usage", "11"))
		})
	})

	Describe("Limit request review", func() {
		var (
			tenantAdmin    metadataTestIdentity
			adminRequests  publicv1.QuotaLimitRequestsClient
			tenantRequests publicv1.QuotaLimitRequestsClient
			adminLimits    publicv1.QuotaLimitsClient
		)

		BeforeEach(func() {
			tenantAdmin = createCatalogItemTenantAdminIdentityFixture(ctx)
			adminRequests = publicv1.NewQuotaLimitRequestsClient(tool.ExternalView().AdminConn())
			tenantRequests = publicv1.NewQuotaLimitRequestsClient(tenantAdmin.Conn)
			adminLimits = publicv1.NewQuotaLimitsClient(tool.ExternalView().AdminConn())
		})

		createRequest := func(dimension string, value int64) *publicv1.QuotaLimitRequest {
			GinkgoHelper()
			response, err := tenantRequests.Create(ctx, publicv1.QuotaLimitRequestsCreateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Spec: publicv1.QuotaLimitRequestSpec_builder{
						Dimension:      dimension,
						RequestedValue: proto.Int64(value),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			return response.GetObject()
		}

		It("Approves a tenant's request under a cloud provider admin identity", func() {
			created := createRequest("compute.vcpus", 42)
			Expect(created.GetMetadata().GetTenant()).To(Equal(tenantAdmin.TenantID))
			Expect(created.GetStatus().GetState()).To(Equal(
				publicv1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_PENDING))

			_, err := adminRequests.Update(ctx, publicv1.QuotaLimitRequestsUpdateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Id: created.GetId(),
					Status: publicv1.QuotaLimitRequestStatus_builder{
						State: publicv1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_APPROVED,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build())
			Expect(err).NotTo(HaveOccurred())

			// The request is terminal:
			reviewed, err := tenantRequests.Get(ctx, publicv1.QuotaLimitRequestsGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(reviewed.GetObject().GetStatus().GetState()).To(Equal(
				publicv1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_APPROVED))

			// The override landed: the approval wrote the requested value as the tenant's
			// override of the dimension:
			overrides, err := adminLimits.List(ctx, publicv1.QuotaLimitsListRequest_builder{
				Filter: new(fmt.Sprintf(
					`this.spec.dimension == "compute.vcpus" && this.metadata.tenant == %q`,
					tenantAdmin.TenantID)),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(overrides.GetItems()).To(HaveLen(1))
			override := overrides.GetItems()[0]
			Expect(override.GetSpec().GetValue()).To(Equal(int64(42)))
			Expect(override.GetMetadata().GetProject()).To(BeEmpty())

			DeferCleanup(func(ctx context.Context) {
				_, err := adminLimits.Delete(ctx, publicv1.QuotaLimitsDeleteRequest_builder{
					Id: override.GetId(),
				}.Build())
				if status.Code(err) == codes.NotFound {
					return
				}
				Expect(err).NotTo(HaveOccurred())
			})
			// The request row has a foreign key to the tenant, so the tenant cleanup
			// cannot archive the tenant while it exists:
			DeferCleanup(func(ctx context.Context) {
				_, err := adminRequests.Delete(ctx, publicv1.QuotaLimitRequestsDeleteRequest_builder{
					Id: created.GetId(),
				}.Build())
				if status.Code(err) == codes.NotFound {
					return
				}
				Expect(err).NotTo(HaveOccurred())
			})
		})

		It("Denies the tenant identity the approval of a request", func() {
			created := createRequest("compute.memory_gib", 8)
			// The request row has a foreign key to the tenant, so the tenant cleanup
			// cannot archive the tenant while it exists:
			DeferCleanup(func(ctx context.Context) {
				_, err := adminRequests.Delete(ctx, publicv1.QuotaLimitRequestsDeleteRequest_builder{
					Id: created.GetId(),
				}.Build())
				if status.Code(err) == codes.NotFound {
					return
				}
				Expect(err).NotTo(HaveOccurred())
			})
			Expect(created.GetStatus().GetState()).To(Equal(
				publicv1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_PENDING))

			_, err := tenantRequests.Update(ctx, publicv1.QuotaLimitRequestsUpdateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Id: created.GetId(),
					Status: publicv1.QuotaLimitRequestStatus_builder{
						State: publicv1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_APPROVED,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build())
			Expect(err).To(HaveOccurred())
			grpcStatus, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(grpcStatus.Code()).To(Equal(codes.PermissionDenied))

			// The refusal changed nothing: the request is still pending and no override
			// was written:
			reviewed, err := tenantRequests.Get(ctx, publicv1.QuotaLimitRequestsGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(reviewed.GetObject().GetStatus().GetState()).To(Equal(
				publicv1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_PENDING))

			overrides, err := adminLimits.List(ctx, publicv1.QuotaLimitsListRequest_builder{
				Filter: new(fmt.Sprintf(
					`this.spec.dimension == "compute.memory_gib" && this.metadata.tenant == %q`,
					tenantAdmin.TenantID)),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(overrides.GetItems()).To(BeEmpty())
		})
	})
})

// quotaRESTRequest sends one request to the REST gateway through one of the tool's HTTP
// clients, which fills in the scheme, the host and the bearer token, and decodes the JSON
// response body. The gateway marshals protobuf field names, so 64-bit integers appear as
// decimal strings.
func quotaRESTRequest(ctx context.Context, client *http.Client, method, path string, body any) (
	int, map[string]any,
) {
	GinkgoHelper()
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		payload = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, path, payload)
	Expect(err).NotTo(HaveOccurred())
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	Expect(err).NotTo(HaveOccurred())
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	Expect(err).NotTo(HaveOccurred())
	result := map[string]any{}
	if len(data) > 0 {
		ExpectWithOffset(1, json.Unmarshal(data, &result)).To(Succeed(), "response body: %s", string(data))
	}
	return response.StatusCode, result
}

// findQuotaUsageDimension returns the entry of one dimension of a usage report scope, or nil
// when the report holds no entry of the dimension.
func findQuotaUsageDimension(dimensions any, name string) map[string]any {
	entries, _ := dimensions.([]any)
	for _, entry := range entries {
		if dimension, ok := entry.(map[string]any); ok && dimension["dimension"] == name {
			return dimension
		}
	}
	return nil
}

// findQuotaUsageProject returns the entry of one project of a usage report, or nil when the
// report holds no entry of the project.
func findQuotaUsageProject(projects any, name string) map[string]any {
	entries, _ := projects.([]any)
	for _, entry := range entries {
		if project, ok := entry.(map[string]any); ok && project["project"] == name {
			return project
		}
	}
	return nil
}
