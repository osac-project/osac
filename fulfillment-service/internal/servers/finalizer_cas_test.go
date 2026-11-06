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
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// These specs check that a writer that holds a stale snapshot of an object can't overwrite the finalizers that
// another writer stored after the snapshot was taken. The concurrency is logical: each scenario orders its writes
// explicitly in one transaction, except where it says otherwise. The specs that cover every resource run the scenario
// of each resource concurrently, on its own object and in its own transaction, with runFinalizerCases.
var _ = Describe("finalizer CAS", func() {
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

	It("rejects a stale private finalizer write", func() {
		runFinalizerCases([]string{finalizerController, finalizerHold},
			func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
				// The reconciler read the object while a hold was present:
				stale := t.get(r.ctx)

				// Another writer released the hold after that read:
				t.release(r.ctx, finalizerController)
				logs := t.recordLogs()

				// The reconciler writes back its stale view without the lock:
				err := t.update(r.ctx, withFinalizers(stale, finalizerHold), finalizersMask(), false)

				t.expectFinalizerConflict(r.ctx, err, finalizerController)
				r.commit()
				Expect(logs.WithMessage(finalizerChangeMessage)).To(BeEmpty(),
					"a rejected write must not log a change")
			})
	})

	It("keeps finalizers on a public nil-mask update", func() {
		runFinalizerCases([]string{finalizerController, finalizerHold},
			func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
				// Public objects have no finalizers, so the object that a public server forwards has none:
				object := withFinalizers(t.get(r.ctx))

				err := t.update(withPublicAPIView(r.ctx), object, nil, false)
				Expect(err).ToNot(HaveOccurred())

				Expect(t.finalizers(r.ctx)).To(ConsistOf(finalizerController, finalizerHold),
					"a public update must not clear the finalizers")
			})
	})

	// A public request has no finalizers and no way to prove a version for them, so the server restores the stored
	// ones before it checks anything else. A stale public request therefore succeeds and never gets the Aborted
	// error that private finalizer changes get.
	It("keeps finalizers on a stale public nil-mask update", func() {
		runFinalizerCases([]string{finalizerController, finalizerHold},
			func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
				stale := t.get(r.ctx)
				t.bump(r.ctx)

				err := t.update(withPublicAPIView(r.ctx), withFinalizers(stale), nil, false)
				Expect(err).ToNot(HaveOccurred(), "a public request must not be rejected for its finalizers")

				Expect(t.finalizers(r.ctx)).To(ConsistOf(finalizerController, finalizerHold),
					"a stale public update must not clear the finalizers")
			})
	})

	// Most objects have no finalizers. A public nil-mask update of such an object must not give it any, and must not be
	// rejected for its finalizers. Without metadata in the request the update may still be rejected by validation or
	// by the database, as for objects that have finalizers, so that part runs in a savepoint.
	It("keeps an object without finalizers as it is on a public nil-mask update", func() {
		runFinalizerCases(nil, func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
			err := t.update(withPublicAPIView(r.ctx), t.get(r.ctx), nil, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(t.finalizers(r.ctx)).To(BeEmpty())

			err = t.publicUpdateInSavepoint(r.ctx, withoutMetadata(t.get(r.ctx)), nil)
			Expect(status.Code(err)).ToNot(Equal(codes.Aborted),
				"a public request must not be rejected for its finalizers, but the update returned %v", err)
			Expect(t.finalizers(r.ctx)).To(BeEmpty())
		})
	})

	It("keeps a deleting object on a public nil-mask update", func() {
		runFinalizerCases([]string{finalizerController, finalizerHold},
			func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
				// The deletion only marks the object because it has finalizers:
				t.markDeleting(r.ctx)
				object := withFinalizers(t.get(r.ctx))

				err := t.update(withPublicAPIView(r.ctx), object, nil, false)
				Expect(err).ToNot(HaveOccurred())

				stored, err := t.ops.get(r.ctx, t.id)
				Expect(err).ToNot(HaveOccurred(), "a public update must not archive an object with finalizers")
				Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
			})
	})

	// A public request can also arrive without any metadata. The server then has no metadata to restore the stored
	// finalizers into, so it creates it. The merged object may still be rejected afterwards, for example by its
	// validation or by the database, but never for its finalizers, and a rejected update must not write anything.
	DescribeTable("keeps finalizers on a public nil-mask update that has no metadata",
		func(deleting bool) {
			runFinalizerCases([]string{finalizerController, finalizerHold},
				func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
					if deleting {
						t.markDeleting(r.ctx)
					}
					before := t.get(r.ctx)

					err := t.publicUpdateInSavepoint(r.ctx, withoutMetadata(before), nil)

					Expect(status.Code(err)).ToNot(Equal(codes.Aborted),
						"a public request must not be rejected for its finalizers, but the update returned %v", err)
					stored, getErr := t.ops.get(r.ctx, t.id)
					Expect(getErr).ToNot(HaveOccurred(), "a public update must not archive an object with finalizers")
					Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold),
						"a public update must not clear the finalizers")
					if err != nil {
						Expect(proto.Equal(stored, before)).To(BeTrue(), "a rejected update must not write anything")
					}
				})
		},
		Entry("for a live object", false),
		Entry("for a deleting object", true),
	)

	// A public request can also carry a mask that selects the finalizers, directly or through the whole metadata.
	// The stored finalizers are restored after the mask is applied, so such a request is not rejected for its
	// finalizers and doesn't change them, even from a stale version. The restore is the same code for every resource,
	// and the specs above run the nil mask for every resource, so one resource is enough here.
	DescribeTable("keeps finalizers on a stale public update with a mask that selects them",
		func(mask *fieldmaskpb.FieldMask, deleting bool) {
			t := finalizerCaseFor(privatev1.Hubs_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})
			if deleting {
				t.markDeleting(ctx)
			}
			stale := withFinalizers(t.get(ctx))
			t.bump(ctx)

			err := t.publicUpdateInSavepoint(ctx, stale, mask)

			Expect(err).ToNot(HaveOccurred())
			stored, err := t.ops.get(ctx, t.id)
			Expect(err).ToNot(HaveOccurred(), "a public update must not archive an object with finalizers")
			Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold),
				"a public update must not change the finalizers")
		},
		publicMaskEntries(),
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

		// writesSideObject returns a callback that writes another object in the transaction of the request, saves its
		// identifier, and then modifies the candidate.
		writesSideObject := func(sideID *string,
			modify func(*privatev1.Hub) error) PrepareCandidateFunc[*privatev1.Hub] {
			return func(ctx context.Context, _, candidate *privatev1.Hub) error {
				side, err := fixtureDAO[*privatev1.Hub]().Create().SetObject(testHub(finalizerName("side"))).Do(ctx)
				if err != nil {
					return err
				}
				*sideID = side.GetObject().GetId()
				return modify(candidate)
			}
		}

		DescribeTable("commits what the callback wrote only when the update is accepted",
			func(call entryPoint, modify func(*privatev1.Hub) error, code codes.Code) {
				t := finalizerCaseFor(privatev1.Hubs_ServiceDesc.ServiceName).target([]string{finalizerController})
				server := t.ops.(*genericFinalizerOps[*privatev1.Hub]).server
				logs := t.recordLogs()
				object := withFinalizers(t.get(ctx), finalizerController, finalizerHold).(*privatev1.Hub)

				var sideID string
				prepare := writesSideObject(&sideID, modify)

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
					Expect(logs.WithMessage(finalizerChangeMessage)).To(BeEmpty(),
						"a rejected update must not log a finalizer change")
				} else {
					Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
					Expect(sideErr).ToNot(HaveOccurred(), "what the callback wrote must be committed")
					Expect(logs.WithMessage(finalizerChangeMessage)).To(HaveLen(1))
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

		// Create runs the same kind of callback, and a create that is rejected after it must also undo what the
		// callback wrote.
		DescribeTable("commits what a create callback wrote only when the create is accepted",
			func(modify func(*privatev1.Hub) error, code codes.Code) {
				t := finalizerCaseFor(privatev1.Hubs_ServiceDesc.ServiceName).target(nil)
				server := t.ops.(*genericFinalizerOps[*privatev1.Hub]).server

				var sideID string
				prepare := writesSideObject(&sideID, modify)

				// Commit the fixture, and send the request in a transaction of its own:
				Expect(suiteTx.End(ctx)).To(Succeed())
				suiteTx = nil
				tx, err := tm.Begin(context.Background())
				Expect(err).ToNot(HaveOccurred())
				request := proto.Clone(server.createRequest).(interface {
					proto.Message
					SetObject(*privatev1.Hub)
				})
				request.SetObject(testHub(finalizerName("created")))
				var response proto.Message
				createErr := server.CreateWithCandidatePreparation(database.TxIntoContext(context.Background(), tx),
					request, &response, prepare)
				Expect(tx.End(context.Background())).To(Succeed())

				Expect(sideID).ToNot(BeEmpty(), "the callback must have run and written the other object")
				_, sideErr := fixtureDAO[*privatev1.Hub]().Get().SetId(sideID).Do(freshContext())
				Expect(status.Code(createErr)).To(Equal(code), "the create returned %v", createErr)
				if code != codes.OK {
					var notFound *dao.ErrNotFound
					Expect(errors.As(sideErr, &notFound)).To(BeTrue(),
						"what the rejected callback wrote must be rolled back, but reading it returned %v", sideErr)
				} else {
					Expect(sideErr).ToNot(HaveOccurred(), "what the callback wrote must be committed")
				}
			},
			Entry("with a callback that fails", fails, codes.InvalidArgument),
			Entry("with a callback that leaves an invalid candidate", leavesInvalidCandidate, codes.InvalidArgument),
			Entry("with a callback that leaves the candidate alone", leavesAlone, codes.OK),
		)
	})

	Describe("controls", func() {
		It("compares finalizers as sets, ignoring order, duplicates, and nil versus empty", func() {
			Expect(sameFinalizers(nil, []string{})).To(BeTrue())
			Expect(sameFinalizers([]string{finalizerHold, finalizerController, finalizerHold},
				[]string{finalizerController, finalizerHold})).To(BeTrue())
			Expect(sameFinalizers(nil, []string{finalizerHold})).To(BeFalse())
			Expect(sameFinalizers([]string{finalizerHold}, []string{finalizerHold, finalizerController})).To(BeFalse())
		})

		It("accepts a finalizer change made from the current version", func() {
			runFinalizerCases([]string{finalizerController}, func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
				object := withFinalizers(t.get(r.ctx), finalizerController, finalizerHold)
				Expect(t.update(r.ctx, object, finalizersMask(), false)).To(Succeed())

				Expect(t.finalizers(r.ctx)).To(ConsistOf(finalizerController, finalizerHold))
			})
		})

		It("accepts a stale update that doesn't change the finalizers", func() {
			runFinalizerCases([]string{finalizerController, finalizerHold},
				func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
					stale := t.get(r.ctx)
					t.bump(r.ctx)

					object := withLabel(stale, "x", "y")
					Expect(t.update(r.ctx, object, finalizerMaskOf("metadata.labels"), false)).To(Succeed())

					stored := t.get(r.ctx)
					Expect(finalizerMetadataOf(stored).GetLabels()).To(HaveKeyWithValue("x", "y"))
					Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
				})
		})

		It("treats the same finalizers in another order, or repeated, as unchanged", func() {
			runFinalizerCases([]string{finalizerController, finalizerHold},
				func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
					stale := t.get(r.ctx)
					t.bump(r.ctx)

					object := withFinalizers(stale, finalizerHold, finalizerController, finalizerHold)
					snapshot := proto.Clone(object)
					Expect(t.update(r.ctx, object, finalizersMask(), false)).To(Succeed())

					Expect(t.finalizers(r.ctx)).To(ConsistOf(finalizerController, finalizerHold))
					Expect(proto.Equal(object, snapshot)).To(BeTrue(), "the request object must not be modified")
				})
		})

		It("accepts a stale finalizer write that leaves an object without finalizers as it is", func() {
			runFinalizerCases(nil, func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
				stale := t.get(r.ctx)
				t.bump(r.ctx)

				object := withFinalizers(stale)
				Expect(t.update(r.ctx, object, finalizersMask(), false)).To(Succeed())

				Expect(t.finalizers(r.ctx)).To(BeEmpty())
			})
		})

		It("keeps a foreign finalizer when another one is removed from the current version", func() {
			runFinalizerCases([]string{finalizerController, finalizerHold, finalizerForeign},
				func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
					object := withFinalizers(t.get(r.ctx), finalizerController, finalizerForeign)
					Expect(t.update(r.ctx, object, finalizersMask(), false)).To(Succeed())

					Expect(t.finalizers(r.ctx)).To(ConsistOf(finalizerController, finalizerForeign))
				})
		})

		It("archives a deleting object when its last finalizer is removed from the current version", func() {
			runFinalizerCases([]string{finalizerController}, func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
				t.markDeleting(r.ctx)

				object := withFinalizers(t.get(r.ctx))
				Expect(t.update(r.ctx, object, finalizersMask(), false)).To(Succeed())

				_, err := t.ops.get(r.ctx, t.id)
				Expect(status.Code(err)).To(Equal(codes.NotFound))
			})
		})

		It("still rejects a stale update that asks for the lock", func() {
			runFinalizerCases([]string{finalizerController, finalizerHold},
				func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
					stale := t.get(r.ctx)
					t.bump(r.ctx)

					object := withLabel(stale, "x", "y")
					err := t.update(r.ctx, object, finalizerMaskOf("metadata.labels"), true)

					Expect(status.Code(err)).To(Equal(codes.Aborted))
				})
		})

		It("accepts an update without metadata that doesn't change the finalizers", func() {
			runFinalizerCases([]string{finalizerController, finalizerHold},
				func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
					t.bump(r.ctx)

					// The mask selects the labels, so the finalizers of the stored object are untouched:
					object := withoutMetadata(t.get(r.ctx))
					Expect(t.update(r.ctx, object, finalizerMaskOf("metadata.labels"), false)).To(Succeed())

					Expect(t.finalizers(r.ctx)).To(ConsistOf(finalizerController, finalizerHold))
				})
		})

		It("leaves the finalizers alone when the update mask is empty", func() {
			runFinalizerCases([]string{finalizerController, finalizerHold},
				func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
					stale := t.get(r.ctx)
					t.release(r.ctx, finalizerController)

					// An empty mask is not the nil mask: nothing is applied.
					object := withFinalizers(stale, finalizerHold)
					Expect(t.update(r.ctx, object, &fieldmaskpb.FieldMask{}, false)).To(Succeed())

					Expect(t.finalizers(r.ctx)).To(ConsistOf(finalizerController))
				})
		})
	})

	// The generic server logs a finalizer change once the transaction that wrote it commits, so that the writer of a
	// finalizer transition can be found in the server logs. The specs end the transaction before they look at the
	// log.
	Describe("finalizer change log", func() {
		// The lists are the ones that the DAO wrote, which are sorted and have no repeated finalizer, and the
		// version is the one that the change was checked against, not the one that the write produced. The object
		// is written once before, so that its version is not zero.
		It("logs an accepted finalizer change once, when it is committed", func() {
			runFinalizerCases([]string{finalizerController}, func(r *finalizerRun, c finalizerCase, t finalizerTarget) {
				t.bump(r.ctx)
				before := t.get(r.ctx)
				Expect(finalizerMetadataOf(before).GetVersion()).To(BeNumerically(">", 0))
				logs := t.recordLogs()

				object := withFinalizers(before, finalizerHold, finalizerController, finalizerHold)
				Expect(t.update(r.ctx, object, finalizersMask(), false)).To(Succeed())
				Expect(logs.WithMessage(finalizerChangeMessage)).To(BeEmpty(),
					"nothing must be logged before the transaction commits")
				r.commit()

				records := logs.WithMessage(finalizerChangeMessage)
				Expect(records).To(HaveLen(1))
				Expect(records[0].Level).To(Equal(slog.LevelInfo))
				Expect(records[0].Attrs).To(HaveKeyWithValue("service", c.service))
				Expect(records[0].Attrs).To(HaveKeyWithValue("id", t.id))
				Expect(records[0].Attrs).To(HaveKeyWithValue("from", Equal([]string{finalizerController})))
				Expect(records[0].Attrs).To(HaveKeyWithValue("to",
					Equal([]string{finalizerController, finalizerHold})))
				Expect(records[0].Attrs).To(HaveKeyWithValue("version",
					int64(finalizerMetadataOf(before).GetVersion())))
				Expect(finalizerMetadataOf(t.get(r.ctx)).GetVersion()).To(
					Equal(finalizerMetadataOf(before).GetVersion()+1),
					"the write must have produced the next version")
			})
		})

		It("logs the removal of the last finalizer of a deleting object", func() {
			runFinalizerCases([]string{finalizerController}, func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
				t.markDeleting(r.ctx)
				logs := t.recordLogs()

				object := withFinalizers(t.get(r.ctx))
				Expect(t.update(r.ctx, object, finalizersMask(), false)).To(Succeed())
				r.commit()

				_, err := t.ops.get(r.ctx, t.id)
				Expect(status.Code(err)).To(Equal(codes.NotFound), "the object must have been archived")
				records := logs.WithMessage(finalizerChangeMessage)
				Expect(records).To(HaveLen(1))
				Expect(records[0].Attrs).To(HaveKeyWithValue("from", Equal([]string{finalizerController})))
				Expect(records[0].Attrs).To(HaveKeyWithValue("to", BeEmpty()))
			})
		})

		It("logs nothing when the finalizers are left as they are", func() {
			runFinalizerCases([]string{finalizerController, finalizerHold},
				func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
					logs := t.recordLogs()

					object := withLabel(t.get(r.ctx), "x", "y")
					Expect(t.update(r.ctx, object, finalizerMaskOf("metadata.labels"), false)).To(Succeed())
					r.commit()

					Expect(finalizerMetadataOf(t.get(r.ctx)).GetLabels()).To(HaveKeyWithValue("x", "y"))
					Expect(logs.WithMessage(finalizerChangeMessage)).To(BeEmpty())
				})
		})

		// The finalizer check accepts the change, then the validation of the labels rejects the update.
		It("logs nothing when the update is rejected after the finalizer check", func() {
			runFinalizerCases([]string{finalizerController}, func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
				logs := t.recordLogs()

				object := withMetadata(t.get(r.ctx), func(metadata *privatev1.Metadata) {
					metadata.SetFinalizers([]string{finalizerController, finalizerHold})
					metadata.SetLabels(map[string]string{"!!!invalid": "value"})
				})
				err := t.update(r.ctx, object, finalizerMaskOf("metadata.finalizers", "metadata.labels"), false)

				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
				r.commit()
				Expect(t.finalizers(r.ctx)).To(ConsistOf(finalizerController))
				Expect(logs.WithMessage(finalizerChangeMessage)).To(BeEmpty())
			})
		})

		// Code that runs after the generic update in the same request, like the settlement of the parent of an
		// ExternalIPAttachment, can report an error that rolls the whole transaction back. The fixture is committed
		// before the scenario starts, so the rollback only undoes the update.
		It("logs nothing when the transaction is rolled back after the write", func() {
			runFinalizerCases([]string{finalizerController}, func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
				logs := t.recordLogs()

				object := withFinalizers(t.get(r.ctx), finalizerController, finalizerHold)
				Expect(t.update(r.ctx, object, finalizersMask(), false)).To(Succeed())
				r.rollBack()

				Expect(t.finalizers(r.ctx)).To(ConsistOf(finalizerController), "the write must have been rolled back")
				Expect(logs.WithMessage(finalizerChangeMessage)).To(BeEmpty())
			})
		})

		// Secrets updates the object inside a savepoint and then stores the data in Vault. When that fails the
		// savepoint is rolled back, but the enclosing transaction can still commit.
		Describe("inside a savepoint", func() {
			It("logs nothing when the savepoint is rolled back", func() {
				t := finalizerCaseFor(privatev1.Hubs_ServiceDesc.ServiceName).target([]string{finalizerController})
				logs := t.recordLogs()
				object := withFinalizers(t.get(ctx), finalizerController, finalizerHold)

				laterErr := errors.New("a later step of the request failed")
				err := suiteTx.Savepoint(ctx, func(ctx context.Context) error {
					if err := t.update(ctx, object, finalizersMask(), false); err != nil {
						return err
					}
					return laterErr
				})
				Expect(err).To(BeIdenticalTo(laterErr))
				commitSuiteTx()

				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController), "the write must have been rolled back")
				Expect(logs.WithMessage(finalizerChangeMessage)).To(BeEmpty())
			})

			It("logs the change once the enclosing transaction commits", func() {
				t := finalizerCaseFor(privatev1.Hubs_ServiceDesc.ServiceName).target([]string{finalizerController})
				logs := t.recordLogs()
				object := withFinalizers(t.get(ctx), finalizerController, finalizerHold)

				// The update arrives through gRPC, so the log line names the method:
				const method = "/osac.private.v1.Hubs/Update"
				err := suiteTx.Savepoint(ctx, func(ctx context.Context) error {
					ctx = grpc.NewContextWithServerTransportStream(ctx, methodStream(method))
					return t.update(ctx, object, finalizersMask(), false)
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(logs.WithMessage(finalizerChangeMessage)).To(BeEmpty(),
					"releasing the savepoint must not log the change")
				commitSuiteTx()

				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
				records := logs.WithMessage(finalizerChangeMessage)
				Expect(records).To(HaveLen(1))
				Expect(records[0].Attrs).To(HaveKeyWithValue("method", method))
			})
		})
	})

	Describe("mask edges", func() {
		// A stale writer that changes the finalizers must be rejected whichever way it selects them: a mask for the
		// whole metadata, no mask at all, or the finalizers themselves.
		DescribeTable("rejects a stale finalizer change",
			func(mask *fieldmaskpb.FieldMask) {
				runFinalizerCases([]string{finalizerController, finalizerHold},
					func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
						stale := t.get(r.ctx)
						t.release(r.ctx, finalizerController)

						err := t.update(r.ctx, withFinalizers(stale, finalizerHold), mask, false)

						t.expectFinalizerConflict(r.ctx, err, finalizerController)
					})
			},
			finalizerMaskEntries(),
		)

		// A private write without metadata can't prove which version it started from, so it is rejected whatever the
		// stored version is, and whether it selects the finalizers with a mask or replaces the whole object. The
		// finalizer check runs before the validation of the object, so every resource gets the conflict.
		DescribeTable("rejects a finalizer change that has no metadata",
			func(bumpFirst bool, mask *fieldmaskpb.FieldMask) {
				runFinalizerCases([]string{finalizerController, finalizerHold},
					func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
						if bumpFirst {
							t.bump(r.ctx)
						}

						err := t.update(r.ctx, withoutMetadata(t.get(r.ctx)), mask, false)

						t.expectFinalizerConflict(r.ctx, err, finalizerController, finalizerHold)
					})
			},
			Entry("at version 0 with metadata.finalizers mask", false, finalizersMask()),
			Entry("at a later version with metadata.finalizers mask", true, finalizersMask()),
			Entry("at version 0 with nil mask", false, (*fieldmaskpb.FieldMask)(nil)),
			Entry("at a later version with nil mask", true, (*fieldmaskpb.FieldMask)(nil)),
		)
	})
})
