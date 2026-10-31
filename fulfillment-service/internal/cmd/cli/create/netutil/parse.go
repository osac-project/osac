/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package netutil

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// ExtractSecurityGroupListSuffix splits a --network-attachment value into the portion before a
// trailing "security-groups=" or "security_groups=" clause (case-insensitive) and the list of
// security group IDs after that clause. The prefix and group values preserve the original case
// of the input string.
func ExtractSecurityGroupListSuffix(s string) (prefix string, groups []string, ok bool) {
	lower := strings.ToLower(s)

	for _, marker := range []string{"security-groups=", "security_groups="} {
		if i := strings.Index(lower, marker); i >= 0 {
			prefix = strings.TrimSpace(strings.TrimSuffix(s[:i], ","))
			rest := strings.TrimSpace(s[i+len(marker):])
			for _, id := range strings.Split(rest, ",") {
				id = strings.TrimSpace(id)
				if id != "" {
					groups = append(groups, id)
				}
			}
			return prefix, groups, true
		}
	}
	return s, nil, false
}

// NetworkAttachmentFlag is a parsed --network-attachment value. Every part is optional: an omitted Subnet selects the
// tenant's default Subnet, and omitted SecurityGroups select the tenant's default SecurityGroup when the Subnet is in
// the tenant's default VirtualNetwork. Subnet and SecurityGroups are names or IDs.
type NetworkAttachmentFlag struct {
	Subnet         string
	SecurityGroups []string
	Interface      string
}

// ParseNetworkAttachmentFlag parses a --network-attachment value: a bare subnet name or ID, or comma separated
// key=value parts (subnet, and interface when allowInterface is true), optionally followed by a last
// security-groups=NAME,NAME... part.
func ParseNetworkAttachmentFlag(value string, allowInterface bool) (result NetworkAttachmentFlag, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		err = fmt.Errorf("empty --network-attachment value")
		return
	}
	prefix, groups, hadGroups := ExtractSecurityGroupListSuffix(value)
	if hadGroups && len(groups) == 0 {
		err = fmt.Errorf("--network-attachment security-groups must list at least one name or ID")
		return
	}
	result.SecurityGroups = groups
	prefix = strings.TrimSpace(strings.TrimSuffix(prefix, ","))
	if prefix == "" {
		return
	}
	if !strings.Contains(prefix, "=") {
		result.Subnet = prefix
		return
	}
	keys := "subnet"
	if allowInterface {
		keys = "subnet, interface"
	}
	for _, fragment := range strings.Split(prefix, ",") {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" {
			continue
		}
		key, val, ok := strings.Cut(fragment, "=")
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		if !ok || val == "" {
			err = fmt.Errorf("invalid --network-attachment part %q (expected key=value)", fragment)
			return
		}
		switch {
		case key == "subnet" && result.Subnet == "":
			result.Subnet = val
		case key == "interface" && allowInterface && result.Interface == "":
			result.Interface = val
		case key == "subnet" || (key == "interface" && allowInterface):
			err = fmt.Errorf("%s appears more than once in --network-attachment %q", key, value)
			return
		default:
			err = fmt.Errorf("unknown key %q in --network-attachment (use %s, or security-groups last)", key, keys)
			return
		}
	}
	return
}

// ResolveNetworkAttachment resolves the names or IDs of a parsed --network-attachment value to IDs in the tenant. It
// fails when a name or ID matches no object or more than one, before anything is created.
func ResolveNetworkAttachment(ctx context.Context, conn grpc.ClientConnInterface, flag NetworkAttachmentFlag) (
	subnet *publicv1.SubnetLocalReference, groups []*publicv1.SecurityGroupLocalReference, err error) {
	if flag.Subnet != "" {
		subnets := publicv1.NewSubnetsClient(conn)
		var found *publicv1.Subnet
		found, err = lookup.Find(flag.Subnet, "subnet", func(filter string, limit int32) ([]*publicv1.Subnet, error) {
			response, err := subnets.List(ctx, publicv1.SubnetsListRequest_builder{
				Filter: &filter,
				Limit:  &limit,
			}.Build())
			if err != nil {
				return nil, fmt.Errorf("failed to resolve subnet %q: %w", flag.Subnet, err)
			}
			return response.GetItems(), nil
		})
		if err != nil {
			return
		}
		subnet = publicv1.SubnetLocalReference_builder{Id: found.GetId()}.Build()
	}
	if len(flag.SecurityGroups) > 0 {
		securityGroups := publicv1.NewSecurityGroupsClient(conn)
		for _, ref := range flag.SecurityGroups {
			var found *publicv1.SecurityGroup
			found, err = lookup.Find(ref, "security group",
				func(filter string, limit int32) ([]*publicv1.SecurityGroup, error) {
					response, err := securityGroups.List(ctx, publicv1.SecurityGroupsListRequest_builder{
						Filter: &filter,
						Limit:  &limit,
					}.Build())
					if err != nil {
						return nil, fmt.Errorf("failed to resolve security group %q: %w", ref, err)
					}
					return response.GetItems(), nil
				})
			if err != nil {
				return
			}
			groups = append(groups, publicv1.SecurityGroupLocalReference_builder{Id: found.GetId()}.Build())
		}
	}
	return
}
