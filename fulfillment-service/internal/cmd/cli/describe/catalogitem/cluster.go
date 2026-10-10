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
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// ClusterCmd describes cluster catalog items.
func ClusterCmd() *cobra.Command {
	return newCommand("clustercatalogitem", "cluster catalog item", fetchCluster)
}

func fetchCluster(ctx context.Context, conn *grpc.ClientConn, ref string) (view, error) {
	client := publicv1.NewClusterCatalogItemsClient(conn)
	matched, err := lookup.Find(ref, "cluster catalog item", func(filter string, limit int32) ([]*publicv1.ClusterCatalogItem, error) {
		response, err := client.List(ctx, publicv1.ClusterCatalogItemsListRequest_builder{Filter: proto.String(filter), Limit: proto.Int32(limit)}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to list cluster catalog items: %w", err)
		}
		return response.GetItems(), nil
	})
	if err != nil {
		return view{}, err
	}
	response, err := client.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{Id: matched.GetId()}.Build())
	if err != nil {
		return view{}, fmt.Errorf("failed to get cluster catalog item: %w", err)
	}
	return clusterView(response.GetObject()), nil
}

func clusterView(item *publicv1.ClusterCatalogItem) view {
	v := view{
		name: item.GetMetadata().GetName(), id: item.GetId(), title: item.GetTitle(),
		description: item.GetDescription(), metadata: item.GetMetadata(),
		published: item.GetPublished(), template: formatFullRef(item.GetTemplate()), kind: "clustercatalogitem",
	}
	fields := item.GetFields()
	if p := fields.GetVersion(); p != nil {
		appendPolicyRow(&v.fields, "Cluster version", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			formatFullRef(p.GetLocked()), formatFullRef(p.GetEditable().GetDefaultValue()))
	}
	addString(&v.fields, "SSH public key", fields.GetSshPublicKey(), false)
	if p := fields.GetPullSecretSecret(); p != nil {
		appendPolicyRow(&v.fields, "Pull secret", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			formatRef(p.GetLocked()), formatRef(p.GetEditable().GetDefaultValue()))
	}
	network := fields.GetNetwork()
	addString(&v.fields, "Pod CIDR", network.GetPodCidr(), false)
	addString(&v.fields, "Service CIDR", network.GetServiceCidr(), false)
	addNodeSets(&v.fields, fields.GetNodeSets())
	addBool(&v.fields, "Auto external IP attachment", fields.GetAutoExternalIpAttachment())
	if p := fields.GetNetworkAttachment(); p != nil {
		appendPolicyRow(&v.fields, "Network attachment", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			clusterAttachment(p.GetLocked()), clusterAttachment(p.GetEditable().GetDefaultValue()))
	}
	addParameters(&v.parameters, item.GetTemplateParameters())
	return v
}

func nodeSetDetails(items map[string]*publicv1.ClusterCatalogNodeSet) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		set := items[key]
		result = append(result, fmt.Sprintf("%s: %d nodes; bare metal instance type: %s", key, set.GetSize(), formatFullRef(set.GetBaremetalInstanceType())))
	}
	return result
}

func addNodeSets(rows *[]row, p *publicv1.ClusterNodeSetMapPolicy) {
	if p != nil {
		addCollection(rows, "Node sets", p.HasLocked(), p.GetEditable().GetDefaultValue() != nil,
			nodeSetDetails(p.GetLocked().GetItems()), nodeSetDetails(p.GetEditable().GetDefaultValue().GetItems()))
	}
}

func clusterAttachment(attachment *publicv1.ClusterNetworkAttachment) string {
	if attachment == nil {
		return "-"
	}
	groups := make([]string, 0, len(attachment.GetSecurityGroups()))
	for _, group := range attachment.GetSecurityGroups() {
		groups = append(groups, formatRef(group))
	}
	result := "subnet: " + formatRef(attachment.GetSubnet())
	if len(groups) > 0 {
		result += "; security groups: " + strings.Join(groups, ", ")
	}
	return summarizeLongDetail(result)
}
