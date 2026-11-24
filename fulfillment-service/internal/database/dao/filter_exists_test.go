/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dao

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	testsv1 "github.com/osac-project/osac/proto/gen/osac/tests/v1"
)

var _ = Describe("Repeated message exists translation", func() {
	var translator *FilterTranslator

	BeforeEach(func() {
		var err error
		translator, err = NewFilterTranslator().SetLogger(logger).
			SetDescriptor((*testsv1.Object)(nil).ProtoReflect().Descriptor()).Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("translates a nested field predicate", func(ctx context.Context) {
		sql, err := translator.Translate(ctx, `this.my_repeated.exists(item, item.my_msg.my_string == 'target')`)
		Expect(err).ToNot(HaveOccurred())
		Expect(sql).To(Equal(`exists (select 1 from jsonb_array_elements(data->'my_repeated') as cel_element_0(value) where cel_element_0.value->'my_msg'->>'my_string' = 'target')`))
	})

	DescribeTable("rejects unsupported comprehensions and ranges", func(ctx context.Context, filter string) {
		_, err := translator.Translate(ctx, filter)
		Expect(err).To(HaveOccurred())
	},
		Entry("all", `this.my_repeated.all(x, x.my_string == 'target')`),
		Entry("exists_one", `this.my_repeated.exists_one(x, x.my_string == 'target')`),
		Entry("map", `this.my_repeated.map(x, x.my_string).size() == 1`),
		Entry("filter", `this.my_repeated.filter(x, x.my_bool).size() == 1`),
		Entry("scalar array", `this.my_string_list.exists(x, x == 'target')`),
		Entry("map range", `this.my_map.exists(x, x == 'target')`),
		Entry("unknown field", `this.my_repeated.exists(x, x.missing == 'target')`),
	)

	DescribeTable("uses the public RoleBinding descriptor", func(ctx context.Context, filter string) {
		translator, err := NewFilterTranslator().SetLogger(logger).
			SetDescriptor((*publicv1.RoleBinding)(nil).ProtoReflect().Descriptor()).Build()
		Expect(err).ToNot(HaveOccurred())
		sql, err := translator.Translate(ctx, filter)
		Expect(err).ToNot(HaveOccurred())
		Expect(sql).To(ContainSubstring("jsonb_array_elements"))
	},
		Entry("users", `this.spec.users.exists(u, u.id == 'user-id')`),
		Entry("service accounts", `this.spec.service_accounts.exists(sa, sa.id == 'account-id')`),
	)

	It("does not expose private fields or reserved metadata through a predicate", func(ctx context.Context) {
		translator, err := NewFilterTranslator().SetLogger(logger).
			SetDescriptor((*publicv1.RoleBinding)(nil).ProtoReflect().Descriptor()).Build()
		Expect(err).ToNot(HaveOccurred())
		for _, filter := range []string{
			`this.spec.users.exists(u, this.status.users.size() > 0)`,
			`this.spec.users.exists(u, this.metadata.labels['admin.osac.openshift.io/managed'] == 'true')`,
		} {
			_, err := translator.Translate(ctx, filter)
			Expect(err).To(HaveOccurred())
		}
	})

	It("keeps concurrent and failed translations independent", func(ctx context.Context) {
		filter := `this.my_repeated.exists(x, x.my_repeated.exists(x, x.my_string == 'target'))`
		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				defer GinkgoRecover()
				for range 5 {
					_, err := translator.Translate(ctx, `this.my_repeated.exists(x, x.my_bytes == b'invalid')`)
					Expect(err).To(HaveOccurred())
					sql, err := translator.Translate(ctx, filter)
					Expect(err).ToNot(HaveOccurred())
					Expect(sql).To(ContainSubstring(`jsonb_array_elements(cel_element_0.value->'my_repeated') as cel_element_1(value)`))
					_, err = translator.Translate(ctx, `this.my_string == 'unchanged'`)
					Expect(err).ToNot(HaveOccurred())
				}
			})
		}
		wg.Wait()
	})
})
