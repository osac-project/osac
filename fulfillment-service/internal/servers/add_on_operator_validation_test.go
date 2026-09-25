/*
Copyright (c) 2026 Red Hat, Inc.

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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Add-on operator graph", func() {
	It("expands dependencies first and deduplicates shared dependencies", func() {
		shared := newGraphTestOperator("shared")
		left := newGraphTestOperator("left")
		left.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
			privatev1.AddOnOperatorLocalReference_builder{Id: shared.GetId()}.Build(),
		})
		right := newGraphTestOperator("right")
		right.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
			privatev1.AddOnOperatorLocalReference_builder{Id: shared.GetId()}.Build(),
		})
		root := newGraphTestOperator("root")
		root.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
			privatev1.AddOnOperatorLocalReference_builder{Id: left.GetId()}.Build(),
			privatev1.AddOnOperatorLocalReference_builder{Id: right.GetId()}.Build(),
		})

		selected, fields, order, err := newAddOnOperatorGraph(graphTestResolver(map[string]*privatev1.AddOnOperator{
			shared.GetId(): shared,
			left.GetId():   left,
			right.GetId():  right,
			root.GetId():   root,
		})).resolveAndExpand(context.Background(), &privatev1.Metadata{}, []*privatev1.AddOnOperatorReference{
			privatev1.AddOnOperatorReference_builder{Id: root.GetId()}.Build(),
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(selected).To(HaveLen(4))
		Expect(order).To(Equal([]string{"shared", "left", "right", "root"}))
		Expect(fields[root.GetId()]).To(Equal("spec.add_on_operators[0]"))
	})

	It("rejects dependency cycles", func() {
		first := newGraphTestOperator("first")
		second := newGraphTestOperator("second")
		first.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
			privatev1.AddOnOperatorLocalReference_builder{Id: second.GetId()}.Build(),
		})
		second.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
			privatev1.AddOnOperatorLocalReference_builder{Id: first.GetId()}.Build(),
		})

		_, _, _, err := newAddOnOperatorGraph(graphTestResolver(map[string]*privatev1.AddOnOperator{
			first.GetId():  first,
			second.GetId(): second,
		})).resolveAndExpand(context.Background(), &privatev1.Metadata{}, []*privatev1.AddOnOperatorReference{
			privatev1.AddOnOperatorReference_builder{Id: first.GetId()}.Build(),
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("dependency cycle detected: first -> second -> first"))
	})

	It("rejects graphs with too many operators", func() {
		root := newGraphTestOperator("root")
		operators := map[string]*privatev1.AddOnOperator{root.GetId(): root}
		dependencies := make([]*privatev1.AddOnOperatorLocalReference, maxClusterAddOnOperators)
		for index := range dependencies {
			id := fmt.Sprintf("dependency-%d", index)
			operator := newGraphTestOperator(id)
			operators[id] = operator
			dependencies[index] = privatev1.AddOnOperatorLocalReference_builder{Id: id}.Build()
		}
		root.SetDependencies(dependencies)

		_, _, _, err := newAddOnOperatorGraph(graphTestResolver(operators)).resolveAndExpand(
			context.Background(), &privatev1.Metadata{}, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: root.GetId()}.Build(),
			})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("resolved add-on operator set exceeds maximum of 32 operators"))
	})

	It("rejects graphs with too many relationships", func() {
		shared := newGraphTestOperator("shared")
		root := newGraphTestOperator("root")
		operators := map[string]*privatev1.AddOnOperator{
			shared.GetId(): shared,
			root.GetId():   root,
		}
		dependencies := make([]*privatev1.AddOnOperatorLocalReference, maxAddOnOperatorRelationshipEdges+1)
		for index := range dependencies {
			dependencies[index] = privatev1.AddOnOperatorLocalReference_builder{Id: shared.GetId()}.Build()
		}
		root.SetDependencies(dependencies)

		_, _, _, err := newAddOnOperatorGraph(graphTestResolver(operators)).resolveAndExpand(
			context.Background(), &privatev1.Metadata{}, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: root.GetId()}.Build(),
			})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("dependency graph exceeds 1024 relationships"))
	})
})

func newGraphTestOperator(id string) *privatev1.AddOnOperator {
	return privatev1.AddOnOperator_builder{
		Id: id,
		Metadata: privatev1.Metadata_builder{
			Name: id,
		}.Build(),
	}.Build()
}

func graphTestResolver(operators map[string]*privatev1.AddOnOperator) addOnOperatorReferenceResolverFunc {
	return func(_ context.Context, ref resourceReference, _ *privatev1.Metadata, _ string) (*privatev1.AddOnOperator, error) {
		operator, ok := operators[ref.GetId()]
		if !ok {
			return nil, fmt.Errorf("operator %q not found", ref.GetId())
		}
		return operator, nil
	}
}
