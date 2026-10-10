/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type bmiObservationClient struct {
	*nodeSetClient
	getErr, listErr error
	gets            int
	omitList        bool
	onGet           func()
}

func (f *bmiObservationClient) GetBareMetalInstance(_ context.Context, id string) (*privatev1.BareMetalInstance, error) {
	f.gets++
	if f.onGet != nil {
		f.onGet()
	}
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, bmi := range f.bmis {
		if bmi.GetId() == id {
			return bmi, nil
		}
	}
	return nil, status.Error(codes.NotFound, "missing")
}
func (f *bmiObservationClient) ListBareMetalInstances(ctx context.Context, filter string) ([]*privatev1.BareMetalInstance, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.omitList {
		return nil, nil
	}
	return f.nodeSetClient.ListBareMetalInstances(ctx, filter)
}
func ownedBMIFixture(co *v1alpha1.ClusterOrder, name, id string) *privatev1.BareMetalInstance {
	return privatev1.BareMetalInstance_builder{Id: id, Metadata: privatev1.Metadata_builder{
		Name: name, Tenant: "tenant", Labels: map[string]string{clusterOrderLabel: co.Name},
		Annotations: map[string]string{ownerReferenceAnnotation: "ClusterOrder/" + co.Name},
	}.Build()}.Build()
}
func bmiStageHarness(phase, id string) (*Reconciler, *bmiObservationClient, *v1alpha1.ClusterOrder) {
	GinkgoHelper()
	r, fc, co := nodeSetHarness("bmi-stage", nodeRequest("standard", 1))
	co.Status.Workers = []v1alpha1.WorkerStatus{newWorkerStatus("standard", "standard", "slot", id, phase)}
	co.Status.Workers[0].BareMetalInstance.Name = "recorded-bmi"
	Expect(r.Status().Update(context.Background(), co)).To(Succeed())
	obs := &bmiObservationClient{nodeSetClient: fc}
	r.fulfillment = obs
	r.macResolver = func(context.Context, string) []string { return nil }
	return r, obs, co
}

func runBMIStage(ctx context.Context, r *Reconciler, co *v1alpha1.ClusterOrder) (bool, ctrl.Result, error) {
	observed, res, err := r.observeWorkerResources(ctx, co)
	if err != nil || !res.IsZero() {
		return false, res, err
	}
	workers, err := r.observeExistingWorkers(ctx, co, "tenant", observed)
	if err != nil {
		return false, ctrl.Result{}, err
	}
	changed := !workerSlicesEqual(co.Status.Workers, workers)
	if changed {
		err = r.updateWorkerStatus(ctx, co, workers)
	}
	return changed, ctrl.Result{}, err
}

// Identity-only deletion-stage harness uses the public finalization stages.
func runDeletionBMIStage(ctx context.Context, r *Reconciler, co *v1alpha1.ClusterOrder) error {
	if err := validateWorkerBMIReferences(co); err != nil {
		return err
	}
	o, res, err := r.observeWorkerResources(ctx, co)
	if err != nil {
		return err
	}
	if !res.IsZero() {
		return errWorkerObservationChanged
	}
	workers, err := r.observeDeletionWorkers(ctx, co, "tenant", o)
	if err != nil {
		return err
	}
	if !workerSlicesEqual(co.Status.Workers, workers) {
		if err := r.updateWorkerStatus(ctx, co, workers); err != nil {
			return err
		}
		return errWorkerObservationChanged
	}
	return nil
}

var _ = It("recovers only the recorded BMI identity", func() {
	r, fc, co := bmiStageHarness(workerPhaseProvisioning, "")
	w := &co.Status.Workers[0]
	failure := metav1.NewTime(time.Unix(100, 0))
	w.AttemptCount, w.LastFailureReason, w.LastFailureTime = 3, "previous", &failure
	Expect(r.Status().Update(context.Background(), co)).To(Succeed())
	fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "slot", "unrecorded"), ownedBMIFixture(co, "recorded-bmi", "owned")}
	changed, res, err := runBMIStage(context.Background(), r, co)
	Expect(err).NotTo(HaveOccurred(), "repair = %v, %+v, %v", changed, res, err)
	Expect(res.IsZero()).To(BeTrue(), "repair = %v, %+v, %v", changed, res, err)
	Expect(changed).To(BeTrue(), "repair = %v, %+v, %v", changed, res, err)
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	got := co.Status.Workers[0]
	Expect(got.BareMetalInstance.ID).To(Equal("owned"), "lost identity/history or created during repair: %+v creates=%v", got, fc.names)
	Expect(got.Phase).To(Equal(workerPhaseWaitingForAgent), "lost identity/history or created during repair: %+v creates=%v", got, fc.names)
	Expect(got.Name).To(Equal("slot"), "lost identity/history or created during repair: %+v creates=%v", got, fc.names)
	Expect(got.AttemptCount).To(Equal(int32(3)), "lost identity/history or created during repair: %+v creates=%v", got, fc.names)
	Expect(got.LastFailureReason).To(Equal("previous"), "lost identity/history or created during repair: %+v creates=%v", got, fc.names)
	Expect(got.LastFailureTime.Equal(&failure)).To(BeTrue(), "lost identity/history or created during repair: %+v creates=%v", got, fc.names)
	Expect(fc.names).To(BeEmpty(), "lost identity/history or created during repair: %+v creates=%v", got, fc.names)
})

var _ = Describe("BMI absence requires authoritative NotFound evidence", func() {
	for _, tt := range []struct {
		name            string
		err             error
		exists, removed bool
	}{
		{name: "NotFound", removed: true},
		{name: "List omission is not absence", exists: true},
		{name: "Unavailable retains worker", err: ErrFulfillmentServiceUnavailable},
		{name: "transport error retains worker", err: status.Error(codes.Internal, "transport")},
	} {
		It(tt.name, func() {
			r, fc, co := bmiStageHarness(workerPhaseWaitingForAgent, "recorded-id")
			fc.getErr = tt.err
			fc.omitList = true
			if tt.exists {
				fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
			}
			_, _, err := runBMIStage(context.Background(), r, co)
			// There is no independent best-effort existence Get now: unknown
			// ownership must stop convergence while retaining the recorded slot.
			Expect(tt.err != nil && !errors.Is(err, tt.err)).To(BeFalse(), "error=%v, want ownership error %v", err, tt.err)
			Expect(tt.err == nil && err != nil).To(BeFalse(), fmt.Sprint(err))
			Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
			Expect((len(co.Status.Workers) == 0)).To(Equal(tt.removed), "workers=%+v removed=%v", co.Status.Workers, tt.removed)
			Expect(tt.removed && planWorkerSlots(co).missingByNodeSet["standard"] != 1).To(BeFalse(), "missing BMI still satisfies capacity")
			Expect(fc.gets).To(Equal(1), "gets=%d creates=%v", fc.gets, fc.names)
			Expect(fc.names).To(BeEmpty(), "gets=%d creates=%v", fc.gets, fc.names)
		})
	}
})

var _ = Describe("BMI observation preserves protected workers", func() {
	for _, phase := range []string{workerPhaseFailed, workerPhaseUnbinding, workerPhaseDeleting, workerPhaseProvisioning} {
		for _, id := range []string{"", "id"} {
			It(phase+"/"+id, func() {
				r, fc, co := bmiStageHarness(phase, id)
				if phase == workerPhaseProvisioning && id != "" {
					return
				}
				next := metav1.NewTime(time.Now().Add(time.Hour))
				if phase == workerPhaseFailed {
					co.Status.Workers[0].NextRetryTime = &next
				}
				Expect(r.Status().Update(context.Background(), co)).To(Succeed())
				before := co.DeepCopy()
				if phase != workerPhaseProvisioning {
					fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "id")}
				}
				_, _, err := runBMIStage(context.Background(), r, co)
				Expect(err).NotTo(HaveOccurred())
				Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
				Expect(co.Status.Workers).To(Equal(before.Status.Workers), "protected slot changed: %+v gets=%d", co.Status.Workers, fc.gets)
				Expect(fc.gets).To(Equal(0), "protected slot changed: %+v gets=%d", co.Status.Workers, fc.gets)
			})
		}
	}
})

var _ = Describe("BMI observation rejects List errors and missing recorded names", func() {
	for _, reason := range []string{"missing name", "list error", "unavailable"} {
		It(reason, func() {
			r, fc, co := bmiStageHarness(workerPhaseProvisioning, "")
			if reason == "missing name" {
				co.Status.Workers[0].BareMetalInstance.Name = ""
				Expect(r.Status().Update(context.Background(), co)).To(Succeed())
			}
			if reason == "list error" {
				fc.listErr = status.Error(codes.Internal, "list error")
			}
			if reason == "unavailable" {
				fc.listErr = ErrFulfillmentServiceUnavailable
			}
			before := co.DeepCopy()
			_, res, err := runBMIStage(context.Background(), r, co)
			if reason == "unavailable" {
				Expect(errors.Is(err, ErrFulfillmentServiceUnavailable)).To(BeTrue(), "unavailable result=%+v err=%v", res, err)
				Expect(res.IsZero()).To(BeTrue(), "unavailable result=%+v err=%v", res, err)
			} else {
				Expect(err).To(HaveOccurred(), "expected reference/list error")
			}
			Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
			Expect(before.Status.Workers).To(Equal(co.Status.Workers), "error altered workers")
		})
	}
})

type bmiConflictClient struct {
	client.Client
	beforePatch func()
	patches     int
}

func (c *bmiConflictClient) Status() client.SubResourceWriter {
	return &bmiConflictWriter{SubResourceWriter: c.Client.Status(), c: c}
}

type bmiConflictWriter struct {
	client.SubResourceWriter
	c *bmiConflictClient
}

func (w *bmiConflictWriter) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
	w.c.patches++
	if w.c.patches == 1 {
		w.c.beforePatch()
		return apierrors.NewConflict(schema.GroupResource{Group: "osac.openshift.io", Resource: "clusterorders"}, obj.GetName(), errors.New("injected conflict"))
	}
	return w.SubResourceWriter.Patch(ctx, obj, p, opts...)
}

var _ = Describe("BMI observation preserves newer references", func() {
	for _, conflict := range []bool{false, true} {
		for _, mutation := range []string{"id", "name", "phase"} {
			It(strings.Join([]string{mutation, map[bool]string{true: "conflict", false: "stale"}[conflict]}, "/"), func() {
				r, fc, co := bmiStageHarness(workerPhaseWaitingForAgent, "old-id")
				kube := r.Client
				var latest *v1alpha1.ClusterOrder
				mutate := func() {
					latest = co.DeepCopy()
					Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest)).To(Succeed())
					switch mutation {
					case "id":
						latest.Status.Workers[0].BareMetalInstance.ID = "new-id"
					case "name":
						latest.Status.Workers[0].BareMetalInstance.Name = "new-name"
					case "phase":
						latest.Status.Workers[0].Phase = workerPhaseDeleting
					}
					Expect(kube.Status().Update(context.Background(), latest)).To(Succeed())
				}
				if conflict {
					r.Client = &bmiConflictClient{Client: kube, beforePatch: mutate}
				} else {
					fc.onGet = mutate
				}
				_, res, err := runBMIStage(context.Background(), r, co)
				Expect(apierrors.IsConflict(err)).To(BeTrue(), "error=%v, want one-shot conflict", err)
				Expect(res.IsZero()).To(BeTrue(), "conflict returned result=%+v", res)
				Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
				Expect(co.Status.Workers).To(Equal(latest.Status.Workers), "old NotFound removed newer identity: %+v", co.Status.Workers)
			})
		}
	}
})
