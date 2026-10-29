/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dao

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Filter translator metadata keys", func() {
	var publicTranslator, privateTranslator *FilterTranslator

	BeforeEach(func() {
		var err error
		publicTranslator, err = NewFilterTranslator().
			SetLogger(logger).
			SetDescriptor((*publicv1.ComputeInstance)(nil).ProtoReflect().Descriptor()).
			Build()
		Expect(err).ToNot(HaveOccurred())

		privateTranslator, err = NewFilterTranslator().
			SetLogger(logger).
			SetDescriptor((*privatev1.ComputeInstance)(nil).ProtoReflect().Descriptor()).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	DescribeTable("rejects reserved metadata keys in public filters",
		func(ctx context.Context, filter string) {
			_, err := publicTranslator.Translate(ctx, filter)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("owner-reference"))
			Expect(err.Error()).NotTo(ContainSubstring("provider-id"))

			_, err = privateTranslator.Translate(ctx, filter)
			Expect(err).ToNot(HaveOccurred())
		},
		Entry("reserved label index", `this.metadata.labels['admin.osac.openshift.io/owner-reference'] == 'stored-value'`),
		Entry("reserved provider label index", `this.metadata.labels['operator.admin.osac.openshift.io/provider-id'] != ''`),
		Entry("reserved label membership", `'admin.osac.openshift.io/owner-reference' in this.metadata.labels`),
	)

	It("allows ordinary public metadata keys", func(ctx context.Context) {
		_, err := publicTranslator.Translate(ctx, `this.metadata.labels['example.com/app'] == 'web'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
