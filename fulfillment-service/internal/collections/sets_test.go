/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package collections

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Sets", func() {
	Describe("Basic Operations", func() {
		It("Can create and query a standard set", func() {
			s := NewSet(1, 2, 3)
			Expect(s.Contains(1)).To(BeTrue())
			Expect(s.Contains(2)).To(BeTrue())
			Expect(s.Contains(3)).To(BeTrue())
			Expect(s.Contains(4)).To(BeFalse())
			Expect(s.Empty()).To(BeFalse())
			Expect(s.Universal()).To(BeFalse())
			Expect(s.Finite()).To(BeTrue())
		})

		It("Can create and query an empty set", func() {
			s := NewEmptySet[int]()
			Expect(s.Contains(1)).To(BeFalse())
			Expect(s.Empty()).To(BeTrue())
			Expect(s.Universal()).To(BeFalse())
			Expect(s.Finite()).To(BeTrue())

			s2 := NewSet[int]() // NewSet with no args
			Expect(s.Equal(s2)).To(BeTrue())
		})

		It("Has an empty set as zero value", func() {
			var s Set[int]
			Expect(s.Empty()).To(BeTrue())
		})

		It("Can create and query a universal set", func() {
			s := NewUniversalSet[int]()
			Expect(s.Contains(1)).To(BeTrue())
			Expect(s.Contains(100)).To(BeTrue())
			Expect(s.Empty()).To(BeFalse())
			Expect(s.Universal()).To(BeTrue())
			Expect(s.Finite()).To(BeFalse())
		})

		It("Deduplicates items", func() {
			s := NewSet(1, 1, 2)
			Expect(s.Equal(NewSet(1, 2))).To(BeTrue())
		})
	})

	Describe("Inclusions and Exclusions", func() {
		It("Returns inclusions for finite set", func() {
			s := NewSet(1, 2, 3)
			inclusions := s.Inclusions()
			Expect(inclusions).To(HaveLen(3))
			Expect(inclusions).To(ConsistOf(1, 2, 3))
		})

		It("Returns empty slice for empty set", func() {
			s := NewEmptySet[int]()
			inclusions := s.Inclusions()
			Expect(inclusions).To(BeEmpty())
		})

		It("Panics when calling Inclusions on infinite set", func() {
			s := NewSet(1, 2).Negate()
			Expect(func() {
				_ = s.Inclusions()
			}).To(PanicWith("tried to get inclussions from an infinite set"))
		})

		It("Panics when calling Inclusions on universal set", func() {
			s := NewUniversalSet[int]()
			Expect(func() {
				_ = s.Inclusions()
			}).To(PanicWith("tried to get inclussions from an infinite set"))
		})

		It("Returns exclusions for infinite set", func() {
			s := NewSet(1, 2, 3).Negate()
			exclusions := s.Exclusions()
			Expect(exclusions).To(HaveLen(3))
			Expect(exclusions).To(ConsistOf(1, 2, 3))
		})

		It("Returns empty slice for universal set", func() {
			s := NewUniversalSet[int]()
			exclusions := s.Exclusions()
			Expect(exclusions).To(BeEmpty())
		})

		It("Panics when calling Exclusions on finite set", func() {
			s := NewSet(1, 2)
			Expect(func() {
				_ = s.Exclusions()
			}).To(PanicWith("tried to get exclusions from a finite set"))
		})

		It("Panics when calling Exclusions on empty set", func() {
			s := NewEmptySet[int]()
			Expect(func() {
				_ = s.Exclusions()
			}).To(PanicWith("tried to get exclusions from a finite set"))
		})
	})

	Describe("Set Operations", func() {
		Describe("Union", func() {
			It("Union of two positive sets", func() {
				a := NewSet(1, 2)
				b := NewSet(2, 3)
				u := a.Union(b)
				Expect(u.Equal(NewSet(1, 2, 3))).To(BeTrue())
			})

			It("Union of positive and universal", func() {
				a := NewSet(1, 2)
				b := NewUniversalSet[int]()
				u := a.Union(b)
				Expect(u.Universal()).To(BeTrue())
			})

			It("Union of two negative sets", func() {
				// A = U \ {1, 2}, B = U \ {2, 3}
				// A u B = U \ ({1,2} n {2,3}) = U \ {2}
				a := NewSet(1, 2).Negate()
				b := NewSet(2, 3).Negate()
				u := a.Union(b)
				expected := NewSet(2).Negate()
				Expect(u.Equal(expected)).To(BeTrue())
			})

			It("Union of negative and positive", func() {
				// A = U \ {1, 2}, B = {2}
				// A u B = (U \ {1, 2}) u {2} = U \ ({1, 2} \ {2}) = U \ {1}
				a := NewSet(1, 2).Negate()
				b := NewSet(2)
				u := a.Union(b)
				expected := NewSet(1).Negate()
				Expect(u.Equal(expected)).To(BeTrue())
			})
		})

		Describe("Intersection", func() {
			It("Intersection of two positive sets", func() {
				a := NewSet(1, 2)
				b := NewSet(2, 3)
				i := a.Intersection(b)
				Expect(i.Equal(NewSet(2))).To(BeTrue())
			})

			It("Intersection of positive and empty", func() {
				a := NewSet(1, 2)
				b := NewEmptySet[int]()
				i := a.Intersection(b)
				Expect(i.Empty()).To(BeTrue())
			})

			It("Intersection of positive and universal", func() {
				a := NewSet(1, 2)
				b := NewUniversalSet[int]()
				i := a.Intersection(b)
				Expect(i.Equal(a)).To(BeTrue())
			})

			It("Intersection of two negative sets", func() {
				// A = U \ {1, 2}, B = U \ {2, 3}
				// A n B = U \ ({1, 2} u {2, 3}) = U \ {1, 2, 3}
				a := NewSet(1, 2).Negate()
				b := NewSet(2, 3).Negate()
				i := a.Intersection(b)
				expected := NewSet(1, 2, 3).Negate()
				Expect(i.Equal(expected)).To(BeTrue())
			})

			It("Intersection of negative and positive", func() {
				// A = U \ {1, 2}, B = {1, 2, 3}
				// A n B = {1, 2, 3} \ {1, 2} = {3}
				a := NewSet(1, 2).Negate()
				b := NewSet(1, 2, 3)
				i := a.Intersection(b)
				Expect(i.Equal(NewSet(3))).To(BeTrue())
			})
		})

		Describe("Difference", func() {
			It("Difference of two positive sets", func() {
				a := NewSet(1, 2, 3)
				b := NewSet(2, 3, 4)
				d := a.Difference(b)
				Expect(d.Equal(NewSet(1))).To(BeTrue())
			})

			It("Difference of positive and negative", func() {
				// A = {1, 2, 3}, B = U \ {3}
				// A - B = A n B' = {1, 2, 3} n {3} = {3}
				a := NewSet(1, 2, 3)
				b := NewSet(3).Negate()
				d := a.Difference(b)
				Expect(d.Equal(NewSet(3))).To(BeTrue())
			})
		})

		Describe("Negate", func() {
			It("Negates a positive set", func() {
				s := NewSet(1, 2)
				n := s.Negate()
				Expect(n.Contains(1)).To(BeFalse())
				Expect(n.Contains(3)).To(BeTrue())
				Expect(n.Finite()).To(BeFalse())
			})

			It("Double negation is identity", func() {
				s := NewSet(1, 2)
				Expect(s.Negate().Negate().Equal(s)).To(BeTrue())
			})

			It("Negate Universal is Empty", func() {
				negated := NewUniversalSet[int]().Negate()
				Expect(negated.Empty()).To(BeTrue())
				Expect(negated.Finite()).To(BeTrue())
			})

			It("Negate Empty is Universal", func() {
				negated := NewEmptySet[int]().Negate()
				Expect(negated.Universal()).To(BeTrue())
				Expect(negated.Finite()).To(BeFalse())
			})
		})

		Describe("Subset", func() {
			It("Positive subsets", func() {
				Expect(NewSet(1).Subset(NewSet(1, 2))).To(BeTrue())
				Expect(NewSet(1, 2).Subset(NewSet(1))).To(BeFalse())
				Expect(NewSet[int]().Subset(NewSet(1))).To(BeTrue()) // Empty is subset of any
			})

			It("Subset with Universal", func() {
				Expect(NewSet(1, 2).Subset(NewUniversalSet[int]())).To(BeTrue())
				Expect(NewUniversalSet[int]().Subset(NewSet(1, 2))).To(BeFalse())
				Expect(NewUniversalSet[int]().Subset(NewUniversalSet[int]())).To(BeTrue())
			})

			It("Negative subsets", func() {
				// U \ {1, 2} <= U \ {2} (Everything except 1,2 is inside Everything except 2? No. 1 is in neither. 3 is in both. Wait.)
				// U \ {1, 2} contains all except 1,2.
				// U \ {2} contains all except 2.
				// Is (All \ {1, 2}) subset of (All \ {2})?
				// Yes. Because (All \ {1, 2}) lacks 1 and 2. (All \ {2}) lacks 2.
				// So every element in LHS is in RHS. (Element x != 1, x != 2 implies x != 2).
				// Implementation: B'.Subset(A')? {2} <= {1, 2}? Yes.
				Expect(NewSet(1, 2).Negate().Subset(NewSet(2).Negate())).To(BeTrue())
				Expect(NewSet(2).Negate().Subset(NewSet(1, 2).Negate())).To(BeFalse())
			})
		})
	})
})
