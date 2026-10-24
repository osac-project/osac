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

package metautil

import (
	"context"
	"testing"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
)

func TestForCreateUsesEffectiveTenantAndProject(t *testing.T) {
	ctx := config.TenantIntoContext(context.Background(), "tenant-from-context")
	ctx = config.ProjectIntoContext(ctx, "project-from-context")

	metadata := ForCreate(ctx, "resource-name").Build()
	if metadata.GetName() != "resource-name" {
		t.Errorf("metadata name = %q, want %q", metadata.GetName(), "resource-name")
	}
	if metadata.GetTenant() != "tenant-from-context" {
		t.Errorf("metadata tenant = %q, want %q", metadata.GetTenant(), "tenant-from-context")
	}
	if metadata.GetProject() != "project-from-context" {
		t.Errorf("metadata project = %q, want %q", metadata.GetProject(), "project-from-context")
	}
}

func TestForCreateLeavesUnsetScopeEmpty(t *testing.T) {
	metadata := ForCreate(context.Background(), "resource-name").Build()
	if metadata.GetTenant() != "" {
		t.Errorf("metadata tenant = %q, want empty", metadata.GetTenant())
	}
	if metadata.GetProject() != "" {
		t.Errorf("metadata project = %q, want empty", metadata.GetProject())
	}
}
