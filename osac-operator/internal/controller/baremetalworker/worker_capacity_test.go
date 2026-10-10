// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type capacityMutationClient struct {
	*nodeSetClient
	beforeType  func()
	afterCreate func()
}

func (f *capacityMutationClient) GetBareMetalInstanceType(ctx context.Context, name string) (*privatev1.BareMetalInstanceType, error) {
	if f.beforeType != nil {
		f.beforeType()
		f.beforeType = nil
	}
	return f.nodeSetClient.GetBareMetalInstanceType(ctx, name)
}
func (f *capacityMutationClient) CreateBareMetalInstance(ctx context.Context, bmi *privatev1.BareMetalInstance) (*privatev1.BareMetalInstance, error) {
	created, err := f.nodeSetClient.CreateBareMetalInstance(ctx, bmi)
	if err == nil && f.afterCreate != nil {
		f.afterCreate()
		f.afterCreate = nil
	}
	return created, err
}
func mutateCapacityOrder(r *Reconciler, co *v1alpha1.ClusterOrder, mutate func(*v1alpha1.ClusterOrder)) {
	GinkgoHelper()
	latest := &v1alpha1.ClusterOrder{}
	ctx := context.Background()
	Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), latest)).To(Succeed())
	mutate(latest)
	desiredStatus := latest.Status
	Expect(r.Update(ctx, latest)).To(Succeed())
	latest.Status = desiredStatus
	Expect(r.Status().Update(ctx, latest)).To(Succeed())
}

var _ = It("preserves the attempt clock after a lost Create acknowledgement", func() {
	r, fc, co := nodeSetHarness("r09-lost-ack", nodeRequest("standard", 1))
	fc.failCreate = true
	if _, err := runWorkerCapacityStage(r, co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers).To(HaveLen(1), "workers=%+v", co.Status.Workers)
	origin := co.Status.Workers[0].AttemptStartedAt
	Expect(origin).ToNot(BeNil(), "attempt origin was not persisted before the first Create")
	// A lost Create acknowledgement retries the same reservation and must not
	// move the durable attempt origin.
	for range 2 {
		if _, err := runWorkerCapacityStage(r, co); err == nil {
			Fail("expected interrupted create")
		}
		Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
		if got := co.Status.Workers[0].AttemptStartedAt; got == nil || !got.Equal(origin) {
			Fail(fmt.Sprintf("lost acknowledgement moved the attempt origin: %+v want %+v", got, origin))
		}
	}
})

var _ = It("returns after persisting reservations before issuing Create", func() {
	r, fc, co := nodeSetHarness("r01-reserve", nodeRequest("standard", 2))
	res, err := runWorkerCapacityStage(r, co)
	Expect(err).NotTo(HaveOccurred())
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers).To(HaveLen(2), "reservation boundary: workers=%+v creates=%v result=%+v", co.Status.Workers, fc.names, res)
	Expect(fc.names).To(BeEmpty(), "reservation boundary: workers=%+v creates=%v result=%+v", co.Status.Workers, fc.names, res)
	Expect(res.IsZero()).To(BeFalse(), "reservation boundary: workers=%+v creates=%v result=%+v", co.Status.Workers, fc.names, res)
	for _, w := range co.Status.Workers {
		Expect(w.BareMetalInstance.Name).NotTo(Equal(""), "invalid reservation: %+v", w)
		Expect(w.BareMetalInstance.ID).To(Equal(""), "invalid reservation: %+v", w)
	}
})

var _ = It("issues at most one Create per invocation", func() {
	r, fc, co := nodeSetHarness("r01-create", nodeRequest("standard", 2))
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	// Capacity consumes the same durable snapshot the observation was built from.
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	res, err := runWorkerCapacityStage(r, co)
	Expect(err).NotTo(HaveOccurred())
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(fc.names).To(HaveLen(1), "create boundary: creates=%v result=%+v", fc.names, res)
	Expect(res.IsZero()).To(BeFalse(), "create boundary: creates=%v result=%+v", fc.names, res)
	if w := workerByName(co.Status.Workers, fc.names[0]); w == nil || w.BareMetalInstance.ID == "" {
		Fail(fmt.Sprintf("successful create identity not persisted: %+v", co.Status.Workers))
	}
})

var _ = It("rejects a stale cached order instead of creating duplicate workers", func() {
	r, fc, co := nodeSetHarness("r01-stale", nodeRequest("standard", 2))
	stale := co.DeepCopy()
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	names := []string{co.Status.Workers[0].Name, co.Status.Workers[1].Name}
	// A caller whose snapshot predates the reservation must return to a fresh
	// invocation rather than refreshing and duplicating allocation.
	if _, err := runWorkerCapacityStage(r, stale); !errors.Is(err, errWorkerObservationChanged) {
		Fail(fmt.Sprintf("error=%v, want stale-observation rejection", err))
	}
	Expect(fc.names).To(BeEmpty(), "stale snapshot created BMIs: %v", fc.names)
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers).To(HaveLen(2), "stale snapshot changed reservations or duplicated allocation: %+v", co.Status.Workers)
	Expect(co.Status.Workers[0].Name).To(Equal(names[0]), "stale snapshot changed reservations or duplicated allocation: %+v", co.Status.Workers)
	Expect(co.Status.Workers[1].Name).To(Equal(names[1]), "stale snapshot changed reservations or duplicated allocation: %+v", co.Status.Workers)
	Expect(fc.bmis).To(BeEmpty(), "stale snapshot changed reservations or duplicated allocation: %+v", co.Status.Workers)
})

var _ = It("does not requeue unchanged capacity for retention ordering", func() {
	r, _, co := nodeSetHarness("r01-noop", nodeRequest("standard", 2))
	co.Status.Workers = []v1alpha1.WorkerStatus{
		newWorkerStatus("standard", "standard", "pending", "pending-id", workerPhaseWaitingForAgent),
		newWorkerStatus("standard", "standard", "ready", "ready-id", workerPhaseReady),
	}
	Expect(r.Status().Update(context.Background(), co)).To(Succeed())
	res, err := runWorkerCapacityStage(r, co)
	Expect(err).NotTo(HaveOccurred(), "unchanged capacity requeued solely for sorted plan: result=%+v err=%v", res, err)
	Expect(res.IsZero()).To(BeTrue(), "unchanged capacity requeued solely for sorted plan: result=%+v err=%v", res, err)
})

var _ = It("reserves another worker while an existing retry waits", func() {
	r, fc, co := nodeSetHarness("r01-retry", nodeRequest("standard", 2))
	future := metav1.NewTime(time.Now().Add(time.Hour))
	co.Status.Workers = []v1alpha1.WorkerStatus{
		newWorkerStatus("standard", "standard", "waiting", "", workerPhaseFailed),
		newWorkerStatus("standard", "standard", "actionable", "", workerPhaseFailed),
	}
	co.Status.Workers[0].NextRetryTime = &future
	past := metav1.NewTime(time.Now().Add(-time.Minute))
	co.Status.Workers[1].NextRetryTime = &past // Persisted cleanup-complete retry checkpoint.
	Expect(r.Status().Update(context.Background(), co)).To(Succeed())
	res, err := runWorkerCapacityStage(r, co)
	Expect(err).NotTo(HaveOccurred(), "waiting slot blocked progress: result=%+v err=%v creates=%v", res, err, fc.names)
	Expect(res.IsZero()).To(BeFalse(), "waiting slot blocked progress: result=%+v err=%v creates=%v", res, err, fc.names)
	Expect(fc.names).To(HaveLen(1), "waiting slot blocked progress: result=%+v err=%v creates=%v", res, err, fc.names)
	Expect(fc.names[0]).To(Equal("actionable"), "waiting slot blocked progress: result=%+v err=%v creates=%v", res, err, fc.names)
})

type capacityRetryClient struct {
	*workerReadClient
	deletes   []string
	deleteErr error
}

func (f *capacityRetryClient) DeleteBareMetalInstance(_ context.Context, id string) error {
	f.deletes = append(f.deletes, id)
	return f.deleteErr
}

var _ = Describe("Failed-worker capacity cleanup stops after one retry Delete", func() {
	for _, tc := range []struct {
		name string
		err  error
	}{{"success", nil}, {"error", errors.New("provider deletion pending")}} {
		It(tc.name, func() {
			deleteErr := tc.err
			r, base, co := workerReadHarness()
			fc := &capacityRetryClient{workerReadClient: base, deleteErr: deleteErr}
			r.fulfillment = fc
			co.Spec.NodeRequests[0].NumberOfNodes = 2
			Expect(r.Update(context.Background(), co)).To(Succeed())
			co.Status.Workers[0].Phase = workerPhaseFailed
			co.Status.Workers = append(co.Status.Workers, newWorkerStatus("standard", "standard", "second", "second-id", workerPhaseFailed))
			Expect(r.Status().Update(context.Background(), co)).To(Succeed())
			fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id"), ownedBMIFixture(co, "second", "second-id")}
			res, err := runWorkerCapacityStage(r, co)
			Expect(errors.Is(err, deleteErr)).To(BeTrue(), "retry-delete boundary: result=%+v err=%v deletes=%v creates=%v", res, err, fc.deletes, fc.names)
			Expect(res.IsZero()).To(BeTrue(), "retry-delete boundary: result=%+v err=%v deletes=%v creates=%v", res, err, fc.deletes, fc.names)
			Expect(fc.deletes).To(HaveLen(1), "retry-delete boundary: result=%+v err=%v deletes=%v creates=%v", res, err, fc.deletes, fc.names)
			Expect(fc.names).To(BeEmpty(), "retry-delete boundary: result=%+v err=%v deletes=%v creates=%v", res, err, fc.deletes, fc.names)
			if deleteErr == nil {
				// A pending provider cleanup is a bounded recheck, not a global gate.
				if deadline := r.workerRecheckDeadline(co.Status.Workers, time.Now()); deadline.RequeueAfter <= 0 {
					Fail(fmt.Sprintf("pending cleanup did not schedule a recheck: %+v", deadline))
				}
			}
			Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
			// Accepted deletion is not completed deletion, including the default
			// immediate-delete fixture. Completion requires another fresh Get.
			wantID := "recorded-id"
			Expect(co.Status.Workers[0].BareMetalInstance.ID).To(Equal(wantID), "retry changed wrong slots: %+v", co.Status.Workers)
			Expect(co.Status.Workers[1].BareMetalInstance.ID).To(Equal("second-id"), "retry changed wrong slots: %+v", co.Status.Workers)
		})
	}
})

var _ = Describe("Capacity actions reject concurrent spec or slot changes", func() {
	for _, mutation := range []string{"spec", "deletion", "reference", "failed", "appended", "tenant", "replacement"} {
		It(mutation, func() {
			r, fc, co := nodeSetHarness("guard-capacity", nodeRequest("standard", 1))
			if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
			provider := &capacityMutationClient{nodeSetClient: fc}
			r.fulfillment = provider
			provider.beforeType = func() {
				mutateCapacityOrder(r, co, func(latest *v1alpha1.ClusterOrder) {
					switch mutation {
					case "spec":
						latest.Spec.NodeRequests[0].NumberOfNodes = 0
					case "deletion":
						latest.Finalizers = []string{bmWorkerFinalizer}
					case "reference":
						latest.Status.Workers[0].BareMetalInstance.Name = "replacement"
					case "failed":
						latest.Status.Workers[0].Phase = workerPhaseFailed
					case "appended":
						latest.Status.Workers = append(latest.Status.Workers, newWorkerStatus("standard", "standard", "appended", "appended-id", workerPhaseReady))
					case "tenant":
						if latest.Annotations == nil {
							latest.Annotations = map[string]string{}
						}
						latest.Annotations["osac.openshift.io/tenant"] = "other-tenant"
					case "replacement":
						latest.UID = "replacement-order"
					}
				})
				if mutation == "deletion" {
					latest := &v1alpha1.ClusterOrder{}
					Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), latest)).To(Succeed())
					Expect(r.Delete(context.Background(), latest)).To(Succeed())
				}
			}
			res, err := runWorkerCapacityStage(r, co)
			Expect(err == nil && res.IsZero()).To(BeFalse(), "capacity action accepted stale plan")
			Expect(fc.names).To(BeEmpty(), "created from stale plan: %v", fc.names)
		})
	}
})
var _ = It("preserves an appended slot while persisting Create and stops the next action", func() {
	r, fc, co := nodeSetHarness("append-after-create", nodeRequest("standard", 2))
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	provider := &capacityMutationClient{nodeSetClient: fc}
	r.fulfillment = provider
	provider.afterCreate = func() {
		mutateCapacityOrder(r, co, func(latest *v1alpha1.ClusterOrder) {
			latest.Status.Workers = append(latest.Status.Workers, newWorkerStatus("standard", "standard", "appended", "appended-id", workerPhaseReady))
		})
	}
	res, err := runWorkerCapacityStage(r, co)
	Expect(apierrors.IsConflict(err)).To(BeTrue(), "result=%+v err=%v, want one-shot conflict", res, err)
	Expect(res.IsZero()).To(BeTrue(), "result=%+v err=%v, want one-shot conflict", res, err)
	Expect(fc.names).To(HaveLen(1), "creates=%v, want only first action", fc.names)
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers).To(HaveLen(3), "lost appended slot: %+v", co.Status.Workers)
	w := workerByName(co.Status.Workers, fc.names[0])
	Expect(w).NotTo(BeNil(), "lost reserved create identity: %+v", w)
	Expect(w.BareMetalInstance.Name).To(Equal(fc.names[0]), "lost reserved create identity: %+v", w)
	Expect(w.BareMetalInstance.ID).To(Equal(""), "lost reserved create identity: %+v", w)
})
var _ = It("rejects reservations for a replacement ClusterOrder", func() {
	r, _, co := nodeSetHarness("replacement-order", nodeRequest("standard", 1))
	stale := co.DeepCopy()
	stale.UID = "old-uid"
	if _, err := r.reserveWorkerSlots(context.Background(), stale); !errors.Is(err, errWorkerObservationChanged) {
		Fail(fmt.Sprintf("error=%v, want identity guard", err))
	}
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers).To(BeEmpty(), "reserved capacity on replacement order")
})
var _ = It("stops capacity actions after a foreign reference is added concurrently", func() {
	r, fc, co := workerReadHarness()
	initial := co.DeepCopy()
	fc.listed = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
	observed, res, err := r.observeWorkerResources(context.Background(), co)
	Expect(err).NotTo(HaveOccurred(), "observe=%+v %v", res, err)
	Expect(res.IsZero()).To(BeTrue(), "observe=%+v %v", res, err)
	if _, err := r.observeExistingWorkers(context.Background(), co, "tenant", observed); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	foreign := ownedBMIFixture(co, "foreign-bmi", "foreign-id")
	foreign.GetMetadata().SetTenant("foreign")
	fc.bmis = append(fc.bmis, foreign)
	mutateCapacityOrder(r, co, func(latest *v1alpha1.ClusterOrder) {
		latest.Status.Workers = append(latest.Status.Workers, v1alpha1.WorkerStatus{Name: "foreign", Kind: workerKindBMI, NodeSet: "standard", Phase: workerPhaseReady, BareMetalInstance: v1alpha1.BareMetalInstanceReference{Name: "foreign-bmi", ID: "foreign-id"}, CreationTimestamp: metav1.Now()})
	})
	if _, err := r.reconcileDueWorkerCreation(context.Background(), initial, "tenant", workerCreationInputs{}, observed); err == nil {
		Fail("unverified refreshed reference permitted capacity actions")
	}
	Expect(fc.names).To(BeEmpty(), "provisioned after foreign reference refresh")
})
