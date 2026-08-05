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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
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
					labels.TenantUuid: name,
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
					labels.TenantUuid: name,
				}, crclient.InNamespace(hubNamespace))
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(tenantList.Items).To(HaveLen(1))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())

		By("Deleting the tenant")
		deleteTenant(ctx, tenantsClient, projectsClient, id)

		By("Verifying Tenant CR is removed from the hub cluster")
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, tenantList, crclient.MatchingLabels{
					labels.TenantUuid: name,
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
					labels.TenantUuid: name,
				}, crclient.InNamespace(hubNamespace))
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(tenantList.Items).To(HaveLen(1))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
		tenantCR := &tenantList.Items[0]

		By("Removing the TenantUuid label from the Tenant CR")
		delete(tenantCR.Labels, labels.TenantUuid)
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

		By("Verifying TenantUuid label is restored on the Tenant CR")
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, tenantList, crclient.MatchingLabels{
					labels.TenantUuid: name,
				}, crclient.InNamespace(hubNamespace))
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(tenantList.Items).To(HaveLen(1))
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

var _ = Describe("Tenant deletion with projects", func() {
	var (
		tenantsClient  privatev1.TenantsClient
		projectsClient privatev1.ProjectsClient
	)

	BeforeEach(func() {
		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		projectsClient = privatev1.NewProjectsClient(tool.InternalView().AdminConn())
	})

	It("Blocks tenant deletion until all projects are removed", func(ctx context.Context) {
		name := fmt.Sprintf("test-%s", uuid.New())

		By(fmt.Sprintf("Creating tenant %q", name))
		id := createTenant(ctx, tenantsClient, name)

		By("Waiting for tenant to reach SYNCED state")
		waitForTenantSynced(ctx, tenantsClient, id)

		By("Creating a project under the tenant")
		projectName := fmt.Sprintf("proj-%s", uuid.New())
		projectResp, err := projectsClient.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   projectName,
					Tenant: name,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		projectId := projectResp.GetObject().GetId()

		By("Requesting tenant deletion (without deleting the project first)")
		_, err = tenantsClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Verifying the tenant is not immediately removed (blocked by project FK)")
		Consistently(
			func(g Gomega) {
				getResp, err := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{
					Id: id,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(getResp.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			},
			10*time.Second,
			time.Second,
		).Should(Succeed())

		By("Deleting the explicitly created project")
		deleteProject(ctx, projectsClient, projectId)

		By("Deleting all remaining projects for the tenant (including auto-created root)")
		listFilter := fmt.Sprintf(
			"this.metadata.tenant == %q && !has(this.metadata.deletion_timestamp)", name,
		)
		for {
			listResp, listErr := projectsClient.List(ctx, privatev1.ProjectsListRequest_builder{
				Filter: &listFilter,
			}.Build())
			Expect(listErr).ToNot(HaveOccurred())
			if listResp.GetTotal() == 0 {
				break
			}
			for _, item := range listResp.GetItems() {
				deleteProject(ctx, projectsClient, item.GetId())
			}
		}

		By("Signaling the tenant to wake reconcilers from backoff")
		_, err = tenantsClient.Signal(ctx, privatev1.TenantsSignalRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

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
