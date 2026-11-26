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
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Tenant onboarding to hub", func() {
	var (
		tenantsClient  privatev1.TenantsClient
		projectsClient privatev1.ProjectsClient
	)

	BeforeEach(func() {
		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		projectsClient = privatev1.NewProjectsClient(tool.InternalView().AdminConn())
	})

	It("Creates Tenant CR and namespace on hub when tenant is created", func(ctx context.Context) {
		name := fmt.Sprintf("test-%s", uuid.New())

		By(fmt.Sprintf("Creating tenant %q", name))
		id := createTenant(ctx, tenantsClient, name)

		By("Waiting for tenant to reach SYNCED state")
		waitForTenantSynced(ctx, tenantsClient, id)

		By("Verifying Tenant CR exists on the hub cluster")
		kubeClient := tool.KubeClient()
		tenantList := &osacv1alpha1.TenantList{}
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, tenantList, crclient.MatchingLabels{
					labels.TenantID: id,
				}, crclient.InNamespace(hubNamespace))
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(tenantList.Items).To(HaveLen(1))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
		tenantCR := &tenantList.Items[0]
		Expect(tenantCR.GetName()).To(Equal(name))
		Expect(tenantCR.GetNamespace()).To(Equal(hubNamespace))
		Expect(tenantCR.Labels[labels.TenantUuid]).To(Equal(name))
		Expect(tenantCR.Labels[labels.TenantID]).To(Equal(id))

		By("Verifying namespace exists on the hub cluster")
		ns := &corev1.Namespace{}
		Eventually(
			func(g Gomega) {
				err := kubeClient.Get(ctx, crclient.ObjectKey{Name: name}, ns)
				g.Expect(err).ToNot(HaveOccurred())
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
		Expect(ns.Labels[labels.TenantRef]).To(Equal(name))
		Expect(ns.Labels[labels.Project]).To(Equal(hubNamespace))
	})

	It("Removes Tenant CR and namespace from hub when tenant is deleted", func(ctx context.Context) {
		name := fmt.Sprintf("test-%s", uuid.New())

		By(fmt.Sprintf("Creating tenant %q", name))
		id := createTenant(ctx, tenantsClient, name)

		By("Waiting for tenant to reach SYNCED state")
		waitForTenantSynced(ctx, tenantsClient, id)

		By("Verifying Tenant CR exists on the hub cluster")
		kubeClient := tool.KubeClient()
		tenantList := &osacv1alpha1.TenantList{}
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, tenantList, crclient.MatchingLabels{
					labels.TenantID: id,
				}, crclient.InNamespace(hubNamespace))
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(tenantList.Items).To(HaveLen(1))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())

		By("Deleting the tenant")
		deleteTenant(ctx, tenantsClient, projectsClient, id, name)

		By("Verifying Tenant CR is removed from the hub cluster")
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, tenantList, crclient.MatchingLabels{
					labels.TenantID: id,
				}, crclient.InNamespace(hubNamespace))
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(tenantList.Items).To(BeEmpty())
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())

		By("Verifying namespace is removed from the hub cluster")
		Eventually(
			func(g Gomega) {
				ns := &corev1.Namespace{}
				err := kubeClient.Get(ctx, crclient.ObjectKey{Name: name}, ns)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())
	})

	It("Restores labels on Tenant CR and namespace after manual removal", func(ctx context.Context) {
		name := fmt.Sprintf("test-%s", uuid.New())

		By(fmt.Sprintf("Creating tenant %q", name))
		id := createTenant(ctx, tenantsClient, name)

		By("Waiting for tenant to reach SYNCED state")
		waitForTenantSynced(ctx, tenantsClient, id)

		By("Verifying Tenant CR exists on the hub cluster")
		kubeClient := tool.KubeClient()
		tenantList := &osacv1alpha1.TenantList{}
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, tenantList, crclient.MatchingLabels{
					labels.TenantID: id,
				}, crclient.InNamespace(hubNamespace))
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(tenantList.Items).To(HaveLen(1))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
		tenantCR := &tenantList.Items[0]

		By("Removing the tenant identity labels from the Tenant CR")
		delete(tenantCR.Labels, labels.TenantUuid)
		delete(tenantCR.Labels, labels.TenantID)
		err := kubeClient.Update(ctx, tenantCR)
		Expect(err).ToNot(HaveOccurred())

		By("Removing the TenantRef label from the namespace")
		ns := &corev1.Namespace{}
		err = kubeClient.Get(ctx, crclient.ObjectKey{Name: name}, ns)
		Expect(err).ToNot(HaveOccurred())
		delete(ns.Labels, labels.TenantRef)
		delete(ns.Labels, labels.Project)
		err = kubeClient.Update(ctx, ns)
		Expect(err).ToNot(HaveOccurred())

		By("Triggering re-reconciliation via Signal")
		_, err = tenantsClient.Signal(ctx, privatev1.TenantsSignalRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Verifying tenant identity labels are restored on the Tenant CR")
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, tenantList, crclient.MatchingLabels{
					labels.TenantID: id,
				}, crclient.InNamespace(hubNamespace))
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(tenantList.Items).To(HaveLen(1))
				g.Expect(tenantList.Items[0].Labels).To(HaveKeyWithValue(labels.TenantUuid, name))
				g.Expect(tenantList.Items[0].Labels).To(HaveKeyWithValue(labels.TenantID, id))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())

		By("Verifying labels are restored on the namespace")
		Eventually(
			func(g Gomega) {
				err := kubeClient.Get(ctx, crclient.ObjectKey{Name: name}, ns)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(ns.Labels[labels.TenantRef]).To(Equal(name))
				g.Expect(ns.Labels[labels.Project]).To(Equal(hubNamespace))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
	})
})

var _ = Describe("Tenant deletion", func() {
	var (
		tenantsClient  privatev1.TenantsClient
		projectsClient privatev1.ProjectsClient
		usersClient    privatev1.UsersClient
	)

	BeforeEach(func() {
		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		projectsClient = privatev1.NewProjectsClient(tool.InternalView().AdminConn())
		usersClient = privatev1.NewUsersClient(tool.InternalView().AdminConn())
	})

	It("Deletes a tenant with a user in its root project", func(ctx context.Context) {
		name := fmt.Sprintf("test-%s", uuid.New())

		By(fmt.Sprintf("Creating tenant %q", name))
		id := createTenant(ctx, tenantsClient, name)
		By("Waiting for tenant to reach SYNCED state")
		waitForTenantSynced(ctx, tenantsClient, id)

		By("Creating a user in the tenant root project")
		userName := fmt.Sprintf("user-%s", uuid.New())
		createdUser, err := usersClient.Create(ctx, privatev1.UsersCreateRequest_builder{
			Object: privatev1.User_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   userName,
					Tenant: name,
				}.Build(),
				Spec: privatev1.UserSpec_builder{
					Username: userName,
					Email:    userName + "@example.com",
					Enabled:  true,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(createdUser.GetObject().GetMetadata().GetProject()).To(BeEmpty())

		By("Deleting the tenant without first deleting its user")
		_, err = tenantsClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		listFilter := fmt.Sprintf("this.metadata.tenant == %q", name)
		Eventually(
			func(g Gomega) {
				usersResp, err := usersClient.List(ctx, privatev1.UsersListRequest_builder{
					Filter: &listFilter,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(usersResp.GetTotal()).To(BeZero())

				projectsResp, err := projectsClient.List(ctx, privatev1.ProjectsListRequest_builder{
					Filter: &listFilter,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(projectsResp.GetTotal()).To(BeZero())

				_, err = tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{
					Id: id,
				}.Build())
				g.Expect(isGRPCCode(err, grpccodes.NotFound)).To(BeTrue())
			},
			3*time.Minute,
			time.Second,
		).Should(Succeed())
	})

	It("Deletes all projects before removing the tenant", func(ctx context.Context) {
		name := fmt.Sprintf("test-%s", uuid.New())

		By(fmt.Sprintf("Creating tenant %q", name))
		id := createTenant(ctx, tenantsClient, name)

		By("Waiting for tenant to reach SYNCED state")
		waitForTenantSynced(ctx, tenantsClient, id)

		By("Creating a project under the tenant")
		projectName := fmt.Sprintf("proj-%s", uuid.New())
		_, err := projectsClient.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   projectName,
					Tenant: name,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		By("Requesting tenant deletion (without deleting the project first)")
		_, err = tenantsClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		listFilter := fmt.Sprintf("this.metadata.tenant == %q", name)

		By("Waiting for tenant deletion to remove every project, including the root")
		Eventually(
			func(g Gomega) {
				listResp, err := projectsClient.List(ctx, privatev1.ProjectsListRequest_builder{
					Filter: &listFilter,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(listResp.GetTotal()).To(BeZero())
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())

		By("Verifying the tenant is eventually fully deleted")
		Eventually(
			func(g Gomega) {
				_, err := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{
					Id: id,
				}.Build())
				g.Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				g.Expect(ok).To(BeTrue())
				g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())
	})

	It("Completes ProjectMembership cleanup when its user has a stale Keycloak ID", func(ctx context.Context) {
		name := fmt.Sprintf("test-%s", uuid.New())

		By(fmt.Sprintf("Creating tenant %q", name))
		id := createTenant(ctx, tenantsClient, name)

		By("Waiting for tenant to reach SYNCED state")
		waitForTenantSynced(ctx, tenantsClient, id)

		By("Creating a user in the tenant root project with a stale Keycloak ID")
		userName := fmt.Sprintf("user-%s", uuid.New())
		staleKeycloakID := "00000000-0000-0000-0000-000000000001"
		createdUser, err := usersClient.Create(ctx, privatev1.UsersCreateRequest_builder{
			Object: privatev1.User_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   userName,
					Tenant: name,
				}.Build(),
				Spec: privatev1.UserSpec_builder{
					Username: userName,
					Email:    userName + "@example.com",
					Enabled:  true,
				}.Build(),
				Status: privatev1.UserStatus_builder{
					KeycloakUserId: staleKeycloakID,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		userID := createdUser.GetObject().GetId()

		DeferCleanup(func(cleanupCtx context.Context) {
			_, err := usersClient.Get(cleanupCtx, privatev1.UsersGetRequest_builder{
				Id: userID,
			}.Build())
			if isGRPCCode(err, grpccodes.NotFound) {
				return
			}
			Expect(err).ToNot(HaveOccurred())

			// Reset the fixture's stale ID before cleanup so this membership
			// regression does not depend on realm-user deletion behavior.
			_, err = usersClient.Update(cleanupCtx, privatev1.UsersUpdateRequest_builder{
				Object: privatev1.User_builder{
					Id:     userID,
					Status: privatev1.UserStatus_builder{}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"status.keycloak_user_id"},
				},
			}.Build())
			if !isGRPCCode(err, grpccodes.NotFound) {
				Expect(err).ToNot(HaveOccurred())
			}
			_, err = usersClient.Delete(cleanupCtx, privatev1.UsersDeleteRequest_builder{
				Id: userID,
			}.Build())
			if !isGRPCCode(err, grpccodes.NotFound) {
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(func(g Gomega) {
				_, err := usersClient.Get(cleanupCtx, privatev1.UsersGetRequest_builder{
					Id: userID,
				}.Build())
				g.Expect(isGRPCCode(err, grpccodes.NotFound)).To(BeTrue())
			}, time.Minute, time.Second).Should(Succeed())
		})

		By("Creating a tenant-admin connection for project creation")
		tenant, err := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenantAdminRole := getOrCreateKeycloakTenantAdminRole(ctx)
		assignKeycloakRealmRole(
			ctx,
			tenant.GetObject().GetStatus().GetBreakGlassUserId(),
			tenantAdminRole,
		)
		_, tokenSource := loginAsBreakGlass(ctx, tenantsClient, name, id)
		publicConn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(publicConn.Close)

		By("Creating a project under the tenant")
		projectName := fmt.Sprintf("proj-%s", uuid.New())
		publicProjectsClient := publicv1.NewProjectsClient(publicConn)
		projectResponse, err := publicProjectsClient.Create(ctx, publicv1.ProjectsCreateRequest_builder{
			Object: publicv1.Project_builder{
				Metadata: publicv1.Metadata_builder{
					Name:   projectName,
					Tenant: name,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		projectID := projectResponse.GetObject().GetId()

		Eventually(func(g Gomega) {
			getProject, err := projectsClient.Get(ctx, privatev1.ProjectsGetRequest_builder{
				Id: projectID,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(getProject.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.ProjectState_PROJECT_STATE_ACTIVE,
			))
		}, 2*time.Minute, time.Second).Should(Succeed())

		By("Creating a ProjectMembership for the stale user")
		membershipsClient := privatev1.NewProjectMembershipsClient(tool.InternalView().AdminConn())
		membershipResponse, err := membershipsClient.Create(ctx, privatev1.ProjectMembershipsCreateRequest_builder{
			Object: privatev1.ProjectMembership_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    fmt.Sprintf("%s-membership", projectName),
					Tenant:  name,
					Project: projectName,
				}.Build(),
				Spec: privatev1.ProjectMembershipSpec_builder{
					Role: privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER,
					Users: []*privatev1.UserReference{
						{Id: userID},
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		membershipID := membershipResponse.GetObject().GetId()

		By("Waiting for the membership to fail because the stale Keycloak user does not exist")
		Eventually(func(g Gomega) {
			membership, err := membershipsClient.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{
				Id: membershipID,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(membership.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_FAILED,
			))
			g.Expect(membership.GetObject().GetStatus().GetMessage()).To(ContainSubstring("User does not exist"))
		}, 2*time.Minute, time.Second).Should(Succeed())

		By("Deleting the membership once")
		_, err = membershipsClient.Delete(ctx, privatev1.ProjectMembershipsDeleteRequest_builder{
			Id: membershipID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Verifying the membership is archived without a second delete")
		Eventually(func(g Gomega) {
			_, err := membershipsClient.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{
				Id: membershipID,
			}.Build())
			g.Expect(isGRPCCode(err, grpccodes.NotFound)).To(BeTrue())
		}, 2*time.Minute, time.Second).Should(Succeed())

		By("Deleting the project after the membership is gone")
		_, err = projectsClient.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
			Id: projectID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Verifying the project is archived without a second membership delete")
		Eventually(func(g Gomega) {
			_, err := projectsClient.Get(ctx, privatev1.ProjectsGetRequest_builder{
				Id: projectID,
			}.Build())
			g.Expect(isGRPCCode(err, grpccodes.NotFound)).To(BeTrue())
		}, 2*time.Minute, time.Second).Should(Succeed())

	})
})

var _ = Describe("Tenant IDP idempotency", func() {
	var tenantsClient privatev1.TenantsClient

	BeforeEach(func() {
		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
	})

	It("Does not create duplicate Keycloak organizations on re-reconcile", func(ctx context.Context) {
		name := fmt.Sprintf("test-%s", uuid.New())

		By(fmt.Sprintf("Creating tenant %q", name))
		id := createTenant(ctx, tenantsClient, name)

		By("Waiting for tenant to reach SYNCED state")
		waitForTenantSynced(ctx, tenantsClient, id)

		By("Verifying exactly one Keycloak organization exists for the tenant")
		code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet,
			fmt.Sprintf("/organizations?exact=true&search=%s", name), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusOK))
		var orgsBefore []map[string]any
		Expect(json.Unmarshal(body, &orgsBefore)).To(Succeed())
		Expect(orgsBefore).To(HaveLen(1))

		By("Triggering re-reconciliation via Signal")
		_, err = tenantsClient.Signal(ctx, privatev1.TenantsSignalRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Verifying no duplicate Keycloak organization appears during re-reconciliation")
		var lastOrgs []map[string]any
		Consistently(
			func(g Gomega) {
				code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet,
					fmt.Sprintf("/organizations?exact=true&search=%s", name), nil)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(code).To(Equal(http.StatusOK))
				g.Expect(json.Unmarshal(body, &lastOrgs)).To(Succeed())
				g.Expect(lastOrgs).To(HaveLen(1))
			},
			15*time.Second,
			2*time.Second,
		).Should(Succeed())

		By("Verifying the organization ID is unchanged")
		Expect(lastOrgs[0]["id"]).To(Equal(orgsBefore[0]["id"]))
	})
})
