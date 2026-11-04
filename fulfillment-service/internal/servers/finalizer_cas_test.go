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
	"context"
	"errors"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// These specs check that a writer that holds a stale snapshot of an object can't overwrite the finalizers that
// another writer stored after the snapshot was taken. The concurrency is logical: the specs run in the suite
// transaction and order the writes explicitly, except where they say otherwise.
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

	// A public request can also arrive without any metadata. The server then has no metadata to restore the stored
	// finalizers into, so it creates it. The merged object may still be rejected afterwards, for example by its
	// validation or by the database, but never for its finalizers, and a rejected update must not write anything.
	DescribeTable("keeps finalizers on a public nil-mask update that has no metadata",
		func(c finalizerCase, deleting bool) {
			t := c.target([]string{finalizerController, finalizerHold})
			if deleting {
				Expect(t.ops.del(ctx, t.id)).To(Succeed())
			}
			before := t.get(ctx)

			// A database rejection aborts the transaction, so the update runs in a savepoint that is rolled back
			// when it fails, as the transaction of a real request would be:
			var err error
			savepointErr := suiteTx.Savepoint(ctx, func(ctx context.Context) error {
				err = t.update(withPublicAPIView(ctx), withoutMetadata(before), nil, false)
				return err
			})
			if err == nil {
				Expect(savepointErr).ToNot(HaveOccurred())
			}

			Expect(status.Code(err)).ToNot(Equal(codes.Aborted),
				"a public request must not be rejected for its finalizers, but the update returned %v", err)
			stored, getErr := t.ops.get(ctx, t.id)
			Expect(getErr).ToNot(HaveOccurred(), "a public update must not archive an object with finalizers")
			Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold),
				"a public update must not clear the finalizers")
			if err != nil {
				Expect(proto.Equal(stored, before)).To(BeTrue(), "a rejected update must not write anything")
			}
		},
		finalizerEntriesPerLifecycle(),
	)

	// Candidate preparation callbacks run after the finalizer check, so they must not change the finalizers that it
	// accepted. The Hub resource has no preparation of its own, so the specs add one and run it through both entry
	// points. They use real transactions to see that a rejected update undoes what its callback wrote.
	Describe("candidate preparation", func() {
		const preparedFinalizer = "test.osac.io/prepared"

		type entryPoint func(server *GenericServer[*privatev1.Hub], ctx context.Context, request proto.Message,
			prepare PrepareCandidateFunc[*privatev1.Hub]) error
		withCandidatePreparation := func(server *GenericServer[*privatev1.Hub], ctx context.Context,
			request proto.Message, prepare PrepareCandidateFunc[*privatev1.Hub]) error {
			var response proto.Message
			return server.UpdateWithCandidatePreparation(ctx, request, &response, prepare)
		}
		withValidation := func(server *GenericServer[*privatev1.Hub], ctx context.Context,
			request proto.Message, prepare PrepareCandidateFunc[*privatev1.Hub]) error {
			var response proto.Message
			return server.UpdateWithValidation(ctx, request, &response,
				func(ctx context.Context, candidate, current *privatev1.Hub) error {
					return prepare(ctx, current, candidate)
				})
		}

		// freshContext returns a context with a new transaction, which is ended when the spec finishes.
		freshContext := func() context.Context {
			GinkgoHelper()
			tx, err := tm.Begin(context.Background())
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = tx.End(context.Background())
			})
			return database.TxIntoContext(context.Background(), tx)
		}

		DescribeTable("changes the finalizers only when the callback leaves them alone",
			func(call entryPoint, addFinalizer bool) {
				t := finalizerCaseFor(privatev1.Hubs_ServiceDesc.ServiceName).target([]string{finalizerController})
				server := t.ops.(*genericFinalizerOps[*privatev1.Hub]).server
				object := withFinalizers(t.get(ctx), finalizerController, finalizerHold).(*privatev1.Hub)

				// The callback writes another object in the transaction of the request, and optionally adds a
				// finalizer to the candidate:
				var sideID string
				prepare := func(ctx context.Context, current, candidate *privatev1.Hub) error {
					side, err := fixtureDAO[*privatev1.Hub]().Create().SetObject(privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   finalizerName("side"),
							Tenant: testTenant,
						}.Build(),
						Spec: privatev1.HubSpec_builder{Kubeconfig: []byte("my_config"), Namespace: "my_ns"}.Build(),
					}.Build()).Do(ctx)
					if err != nil {
						return err
					}
					sideID = side.GetObject().GetId()
					if addFinalizer {
						candidate.GetMetadata().SetFinalizers(append(candidate.GetMetadata().GetFinalizers(),
							preparedFinalizer))
					}
					return nil
				}

				// Commit the fixture, and send the request in a transaction of its own:
				Expect(suiteTx.End(ctx)).To(Succeed())
				suiteTx = nil
				tx, err := tm.Begin(context.Background())
				Expect(err).ToNot(HaveOccurred())
				request := proto.Clone(server.updateRequest).(finalizerUpdateRequest[*privatev1.Hub])
				request.SetObject(object)
				request.SetUpdateMask(finalizersMask())
				updateErr := call(server, database.TxIntoContext(context.Background(), tx), request, prepare)
				Expect(tx.End(context.Background())).To(Succeed())

				fresh := freshContext()
				stored, err := t.ops.get(fresh, t.id)
				Expect(err).ToNot(HaveOccurred())
				_, sideErr := fixtureDAO[*privatev1.Hub]().Get().SetId(sideID).Do(fresh)
				if addFinalizer {
					Expect(status.Code(updateErr)).To(Equal(codes.Internal),
						"a callback that changes the finalizers must be rejected, but the update returned %v", updateErr)
					Expect(finalizersOf(stored)).To(ConsistOf(finalizerController))
					var notFound *dao.ErrNotFound
					Expect(errors.As(sideErr, &notFound)).To(BeTrue(),
						"what the rejected callback wrote must be rolled back, but reading it returned %v", sideErr)
				} else {
					Expect(updateErr).ToNot(HaveOccurred())
					Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
					Expect(sideErr).ToNot(HaveOccurred(), "what the callback wrote must be committed")
				}
			},
			Entry("with UpdateWithCandidatePreparation and a callback that adds a finalizer",
				entryPoint(withCandidatePreparation), true),
			Entry("with UpdateWithValidation and a callback that adds a finalizer",
				entryPoint(withValidation), true),
			Entry("with UpdateWithCandidatePreparation and a callback that leaves the finalizers alone",
				entryPoint(withCandidatePreparation), false),
			Entry("with UpdateWithValidation and a callback that leaves the finalizers alone",
				entryPoint(withValidation), false),
		)
	})

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
				Expect(t.update(ctx, object, finalizersMask(), false)).To(Succeed())

				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
				Expect(proto.Equal(object, snapshot)).To(BeTrue(), "the request object must not be modified")
			},
			finalizerEntries(),
		)

		DescribeTable("accepts a stale finalizer write that leaves an object without finalizers as it is",
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
		// stored version is, and whether it selects the finalizers with a mask or replaces the whole object. The
		// finalizer check runs before the validation of the object, so every resource gets the conflict.
		DescribeTable("rejects a finalizer change that has no metadata",
			func(c finalizerCase, bumpFirst bool, mask *fieldmaskpb.FieldMask) {
				t := c.target([]string{finalizerController, finalizerHold})
				if bumpFirst {
					t.bump(ctx)
				}

				err := t.update(ctx, withoutMetadata(t.get(ctx)), mask, false)

				t.expectFinalizerConflict(ctx, err, finalizerController, finalizerHold)
			},
			finalizerEntriesPerVersion(),
		)
	})
})

// finalizerEntriesPerLifecycle returns an entry for each resource, for a live object and for one that is being deleted.
func finalizerEntriesPerLifecycle() []TableEntry {
	var entries []TableEntry
	for _, c := range finalizerCases() {
		entries = append(entries,
			Entry(c.service+" live", c, false),
			Entry(c.service+" deleting", c, true),
		)
	}
	return entries
}

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
