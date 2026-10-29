/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package it

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	metadataPrivateLabelKey       = "admin.osac.openshift.io/integration"
	metadataPrivateAnnotationKey  = "admin.osac.openshift.io/integration"
	metadataOrdinaryLabelKey      = "example.com/integration"
	metadataOrdinaryAnnotationKey = "example.com/integration"
	fixtureStorageBackendPassword = "metadata-fixture-credential"
)

var _ = Describe("reserved metadata privacy fixtures", Label("metadata"), func() {
	It("keeps private metadata stored and hides it from tenant REST and CLI reads", func(ctx context.Context) {
		tenantAdmin := createCatalogItemTenantAdminIdentityFixture(ctx)
		tenantUser := createCatalogItemMemberIdentityFixture(ctx, tenantAdmin.TenantID)
		secondTenantUser, err := metadataIdentityForExistingUser(ctx, "charles", usersPassword, "sales")
		Expect(err).NotTo(HaveOccurred())
		accounts := metadataAccountsFromTool(tool, tenantAdmin, tenantUser, secondTenantUser)

		fixture, err := newComputeInstanceMetadataFixture(ctx, accounts)
		Expect(err).NotTo(HaveOccurred())
		before, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(before.GetMetadata().GetTenant()).To(Equal(accounts.tenantUser.TenantID))

		ownerReference, hasOwnerReference := before.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]
		By("writing reserved and ordinary metadata through the private API")
		_, err = fixture.updateMetadata(ctx,
			map[string]string{
				metadataPrivateLabelKey:  "stored-label",
				metadataOrdinaryLabelKey: "visible-label",
			},
			map[string]string{
				metadataPrivateAnnotationKey:  "stored-annotation",
				metadataOrdinaryAnnotationKey: "visible-annotation",
			},
		)
		Expect(err).NotTo(HaveOccurred())

		By("allowing private reserved-key queries while denying public existence probes")
		reservedFilter := fmt.Sprintf("this.id == %q && this.metadata.labels[%q] == %q",
			fixture.id, metadataPrivateLabelKey, "stored-label")
		privateComputeInstances := privatev1.NewComputeInstancesClient(accounts.adminPrivate)
		privateList, err := privateComputeInstances.List(ctx, privatev1.ComputeInstancesListRequest_builder{
			Filter: &reservedFilter,
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(privateList.GetTotal()).To(Equal(int32(1)))
		for _, publicClient := range []publicv1.ComputeInstancesClient{
			publicv1.NewComputeInstancesClient(accounts.tenantAdmin.Conn),
			publicv1.NewComputeInstancesClient(accounts.tenantUser.Conn),
			publicv1.NewComputeInstancesClient(tool.ExternalView().AdminConn()),
		} {
			_, listErr := publicClient.List(ctx, publicv1.ComputeInstancesListRequest_builder{
				Filter: &reservedFilter,
			}.Build())
			Expect(listErr).To(HaveOccurred())
			Expect(grpcstatus.Code(listErr)).To(Equal(grpccodes.InvalidArgument))
		}
		ordinaryFilter := fmt.Sprintf("this.id == %q && this.metadata.labels[%q] == %q",
			fixture.id, metadataOrdinaryLabelKey, "visible-label")
		ordinaryList, err := publicv1.NewComputeInstancesClient(accounts.tenantUser.Conn).List(ctx,
			publicv1.ComputeInstancesListRequest_builder{Filter: &ordinaryFilter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(ordinaryList.GetTotal()).To(Equal(int32(1)))
		Expect(ordinaryList.GetItems()).To(HaveLen(1))
		Expect(ordinaryList.GetItems()[0].GetMetadata().GetLabels()).To(Equal(map[string]string{
			metadataOrdinaryLabelKey: "visible-label",
		}))

		By("checking both tenant identities through the deployed REST gateway")
		for _, reader := range []struct {
			name     string
			identity metadataTestIdentity
		}{
			{name: "Tenant Admin", identity: accounts.tenantAdmin},
			{name: "Tenant User", identity: accounts.tenantUser},
		} {
			publicObject, statusCode, _, readErr := readComputeInstanceREST(ctx, reader.identity.HTTPClient, fixture.id)
			Expect(readErr).NotTo(HaveOccurred(), reader.name)
			Expect(statusCode).To(Equal(http.StatusOK), reader.name)
			expectMetadataProjection(publicObject, before, accounts.tenantUser.TenantID)
		}

		By("checking the administrator public API projection")
		publicAdminClient := publicv1.NewComputeInstancesClient(tool.ExternalView().AdminConn())
		adminResponse, err := publicAdminClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: fixture.id}.Build())
		Expect(err).NotTo(HaveOccurred())
		expectMetadataProjection(adminResponse.GetObject(), before, accounts.tenantUser.TenantID)

		By("checking the second tenant cannot read the instance")
		_, statusCode, _, err := readComputeInstanceREST(ctx, accounts.secondTenantUser.HTTPClient, fixture.id)
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusNotFound))

		By("checking both tenant identities through the deployed CLI")
		for _, reader := range []struct {
			name     string
			identity metadataTestIdentity
		}{
			{name: "Tenant Admin", identity: accounts.tenantAdmin},
			{name: "Tenant User", identity: accounts.tenantUser},
		} {
			publicObject := readComputeInstanceCLI(ctx, reader.identity, fixture.id)
			expectMetadataProjection(publicObject, before, accounts.tenantUser.TenantID)
		}

		By("confirming private storage and fixture ownership metadata are unchanged")
		after, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.GetMetadata().GetTenant()).To(Equal(before.GetMetadata().GetTenant()))
		afterOwnerReference, afterHasOwnerReference := after.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]
		Expect(afterHasOwnerReference).To(Equal(hasOwnerReference))
		Expect(afterOwnerReference).To(Equal(ownerReference))
		Expect(maps.Equal(after.GetMetadata().GetLabels(), map[string]string{
			metadataPrivateLabelKey:  "stored-label",
			metadataOrdinaryLabelKey: "visible-label",
		})).To(BeTrue())
		Expect(maps.Equal(after.GetMetadata().GetAnnotations(), map[string]string{
			metadataPrivateAnnotationKey:  "stored-annotation",
			metadataOrdinaryAnnotationKey: "visible-annotation",
		})).To(BeTrue())

		By("checking REST map-level PATCH rejection and replacement for both metadata maps")
		rejectedStatus, _, err := patchComputeInstanceREST(ctx, accounts.tenantUser.HTTPClient, fixture.id,
			"metadata.labels", map[string]string{
				metadataPrivateLabelKey:  "rest-rejected",
				metadataOrdinaryLabelKey: "must-not-persist",
			}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(rejectedStatus).To(Equal(http.StatusBadRequest))
		afterRESTRejection, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(after.GetMetadata().GetLabels(), afterRESTRejection.GetMetadata().GetLabels())).To(BeTrue())
		Expect(maps.Equal(after.GetMetadata().GetAnnotations(), afterRESTRejection.GetMetadata().GetAnnotations())).To(BeTrue())
		Expect(after.GetMetadata().GetVersion()).To(Equal(afterRESTRejection.GetMetadata().GetVersion()))

		updatedRESTStatus, _, err := patchComputeInstanceREST(ctx, accounts.tenantUser.HTTPClient, fixture.id,
			"metadata.labels", map[string]string{metadataOrdinaryLabelKey: "rest-visible"}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(updatedRESTStatus).To(Equal(http.StatusOK))
		afterRESTUpdate, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(afterRESTUpdate.GetMetadata().GetLabels(), map[string]string{
			metadataPrivateLabelKey:  "stored-label",
			metadataOrdinaryLabelKey: "rest-visible",
		})).To(BeTrue())
		Expect(maps.Equal(afterRESTUpdate.GetMetadata().GetAnnotations(), map[string]string{
			metadataPrivateAnnotationKey:  "stored-annotation",
			metadataOrdinaryAnnotationKey: "visible-annotation",
		})).To(BeTrue())

		rejectedAnnotationStatus, _, err := patchComputeInstanceREST(ctx, accounts.tenantUser.HTTPClient, fixture.id,
			"metadata.annotations", map[string]string{
				metadataPrivateAnnotationKey:  "rest-rejected",
				metadataOrdinaryAnnotationKey: "must-not-persist",
			}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(rejectedAnnotationStatus).To(Equal(http.StatusBadRequest))
		afterRESTAnnotationRejection, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(afterRESTUpdate.GetMetadata().GetLabels(), afterRESTAnnotationRejection.GetMetadata().GetLabels())).To(BeTrue())
		Expect(maps.Equal(afterRESTUpdate.GetMetadata().GetAnnotations(), afterRESTAnnotationRejection.GetMetadata().GetAnnotations())).To(BeTrue())
		Expect(afterRESTUpdate.GetMetadata().GetVersion()).To(Equal(afterRESTAnnotationRejection.GetMetadata().GetVersion()))

		updatedRESTAnnotationStatus, _, err := patchComputeInstanceREST(ctx, accounts.tenantUser.HTTPClient, fixture.id,
			"metadata.annotations", nil, map[string]string{metadataOrdinaryAnnotationKey: "rest-visible"})
		Expect(err).NotTo(HaveOccurred())
		Expect(updatedRESTAnnotationStatus).To(Equal(http.StatusOK))
		afterRESTAnnotationUpdate, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(afterRESTAnnotationUpdate.GetMetadata().GetLabels(), map[string]string{
			metadataPrivateLabelKey:  "stored-label",
			metadataOrdinaryLabelKey: "rest-visible",
		})).To(BeTrue())
		Expect(maps.Equal(afterRESTAnnotationUpdate.GetMetadata().GetAnnotations(), map[string]string{
			metadataPrivateAnnotationKey:  "stored-annotation",
			metadataOrdinaryAnnotationKey: "rest-visible",
		})).To(BeTrue())

		emptyRESTLabelStatus, _, err := patchComputeInstanceREST(ctx, accounts.tenantUser.HTTPClient, fixture.id,
			"metadata.labels", map[string]string{}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(emptyRESTLabelStatus).To(Equal(http.StatusOK))
		afterRESTEmptyLabelUpdate, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(afterRESTEmptyLabelUpdate.GetMetadata().GetLabels(), map[string]string{
			metadataPrivateLabelKey: "stored-label",
		})).To(BeTrue())
		Expect(maps.Equal(afterRESTEmptyLabelUpdate.GetMetadata().GetAnnotations(), map[string]string{
			metadataPrivateAnnotationKey:  "stored-annotation",
			metadataOrdinaryAnnotationKey: "rest-visible",
		})).To(BeTrue())

		By("checking CLI key=value and key- updates against the public metadata boundary")
		cliHome, err := tool.NewCLIHomeDir()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(cliHome)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, cliHome, accounts.tenantUser.Username, accounts.tenantUser.Password)
		Expect(exitCode).To(Equal(0))
		runMetadataCLI := func(args ...string) int {
			GinkgoHelper()
			_, _, code := tool.RunCLI(ctx, cliHome, args...)
			return code
		}

		Expect(runMetadataCLI("label", "computeinstance", fixture.id,
			metadataPrivateLabelKey+"=rejected", metadataOrdinaryLabelKey+"=must-not-persist")).NotTo(Equal(0))
		Expect(runMetadataCLI("annotate", "computeinstance", fixture.id,
			metadataPrivateAnnotationKey+"=rejected", metadataOrdinaryAnnotationKey+"=must-not-persist")).NotTo(Equal(0))
		afterCLIRejection, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(afterRESTEmptyLabelUpdate.GetMetadata().GetLabels(), afterCLIRejection.GetMetadata().GetLabels())).To(BeTrue())
		Expect(maps.Equal(afterRESTEmptyLabelUpdate.GetMetadata().GetAnnotations(), afterCLIRejection.GetMetadata().GetAnnotations())).To(BeTrue())
		Expect(afterRESTEmptyLabelUpdate.GetMetadata().GetVersion()).To(Equal(afterCLIRejection.GetMetadata().GetVersion()))

		Expect(runMetadataCLI("label", "computeinstance", fixture.id, metadataOrdinaryLabelKey+"=cli-visible")).To(Equal(0))
		Expect(runMetadataCLI("annotate", "computeinstance", fixture.id, metadataOrdinaryAnnotationKey+"=cli-visible")).To(Equal(0))
		cliUpdated, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(cliUpdated.GetMetadata().GetLabels(), map[string]string{
			metadataPrivateLabelKey:  "stored-label",
			metadataOrdinaryLabelKey: "cli-visible",
		})).To(BeTrue())
		Expect(maps.Equal(cliUpdated.GetMetadata().GetAnnotations(), map[string]string{
			metadataPrivateAnnotationKey:  "stored-annotation",
			metadataOrdinaryAnnotationKey: "cli-visible",
		})).To(BeTrue())

		Expect(runMetadataCLI("label", "computeinstance", fixture.id, metadataOrdinaryLabelKey+"-")).To(Equal(0))
		Expect(runMetadataCLI("annotate", "computeinstance", fixture.id, metadataOrdinaryAnnotationKey+"-")).To(Equal(0))
		cliRemoved, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(cliRemoved.GetMetadata().GetLabels(), map[string]string{metadataPrivateLabelKey: "stored-label"})).To(BeTrue())
		Expect(maps.Equal(cliRemoved.GetMetadata().GetAnnotations(), map[string]string{metadataPrivateAnnotationKey: "stored-annotation"})).To(BeTrue())

		By("checking descriptor-driven private replacement and empty-map behavior")
		services, err := metadataServices("ComputeInstances")
		Expect(err).NotTo(HaveOccurred())
		updated, err := updatePrivateMetadata(ctx, accounts.adminPrivate, services.private, fixture.id,
			map[string]string{metadataPrivateLabelKey: "replacement-label", "example.com/replacement": "visible"},
			map[string]string{metadataPrivateAnnotationKey: "replacement-annotation", "example.com/replacement": "visible"},
		)
		Expect(err).NotTo(HaveOccurred())
		labels, err := metadataMapValues(updated.ProtoReflect(), "labels")
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(labels, map[string]string{
			metadataPrivateLabelKey:   "replacement-label",
			"example.com/replacement": "visible",
		})).To(BeTrue())
		annotations, err := metadataMapValues(updated.ProtoReflect(), "annotations")
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(annotations, map[string]string{
			metadataPrivateAnnotationKey: "replacement-annotation",
			"example.com/replacement":    "visible",
		})).To(BeTrue())

		empty, err := updatePrivateMetadata(ctx, accounts.adminPrivate, services.private, fixture.id, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		labels, err = metadataMapValues(empty.ProtoReflect(), "labels")
		Expect(err).NotTo(HaveOccurred())
		Expect(labels).To(BeEmpty())
		annotations, err = metadataMapValues(empty.ProtoReflect(), "annotations")
		Expect(err).NotTo(HaveOccurred())
		Expect(annotations).To(BeEmpty())
	})

	It("replaces private labels and annotations on workload, storage, and administration resources", func(ctx context.Context) {
		fixtures := []struct {
			name string
			id   string
		}{
			{name: "DiskImages", id: createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName()).GetId()},
			{name: "InstanceTypes", id: createCatalogItemComputeInstanceTypeFixture(ctx)},
			{name: "StorageTiers", id: createCatalogItemStorageTierFixture(ctx)},
			{name: "ClusterTemplates", id: createCatalogItemClusterTemplateFixture(ctx, nil, nil)},
			{name: "ComputeInstanceTemplates", id: createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)},
			{name: "BareMetalInstanceTemplates", id: createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)},
			{name: "BareMetalInstanceTypes", id: createCatalogItemBareMetalInstanceTypeFixture(ctx, "")},
			{name: "ClusterVersions", id: createCatalogItemClusterVersionFixture(ctx, "4.18.0")},
			{name: "Secrets", id: createCatalogItemUserDataSecretFixture(ctx, "users")},
		}

		projectName := createCatalogItemProjectFixture(ctx, "users")
		projects := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
		projectResponse, err := projects.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new(fmt.Sprintf("this.metadata.name == %q && this.metadata.tenant == %q", projectName, "users")),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(projectResponse.GetItems()).To(HaveLen(1))
		fixtures = append(fixtures, struct {
			name string
			id   string
		}{name: "Projects", id: projectResponse.GetItems()[0].GetId()})

		By("creating a project membership without changing a user assignment")
		memberships := privatev1.NewProjectMembershipsClient(tool.InternalView().AdminConn())
		membership, err := memberships.Create(ctx, privatev1.ProjectMembershipsCreateRequest_builder{
			Object: privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    catalogItemFixtureName(),
					Tenant:  "users",
					Project: projectName,
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Role: privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, deleteErr := memberships.Delete(cleanupCtx, privatev1.ProjectMembershipsDeleteRequest_builder{Id: membership.GetObject().GetId()}.Build())
			Expect(deleteErr).NotTo(HaveOccurred())
		})
		fixtures = append(fixtures, struct {
			name string
			id   string
		}{name: "ProjectMemberships", id: membership.GetObject().GetId()})

		By("creating workload catalog item records without starting workloads")
		catalogAdmin := tool.ExternalView().AdminConn()
		computeTemplateID := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
		computeItem := createComputeInstanceCatalogItemFixture(ctx, catalogAdmin, publicv1.ComputeInstanceCatalogItem_builder{
			Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: "users"}.Build(),
			Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: computeTemplateID}.Build(),
			Published: true,
			Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
				RunStrategy: publicv1.ComputeInstanceRunStrategyFieldPolicy_builder{
					Locked: new(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED),
				}.Build(),
			}.Build(),
		}.Build())
		fixtures = append(fixtures, struct {
			name string
			id   string
		}{name: "ComputeInstanceCatalogItems", id: computeItem.GetId()})

		clusterTemplateID := createCatalogItemClusterTemplateFixture(ctx, nil, nil)
		clusterCatalogItem := createClusterCatalogItemFixture(ctx, catalogAdmin, publicv1.ClusterCatalogItem_builder{
			Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: "users"}.Build(),
			Template:  publicv1.ClusterTemplateReference_builder{Id: clusterTemplateID}.Build(),
			Title:     "Metadata integration cluster offering",
			Published: true,
		}.Build())
		fixtures = append(fixtures, struct {
			name string
			id   string
		}{name: "ClusterCatalogItems", id: clusterCatalogItem.GetId()})

		bareMetalTemplateID := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
		bareMetalCatalogItem := createBareMetalInstanceCatalogItemFixture(ctx, catalogAdmin, publicv1.BareMetalInstanceCatalogItem_builder{
			Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: "users"}.Build(),
			Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: bareMetalTemplateID}.Build(),
			Title:     "Metadata integration bare metal offering",
			Published: true,
		}.Build())
		fixtures = append(fixtures, struct {
			name string
			id   string
		}{name: "BareMetalInstanceCatalogItems", id: bareMetalCatalogItem.GetId()})

		By("creating a halted bare-metal instance without invoking provider provisioning")
		ensureTenantDefaultNetworkingFixture(ctx, usersGroup, "")
		bareMetalImage := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
		bareMetalInstanceTypeID := createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup)
		bareMetalInstanceTemplateID := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
		haltedBareMetalItem := createBareMetalInstanceCatalogItemFixture(ctx, catalogAdmin, publicv1.BareMetalInstanceCatalogItem_builder{
			Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: "users"}.Build(),
			Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: bareMetalInstanceTemplateID}.Build(),
			Published: true,
			Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
				RunStrategy: publicv1.BareMetalInstanceRunStrategyFieldPolicy_builder{
					Locked: new(publicv1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED),
				}.Build(),
				UserDataSource: publicv1.UserDataSourceFieldPolicy_builder{
					Locked: publicv1.UserDataSource_builder{Inline: new("#cloud-config\nusers: []")}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		bareMetalInstance, err := createBareMetalInstanceFixture(ctx, tool.ExternalView().UserConn(), publicv1.BareMetalInstanceSpec_builder{
			CatalogItem:  publicv1.BareMetalInstanceCatalogItemReference_builder{Id: haltedBareMetalItem.GetId()}.Build(),
			DiskImage:    publicv1.DiskImageReference_builder{Id: bareMetalImage.GetId()}.Build(),
			InstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: bareMetalInstanceTypeID, Shared: true}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		fixtures = append(fixtures, struct {
			name string
			id   string
		}{name: "BareMetalInstances", id: bareMetalInstance.GetId()})

		By("creating an ephemeral tenant and exercising its private metadata maps")
		tenantName := catalogItemFixtureName()
		tenantID := createTenant(ctx, privatev1.NewTenantsClient(tool.InternalView().AdminConn()), tenantName)
		fixtures = append(fixtures, struct {
			name string
			id   string
		}{name: "Tenants", id: tenantID})

		By("creating a tenant identity provider for metadata coverage")
		tenants := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		waitForTenantSynced(ctx, tenants, tenantID)
		identityProviders := privatev1.NewIdentityProvidersClient(tool.InternalView().AdminConn())
		identityProvider, err := identityProviders.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: tenantName}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "Metadata integration identity provider",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.invalid/authorize",
						TokenUrl:         "https://oidc.example.invalid/token",
						ClientId:         "metadata-test",
						Issuer:           "https://oidc.example.invalid",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, deleteErr := identityProviders.Delete(cleanupCtx, privatev1.IdentityProvidersDeleteRequest_builder{Id: identityProvider.GetObject().GetId()}.Build())
			Expect(deleteErr).NotTo(HaveOccurred())
		})
		fixtures = append(fixtures, struct {
			name string
			id   string
		}{name: "IdentityProviders", id: identityProvider.GetObject().GetId()})

		By("creating a private-only storage backend")
		backends := privatev1.NewStorageBackendsClient(tool.InternalView().AdminConn())
		backend, err := backends.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Metadata: privatev1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Spec: privatev1.StorageBackendSpec_builder{
					Provider: "metadata-test",
					Endpoint: "https://storage.example.invalid",
					Credentials: privatev1.StorageBackendCredentials_builder{
						Username: "metadata-test",
						Password: fixtureStorageBackendPassword,
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, deleteErr := backends.Delete(cleanupCtx, privatev1.StorageBackendsDeleteRequest_builder{Id: backend.GetObject().GetId()}.Build())
			Expect(deleteErr).NotTo(HaveOccurred())
		})
		fixtures = append(fixtures, struct {
			name string
			id   string
		}{name: "StorageBackends", id: backend.GetObject().GetId()})

		By("creating a role, user, and role binding")
		userID := createMetadataUserFixture(ctx)
		roles := privatev1.NewRolesClient(tool.InternalView().AdminConn())
		roleName := catalogItemFixtureName()
		role, err := roles.Create(ctx, privatev1.RolesCreateRequest_builder{
			Object: privatev1.Role_builder{Metadata: privatev1.Metadata_builder{Name: roleName}.Build()}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, deleteErr := roles.Delete(cleanupCtx, privatev1.RolesDeleteRequest_builder{Id: role.GetObject().GetId()}.Build())
			Expect(deleteErr).NotTo(HaveOccurred())
		})
		roleBindings := privatev1.NewRoleBindingsClient(tool.InternalView().AdminConn())
		binding, err := roleBindings.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
			Object: privatev1.RoleBinding_builder{
				Metadata: privatev1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role:  privatev1.RoleReference_builder{Name: roleName}.Build(),
					Users: []*privatev1.UserReference{privatev1.UserReference_builder{Id: userID}.Build()},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, deleteErr := roleBindings.Delete(cleanupCtx, privatev1.RoleBindingsDeleteRequest_builder{Id: binding.GetObject().GetId()}.Build())
			Expect(deleteErr).NotTo(HaveOccurred())
		})
		fixtures = append(fixtures,
			struct {
				name string
				id   string
			}{name: "Roles", id: role.GetObject().GetId()},
			struct {
				name string
				id   string
			}{name: "Users", id: userID},
			struct {
				name string
				id   string
			}{name: "RoleBindings", id: binding.GetObject().GetId()},
		)

		By("creating an add-on operator without creating a provider workload")
		operators := privatev1.NewAddOnOperatorsClient(tool.InternalView().AdminConn())
		operatorID := "metadata-operator-" + uuid.New()[24:]
		operator, err := operators.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
			Object: privatev1.AddOnOperator_builder{
				Id:          operatorID,
				Metadata:    privatev1.Metadata_builder{Name: operatorID}.Build(),
				Title:       "Metadata integration operator",
				Description: "Metadata fixture only",
				Published:   new(true),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, deleteErr := operators.Delete(cleanupCtx, privatev1.AddOnOperatorsDeleteRequest_builder{Id: operator.GetObject().GetId()}.Build())
			Expect(deleteErr).NotTo(HaveOccurred())
		})
		fixtures = append(fixtures, struct {
			name string
			id   string
		}{name: "AddOnOperators", id: operator.GetObject().GetId()})

		By("snapshotting and restoring the harness hub around its private-only metadata case")
		hubService, err := metadataServices("Hubs")
		Expect(err).NotTo(HaveOccurred())
		hubBefore, err := privateMetadataSnapshot(ctx, tool.InternalView().AdminConn(), hubService.private, hubId)
		Expect(err).NotTo(HaveOccurred())
		hubLabels, err := metadataMapValues(hubBefore.ProtoReflect(), "labels")
		Expect(err).NotTo(HaveOccurred())
		hubAnnotations, err := metadataMapValues(hubBefore.ProtoReflect(), "annotations")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, restoreErr := updatePrivateMetadata(cleanupCtx, tool.InternalView().AdminConn(), hubService.private, hubId, hubLabels, hubAnnotations)
			Expect(restoreErr).NotTo(HaveOccurred())
		})
		fixtures = append(fixtures, struct {
			name string
			id   string
		}{name: "Hubs", id: hubId})

		for _, fixture := range fixtures {
			By("replacing both maps on " + fixture.name)
			exercisePrivateMetadataReplacement(ctx, fixture.name, fixture.id)
		}
	})
})

func exercisePrivateMetadataReplacement(ctx context.Context, serviceName, id string) {
	GinkgoHelper()
	services, err := metadataServices(serviceName)
	Expect(err).NotTo(HaveOccurred())

	firstLabels := map[string]string{
		metadataPrivateLabelKey:  "private-one",
		metadataOrdinaryLabelKey: "ordinary-one",
	}
	firstAnnotations := map[string]string{
		metadataPrivateAnnotationKey:  "private-one",
		metadataOrdinaryAnnotationKey: "ordinary-one",
	}
	updated, err := updatePrivateMetadata(ctx, tool.InternalView().AdminConn(), services.private, id, firstLabels, firstAnnotations)
	Expect(err).NotTo(HaveOccurred(), serviceName)
	labels, err := metadataMapValues(updated.ProtoReflect(), "labels")
	Expect(err).NotTo(HaveOccurred(), serviceName)
	Expect(maps.Equal(labels, firstLabels)).To(BeTrue(), serviceName)
	annotations, err := metadataMapValues(updated.ProtoReflect(), "annotations")
	Expect(err).NotTo(HaveOccurred(), serviceName)
	Expect(maps.Equal(annotations, firstAnnotations)).To(BeTrue(), serviceName)

	reservedFilter := fmt.Sprintf("this.id == %q && this.metadata.labels[%q] == %q",
		id, metadataPrivateLabelKey, "private-one")
	if serviceHasMetadataMethod(services.private, "List") {
		By("querying stored reserved metadata through the private List API for " + serviceName)
		privateResponse, listErr := metadataList(ctx, tool.InternalView().AdminConn(), services.private, reservedFilter)
		Expect(listErr).NotTo(HaveOccurred(), serviceName)
		privateItems, privateTotal, listErr := metadataListObjects(privateResponse)
		Expect(listErr).NotTo(HaveOccurred(), serviceName)
		Expect(privateTotal).To(Equal(int32(1)), serviceName)
		Expect(privateItems).To(HaveLen(1), serviceName)
		privateLabels, mapErr := metadataMapValues(privateItems[0].ProtoReflect(), "labels")
		Expect(mapErr).NotTo(HaveOccurred(), serviceName)
		Expect(maps.Equal(privateLabels, firstLabels)).To(BeTrue(), serviceName)
		privateAnnotations, mapErr := metadataMapValues(privateItems[0].ProtoReflect(), "annotations")
		Expect(mapErr).NotTo(HaveOccurred(), serviceName)
		Expect(maps.Equal(privateAnnotations, firstAnnotations)).To(BeTrue(), serviceName)
	}
	if serviceHasMetadataMethod(services.public, "List") {
		By("rejecting public reserved-key queries and retaining ordinary filters for " + serviceName)
		_, listErr := metadataList(ctx, tool.ExternalView().AdminConn(), services.public, reservedFilter)
		Expect(listErr).To(HaveOccurred(), serviceName)
		Expect(grpcstatus.Code(listErr)).To(Equal(grpccodes.InvalidArgument), serviceName)

		ordinaryFilter := fmt.Sprintf("this.id == %q && this.metadata.labels[%q] == %q",
			id, metadataOrdinaryLabelKey, "ordinary-one")
		publicResponse, listErr := metadataList(ctx, tool.ExternalView().AdminConn(), services.public, ordinaryFilter)
		Expect(listErr).NotTo(HaveOccurred(), serviceName)
		publicItems, publicTotal, listErr := metadataListObjects(publicResponse)
		Expect(listErr).NotTo(HaveOccurred(), serviceName)
		Expect(publicTotal).To(Equal(int32(1)), serviceName)
		Expect(publicItems).To(HaveLen(1), serviceName)
		publicLabels, mapErr := metadataMapValues(publicItems[0].ProtoReflect(), "labels")
		Expect(mapErr).NotTo(HaveOccurred(), serviceName)
		Expect(maps.Equal(publicLabels, map[string]string{metadataOrdinaryLabelKey: "ordinary-one"})).To(BeTrue(), serviceName)
		publicAnnotations, mapErr := metadataMapValues(publicItems[0].ProtoReflect(), "annotations")
		Expect(mapErr).NotTo(HaveOccurred(), serviceName)
		Expect(maps.Equal(publicAnnotations, map[string]string{metadataOrdinaryAnnotationKey: "ordinary-one"})).To(BeTrue(), serviceName)
	}
	if serviceName == "BareMetalInstanceTypes" {
		for _, filter := range []string{
			"this.metadata.labels == this.spec.hardware.capabilities",
			"this.spec.hardware.capabilities != this.metadata.labels",
		} {
			By("preserving whole-map filter support on private and public views for " + serviceName)
			privateResponse, listErr := metadataList(ctx, tool.InternalView().AdminConn(), services.private, filter)
			Expect(listErr).NotTo(HaveOccurred(), serviceName)
			_, _, listErr = metadataListObjects(privateResponse)
			Expect(listErr).NotTo(HaveOccurred(), serviceName)

			publicResponse, listErr := metadataList(ctx, tool.ExternalView().AdminConn(), services.public, filter)
			Expect(listErr).NotTo(HaveOccurred(), serviceName)
			_, _, listErr = metadataListObjects(publicResponse)
			Expect(listErr).NotTo(HaveOccurred(), serviceName)
		}
	}

	if services.public != nil && serviceHasMetadataMethod(services.public, "Get") {
		publicObject, publicErr := metadataObjectSnapshot(ctx, tool.ExternalView().AdminConn(), services.public, id)
		Expect(publicErr).NotTo(HaveOccurred(), serviceName)
		publicLabels, mapErr := metadataMapValues(publicObject.ProtoReflect(), "labels")
		Expect(mapErr).NotTo(HaveOccurred(), serviceName)
		Expect(maps.Equal(publicLabels, map[string]string{metadataOrdinaryLabelKey: "ordinary-one"})).To(BeTrue(), serviceName)
		publicAnnotations, mapErr := metadataMapValues(publicObject.ProtoReflect(), "annotations")
		Expect(mapErr).NotTo(HaveOccurred(), serviceName)
		Expect(maps.Equal(publicAnnotations, map[string]string{metadataOrdinaryAnnotationKey: "ordinary-one"})).To(BeTrue(), serviceName)

		// Secrets and Tenants do not support public Create (Tenants only exposes
		// public List/Get despite the generated descriptor listing Create).
		if serviceName != "Secrets" && serviceName != "Tenants" &&
			serviceHasMetadataMethod(services.public, "Create") &&
			serviceHasMetadataMethod(services.private, "List") &&
			serviceHasMetadataMethod(services.private, "Delete") {
			for _, createCase := range []struct {
				name        string
				labels      map[string]string
				annotations map[string]string
			}{
				{
					name:   "label",
					labels: map[string]string{metadataPrivateLabelKey: "create-rejected", metadataOrdinaryLabelKey: "must-not-persist"},
					annotations: map[string]string{
						metadataOrdinaryAnnotationKey: "ordinary-create-value",
					},
				},
				{
					name:   "annotation",
					labels: map[string]string{metadataOrdinaryLabelKey: "ordinary-create-value"},
					annotations: map[string]string{
						metadataPrivateAnnotationKey:  "create-rejected",
						metadataOrdinaryAnnotationKey: "must-not-persist",
					},
				},
			} {
				By("rejecting reserved " + createCase.name + " metadata on public Create for " + serviceName)
				createName := fmt.Sprintf("metadata-create-%s-%s", uuid.New()[24:32], createCase.name)
				createObject, objectErr := publicMetadataCreateObject(
					publicObject, createName, createCase.labels, createCase.annotations)
				Expect(objectErr).NotTo(HaveOccurred(), serviceName)
				if objectErr != nil {
					return
				}
				DeferCleanup(func(cleanupCtx context.Context) {
					Expect(cleanupMetadataObjectsNamed(cleanupCtx, tool.InternalView().AdminConn(), services.private, createName)).To(Succeed())
				})
				_, createErr := createPublicMetadataObject(ctx, tool.ExternalView().AdminConn(), services.public, createObject)
				Expect(createErr).To(HaveOccurred(), serviceName)
				Expect(grpcstatus.Code(createErr)).To(Equal(grpccodes.InvalidArgument), serviceName)

				privateResponse, listErr := metadataList(ctx, tool.InternalView().AdminConn(), services.private,
					fmt.Sprintf("this.metadata.name == %q", createName))
				Expect(listErr).NotTo(HaveOccurred(), serviceName)
				_, total, listErr := metadataListObjects(privateResponse)
				Expect(listErr).NotTo(HaveOccurred(), serviceName)
				Expect(total).To(BeZero(), serviceName)
				cleanupErr := cleanupMetadataObjectsNamed(ctx, tool.InternalView().AdminConn(), services.private, createName)
				Expect(cleanupErr).NotTo(HaveOccurred(), serviceName)
			}
		}
	}
	// The public Tenants server implements only List and Get. The generated
	// descriptors also include Create and Update, but those methods are unimplemented.
	// BareMetalInstanceTemplates similarly advertises Update without a handler.
	if services.public != nil && serviceHasMetadataMethod(services.public, "Update") &&
		serviceName != "Tenants" && serviceName != "BareMetalInstanceTemplates" {
		updateMethod := services.public.Methods().ByName("Update")
		if updateMethod.Input().Fields().ByName("update_mask") != nil {
			By("rejecting reserved public update input atomically on " + serviceName)
			beforeRejectedLabels, mapErr := metadataMapValues(updated.ProtoReflect(), "labels")
			Expect(mapErr).NotTo(HaveOccurred(), serviceName)
			beforeRejectedAnnotations, mapErr := metadataMapValues(updated.ProtoReflect(), "annotations")
			Expect(mapErr).NotTo(HaveOccurred(), serviceName)
			err = updatePublicMetadata(ctx, tool.ExternalView().AdminConn(), services.public, id,
				map[string]string{
					metadataPrivateLabelKey:  "rejected-marker",
					metadataOrdinaryLabelKey: "must-not-persist",
				}, map[string]string{
					metadataPrivateAnnotationKey:  "rejected-marker",
					metadataOrdinaryAnnotationKey: "must-not-persist",
				})
			Expect(err).To(HaveOccurred(), serviceName)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument), serviceName)
			afterRejected, snapshotErr := privateMetadataSnapshot(ctx, tool.InternalView().AdminConn(), services.private, id)
			Expect(snapshotErr).NotTo(HaveOccurred(), serviceName)
			afterRejectedLabels, mapErr := metadataMapValues(afterRejected.ProtoReflect(), "labels")
			Expect(mapErr).NotTo(HaveOccurred(), serviceName)
			Expect(maps.Equal(afterRejectedLabels, beforeRejectedLabels)).To(BeTrue(), serviceName)
			afterRejectedAnnotations, mapErr := metadataMapValues(afterRejected.ProtoReflect(), "annotations")
			Expect(mapErr).NotTo(HaveOccurred(), serviceName)
			Expect(maps.Equal(afterRejectedAnnotations, beforeRejectedAnnotations)).To(BeTrue(), serviceName)

			By("preserving reserved metadata on ordinary public replacement for " + serviceName)
			err = updatePublicMetadata(ctx, tool.ExternalView().AdminConn(), services.public, id,
				map[string]string{metadataOrdinaryLabelKey: "ordinary-public-replacement"},
				map[string]string{metadataOrdinaryAnnotationKey: "ordinary-public-replacement"})
			Expect(err).NotTo(HaveOccurred(), serviceName)
			afterPublicUpdate, snapshotErr := privateMetadataSnapshot(ctx, tool.InternalView().AdminConn(), services.private, id)
			Expect(snapshotErr).NotTo(HaveOccurred(), serviceName)
			labels, mapErr := metadataMapValues(afterPublicUpdate.ProtoReflect(), "labels")
			Expect(mapErr).NotTo(HaveOccurred(), serviceName)
			Expect(maps.Equal(labels, map[string]string{
				metadataPrivateLabelKey:  "private-one",
				metadataOrdinaryLabelKey: "ordinary-public-replacement",
			})).To(BeTrue(), serviceName)
			annotations, mapErr := metadataMapValues(afterPublicUpdate.ProtoReflect(), "annotations")
			Expect(mapErr).NotTo(HaveOccurred(), serviceName)
			Expect(maps.Equal(annotations, map[string]string{
				metadataPrivateAnnotationKey:  "private-one",
				metadataOrdinaryAnnotationKey: "ordinary-public-replacement",
			})).To(BeTrue(), serviceName)
		}
	}

	secondLabels := map[string]string{metadataPrivateLabelKey: "private-two"}
	secondAnnotations := map[string]string{metadataPrivateAnnotationKey: "private-two"}
	updated, err = updatePrivateMetadata(ctx, tool.InternalView().AdminConn(), services.private, id, secondLabels, secondAnnotations)
	Expect(err).NotTo(HaveOccurred(), serviceName)
	labels, err = metadataMapValues(updated.ProtoReflect(), "labels")
	Expect(err).NotTo(HaveOccurred(), serviceName)
	Expect(maps.Equal(labels, secondLabels)).To(BeTrue(), serviceName)
	annotations, err = metadataMapValues(updated.ProtoReflect(), "annotations")
	Expect(err).NotTo(HaveOccurred(), serviceName)
	Expect(maps.Equal(annotations, secondAnnotations)).To(BeTrue(), serviceName)

	updated, err = updatePrivateMetadata(ctx, tool.InternalView().AdminConn(), services.private, id, nil, nil)
	Expect(err).NotTo(HaveOccurred(), serviceName)
	labels, err = metadataMapValues(updated.ProtoReflect(), "labels")
	Expect(err).NotTo(HaveOccurred(), serviceName)
	Expect(labels).To(BeEmpty(), serviceName)
	annotations, err = metadataMapValues(updated.ProtoReflect(), "annotations")
	Expect(err).NotTo(HaveOccurred(), serviceName)
	Expect(annotations).To(BeEmpty(), serviceName)
}

func readComputeInstanceREST(
	ctx context.Context,
	client *http.Client,
	id string,
) (*publicv1.ComputeInstance, int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"/api/fulfillment/v1/compute_instances/"+id, nil)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("build ComputeInstance REST request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("send ComputeInstance REST request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.StatusCode, nil, fmt.Errorf("read ComputeInstance REST response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, body, nil
	}
	object := &publicv1.ComputeInstance{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, object); err != nil {
		return nil, response.StatusCode, body, fmt.Errorf("decode ComputeInstance REST response: %w", err)
	}
	return object, response.StatusCode, body, nil
}

func patchComputeInstanceREST(
	ctx context.Context,
	client *http.Client,
	id, updateMask string,
	labels, annotations map[string]string,
) (int, []byte, error) {
	metadata := map[string]any{}
	if labels != nil {
		metadata["labels"] = labels
	}
	if annotations != nil {
		metadata["annotations"] = annotations
	}
	payload, err := json.Marshal(map[string]any{"id": id, "metadata": metadata})
	if err != nil {
		return 0, nil, fmt.Errorf("encode ComputeInstance REST update: %w", err)
	}
	target := "/api/fulfillment/v1/compute_instances/" + id
	if updateMask != "" {
		target += "?updateMask=" + updateMask
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, target, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("build ComputeInstance REST update: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("send ComputeInstance REST update: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, nil, fmt.Errorf("read ComputeInstance REST update response: %w", err)
	}
	return response.StatusCode, body, nil
}

func readComputeInstanceCLI(ctx context.Context, identity metadataTestIdentity, id string) *publicv1.ComputeInstance {
	GinkgoHelper()
	homeDir, err := tool.NewCLIHomeDir()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
	_, stderr, exitCode := tool.LoginCLI(ctx, homeDir, identity.Username, identity.Password)
	Expect(exitCode).To(Equal(0), "CLI login failed: %s", stderr)
	output, stderr, exitCode := tool.RunCLI(ctx, homeDir, "get", "computeinstance", id, "-o", "json")
	Expect(exitCode).To(Equal(0), "CLI get failed: %s", stderr)
	object := &publicv1.ComputeInstance{}
	Expect((protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(output), object)).To(Succeed())
	return object
}

func expectMetadataProjection(object *publicv1.ComputeInstance, privateObject *privatev1.ComputeInstance, tenantID string) {
	Expect(object).NotTo(BeNil())
	Expect(object.GetMetadata().GetTenant()).To(Equal(tenantID))
	Expect(object.GetMetadata().GetLabels()).To(HaveKeyWithValue(metadataOrdinaryLabelKey, "visible-label"))
	Expect(object.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(metadataOrdinaryAnnotationKey, "visible-annotation"))
	Expect(object.GetMetadata().GetLabels()).NotTo(HaveKey(metadataPrivateLabelKey))
	Expect(object.GetMetadata().GetAnnotations()).NotTo(HaveKey(metadataPrivateAnnotationKey))
	privateOwnerReference := privateObject.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]
	publicOwnerReference, hasPublicOwnerReference := object.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]
	if privateOwnerReference == "" {
		Expect(hasPublicOwnerReference).To(BeFalse())
	} else {
		Expect(publicOwnerReference).To(Equal(privateOwnerReference))
	}
}
