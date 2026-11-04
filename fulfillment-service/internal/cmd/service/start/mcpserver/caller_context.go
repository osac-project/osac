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
	"errors"
	"fmt"
	"slices"

	"github.com/golang-jwt/jwt/v5"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

const callerContextExtraKey = "caller_context"

type safeCallerContext struct {
	Subject          string   `json:"subject"`
	Tenants          []string `json:"tenants,omitempty"`
	TenantsTruncated bool     `json:"tenants_truncated"`
}

func safeCallerContextFromToken(token *jwt.Token) (safeCallerContext, error) {
	if token == nil {
		return safeCallerContext{}, errors.New("MCP caller token is missing")
	}
	subject, err := token.Claims.GetSubject()
	if err != nil || subject == "" {
		return safeCallerContext{}, errors.New("MCP caller token has no subject")
	}
	if !isSafeIdentifier(subject) {
		return safeCallerContext{}, errors.New("MCP caller token has an unsafe subject")
	}
	authContext, err := auth.ExtractAuthContext(token)
	if err != nil {
		return safeCallerContext{}, errors.New("MCP caller token has no safe identity context")
	}
	tenants, tenantsTruncated, err := tenantNames(authContext)
	if err != nil {
		return safeCallerContext{}, err
	}
	return safeCallerContext{
		Subject:          subject,
		Tenants:          tenants,
		TenantsTruncated: tenantsTruncated,
	}, nil
}

func tenantNames(authContext *auth.AuthContext) ([]string, bool, error) {
	tenants, err := boundedTenantNames(authContext.Organization)
	if err != nil {
		return nil, false, errors.New("MCP caller token has an unsafe tenant context")
	}
	slices.Sort(tenants)
	tenants = slices.Compact(tenants)
	tenantsTruncated := len(tenants) > maxCheckConnectionTenants
	if tenantsTruncated {
		tenants = tenants[:maxCheckConnectionTenants]
	}
	return tenants, tenantsTruncated, nil
}

func boundedTenantNames(organization any) ([]string, error) {
	maxDiscoveredTenants := maxCheckConnectionTenants + 1
	if organization == nil {
		return []string{}, nil
	}
	switch organization := organization.(type) {
	case []any:
		tenants := make([]string, 0, len(organization))
		seen := make(map[string]struct{}, len(organization))
		for _, value := range organization {
			tenant, ok := value.(string)
			if !ok {
				continue
			}
			if !isSafeIdentifier(tenant) {
				return nil, fmt.Errorf("unsafe tenant name")
			}
			if _, duplicate := seen[tenant]; duplicate {
				continue
			}
			seen[tenant] = struct{}{}
			tenants = append(tenants, tenant)
		}
		slices.Sort(tenants)
		if len(tenants) > maxDiscoveredTenants {
			tenants = tenants[:maxDiscoveredTenants]
		}
		return tenants, nil
	case map[string]any:
		tenants := make([]string, 0, len(organization))
		for tenant := range organization {
			if !isSafeIdentifier(tenant) {
				return nil, fmt.Errorf("unsafe tenant name")
			}
			tenants = append(tenants, tenant)
		}
		slices.Sort(tenants)
		if len(tenants) > maxDiscoveredTenants {
			tenants = tenants[:maxDiscoveredTenants]
		}
		return tenants, nil
	default:
		return nil, fmt.Errorf(
			"organization claim should be an array or object, got %T", organization,
		)
	}
}

func isSafeIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 63 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '-' && index > 0 && index < len(value)-1:
		default:
			return false
		}
	}
	return true
}
