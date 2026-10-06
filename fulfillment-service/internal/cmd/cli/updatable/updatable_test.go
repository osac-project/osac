/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package updatable

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
)

var _ = Describe("Ensure", func() {
	It("accepts update-capable objects", func() {
		ctrl := gomock.NewController(GinkgoT())
		helper := reflection.NewMockObjectHelper(ctrl)
		helper.EXPECT().IsUpdatable().Return(true)

		Expect(Ensure(helper)).To(Succeed())
	})

	It("rejects immutable objects", func() {
		ctrl := gomock.NewController(GinkgoT())
		helper := reflection.NewMockObjectHelper(ctrl)
		helper.EXPECT().IsUpdatable().Return(false)
		helper.EXPECT().FullName().Return(protoreflect.FullName("osac.public.v1.Subnet"))

		Expect(Ensure(helper)).To(MatchError(`object type "osac.public.v1.Subnet" is immutable; updates are not supported`))
	})
})
