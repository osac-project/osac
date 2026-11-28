/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/mcpserver"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("MCP normalized outcomes", func() {
	var ctx context.Context

	BeforeEach(func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Minute)
		DeferCleanup(cancel)
	})

	It("reconstructs the same public Project outcome from replacement servers and fresh sessions", func() {
		revokeAdamTenantAdmin := grantMCPDiscoveryTenantAdmin(ctx, "adam")

		adamConn, err := tool.MakeUserConnection(ctx, "adam", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(adamConn.Close()).To(Succeed()) })
		_, err = publicv1.NewProjectsClient(adamConn).List(ctx, publicv1.ProjectsListRequest_builder{
			Limit: new(int32(1)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		adminConn := tool.InternalView().AdminConn()
		projectID := createMCPDiscoveryProject(
			ctx,
			publicv1.NewProjectsClient(adamConn),
			privatev1.NewProjectsClient(adminConn),
			privatev1.NewProjectMembershipsClient(adminConn),
			privatev1.NewUsersClient(adminConn),
			"adam",
			"engineering",
		)
		revokeAdamTenantAdmin(ctx)

		firstServer := startInProcessMCPServer(ctx, "fulfillment-mcp-outcome-it-1")
		firstSession := connectMCPCaller(ctx, firstServer.URL, "it-mcp-adam-outcome-1", "adam")
		firstOutcome := getMCPResourceOutcome(ctx, firstSession, mcpserver.ResourceTypeProject, projectID)

		secondServer := startInProcessMCPServer(ctx, "fulfillment-mcp-outcome-it-2")
		secondSession := connectMCPCaller(ctx, secondServer.URL, "it-mcp-adam-outcome-2", "adam")
		secondOutcome := getMCPResourceOutcome(ctx, secondSession, mcpserver.ResourceTypeProject, projectID)

		Expect(firstOutcome.Resource).To(Equal(mcpserver.ResourceOutcomeResource{
			Type: "project", ID: projectID, Version: firstOutcome.Resource.Version,
		}))
		Expect(firstOutcome.Resource.Version).To(BeNumerically(">", 0))
		Expect(firstOutcome.LifecycleState).To(Equal("PROJECT_STATE_ACTIVE"))
		Expect(firstOutcome.Conditions).To(BeEmpty())
		Expect(firstOutcome.Facts).To(BeEmpty())
		Expect(firstOutcome.Unknowns).To(BeEmpty())
		Expect(firstOutcome.NextActions).To(BeEmpty())

		firstOutcome.Timing.ObservedAt = ""
		secondOutcome.Timing.ObservedAt = ""
		Expect(secondOutcome).To(Equal(firstOutcome))

		encoded, err := json.Marshal(secondOutcome)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(encoded)).ToNot(ContainSubstring("sensitive"))
	})
})

func getMCPResourceOutcome(
	ctx context.Context, session *mcp.ClientSession, resourceType mcpserver.ResourceType, resourceID string,
) mcpserver.GetResourceOutcomeOutput {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_resource_outcome",
		Arguments: mcpserver.GetResourceOutcomeInput{
			ResourceType: resourceType,
			ResourceID:   resourceID,
		},
	})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, result.IsError).To(BeFalse())
	raw, err := json.Marshal(result.StructuredContent)
	ExpectWithOffset(1, err).To(Succeed())
	var output mcpserver.GetResourceOutcomeOutput
	ExpectWithOffset(1, json.Unmarshal(raw, &output)).To(Succeed())
	return output
}
