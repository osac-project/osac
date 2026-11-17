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
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("System-attributed project activation", Label("project-activation"), func() {
	It("activates tenant roots and named private-API projects without a synthetic User membership", func(ctx context.Context) {
		conn := tool.InternalView().AdminConn()
		tenants := privatev1.NewTenantsClient(conn)
		projects := privatev1.NewProjectsClient(conn)
		memberships := privatev1.NewProjectMembershipsClient(conn)
		name := "project-activation-" + uuid.New()[24:]
		id := createTenant(ctx, tenants, name)
		waitForTenantSynced(ctx, tenants, id)

		By("confirming that the tenant organisation exists in real Keycloak")
		code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet,
			"/organizations?search="+url.QueryEscape(name)+"&exact=true", nil)
		if err != nil {
			Fail("Keycloak organisation lookup failed")
		}
		Expect(code).To(Equal(http.StatusOK))
		var organisations []struct {
			Alias   string `json:"alias"`
			Enabled bool   `json:"enabled"`
		}
		Expect(json.Unmarshal(body, &organisations)).To(Succeed())
		organisationCount := len(organisations)
		Expect(organisationCount).To(Equal(1))
		Expect(organisations[0].Alias).To(Equal(name))
		Expect(organisations[0].Enabled).To(BeTrue())

		expectActive := func(projectName string) {
			GinkgoHelper()
			filter := fmt.Sprintf("this.metadata.tenant == %q && this.metadata.name == %q", name, projectName)
			Eventually(ctx, func(g Gomega) {
				response, listErr := projects.List(ctx, privatev1.ProjectsListRequest_builder{Filter: &filter}.Build())
				g.Expect(status.Code(listErr)).To(Equal(codes.OK))
				projectCount := len(response.GetItems())
				g.Expect(projectCount).To(Equal(1))
				project := response.GetItems()[0]
				g.Expect(project.GetMetadata().GetCreator()).To(Equal("system"))
				g.Expect(project.GetStatus().GetState()).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
				var keycloakCondition *privatev1.ProjectCondition
				for _, condition := range project.GetStatus().GetConditions() {
					if condition.GetType() == privatev1.ProjectConditionType_PROJECT_CONDITION_TYPE_KEYCLOAK_SYNC {
						keycloakCondition = condition
					}
				}
				g.Expect(keycloakCondition).ToNot(BeNil(), "Keycloak sync condition is missing")
				g.Expect(keycloakCondition.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
				g.Expect(keycloakCondition.GetReason()).To(Equal("GroupsCreated"))
			}, time.Minute, time.Second).Should(Succeed())
		}

		By("waiting for automatic root activation without a manual update or Signal")
		expectActive("")

		By("creating a named project through the same private API attribution path")
		response, err := projects.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{Name: "private-project", Tenant: name}.Build(),
			}.Build(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.OK))
		DeferCleanup(func(cleanupCtx context.Context) {
			deleteProject(cleanupCtx, projects, response.GetObject().GetId())
		})
		expectActive("private-project")

		By("confirming neither project acquired a membership for the synthetic creator")
		filter := fmt.Sprintf("this.metadata.tenant == %q", name)
		list, err := memberships.List(ctx, privatev1.ProjectMembershipsListRequest_builder{Filter: &filter}.Build())
		Expect(status.Code(err)).To(Equal(codes.OK))
		membershipCount := len(list.GetItems())
		Expect(membershipCount).To(BeZero())
	})
})
