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
	"log/slog"
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
			"services missing from the finalizer cases: %v\n"+
				"services in the finalizer cases that have no finalizer-bearing Update: %v",
			missing, stale)
	})

	DescribeTable("rejects a stale private finalizer write",
		func(c finalizerCase) {
			// The reconciler read the object while a hold was present:
			t := c.target([]string{finalizerController, finalizerHold})
			stale := t.get(ctx)

			// Another writer released the hold after that read:
			t.release(ctx, finalizerController)
			logs := t.recordLogs()

			// The reconciler writes back its stale view without the lock:
			err := t.update(ctx, withFinalizers(stale, finalizerHold), finalizersMask(), false)

			t.expectFinalizerConflict(ctx, err, finalizerController)
			Expect(logs.withMessage(finalizerChangeMessage)).To(BeEmpty(), "a rejected write must not log a change")
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
	// points. They use real transactions to see that an update that is rejected for any reason undoes what its
	// callback wrote: because the callback changed the finalizers, because it failed, or because the candidate it left
	// is not valid.
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

		// What the callbacks do to the candidate, after they have written the other object:
		leavesAlone := func(*privatev1.Hub) error {
			return nil
		}
		addsFinalizer := func(candidate *privatev1.Hub) error {
			candidate.GetMetadata().SetFinalizers(append(candidate.GetMetadata().GetFinalizers(),
				preparedFinalizer))
			return nil
		}
		fails := func(*privatev1.Hub) error {
			return status.Error(codes.InvalidArgument, "the callback failed")
		}
		changesIdentity := func(candidate *privatev1.Hub) error {
			candidate.SetId(finalizerName("other"))
			return nil
		}
		leavesInvalidCandidate := func(candidate *privatev1.Hub) error {
			candidate.GetMetadata().SetLabels(map[string]string{"!!!invalid": "value"})
			return nil
		}

		DescribeTable("commits what the callback wrote only when the update is accepted",
			func(call entryPoint, modify func(*privatev1.Hub) error, code codes.Code) {
				t := finalizerCaseFor(privatev1.Hubs_ServiceDesc.ServiceName).target([]string{finalizerController})
				server := t.ops.(*genericFinalizerOps[*privatev1.Hub]).server
				logs := t.recordLogs()
				object := withFinalizers(t.get(ctx), finalizerController, finalizerHold).(*privatev1.Hub)

				// The callback writes another object in the transaction of the request, and then modifies the
				// candidate:
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
					return modify(candidate)
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
				Expect(sideID).ToNot(BeEmpty(), "the callback must have run and written the other object")
				_, sideErr := fixtureDAO[*privatev1.Hub]().Get().SetId(sideID).Do(fresh)
				Expect(status.Code(updateErr)).To(Equal(code), "the update returned %v", updateErr)
				if code != codes.OK {
					Expect(finalizersOf(stored)).To(ConsistOf(finalizerController))
					var notFound *dao.ErrNotFound
					Expect(errors.As(sideErr, &notFound)).To(BeTrue(),
						"what the rejected callback wrote must be rolled back, but reading it returned %v", sideErr)
					Expect(logs.withMessage(finalizerChangeMessage)).To(BeEmpty(),
						"a rejected update must not log a finalizer change")
				} else {
					Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
					Expect(sideErr).ToNot(HaveOccurred(), "what the callback wrote must be committed")
					Expect(logs.withMessage(finalizerChangeMessage)).To(HaveLen(1))
				}
			},
			Entry("with UpdateWithCandidatePreparation and a callback that adds a finalizer",
				entryPoint(withCandidatePreparation), addsFinalizer, codes.Internal),
			Entry("with UpdateWithValidation and a callback that adds a finalizer",
				entryPoint(withValidation), addsFinalizer, codes.Internal),
			Entry("with UpdateWithCandidatePreparation and a callback that fails",
				entryPoint(withCandidatePreparation), fails, codes.InvalidArgument),
			Entry("with UpdateWithValidation and a callback that fails",
				entryPoint(withValidation), fails, codes.InvalidArgument),
			Entry("with UpdateWithCandidatePreparation and a callback that changes the identifier",
				entryPoint(withCandidatePreparation), changesIdentity, codes.PermissionDenied),
			Entry("with UpdateWithValidation and a callback that changes the identifier",
				entryPoint(withValidation), changesIdentity, codes.PermissionDenied),
			Entry("with UpdateWithCandidatePreparation and a callback that leaves an invalid candidate",
				entryPoint(withCandidatePreparation), leavesInvalidCandidate, codes.InvalidArgument),
			Entry("with UpdateWithValidation and a callback that leaves an invalid candidate",
				entryPoint(withValidation), leavesInvalidCandidate, codes.InvalidArgument),
			Entry("with UpdateWithCandidatePreparation and a callback that leaves the finalizers alone",
				entryPoint(withCandidatePreparation), leavesAlone, codes.OK),
			Entry("with UpdateWithValidation and a callback that leaves the finalizers alone",
				entryPoint(withValidation), leavesAlone, codes.OK),
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

	// The generic server logs a finalizer change once it has been written, so that the writer of a finalizer
	// transition can be found in the server logs.
	Describe("finalizer change log", func() {
		// The lists are the ones that the database stored, which are sorted and have no repeated finalizer, and the
		// version is the one that the change was checked against, not the one that the write produced. The object
		// is written once before, so that its version is not zero.
		DescribeTable("logs an accepted finalizer change once",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController})
				t.bump(ctx)
				before := t.get(ctx)
				Expect(finalizerMetadataOf(before).GetVersion()).To(BeNumerically(">", 0))
				logs := t.recordLogs()

				object := withFinalizers(before, finalizerHold, finalizerController, finalizerHold)
				Expect(t.update(ctx, object, finalizersMask(), false)).To(Succeed())

				records := logs.withMessage(finalizerChangeMessage)
				Expect(records).To(HaveLen(1))
				Expect(records[0].level).To(Equal(slog.LevelInfo))
				Expect(records[0].attrs).To(HaveKeyWithValue("id", t.id))
				Expect(records[0].attrs).To(HaveKeyWithValue("from", Equal([]string{finalizerController})))
				Expect(records[0].attrs).To(HaveKeyWithValue("to", Equal([]string{finalizerController, finalizerHold})))
				Expect(records[0].attrs).To(HaveKeyWithValue("version",
					int64(finalizerMetadataOf(before).GetVersion())))
				Expect(finalizerMetadataOf(t.get(ctx)).GetVersion()).To(Equal(finalizerMetadataOf(before).GetVersion()+1),
					"the write must have produced the next version")
			},
			finalizerEntries(),
		)

		DescribeTable("logs the removal of the last finalizer of a deleting object",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController})
				Expect(t.ops.del(ctx, t.id)).To(Succeed())
				logs := t.recordLogs()

				object := withFinalizers(t.get(ctx))
				Expect(t.update(ctx, object, finalizersMask(), false)).To(Succeed())

				_, err := t.ops.get(ctx, t.id)
				Expect(status.Code(err)).To(Equal(codes.NotFound), "the object must have been archived")
				records := logs.withMessage(finalizerChangeMessage)
				Expect(records).To(HaveLen(1))
				Expect(records[0].attrs).To(HaveKeyWithValue("from", Equal([]string{finalizerController})))
				Expect(records[0].attrs).To(HaveKeyWithValue("to", BeEmpty()))
			},
			finalizerEntries(),
		)

		DescribeTable("logs nothing when the finalizers are left as they are",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController, finalizerHold})
				logs := t.recordLogs()

				object := withMetadata(t.get(ctx), func(metadata *privatev1.Metadata) {
					metadata.SetLabels(map[string]string{"x": "y"})
				})
				Expect(t.update(ctx, object, finalizerMaskOf("metadata.labels"), false)).To(Succeed())

				Expect(finalizerMetadataOf(t.get(ctx)).GetLabels()).To(HaveKeyWithValue("x", "y"))
				Expect(logs.withMessage(finalizerChangeMessage)).To(BeEmpty())
			},
			finalizerEntries(),
		)

		// The finalizer check accepts the change, then the validation of the labels rejects the update.
		DescribeTable("logs nothing when the update is rejected after the finalizer check",
			func(c finalizerCase) {
				t := c.target([]string{finalizerController})
				logs := t.recordLogs()

				object := withMetadata(t.get(ctx), func(metadata *privatev1.Metadata) {
					metadata.SetFinalizers([]string{finalizerController, finalizerHold})
					metadata.SetLabels(map[string]string{"!!!invalid": "value"})
				})
				err := t.update(ctx, object, finalizerMaskOf("metadata.finalizers", "metadata.labels"), false)

				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController))
				Expect(logs.withMessage(finalizerChangeMessage)).To(BeEmpty())
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

// finalizerEntriesPerMask returns an entry for each resource and each way of selecting the finalizers. The indexed path
// replaces a single entry of the stored list, so a stale write through it changes the finalizer set as well. The
// overlapping masks select the finalizers twice, or together with the version.
func finalizerEntriesPerMask() []TableEntry {
	masks := []struct {
		name string
		mask *fieldmaskpb.FieldMask
	}{
		{"metadata mask", finalizerMaskOf("metadata")},
		{"nil mask", nil},
		{"metadata.finalizers mask", finalizerMaskOf("metadata.finalizers")},
		{"metadata.finalizers.0 mask", finalizerMaskOf("metadata.finalizers.0")},
		{"metadata and metadata.finalizers mask", finalizerMaskOf("metadata", "metadata.finalizers")},
		{"metadata.finalizers and metadata.version mask", finalizerMaskOf("metadata.finalizers", "metadata.version")},
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
