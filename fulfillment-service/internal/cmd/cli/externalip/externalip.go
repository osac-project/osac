/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package externalip holds helpers shared by CLI commands that resolve
// external IPs or create attachments to them.
package externalip

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Find resolves a name-or-ID reference to a single ExternalIP.
func Find(ctx context.Context, client publicv1.ExternalIPsClient, ref string) (*publicv1.ExternalIP, error) {
	return lookup.Find(ref, "external IP", func(filter string, limit int32) ([]*publicv1.ExternalIP, error) {
		resp, err := client.List(ctx, publicv1.ExternalIPsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to resolve external IP %q: %w", ref, err)
		}
		return resp.GetItems(), nil
	})
}

// AttachmentName derives an attachment name from the external IP: its
// metadata name, or eip-<ID> when the IP has no name, plus the
// "-attachment" suffix. Commands that create an attachment without an
// explicit name use this so the convention stays in one place.
//
// The result is always a valid metadata name: at most 63 characters.
// When the metadata name plus the suffix would exceed that limit the
// name falls back to the shorter ID-based form, and if even that is too
// long it is truncated from the right so the suffix is preserved.
func AttachmentName(eip *publicv1.ExternalIP) string {
	const maxLength = 63
	const suffix = "-attachment"

	name := eip.GetMetadata().GetName()
	if name == "" || len(name)+len(suffix) > maxLength {
		name = fmt.Sprintf("eip-%s", eip.GetId())
	}
	if remaining := maxLength - len(suffix); len(name) > remaining {
		name = name[:remaining]
	}
	return name + suffix
}
