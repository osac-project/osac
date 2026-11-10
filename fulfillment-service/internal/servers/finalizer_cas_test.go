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

// A writer with a stale snapshot must not overwrite finalizers stored after it. Writes are ordered within one
// transaction unless a spec says otherwise.
var _ = Describe("finalizer CAS", func() {
	// Coverage is at the GenericServer level; typed wrappers that rebuild the request have their own specs.
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
				stale := t.get(r.ctx)
				t.release(r.ctx, finalizerController)
				logs := t.recordLogs()

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
				// Public objects have no finalizers.
				object := withFinalizers(t.get(r.ctx))

				err := t.update(withPublicAPIView(r.ctx), object, nil, false)
				Expect(err).ToNot(HaveOccurred())

				Expect(t.finalizers(r.ctx)).To(ConsistOf(finalizerController, finalizerHold),
					"a public update must not clear the finalizers")
			})
	})

	// Public requests can't carry finalizers, so the server restores the stored ones and never returns Aborted.
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

	// Without metadata the update may still fail validation or in the database, so that part runs in a savepoint.
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
				t.markDeleting(r.ctx)
				object := withFinalizers(t.get(r.ctx))

				err := t.update(withPublicAPIView(r.ctx), object, nil, false)
				Expect(err).ToNot(HaveOccurred())

				stored, err := t.ops.get(r.ctx, t.id)
				Expect(err).ToNot(HaveOccurred(), "a public update must not archive an object with finalizers")
				Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
			})
	})

	// The update may still be rejected, by validation or the database, but never for its finalizers.
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

	// The restore is shared code already run for every resource above, so one resource is enough here.
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

	// Preparation callbacks run after the finalizer check, so they must not change the finalizers. Real transactions
	// show that a rejected update also undoes what its callback wrote.
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

		freshContext := func() context.Context {
			GinkgoHelper()
			tx, err := tm.Begin(context.Background())
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = tx.End(context.Background())
			})
			return database.TxIntoContext(context.Background(), tx)
		}

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

		// writesSideObject writes another object in the request transaction before modifying the candidate.
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

				// Commit the fixture and send the request in its own transaction.
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

		DescribeTable("commits what a create callback wrote only when the create is accepted",
			func(modify func(*privatev1.Hub) error, code codes.Code) {
				t := finalizerCaseFor(privatev1.Hubs_ServiceDesc.ServiceName).target(nil)
				server := t.ops.(*genericFinalizerOps[*privatev1.Hub]).server

				var sideID string
				prepare := writesSideObject(&sideID, modify)

				// Commit the fixture and send the request in its own transaction.
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

	// The change is logged only on commit, so specs end the transaction before checking the log.
	Describe("finalizer change log", func() {
		// Lists are the DAO's sorted, deduplicated ones; version is the one checked against, not the one written.
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

		// The finalizer check passes, then label validation rejects the update.
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

		// Later code in the same request, like ExternalIPAttachment parent settlement, can roll back the transaction.
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

		// Secrets rolls back a savepoint when Vault fails, but the enclosing transaction can still commit.
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

		// Without metadata the write has nowhere to carry a version, so it can never succeed: it is invalid, not a
		// conflict to retry. The finalizer check runs before validation, so every resource gets the same rejection.
		DescribeTable("rejects a finalizer change that has no metadata as invalid",
			func(bumpFirst bool, mask *fieldmaskpb.FieldMask) {
				runFinalizerCases([]string{finalizerController, finalizerHold},
					func(r *finalizerRun, _ finalizerCase, t finalizerTarget) {
						if bumpFirst {
							t.bump(r.ctx)
						}

						err := t.update(r.ctx, withoutMetadata(t.get(r.ctx)), mask, false)

						t.expectFinalizerInvalid(r.ctx, err, finalizerController, finalizerHold)
					})
			},
			Entry("at version 0 with metadata.finalizers mask", false, finalizersMask()),
			Entry("at a later version with metadata.finalizers mask", true, finalizersMask()),
			Entry("at version 0 with nil mask", false, (*fieldmaskpb.FieldMask)(nil)),
			Entry("at a later version with nil mask", true, (*fieldmaskpb.FieldMask)(nil)),
		)
	})
})
