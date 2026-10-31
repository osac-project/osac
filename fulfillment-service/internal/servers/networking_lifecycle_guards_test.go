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
	"bytes"
	"context"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Paged networking lifecycle guards", func() {
	It("keeps contextual logging and the public Internal message for DAO failures", func() {
		var output bytes.Buffer
		log := slog.New(slog.NewTextHandler(&output, nil))
		// No transaction in this context makes the real DAO return a list error.
		err := rejectDeleteIfReferenced(context.Background(), log, fixtureDAO[*privatev1.SecurityGroup](),
			"SecurityGroup", "VirtualNetwork", "vn", nil, func(*privatev1.SecurityGroup) bool {
				Fail("visitor called after a failed List")
				return false
			})
		Expect(err).To(MatchError("rpc error: code = Internal desc = failed to check VirtualNetwork references"))
		Expect(output.String()).To(And(
			ContainSubstring("Failed to check networking dependents"),
			ContainSubstring("parent_kind=VirtualNetwork"),
			ContainSubstring("parent_id=vn"),
			ContainSubstring("dependent_kind=SecurityGroup"),
		))
	})

	It("retains the dependent count and first named resource in rejection errors", func() {
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		for _, name := range []string{"alpha", "beta", "gamma"} {
			seedSecurityGroup(vn, name, privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		}
		err := rejectDeleteIfReferenced(ctx, logger, fixtureDAO[*privatev1.SecurityGroup](),
			"SecurityGroup", "VirtualNetwork", vn.GetId(), vn.GetMetadata(), func(sg *privatev1.SecurityGroup) bool {
				return refKey(sg.GetSpec().GetVirtualNetwork()) == vn.GetId()
			})
		Expect(err).To(MatchError(And(
			ContainSubstring("rpc error: code = FailedPrecondition"),
			ContainSubstring("3 active SecurityGroup resource(s) still reference it"),
			ContainSubstring("including 'alpha'"),
		)))
	})
})
