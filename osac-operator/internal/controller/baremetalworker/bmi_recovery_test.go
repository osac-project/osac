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
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestBMIRecoveryRejectsForeignIdentity(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		for _, field := range []string{"tenant", "owner", "label", "empty ID", "unrecorded name"} {
			t.Run(map[bool]string{true: "deletion", false: "normal"}[deletion]+"/"+field, func(t *testing.T) {
				r, fc, co := bmiStageHarness(t, workerPhaseProvisioning, "")
				bmi := ownedBMIFixture(co, "recorded-bmi", "owned-id")
				switch field {
				case "tenant":
					bmi.GetMetadata().SetTenant("foreign")
				case "owner":
					bmi.GetMetadata().SetAnnotations(map[string]string{ownerReferenceAnnotation: "ClusterOrder/foreign"})
				case "label":
					bmi.GetMetadata().SetLabels(map[string]string{clusterOrderLabel: "foreign"})
				case "empty ID":
					bmi.SetId("")
				case "unrecorded name":
					bmi.GetMetadata().SetName("slot")
				}
				fc.bmis = []*privatev1.BareMetalInstance{bmi}
				before := co.DeepCopy()
				var err error
				if deletion {
					err = runDeletionBMIStage(context.Background(), r, co)
				} else {
					_, _, err = runBMIStage(context.Background(), r, co)
				}
				if field == "unrecorded name" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("adopted an invalid BMI")
				}
				if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before.Status.Workers, co.Status.Workers) || len(fc.names) != 0 {
					t.Fatalf("invalid recovery mutated workers: %+v", co.Status.Workers)
				}
			})
		}
	}
}

func TestBMIDeletionRecoveryPreservesPhaseAndHistory(t *testing.T) {
	for _, phase := range []string{workerPhaseProvisioning, workerPhaseFailed, workerPhaseUnbinding, workerPhaseDeleting} {
		t.Run(phase, func(t *testing.T) {
			r, fc, co := bmiStageHarness(t, phase, "")
			next := metav1.NewTime(time.Now().Add(time.Hour))
			co.Status.Workers[0].NextRetryTime = &next
			co.Status.Workers[0].AttemptCount = 4
			co.Status.Workers[0].LastFailureReason = "previous"
			if err := r.Status().Update(context.Background(), co); err != nil {
				t.Fatal(err)
			}
			want := co.Status.Workers[0]
			want.BareMetalInstance.ID = "owned-id"
			fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "owned-id")}
			if err := runDeletionBMIStage(context.Background(), r, co); !errors.Is(err, errWorkerObservationChanged) {
				t.Fatalf("first deletion observation error=%v, want boundary", err)
			}
			if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
				t.Fatal(err)
			}
			if err := runDeletionBMIStage(context.Background(), r, co); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(co.Status.Workers[0], want) || len(fc.names) != 0 || fc.gets != 0 {
				t.Fatalf("deletion recovery changed history/phase or provisioned: %+v", co.Status.Workers)
			}
		})
	}
}

func TestBMIRecoveryDoesNotOverwriteNewerReservation(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		for _, field := range []string{"ID", "name", "phase", "retry", "kind"} {
			t.Run(map[bool]string{true: "deletion", false: "normal"}[deletion]+"/"+field, func(t *testing.T) {
				r, fc, co := bmiStageHarness(t, workerPhaseProvisioning, "")
				kube := r.Client
				fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "observed-id")}
				var latest *v1alpha1.ClusterOrder
				r.Client = &bmiConflictClient{Client: kube, beforePatch: func() {
					latest = co.DeepCopy()
					if err := kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest); err != nil {
						t.Fatal(err)
					}
					w := &latest.Status.Workers[0]
					switch field {
					case "ID":
						w.BareMetalInstance.ID = "new-id"
					case "name":
						w.BareMetalInstance.Name = "new-name"
					case "phase":
						w.Phase = workerPhaseDeleting
					case "retry":
						next := metav1.NewTime(time.Now().Add(time.Hour))
						w.NextRetryTime = &next
					case "kind":
						w.Kind = "Other"
					}
					if err := kube.Status().Update(context.Background(), latest); err != nil {
						t.Fatal(err)
					}
				}}
				if deletion {
					if err := runDeletionBMIStage(context.Background(), r, co); !apierrors.IsConflict(err) {
						t.Fatalf("error=%v, want one-shot conflict", err)
					}
				} else {
					_, res, err := runBMIStage(context.Background(), r, co)
					if !apierrors.IsConflict(err) || !res.IsZero() {
						t.Fatalf("result=%+v err=%v, want one-shot conflict", res, err)
					}
				}
				if err := kube.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(co.Status.Workers, latest.Status.Workers) {
					t.Fatalf("overwrote newer reservation: %+v", co.Status.Workers)
				}
				if len(fc.names) != 0 {
					t.Fatal("created during recovery")
				}
			})
		}
	}
}

func TestBMIProvisioningRecoversAuthoritativeReservation(t *testing.T) {
	r, fc, co := bmiStageHarness(t, workerPhaseProvisioning, "")
	fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "created-id")}
	if _, err := runWorkerCapacityStage(t, r, co); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if len(co.Status.Workers) != 1 || co.Status.Workers[0].BareMetalInstance.ID != "created-id" || len(fc.names) != 0 {
		t.Fatalf("did not recover authoritative reservation: %+v creates=%v", co.Status.Workers, fc.names)
	}
}

func TestBMIMissingSlotIsReplacedWithANewReservation(t *testing.T) {
	r, fc, co := bmiStageHarness(t, workerPhaseWaitingForAgent, "gone-id")
	old := co.Status.Workers[0]
	if _, _, err := runBMIStage(context.Background(), r, co); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if _, err := runWorkerCapacityStage(t, r, co); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if len(co.Status.Workers) != 1 {
		t.Fatalf("workers=%+v", co.Status.Workers)
	}
	if co.Status.Workers[0].BareMetalInstance.ID != "" || len(fc.names) != 0 {
		t.Fatal("reservation did not return before create")
	}
	if _, err := runWorkerCapacityStage(t, r, co); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	w := co.Status.Workers[0]
	if w.Name == old.Name || w.BareMetalInstance.Name == old.BareMetalInstance.Name || w.BareMetalInstance.ID == "" || len(fc.names) != 1 || fc.reservationMissing {
		t.Fatalf("replacement lost durable identity boundary: %+v creates=%v", w, fc.names)
	}
}
