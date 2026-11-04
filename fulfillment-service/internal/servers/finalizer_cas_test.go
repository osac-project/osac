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
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// These specs reproduce OSAC-7221: a writer that holds a stale snapshot of an object must not be able to overwrite
// the finalizers that another writer stored after the snapshot was taken. All the concurrency is logical: the
// specs run in the suite transaction and order the writes explicitly.
var _ = Describe("finalizer CAS", func() {
	finalizersMask := func() *fieldmaskpb.FieldMask {
		return finalizerMaskOf("metadata.finalizers")
	}

	// The guard proves coverage of the RPCs at the level of the bare GenericServer, not of the production typed
	// wrappers: a wrapper that rebuilds the request from the stored object without restoring the version of the
	// client would defeat the compare-and-swap without failing these specs. DiskImage and InstanceType rebuild the
	// request from the stored object: their masked paths ignore metadata.finalizers, and their nil-mask path merges
	// the request over the stored object, which unions the request finalizers with the stored ones, and the object
	// that the public servers forward already carries the stored finalizers. BareMetalInstanceType applies the masks
	// itself and has its own specs with the typed servers.
	It("covers every finalizer-bearing private resource", func() {
		covered := map[string]bool{}
		for _, c := range finalizerCases() {
			covered[c.service] = true
		}
		for service := range finalizerCaseSkips {
			covered[service] = true
		}
		expected := map[string]bool{}
		for _, service := range finalizerBearingServices() {
			expected[service] = true
		}

		var missing, stale []string
		for service := range expected {
			if !covered[service] {
				missing = append(missing, service)
			}
		}
		for service := range covered {
			if !expected[service] {
				stale = append(stale, service)
			}
		}
		sort.Strings(missing)
		sort.Strings(stale)
		Expect(len(missing)+len(stale)).To(BeZero(),
			"services missing from the finalizer cases and from finalizerCaseSkips: %v\n"+
				"services in the finalizer cases or skips that have no finalizer-bearing Update: %v",
			missing, stale)
	})

	DescribeTable("rejects a stale private finalizer write",
		func(c finalizerCase) {
			// The reconciler read the object while a hold was present:
			t := c.target([]string{finalizerController, finalizerHold})
			stale := t.get(ctx)

			// Another writer released the hold after that read:
			t.release(ctx, finalizerController)

			// The reconciler writes back its stale view without the lock:
			err := t.update(ctx, withFinalizers(stale, finalizerHold), finalizersMask(), false)

			t.expectFinalizerConflict(ctx, err, finalizerController)
		},
		finalizerEntries(),
	)

	DescribeTable("keeps finalizers on a public nil-mask update",
		func(c finalizerCase) {
			// Public objects have no finalizers, so the object that a public server forwards has none:
			t := c.target([]string{finalizerController, finalizerHold})
			object := withFinalizers(t.get(ctx))

			err := t.update(withPublicAPIView(ctx), object, nil, false)
			Expect(err).ToNot(HaveOccurred())

			Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold),
				"a public update must not clear the finalizers")
		},
		finalizerEntries(),
	)

	// A public request has no finalizers and no way to prove a version for them, so the server restores the stored
	// ones before it checks anything else. A stale public request therefore succeeds and never gets the Aborted
	// error that private finalizer changes get.
	DescribeTable("keeps finalizers on a stale public nil-mask update",
		func(c finalizerCase) {
			t := c.target([]string{finalizerController, finalizerHold})
			stale := t.get(ctx)
			t.bump(ctx)

			err := t.update(withPublicAPIView(ctx), withFinalizers(stale), nil, false)
			Expect(err).ToNot(HaveOccurred(), "a public request must not be rejected for its finalizers")

			Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold),
				"a stale public update must not clear the finalizers")
		},
		finalizerEntries(),
	)

	DescribeTable("keeps a deleting object on a public nil-mask update",
		func(c finalizerCase) {
			t := c.target([]string{finalizerController, finalizerHold})

			// The deletion only marks the object because it has finalizers:
			Expect(t.ops.del(ctx, t.id)).To(Succeed())
			object := withFinalizers(t.get(ctx))

			err := t.update(withPublicAPIView(ctx), object, nil, false)
			Expect(err).ToNot(HaveOccurred())

			stored, err := t.ops.get(ctx, t.id)
			Expect(err).ToNot(HaveOccurred(), "a public update must not archive an object with finalizers")
			Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
		},
		finalizerEntries(),
	)

	Describe("controls", func() {
		DescribeTable("accepts a finalizer change made from the current version",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController})

				object := withFinalizers(t.get(ctx), finalizerController, finalizerHold)
				Expect(t.update(ctx, object, finalizersMask(), false)).To(Succeed())

				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
			},
			finalizerEntries(),
		)

		DescribeTable("accepts a stale update that doesn't change the finalizers",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController, finalizerHold})
				stale := t.get(ctx)
				t.bump(ctx)

				object := withMetadata(stale, func(metadata *privatev1.Metadata) {
					metadata.SetLabels(map[string]string{"x": "y"})
				})
				Expect(t.update(ctx, object, finalizerMaskOf("metadata.labels"), false)).To(Succeed())

				stored := t.get(ctx)
				Expect(finalizerMetadataOf(stored).GetLabels()).To(HaveKeyWithValue("x", "y"))
				Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
			},
			finalizerEntries(),
		)

		DescribeTable("treats the same finalizers in another order, or repeated, as unchanged",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController, finalizerHold})
				stale := t.get(ctx)
				t.bump(ctx)

				object := withFinalizers(stale, finalizerHold, finalizerController, finalizerHold)
				snapshot := proto.Clone(object)
				stored := t.get(ctx)
				storedSnapshot := proto.Clone(stored)
				Expect(t.update(ctx, object, finalizersMask(), false)).To(Succeed())

				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
				Expect(proto.Equal(object, snapshot)).To(BeTrue(), "the request object must not be modified")
				Expect(proto.Equal(stored, storedSnapshot)).To(BeTrue(), "the stored snapshot must not be modified")
			},
			finalizerEntries(),
		)

		DescribeTable("treats a missing finalizer list and an empty one as the same",
			func(c finalizerCase) {
				t := c.target(nil)
				stale := t.get(ctx)
				t.bump(ctx)

				object := withFinalizers(stale)
				Expect(t.update(ctx, object, finalizersMask(), false)).To(Succeed())

				Expect(t.finalizers(ctx)).To(BeEmpty())
			},
			finalizerEntries(),
		)

		DescribeTable("keeps a foreign finalizer when another one is removed from the current version",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController, finalizerHold, finalizerForeign})

				object := withFinalizers(t.get(ctx), finalizerController, finalizerForeign)
				Expect(t.update(ctx, object, finalizersMask(), false)).To(Succeed())

				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerForeign))
			},
			finalizerEntries(),
		)

		DescribeTable("archives a deleting object when its last finalizer is removed from the current version",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController})
				Expect(t.ops.del(ctx, t.id)).To(Succeed())

				object := withFinalizers(t.get(ctx))
				Expect(t.update(ctx, object, finalizersMask(), false)).To(Succeed())

				_, err := t.ops.get(ctx, t.id)
				Expect(status.Code(err)).To(Equal(codes.NotFound))
			},
			finalizerEntries(),
		)

		DescribeTable("still rejects a stale update that asks for the lock",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController, finalizerHold})
				stale := t.get(ctx)
				t.bump(ctx)

				object := withMetadata(stale, func(metadata *privatev1.Metadata) {
					metadata.SetLabels(map[string]string{"x": "y"})
				})
				err := t.update(ctx, object, finalizerMaskOf("metadata.labels"), true)

				Expect(status.Code(err)).To(Equal(codes.Aborted))
			},
			finalizerEntries(),
		)

		DescribeTable("accepts an update without metadata that doesn't change the finalizers",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController, finalizerHold})
				t.bump(ctx)

				// The mask selects the labels, so the finalizers of the stored object are untouched:
				object := withoutMetadata(t.get(ctx))
				Expect(t.update(ctx, object, finalizerMaskOf("metadata.labels"), false)).To(Succeed())

				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
			},
			finalizerEntries(),
		)

		DescribeTable("leaves the finalizers alone when the update mask is empty",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController, finalizerHold})
				stale := t.get(ctx)
				t.release(ctx, finalizerController)

				// An empty mask is not the nil mask: nothing is applied.
				object := withFinalizers(stale, finalizerHold)
				Expect(t.update(ctx, object, &fieldmaskpb.FieldMask{}, false)).To(Succeed())

				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController))
			},
			finalizerEntries(),
		)
	})

	Describe("mask edges", func() {
		// A stale writer that changes the finalizers must be rejected whichever way it selects them: a mask for the
		// whole metadata, no mask at all, or the finalizers themselves.
		DescribeTable("rejects a stale finalizer change",
			func(c finalizerCase, mask *fieldmaskpb.FieldMask) {
				t := c.target([]string{finalizerController, finalizerHold})
				stale := t.get(ctx)
				t.release(ctx, finalizerController)

				err := t.update(ctx, withFinalizers(stale, finalizerHold), mask, false)

				t.expectFinalizerConflict(ctx, err, finalizerController)
			},
			finalizerEntriesPerMask(),
		)

		// A private write without metadata can't prove which version it started from, so it is rejected whatever the
		// stored version is, and whether it selects the finalizers with a mask or replaces the whole object.
		DescribeTable("rejects a finalizer change that has no metadata",
			func(c finalizerCase, bumpFirst bool, mask *fieldmaskpb.FieldMask) {
				t := c.target([]string{finalizerController, finalizerHold})
				if bumpFirst {
					t.bump(ctx)
				}

				err := t.update(ctx, withoutMetadata(t.get(ctx)), mask, false)

				if mask == nil && finalizerRequiresMetadata[c.service] {
					// The validation of the object rejects a whole-object write without metadata, so nothing is
					// written. That is as safe as the conflict, whichever check runs first.
					Expect(status.Code(err)).To(BeElementOf(codes.InvalidArgument, codes.Aborted),
						"the write without metadata returned %v", err)
					Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
					return
				}
				t.expectFinalizerConflict(ctx, err, finalizerController, finalizerHold)
			},
			finalizerEntriesPerVersion(),
		)
	})
})

// finalizerEntriesPerMask returns an entry for each resource and each way of selecting the finalizers.
func finalizerEntriesPerMask() []TableEntry {
	masks := []struct {
		name string
		mask *fieldmaskpb.FieldMask
	}{
		{"metadata mask", finalizerMaskOf("metadata")},
		{"nil mask", nil},
		{"metadata.finalizers mask", finalizerMaskOf("metadata.finalizers")},
	}
	var entries []TableEntry
	for _, c := range finalizerCases() {
		for _, m := range masks {
			entries = append(entries, Entry(c.service+" with "+m.name, c, m.mask))
		}
	}
	return entries
}

// finalizerEntriesPerVersion returns an entry for each resource, for an object at its creation version and for an
// object that has been written since, and for a mask on the finalizers and no mask at all.
func finalizerEntriesPerVersion() []TableEntry {
	var entries []TableEntry
	for _, c := range finalizerCases() {
		entries = append(entries,
			Entry(c.service+" at version 0 with metadata.finalizers mask", c, false, finalizerMaskOf("metadata.finalizers")),
			Entry(c.service+" at a later version with metadata.finalizers mask", c, true,
				finalizerMaskOf("metadata.finalizers")),
			Entry(c.service+" at version 0 with nil mask", c, false, (*fieldmaskpb.FieldMask)(nil)),
			Entry(c.service+" at a later version with nil mask", c, true, (*fieldmaskpb.FieldMask)(nil)),
		)
	}
	return entries
}
