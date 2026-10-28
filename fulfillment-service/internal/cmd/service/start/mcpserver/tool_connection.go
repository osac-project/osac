/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"context"
	"errors"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/version"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type CheckConnectionInput struct{}

type CheckConnectionServer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type CheckConnectionPermission struct {
	Tenant  string `json:"tenant,omitempty"`
	Service string `json:"service"`
	Method  string `json:"method"`
	Allowed bool   `json:"allowed"`
}

type CheckConnectionOutput struct {
	Endpoint             string                      `json:"endpoint"`
	Server               CheckConnectionServer       `json:"server"`
	Caller               safeCallerContext           `json:"caller"`
	ResourceFamilies     []ResourceType              `json:"resource_families"`
	Tools                []string                    `json:"tools"`
	Permissions          []CheckConnectionPermission `json:"permission_summary"`
	PermissionsTruncated bool                        `json:"permission_summary_truncated"`
}

const maxCheckConnectionTenants = 10

func handleCheckConnection(
	endpoint string,
	resources resourceRegistry,
	registry typedToolRegistry,
	tenantsClient publicv1.TenantsClient,
	reviewsClient publicv1.SelfSubjectAccessReviewsClient,
) mcp.ToolHandlerFor[CheckConnectionInput, CheckConnectionOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, _ CheckConnectionInput,
	) (*mcp.CallToolResult, CheckConnectionOutput, error) {
		caller, err := callerFromRequest(req)
		if err != nil {
			return nil, CheckConnectionOutput{}, safeTokenExchangeError(err)
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, CheckConnectionOutput{}, err
		}

		limit := int32(maxCheckConnectionTenants + 1)
		tenantResponse, err := tenantsClient.List(ctx, publicv1.TenantsListRequest_builder{
			Limit: &limit,
		}.Build())
		if err != nil {
			return nil, CheckConnectionOutput{}, safePublicAPIError(err)
		}
		visibleTenants := visibleTenantNames(tenantResponse)
		tenants := slices.DeleteFunc(visibleTenants, func(tenant string) bool {
			return tenant == auth.SharedTenant
		})
		permissionsTruncated := int64(tenantResponse.GetTotal()) > int64(len(visibleTenants)) ||
			len(tenants) > maxCheckConnectionTenants
		if len(tenants) > maxCheckConnectionTenants {
			tenants = tenants[:maxCheckConnectionTenants]
		}
		caller.Tenants = tenants
		permissionTenants := tenants
		if len(permissionTenants) == 0 && !permissionsTruncated {
			tenants = []string{""}
			permissionTenants = tenants
		}
		permissions := make([]CheckConnectionPermission, 0, len(permissionTenants)*2)
		for _, tenant := range permissionTenants {
			for _, method := range []string{"List", "Create"} {
				allowed, err := checkComputeInstancePermission(ctx, reviewsClient, tenant, method)
				if err != nil {
					return nil, CheckConnectionOutput{}, safePublicAPIError(err)
				}
				permissions = append(permissions, CheckConnectionPermission{
					Tenant:  tenant,
					Service: "osac.public.v1.ComputeInstances",
					Method:  method,
					Allowed: allowed,
				})
			}
		}

		return nil, CheckConnectionOutput{
			Endpoint:             endpoint,
			Server:               CheckConnectionServer{Name: "osac-deployment-mcp", Version: version.Get()},
			Caller:               caller,
			ResourceFamilies:     resources.resourceTypes(),
			Tools:                registry.toolNames(),
			Permissions:          permissions,
			PermissionsTruncated: permissionsTruncated,
		}, nil
	}
}

func visibleTenantNames(response *publicv1.TenantsListResponse) []string {
	names := make([]string, 0, len(response.GetItems()))
	for _, tenant := range response.GetItems() {
		if name := tenant.GetMetadata().GetName(); name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func callerFromRequest(req *mcp.CallToolRequest) (safeCallerContext, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return safeCallerContext{}, errors.New("MCP caller token is missing")
	}
	caller, ok := req.Extra.TokenInfo.Extra[callerContextExtraKey].(safeCallerContext)
	if !ok || caller.Subject == "" {
		return safeCallerContext{}, errors.New("MCP caller identity is unavailable")
	}
	return caller, nil
}

func checkComputeInstancePermission(
	ctx context.Context, client publicv1.SelfSubjectAccessReviewsClient, tenant, method string,
) (bool, error) {
	objectMetadata := publicv1.Metadata_builder{Name: "mcp-check-connection"}
	if tenant != "" {
		objectMetadata.Tenant = tenant
	}
	object := publicv1.SelfSubjectAccessReview_builder{
		Metadata: objectMetadata.Build(),
		Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
			Service: "osac.public.v1.ComputeInstances",
			Method:  method,
		}.Build(),
	}
	response, err := client.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
		Object: object.Build(),
	}.Build())
	if err != nil {
		return false, err
	}
	return response.GetObject().GetStatus().GetAllowed(), nil
}
