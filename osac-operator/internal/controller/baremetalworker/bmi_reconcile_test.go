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
	"reflect"
	"strings"
	"testing"
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
func bmiStageHarness(t *testing.T, phase, id string) (*Reconciler, *bmiObservationClient, *v1alpha1.ClusterOrder) {
	t.Helper()
	r, fc, co := nodeSetHarness(t, "bmi-stage", nodeRequest("standard", 1))
	co.Status.Workers = []v1alpha1.WorkerStatus{newWorkerStatus("standard", "standard", "slot", id, phase)}
	co.Status.Workers[0].BareMetalInstance.Name = "recorded-bmi"
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
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
	changes, err := r.observeExistingWorkers(ctx, co, "tenant", observed)
	if err != nil {
		return false, ctrl.Result{}, err
	}
	res, err = r.persistWorkerChangesAndRefresh(ctx, co, changes)
	return len(changes) > 0, res, err
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
	changes, err := r.observeDeletionWorkers(ctx, co, "tenant", o)
	if err != nil {
		return err
	}
	res, err = r.persistWorkerChangesAndRefresh(ctx, co, changes)
	if err != nil {
		return err
	}
	if !res.IsZero() {
		return errWorkerObservationChanged
	}
	return nil
}

func TestBMIStageRecoversOnlyRecordedIdentity(t *testing.T) {
	r, fc, co := bmiStageHarness(t, workerPhaseProvisioning, "")
	w := &co.Status.Workers[0]
	failure := metav1.NewTime(time.Unix(100, 0))
	w.AttemptCount, w.LastFailureReason, w.LastFailureTime = 3, "previous", &failure
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "slot", "unrecorded"), ownedBMIFixture(co, "recorded-bmi", "owned")}
	changed, res, err := runBMIStage(context.Background(), r, co)
	if err != nil || !res.IsZero() || !changed {
		t.Fatalf("repair = %v, %+v, %v", changed, res, err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	got := co.Status.Workers[0]
	if got.BareMetalInstance.ID != "owned" || got.Phase != workerPhaseWaitingForAgent || got.Name != "slot" || got.AttemptCount != 3 || got.LastFailureReason != "previous" || !got.LastFailureTime.Equal(&failure) || len(fc.names) != 0 {
		t.Fatalf("lost identity/history or created during repair: %+v creates=%v", got, fc.names)
	}
}

func TestBMIStageAbsenceRequiresAuthoritativeNotFound(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			r, fc, co := bmiStageHarness(t, workerPhaseWaitingForAgent, "recorded-id")
			fc.getErr = tt.err
			fc.omitList = true
			if tt.exists {
				fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
			}
			_, _, err := runBMIStage(context.Background(), r, co)
			// There is no independent best-effort existence Get now: unknown
			// ownership must stop convergence while retaining the recorded slot.
			if tt.err != nil && !errors.Is(err, tt.err) {
				t.Fatalf("error=%v, want ownership error %v", err, tt.err)
			}
			if tt.err == nil && err != nil {
				t.Fatal(err)
			}
			if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
				t.Fatal(err)
			}
			if (len(co.Status.Workers) == 0) != tt.removed {
				t.Fatalf("workers=%+v removed=%v", co.Status.Workers, tt.removed)
			}
			if tt.removed && planWorkerSlots(co).missingByNodeSet["standard"] != 1 {
				t.Fatal("missing BMI still satisfies capacity")
			}
			if fc.gets != 1 || len(fc.names) != 0 {
				t.Fatalf("gets=%d creates=%v", fc.gets, fc.names)
			}
		})
	}
}

func TestBMIStagePreservesProtectedWorkers(t *testing.T) {
	for _, phase := range []string{workerPhaseFailed, workerPhaseUnbinding, workerPhaseDeleting, workerPhaseProvisioning} {
		for _, id := range []string{"", "id"} {
			t.Run(phase+"/"+id, func(t *testing.T) {
				r, fc, co := bmiStageHarness(t, phase, id)
				if phase == workerPhaseProvisioning && id != "" {
					return
				}
				next := metav1.NewTime(time.Now().Add(time.Hour))
				if phase == workerPhaseFailed {
					co.Status.Workers[0].NextRetryTime = &next
				}
				if err := r.Status().Update(context.Background(), co); err != nil {
					t.Fatal(err)
				}
				before := co.DeepCopy()
				if phase != workerPhaseProvisioning {
					fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "id")}
				}
				_, _, err := runBMIStage(context.Background(), r, co)
				if err != nil {
					t.Fatal(err)
				}
				if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(co.Status.Workers, before.Status.Workers) || fc.gets != 0 {
					t.Fatalf("protected slot changed: %+v gets=%d", co.Status.Workers, fc.gets)
				}
			})
		}
	}
}

func TestBMIStageListErrorAndMissingName(t *testing.T) {
	for _, reason := range []string{"missing name", "list error", "unavailable"} {
		t.Run(reason, func(t *testing.T) {
			r, fc, co := bmiStageHarness(t, workerPhaseProvisioning, "")
			if reason == "missing name" {
				co.Status.Workers[0].BareMetalInstance.Name = ""
				if err := r.Status().Update(context.Background(), co); err != nil {
					t.Fatal(err)
				}
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
				if err != nil || res.RequeueAfter != unavailableBackoff {
					t.Fatalf("backoff=%+v err=%v", res, err)
				}
			} else if err == nil {
				t.Fatal("expected reference/list error")
			}
			if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.Status.Workers, co.Status.Workers) {
				t.Fatal("error altered workers")
			}
		})
	}
}

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
func TestBMIStageDoesNotRemoveNewerReference(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		for _, mutation := range []string{"id", "name", "phase"} {
			t.Run(strings.Join([]string{mutation, map[bool]string{true: "conflict", false: "stale"}[conflict]}, "/"), func(t *testing.T) {
				r, fc, co := bmiStageHarness(t, workerPhaseWaitingForAgent, "old-id")
				kube := r.Client
				var latest *v1alpha1.ClusterOrder
				mutate := func() {
					latest = co.DeepCopy()
					if err := kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest); err != nil {
						t.Fatal(err)
					}
					switch mutation {
					case "id":
						latest.Status.Workers[0].BareMetalInstance.ID = "new-id"
					case "name":
						latest.Status.Workers[0].BareMetalInstance.Name = "new-name"
					case "phase":
						latest.Status.Workers[0].Phase = workerPhaseDeleting
					}
					if err := kube.Status().Update(context.Background(), latest); err != nil {
						t.Fatal(err)
					}
				}
				if conflict {
					r.Client = &bmiConflictClient{Client: kube, beforePatch: mutate}
				} else {
					fc.onGet = mutate
				}
				_, res, err := runBMIStage(context.Background(), r, co)
				if err != nil {
					t.Fatal(err)
				}
				if err := kube.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(co.Status.Workers, latest.Status.Workers) {
					t.Fatalf("old NotFound removed newer identity: %+v", co.Status.Workers)
				}
				if res.IsZero() {
					t.Fatal("changed evidence must stop provisioning and requeue")
				}
			})
		}
	}
}
