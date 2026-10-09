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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("BMI recovery rejects foreign identities", func() {
	for _, deletion := range []bool{false, true} {
		for _, field := range []string{"tenant", "owner", "label", "empty ID", "unrecorded name"} {
			It(map[bool]string{true: "deletion", false: "normal"}[deletion]+"/"+field, func() {
				r, fc, co := bmiStageHarness(workerPhaseProvisioning, "")
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
					Expect(err).NotTo(HaveOccurred())
				} else {
					Expect(err).To(HaveOccurred(), "adopted an invalid BMI")
				}
				Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
				Expect(before.Status.Workers).To(Equal(co.Status.Workers), "invalid recovery mutated workers: %+v", co.Status.Workers)
				Expect(fc.names).To(BeEmpty(), "invalid recovery mutated workers: %+v", co.Status.Workers)
			})
		}
	}
})

var _ = Describe("BMI deletion recovery preserves phase and history", func() {
	for _, phase := range []string{workerPhaseProvisioning, workerPhaseFailed, workerPhaseUnbinding, workerPhaseDeleting} {
		It(phase, func() {
			r, fc, co := bmiStageHarness(phase, "")
			next := metav1.NewTime(time.Now().Add(time.Hour))
			co.Status.Workers[0].NextRetryTime = &next
			co.Status.Workers[0].AttemptCount = 4
			co.Status.Workers[0].LastFailureReason = "previous"
			Expect(r.Status().Update(context.Background(), co)).To(Succeed())
			want := co.Status.Workers[0]
			want.BareMetalInstance.ID = "owned-id"
			fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "owned-id")}
			if err := runDeletionBMIStage(context.Background(), r, co); !errors.Is(err, errWorkerObservationChanged) {
				Fail(fmt.Sprintf("first deletion observation error=%v, want boundary", err))
			}
			Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
			Expect(runDeletionBMIStage(context.Background(), r, co)).To(Succeed())
			Expect(co.Status.Workers[0]).To(Equal(want), "deletion recovery changed history/phase or provisioned: %+v", co.Status.Workers)
			Expect(fc.names).To(BeEmpty(), "deletion recovery changed history/phase or provisioned: %+v", co.Status.Workers)
			Expect(fc.gets).To(Equal(0), "deletion recovery changed history/phase or provisioned: %+v", co.Status.Workers)
		})
	}
})

var _ = Describe("BMI recovery does not overwrite newer reservation", func() {
	for _, deletion := range []bool{false, true} {
		for _, field := range []string{"ID", "name", "phase", "retry", "kind"} {
			It(map[bool]string{true: "deletion", false: "normal"}[deletion]+"/"+field, func() {
				r, fc, co := bmiStageHarness(workerPhaseProvisioning, "")
				kube := r.Client
				fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "observed-id")}
				var latest *v1alpha1.ClusterOrder
				r.Client = &bmiConflictClient{Client: kube, beforePatch: func() {
					latest = co.DeepCopy()
					Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest)).To(Succeed())
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
					Expect(kube.Status().Update(context.Background(), latest)).To(Succeed())
				}}
				if deletion {
					if err := runDeletionBMIStage(context.Background(), r, co); !apierrors.IsConflict(err) {
						Fail(fmt.Sprintf("error=%v, want one-shot conflict", err))
					}
				} else {
					_, res, err := runBMIStage(context.Background(), r, co)
					Expect(apierrors.IsConflict(err)).To(BeTrue(), "result=%+v err=%v, want one-shot conflict", res, err)
					Expect(res.IsZero()).To(BeTrue(), "result=%+v err=%v, want one-shot conflict", res, err)
				}
				Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
				Expect(co.Status.Workers).To(Equal(latest.Status.Workers), "overwrote newer reservation: %+v", co.Status.Workers)
				Expect(fc.names).To(BeEmpty(), "created during recovery")
			})
		}
	}
})

var _ = It("recovers an authoritative reservation before provisioning a BMI", func() {
	r, fc, co := bmiStageHarness(workerPhaseProvisioning, "")
	fc.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "created-id")}
	if _, err := runWorkerCapacityStage(r, co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers).To(HaveLen(1), "did not recover authoritative reservation: %+v creates=%v", co.Status.Workers, fc.names)
	Expect(co.Status.Workers[0].BareMetalInstance.ID).To(Equal("created-id"), "did not recover authoritative reservation: %+v creates=%v", co.Status.Workers, fc.names)
	Expect(fc.names).To(BeEmpty(), "did not recover authoritative reservation: %+v creates=%v", co.Status.Workers, fc.names)
})

var _ = It("replaces a confirmed-missing BMI slot with a new reservation", func() {
	r, fc, co := bmiStageHarness(workerPhaseWaitingForAgent, "gone-id")
	old := co.Status.Workers[0]
	if _, _, err := runBMIStage(context.Background(), r, co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	if _, err := runWorkerCapacityStage(r, co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers).To(HaveLen(1), "workers=%+v", co.Status.Workers)
	Expect(co.Status.Workers[0].BareMetalInstance.ID).To(Equal(""), "reservation did not return before create")
	Expect(fc.names).To(BeEmpty(), "reservation did not return before create")
	if _, err := runWorkerCapacityStage(r, co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	w := co.Status.Workers[0]
	Expect(w.Name).NotTo(Equal(old.Name), "replacement lost durable identity boundary: %+v creates=%v", w, fc.names)
	Expect(w.BareMetalInstance.Name).NotTo(Equal(old.BareMetalInstance.Name), "replacement lost durable identity boundary: %+v creates=%v", w, fc.names)
	Expect(w.BareMetalInstance.ID).NotTo(Equal(""), "replacement lost durable identity boundary: %+v creates=%v", w, fc.names)
	Expect(fc.names).To(HaveLen(1), "replacement lost durable identity boundary: %+v creates=%v", w, fc.names)
	Expect(fc.reservationMissing).To(BeFalse(), "replacement lost durable identity boundary: %+v creates=%v", w, fc.names)
})
