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
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Public metadata map translation", func() {
	var publicTranslator, privateTranslator *FilterTranslator

	BeforeEach(func() {
		var err error
		publicTranslator, err = NewFilterTranslator().
			SetLogger(logger).
			SetDescriptor((*publicv1.Cluster)(nil).ProtoReflect().Descriptor()).
			Build()
		Expect(err).ToNot(HaveOccurred())

		privateTranslator, err = NewFilterTranslator().
			SetLogger(logger).
			SetDescriptor((*privatev1.Cluster)(nil).ProtoReflect().Descriptor()).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("uses the public map projection only for whole-map comparisons", func(ctx context.Context) {
		translated, err := publicTranslator.Translate(ctx, `this.metadata.labels == this.metadata.annotations`)
		Expect(err).ToNot(HaveOccurred())
		Expect(translated).To(ContainSubstring(`jsonb_each(labels)`))
		Expect(translated).To(ContainSubstring(`jsonb_each(annotations)`))
		Expect(translated).To(ContainSubstring(`position('/' in public_metadata.key) = 0`))
		Expect(translated).To(ContainSubstring(`not like '%.admin.osac.openshift.io'`))

		translated, err = publicTranslator.Translate(ctx, `'ordinary' in this.metadata.labels`)
		Expect(err).ToNot(HaveOccurred())
		Expect(translated).To(Equal(`labels ? 'ordinary'`))

		translated, err = publicTranslator.Translate(ctx, `this.metadata.labels['ordinary'] == 'visible'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(translated).To(Equal(`labels @> '{"ordinary":"visible"}'`))

		translated, err = publicTranslator.Translate(ctx, `this.metadata.labels['probe'] in this.metadata.labels`)
		Expect(err).ToNot(HaveOccurred())
		Expect(translated).To(ContainSubstring(`labels->>'probe'`))
		Expect(translated).To(ContainSubstring(`jsonb_each(labels)`))
		Expect(translated).ToNot(ContainSubstring(`labels ? labels->>'probe'`))

		translated, err = publicTranslator.Translate(ctx, `this.metadata.annotations['ordinary'] == 'value'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(translated).To(Equal(`annotations @> '{"ordinary":"value"}'`))

		translated, err = privateTranslator.Translate(ctx, `this.metadata.labels == this.metadata.annotations`)
		Expect(err).ToNot(HaveOccurred())
		Expect(translated).To(Equal(`labels = annotations`))
	})

	It("rejects unsupported public map operands", func() {
		_, err := publicMetadataMapSQL(publicMetadataMapOperand(255))
		Expect(err).To(MatchError("unsupported public metadata map operand 255"))
	})
})
