/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Batched attachment SecurityGroup resolution", func() {
	group := func(id, name, vn string) *privatev1.SecurityGroup {
		return privatev1.SecurityGroup_builder{
			Id:       id,
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant}.Build(),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vn}.Build(),
			}.Build(),
		}.Build()
	}
	request := func(keys ...string) networkAttachmentRequest {
		r := networkAttachmentRequest{tenant: testTenant, source: "spec.network_attachments[0]"}
		for _, key := range keys {
			r.securityGroups = append(r.securityGroups, privatev1.SecurityGroupLocalReference_builder{Id: key}.Build())
		}
		return r
	}

	It("looks up all keys once and keeps request order with exact IDs taking precedence", func() {
		first, second := group("sg-a", "alpha", "vn"), group("sg-b", "beta", "vn")
		nameCollision := group("sg-c", "sg-a", "vn")
		calls := 0
		lookup := func(_ context.Context, tenant string, ids, names []string) ([]*privatev1.SecurityGroup, error) {
			calls++
			Expect(tenant).To(Equal(testTenant))
			Expect(ids).To(Equal([]string{"beta", "sg-a"}))
			Expect(names).To(Equal(ids))
			return []*privatev1.SecurityGroup{nameCollision, first, second}, nil
		}
		resolved, err := resolveRequestedSecurityGroupsWithLookup(ctx, request("beta", "sg-a"), "vn", "subnet", lookup)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal([]*privatev1.SecurityGroup{second, first}))
		Expect(calls).To(Equal(1))
	})

	DescribeTable("preserves reference validation errors in request order",
		func(keys []string, rows []*privatev1.SecurityGroup, message string) {
			calls := 0
			lookup := func(context.Context, string, []string, []string) ([]*privatev1.SecurityGroup, error) {
				calls++
				return rows, nil
			}
			_, err := resolveRequestedSecurityGroupsWithLookup(ctx, request(keys...), "vn", "subnet", lookup)
			Expect(err).To(MatchError(message))
			Expect(calls).To(Equal(1))
		},
		Entry("ambiguous name", []string{"same"}, []*privatev1.SecurityGroup{group("a", "same", "vn"), group("b", "same", "vn")},
			"rpc error: code = InvalidArgument desc = name 'same' matches 2 records; use the ID"),
		Entry("missing reference before a later invalid reference", []string{"absent", "other"}, []*privatev1.SecurityGroup{group("other", "other", "other-vn")},
			"rpc error: code = InvalidArgument desc = spec.network_attachments[0].security_groups[0]: SecurityGroup 'absent' does not exist"),
		Entry("duplicate ID and name", []string{"a", "alpha"}, []*privatev1.SecurityGroup{group("a", "alpha", "vn")},
			"rpc error: code = InvalidArgument desc = spec.network_attachments[0].security_groups[1]: SecurityGroup 'a' is listed twice"),
		Entry("VirtualNetwork mismatch", []string{"a"}, []*privatev1.SecurityGroup{group("a", "alpha", "other-vn")},
			"rpc error: code = InvalidArgument desc = spec.network_attachments[0].security_groups[0]: SecurityGroup 'a' belongs to another VirtualNetwork than Subnet 'subnet'"),
	)

	It("deduplicates query keys while preserving duplicate-reference rejection", func() {
		lookup := func(_ context.Context, _ string, ids, names []string) ([]*privatev1.SecurityGroup, error) {
			Expect(ids).To(Equal([]string{"a"}))
			Expect(names).To(Equal(ids))
			return []*privatev1.SecurityGroup{group("a", "alpha", "vn")}, nil
		}
		_, err := resolveRequestedSecurityGroupsWithLookup(ctx, request("a", "a"), "vn", "subnet", lookup)
		Expect(err).To(MatchError(ContainSubstring("is listed twice")))
	})

	It("maps a lookup failure to the existing Internal error", func() {
		lookup := func(context.Context, string, []string, []string) ([]*privatev1.SecurityGroup, error) {
			return nil, errors.New("database unavailable")
		}
		_, err := resolveRequestedSecurityGroupsWithLookup(ctx, request("a"), "vn", "subnet", lookup)
		Expect(err).To(MatchError("rpc error: code = Internal desc = failed to resolve the network references"))
	})

	It("resolves mixed IDs and names through the real scoped DAO", func() {
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		first := seedSecurityGroup(vn, "alpha", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		second := seedSecurityGroup(vn, "beta", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		r := request(second.GetMetadata().GetName(), first.GetId())
		r.securityGroupsDao = fixtureDAO[*privatev1.SecurityGroup]()
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(Succeed())
		Expect(result.securityGroupIDs()).To(Equal([]string{second.GetId(), first.GetId()}))
		references := result.securityGroupReferences()
		Expect(references).To(HaveLen(2))
		Expect(references[0].GetId()).To(Equal(second.GetId()))
		Expect(references[0].GetName()).To(Equal("beta"))
		Expect(references[1].GetId()).To(Equal(first.GetId()))
		Expect(references[1].GetName()).To(Equal("alpha"))
	})

	It("cannot resolve a group in another tenant even through a total-visibility DAO", func() {
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		sg := seedSecurityGroup(vn, "alpha", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		r := request(sg.GetId())
		r.tenant = "another-tenant"
		r.securityGroupsDao = fixtureDAO[*privatev1.SecurityGroup]()
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(MatchError(ContainSubstring("does not exist")))
	})

	It("cannot resolve a group in another project", func() {
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		sg := seedSecurityGroup(vn, "alpha", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		r := request(sg.GetId())
		r.project = "another-project"
		r.securityGroupsDao = fixtureDAO[*privatev1.SecurityGroup]()
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(MatchError(ContainSubstring("does not exist")))
	})

	It("fails closed when the DAO cap truncates matching candidates", func() {
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		first := seedSecurityGroup(vn, "alpha", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		second := seedSecurityGroup(vn, "beta", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		limited, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().SetLogger(logger).SetTenancyLogic(tenancy).
			SetDefaultLimit(1).SetMaxLimit(1).Build()
		Expect(err).NotTo(HaveOccurred())
		r := request(first.GetId(), second.GetId())
		r.securityGroupsDao = limited
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(MatchError(
			"rpc error: code = Internal desc = failed to resolve the network references: matching SecurityGroups exceed the DAO query limit"))
		Expect(result.securityGroups).To(BeEmpty())
	})

	It("locks resolved groups together and preserves attachment order", func() {
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		first := seedSecurityGroup(vn, "alpha", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		second := seedSecurityGroup(vn, "beta", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		result := &networkAttachmentResolution{securityGroups: []*privatev1.SecurityGroup{second, first}}
		guards := &networkingGuards{securityGroups: fixtureDAO[*privatev1.SecurityGroup]()}

		Expect(lockAttachmentSecurityGroups(ctx, guards, result)).To(Succeed())
		Expect(result.securityGroupIDs()).To(Equal([]string{second.GetId(), first.GetId()}))
		Expect(result.securityGroups[0].GetMetadata().GetName()).To(Equal("beta"))
		Expect(result.securityGroups[1].GetMetadata().GetName()).To(Equal("alpha"))
	})
})

var _ = Describe("Network attachment Subnet resolution", func() {
	It("rejects a reference whose ID and name identify different Subnets", func() {
		virtualNetwork := seedVirtualNetwork(testTenant, "10.20.0.0/16",
			privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		subnet := seedSubnet(virtualNetwork, "subnet-a", "10.20.1.0/24",
			privatev1.SubnetState_SUBNET_STATE_READY, nil)
		request := networkAttachmentRequest{
			tenant:     testTenant,
			subnetsDao: fixtureDAO[*privatev1.Subnet](),
			subnet: privatev1.SubnetLocalReference_builder{
				Id: subnet.GetId(), Name: "different-subnet",
			}.Build(),
			source: "spec.network_attachments[0]",
		}
		guards := &networkingGuards{
			subnets:         fixtureDAO[*privatev1.Subnet](),
			virtualNetworks: fixtureDAO[*privatev1.VirtualNetwork](),
		}

		err := resolveAttachmentSubnet(ctx, guards, request, &networkAttachmentResolution{})
		Expect(err).To(MatchError(ContainSubstring("id and name do not refer to the same resource")))
	})
})
