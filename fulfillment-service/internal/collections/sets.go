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

// Set represents a set of comparable objects that can be finite or infinite.
//
// This implementation can represent both finite and infinite sets, but only those that can be constructed using a
// finite set of inclusions or exclusions. For example:
//
//   - Finite sets: {1, 2, 3} - a set containing exactly these elements.
//   - Infinite sets by exclusion: U \ {1, 2} - the set of all elements except 1 and 2.
//   - The universal set: U - the set of all possible elements of type T.
//   - The empty set: {} - the set containing no elements.
//
// However, this representation cannot express infinite sets that require an infinite number of inclusions or
// exclusions. For example, the set of all even numbers cannot be represented this way, as it would require listing
// infinitely many elements.
//
// The universal set is a key feature of this implementation. It represents the set of all possible values of type T,
// and is represented internally as an infinite set with no excluded elements. This allows operations like complement,
// union, and intersection to work correctly with infinite sets.
//
// Sets are immutable once created. All operations return new sets rather than modifying existing ones.
//
// The zero value of Set is an empty set.
type Set[T comparable] struct {
	// infinite indicates if the set is infinite. When false the set is finite and items contains the included
	// elements. When true the set is infinite and items contains the excluded elements.
	infinite bool

	// items contains the elements of the set when infinite is false, or the elements excluded from the set when
	// infinite is true.
	items map[T]struct{}
}

// NewSet creates a new set with the given items.
func NewSet[T comparable](items ...T) Set[T] {
	set := make(map[T]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return Set[T]{
		items: set,
	}
}

// NewUniversalSet creates a set containing all possible elements.
func NewUniversalSet[T comparable]() Set[T] {
	return Set[T]{
		infinite: true,
		items:    nil,
	}
}

// NewEmptySet creates an empty set.
func NewEmptySet[T comparable]() Set[T] {
	return Set[T]{
		items: make(map[T]struct{}),
	}
}

// Empty returns true if the set is empty.
func (s Set[T]) Empty() bool {
	return !s.infinite && len(s.items) == 0
}

// Universal returns true if the set contains all possible elements.
func (s Set[T]) Universal() bool {
	return s.infinite && len(s.items) == 0
}

// Finite returns true if the set is finite, false if it is infinite.
//
// A set is finite if it is defined by inclusion, and infinite if it is defined by exclusion.
//
// The empty set is finite, and the universal set is infinite.
func (s Set[T]) Finite() bool {
	return !s.infinite
}

// Contains returns true if the set contains the given item.
func (s Set[T]) Contains(item T) bool {
	_, found := s.items[item]
	if !s.infinite {
		return found
	}
	return !found
}

// Inclusions returns the slice of items included in the set. This method panics if called on an infinite set. Use
// Finite to check before calling.
func (s Set[T]) Inclusions() []T {
	if s.infinite {
		panic("tried to get inclussions from an infinite set")
	}
	if s.items == nil {
		return nil
	}
	result := make([]T, 0, len(s.items))
	for item := range s.items {
		result = append(result, item)
	}
	return result
}

// Exclusions returns the slice of items excluded from the set. This method panics if called on a finite set. Use
// Finite to check before calling.
func (s Set[T]) Exclusions() []T {
	if !s.infinite {
		panic("tried to get exclusions from a finite set")
	}
	if s.items == nil {
		return nil
	}
	result := make([]T, 0, len(s.items))
	for item := range s.items {
		result = append(result, item)
	}
	return result
}

// Negate returns the complement of the set.
func (s Set[T]) Negate() Set[T] {
	return Set[T]{
		infinite: !s.infinite,
		items:    s.items,
	}
}

// Union returns the union of this set with another set.
func (s Set[T]) Union(other Set[T]) Set[T] {
	// Case 1: Both finite. Result is finite union of items.
	if !s.infinite && !other.infinite {
		return Set[T]{
			items: unionMaps(s.items, other.items),
		}
	}

	// Case 2: Both infinite. (U \ A) u (U \ B) = U \ (A n B). Result is infinite with intersection of excluded items.
	if s.infinite && other.infinite {
		return Set[T]{
			infinite: true,
			items:    intersectMaps(s.items, other.items),
		}
	}

	// Case 3: Mixed.
	// Let A be infinite (U \ Ai), B be finite (Bi).
	// (U \ Ai) u Bi = U \ (Ai \ Bi). Result is infinite with difference of excluded items (Ai - Bi).
	if s.infinite && !other.infinite {
		return Set[T]{
			infinite: true,
			items:    diffMaps(s.items, other.items),
		}
	}

	// A is finite (Ai), B is infinite (U \ Bi).
	// Ai u (U \ Bi) = U \ (Bi \ Ai). Result is infinite with difference of excluded items (Bi - Ai).
	return Set[T]{
		infinite: true,
		items:    diffMaps(other.items, s.items),
	}
}

// Intersection returns the intersection of this set with another set.
func (s Set[T]) Intersection(other Set[T]) Set[T] {
	// Case 1: Both finite. Result is finite intersection of items.
	if !s.infinite && !other.infinite {
		return Set[T]{
			items: intersectMaps(s.items, other.items),
		}
	}

	// Case 2: Both infinite. (U \ A) n (U \ B) = U \ (A u B). Result is infinite with union of excluded items.
	if s.infinite && other.infinite {
		return Set[T]{
			infinite: true,
			items:    unionMaps(s.items, other.items),
		}
	}

	// Case 3: Mixed.
	// A infinite (U \ Ai), B finite (Bi).
	// (U \ Ai) n Bi = Bi \ Ai. Result is finite with difference of items (Bi - Ai).
	if s.infinite && !other.infinite {
		return Set[T]{
			items: diffMaps(other.items, s.items),
		}
	}
	// A finite (Ai), B infinite (U \ Bi).
	// Ai n (U \ Bi) = Ai \ Bi. Result is finite with difference of items (Ai - Bi).
	return Set[T]{
		items: diffMaps(s.items, other.items),
	}
}

// Difference returns the difference of this set with another set (s - other).
func (s Set[T]) Difference(other Set[T]) Set[T] {
	return s.Intersection(other.Negate())
}

// Subset returns true if this set is a subset of the other set.
func (s Set[T]) Subset(other Set[T]) bool {
	// Case 1: Both finite.
	if !s.infinite && !other.infinite {
		// Check if all items in s are in other
		for item := range s.items {
			_, ok := other.items[item]
			if !ok {
				return false
			}
		}
		return true
	}

	// Case 2: This set infinite, other finite.
	// s (infinite) <= other (finite). False, an infinite set cannot be a subset of a finite set.
	if s.infinite && !other.infinite {
		return false
	}

	// Case 3: This set finite, other infinite.
	// s <= U \ other_excluded <=> s intersect other_excluded is empty.
	if !s.infinite && other.infinite {
		for item := range s.items {
			_, ok := other.items[item]
			if ok {
				return false
			}
		}
		return true
	}

	// Case 4: Both infinite.
	// U \ s_excluded <= U \ other_excluded <==> other_excluded <= s_excluded.
	if s.infinite && other.infinite {
		for item := range other.items {
			_, ok := s.items[item]
			if !ok {
				return false
			}
		}
		return true
	}
	return false
}

// Equal returns true if this set is equal to the other set.
func (s Set[T]) Equal(other Set[T]) bool {
	if s.infinite != other.infinite {
		return false
	}

	// Both have same finiteness. Check items match.
	if len(s.items) != len(other.items) {
		return false
	}

	for item := range s.items {
		_, ok := other.items[item]
		if !ok {
			return false
		}
	}
	return true
}

func unionMaps[T comparable](a, b map[T]struct{}) map[T]struct{} {
	result := make(map[T]struct{}, len(a)+len(b))
	for item := range a {
		result[item] = struct{}{}
	}
	for item := range b {
		result[item] = struct{}{}
	}
	return result
}

func intersectMaps[T comparable](a, b map[T]struct{}) map[T]struct{} {
	result := make(map[T]struct{})
	for item := range a {
		_, ok := b[item]
		if ok {
			result[item] = struct{}{}
		}
	}
	return result
}

func diffMaps[T comparable](a, b map[T]struct{}) map[T]struct{} {
	result := make(map[T]struct{})
	for item := range a {
		_, ok := b[item]
		if !ok {
			result[item] = struct{}{}
		}
	}
	return result
}
