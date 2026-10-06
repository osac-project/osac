/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package catalogitem

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// BareMetalCmd describes bare metal instance catalog items.
func BareMetalCmd() *cobra.Command {
	return newCommand("baremetalinstancecatalogitem", "bare metal instance catalog item", fetchBareMetal)
}

func fetchBareMetal(ctx context.Context, conn *grpc.ClientConn, ref string) (view, error) {
	client := publicv1.NewBareMetalInstanceCatalogItemsClient(conn)
	matched, err := lookup.Find(ref, "bare metal instance catalog item", func(filter string, limit int32) ([]*publicv1.BareMetalInstanceCatalogItem, error) {
		response, err := client.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{Filter: proto.String(filter), Limit: proto.Int32(limit)}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list bare metal instance catalog items: %w", err)
		}
		return response.GetItems(), nil
	})
	if err != nil {
		return view{}, err
	}
	response, err := client.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{Id: matched.GetId()}.Build())
	if err != nil {
		return view{}, fmt.Errorf("failed to get bare metal instance catalog item: %w", err)
	}
	return bareMetalView(response.GetObject()), nil
}

func bareMetalView(item *publicv1.BareMetalInstanceCatalogItem) view {
	v := view{
		name: item.GetMetadata().GetName(), id: item.GetId(), title: item.GetTitle(),
		description: item.GetDescription(), metadata: item.GetMetadata(),
		published: item.GetPublished(), template: formatFullRef(item.GetTemplate()), kind: "baremetalinstancecatalogitem",
	}
	fields := item.GetFields()
	addString(&v.fields, "SSH public key", fields.GetSshPublicKey(), false)
	addString(&v.fields, "User data", fields.GetUserData(), true)
	if p := fields.GetRunStrategy(); p != nil {
		format := func(value publicv1.BareMetalInstanceRunStrategy) string {
			return strings.TrimPrefix(value.String(), "BARE_METAL_INSTANCE_RUN_STRATEGY_")
		}
		appendPolicyRow(&v.fields, "Run strategy", p.HasLocked(), p.GetEditable().HasDefaultValue(),
			format(p.GetLocked()), format(p.GetEditable().GetDefaultValue()))
	}
	addBareMetalAttachments(&v.fields, fields.GetNetworkAttachments())
	addBool(&v.fields, "Auto external IP attachment", fields.GetAutoExternalIpAttachment())
	if p := fields.GetInstanceType(); p != nil {
		appendPolicyRow(&v.fields, "Instance type", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			formatFullRef(p.GetLocked()), formatFullRef(p.GetEditable().GetDefaultValue()))
	}
	addDiskImage(&v.fields, fields.GetDiskImage())
	addParameters(&v.parameters, item.GetTemplateParameters())
	return v
}

func bareMetalAttachmentDetails(items []*publicv1.BareMetalNetworkAttachment) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		groups := make([]string, 0, len(item.GetSecurityGroups()))
		for _, group := range item.GetSecurityGroups() {
			groups = append(groups, formatRef(group))
		}
		detail := "Subnet: " + formatRef(item.GetSubnet())
		if len(groups) > 0 {
			detail += "; security groups: " + strings.Join(groups, ", ")
		}
		if item.HasInterface() {
			detail += "; interface: " + item.GetInterface()
		}
		if item.HasPrimary() {
			detail += fmt.Sprintf("; primary: %t", item.GetPrimary())
		}
		result = append(result, detail)
	}
	return result
}

func addBareMetalAttachments(rows *[]row, p *publicv1.BareMetalNetworkAttachmentListFieldPolicy) {
	if p != nil {
		addCollection(rows, "Network attachments", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			bareMetalAttachmentDetails(p.GetLocked().GetItems()), bareMetalAttachmentDetails(p.GetEditable().GetDefaultValue().GetItems()))
	}
}
