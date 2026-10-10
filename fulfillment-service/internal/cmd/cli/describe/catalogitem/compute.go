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

// ComputeCmd describes compute instance catalog items.
func ComputeCmd() *cobra.Command {
	return newCommand("computeinstancecatalogitem", "compute instance catalog item", fetchCompute)
}

func fetchCompute(ctx context.Context, conn *grpc.ClientConn, ref string) (view, error) {
	client := publicv1.NewComputeInstanceCatalogItemsClient(conn)
	matched, err := lookup.Find(ref, "compute instance catalog item", func(filter string, limit int32) ([]*publicv1.ComputeInstanceCatalogItem, error) {
		response, err := client.List(ctx, publicv1.ComputeInstanceCatalogItemsListRequest_builder{Filter: proto.String(filter), Limit: proto.Int32(limit)}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list compute instance catalog items: %w", err)
		}
		return response.GetItems(), nil
	})
	if err != nil {
		return view{}, err
	}
	response, err := client.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: matched.GetId()}.Build())
	if err != nil {
		return view{}, fmt.Errorf("failed to get compute instance catalog item: %w", err)
	}
	return computeView(response.GetObject()), nil
}

func computeView(item *publicv1.ComputeInstanceCatalogItem) view {
	v := view{
		name: item.GetMetadata().GetName(), id: item.GetId(), title: item.GetTitle(),
		description: item.GetDescription(), metadata: item.GetMetadata(),
		published: item.GetPublished(), template: formatFullRef(item.GetTemplate()), kind: "computeinstancecatalogitem",
	}
	fields := item.GetFields()
	addDiskImage(&v.fields, fields.GetDiskImage())
	addInstanceType(&v.fields, fields.GetInstanceType())
	boot := fields.GetBootDisk()
	addInt32(&v.fields, "Boot disk size", boot.GetSizeGib(), " GiB")
	if p := boot.GetStorageTier(); p != nil {
		appendPolicyRow(&v.fields, "Boot disk storage tier", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			formatRef(p.GetLocked()), formatRef(p.GetEditable().GetDefaultValue()))
	}
	if p := fields.GetSshKey(); p != nil {
		appendPolicyRow(&v.fields, "SSH key", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			formatRef(p.GetLocked()), formatRef(p.GetEditable().GetDefaultValue()))
	}
	addComputeRunStrategy(&v.fields, fields.GetRunStrategy())
	addString(&v.fields, "User data", fields.GetUserData(), true)
	addComputeAttachments(&v.fields, fields.GetNetworkAttachments())
	addBool(&v.fields, "Auto external IP attachment", fields.GetAutoExternalIpAttachment())
	addComputeDisks(&v.fields, fields.GetAdditionalDisks())
	addParameters(&v.parameters, item.GetTemplateParameters())
	return v
}

func addDiskImage(rows *[]row, p *publicv1.DiskImageReferenceFieldPolicy) {
	if p != nil {
		appendPolicyRow(rows, "Disk image", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			formatFullRef(p.GetLocked()), formatFullRef(p.GetEditable().GetDefaultValue()))
	}
}

func addInstanceType(rows *[]row, p *publicv1.InstanceTypeReferenceFieldPolicy) {
	if p != nil {
		appendPolicyRow(rows, "Instance type", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			formatFullRef(p.GetLocked()), formatFullRef(p.GetEditable().GetDefaultValue()))
	}
}

func addComputeRunStrategy(rows *[]row, p *publicv1.ComputeInstanceRunStrategyFieldPolicy) {
	if p != nil {
		format := func(value publicv1.ComputeInstanceRunStrategy) string {
			return strings.TrimPrefix(value.String(), "COMPUTE_INSTANCE_RUN_STRATEGY_")
		}
		appendPolicyRow(rows, "Run strategy", p.HasLocked(), p.GetEditable().HasDefaultValue(),
			format(p.GetLocked()), format(p.GetEditable().GetDefaultValue()))
	}
}

func computeAttachmentDetails(items []*publicv1.ComputeNetworkAttachment) []string {
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
		result = append(result, detail)
	}
	return result
}

func addComputeAttachments(rows *[]row, p *publicv1.ComputeNetworkAttachmentListFieldPolicy) {
	if p != nil {
		addCollection(rows, "Network attachments", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			computeAttachmentDetails(p.GetLocked().GetItems()), computeAttachmentDetails(p.GetEditable().GetDefaultValue().GetItems()))
	}
}

func diskDetails(items []*publicv1.ComputeInstanceDisk) []string {
	result := make([]string, 0, len(items))
	for _, disk := range items {
		result = append(result, fmt.Sprintf("%d GiB; storage tier: %s", disk.GetSizeGib(), formatRef(disk.GetStorageTier())))
	}
	return result
}

func addComputeDisks(rows *[]row, p *publicv1.ComputeInstanceDiskListFieldPolicy) {
	if p != nil {
		addCollection(rows, "Additional disks", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			diskDetails(p.GetLocked().GetItems()), diskDetails(p.GetEditable().GetDefaultValue().GetItems()))
	}
}
