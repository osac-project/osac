/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package clusterversions

import (
	"context"
	"embed"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

//go:embed templates
var templatesFS embed.FS

// Find resolves a cluster version by metadata.name or spec.version. A metadata-name
// match takes precedence when the same reference also matches another version's
// spec.version. If no version matches, it displays available examples and returns
// an error.
func Find(ctx context.Context, client publicv1.ClusterVersionsClient,
	console *terminal.Console, ref string) (result *publicv1.ClusterVersion, err error) {
	if err := console.AddTemplates(templatesFS, "templates"); err != nil {
		return nil, fmt.Errorf("failed to load cluster-version templates: %w", err)
	}

	filter := fmt.Sprintf(
		"this.metadata.name == %[1]q || this.spec.version == %[1]q",
		ref,
	)
	response, err := client.List(ctx, publicv1.ClusterVersionsListRequest_builder{
		Filter: proto.String(filter),
		Limit:  proto.Int32(2),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster versions: %w", err)
	}
	matches := response.GetItems()

	if len(matches) > 0 {
		for _, match := range matches {
			if match.GetMetadata().GetName() == ref {
				return match, nil
			}
		}
		return matches[0], nil
	}

	response, err = client.List(ctx, publicv1.ClusterVersionsListRequest_builder{
		Limit: proto.Int32(10),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster versions: %w", err)
	}
	console.Render(ctx, "version_not_found.txt", map[string]any{
		"Examples": response.GetItems(),
		"Ref":      ref,
	})
	return nil, exit.Error(1)
}
