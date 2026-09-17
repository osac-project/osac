/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed under the
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package annotate

import (
	"testing"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/updatable"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
)

func TestAnnotate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Annotate command")
}

var _ = Describe("Annotate command", func() {
	It("does not offer immutable networking resources for annotation completion", func() {
		completed, directive := completeObjectTypes(nil, nil, "")
		Expect(directive).To(Equal(cobra.ShellCompDirectiveNoFileComp))
		Expect(completed).ToNot(ContainElements(
			"virtualnetwork", "virtualnetworks",
			"subnet", "subnets",
			"securitygroup", "securitygroups",
			"externalip", "externalips",
			"externalipattachment", "externalipattachments",
			"natgateway", "natgateways",
		))
	})

	It("rejects direct annotation of immutable objects before changing metadata", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		mockHelper := reflection.NewMockObjectHelper(ctrl)
		mockHelper.EXPECT().IsUpdatable().Return(false)
		mockHelper.EXPECT().FullName().Return(protoreflect.FullName("osac.public.v1.Subnet"))

		err := updatable.Ensure(mockHelper)
		Expect(err).To(MatchError(`object type "osac.public.v1.Subnet" is immutable; updates are not supported`))
	})

	It("allows annotation of update-capable objects", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		mockHelper := reflection.NewMockObjectHelper(ctrl)
		mockHelper.EXPECT().IsUpdatable().Return(true)

		Expect(updatable.Ensure(mockHelper)).ToNot(HaveOccurred())
	})
})
