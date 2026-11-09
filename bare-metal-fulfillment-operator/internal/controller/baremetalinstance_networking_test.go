/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const testNetworkingNamespace = "default"

var _ = Describe("BareMetalInstance NetworkAttachment lifecycle", func() {
	var (
		ctx        context.Context
		bmi        *v1alpha1.BareMetalInstance
		reconciler *BareMetalInstanceReconciler
	)

	newBMI := func() *v1alpha1.BareMetalInstance {
		name := fmt.Sprintf("bm-network-%d", time.Now().UnixNano())
		return &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNetworkingNamespace,
				Labels:    map[string]string{BareMetalInstanceUUIDLabel: "api-" + name},
				Annotations: map[string]string{
					bareMetalInstanceTenantAnnotation: "tenant-a",
					bareMetalInstanceOwnerAnnotation:  "BareMetalPool/pool-a",
				},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				Selector:         v1alpha1.HostSelectorSpec{HostSelector: map[string]string{"test": "networking"}},
				TemplateID:       "noop",
				ExternalHostID:   "inventory/host-a",
				ExternalHostName: "host-a",
				HostClass:        "metal3",
				NetworkAttachments: []v1alpha1.BareMetalNetworkAttachment{{
					SubnetRef:         "subnet-a",
					Interface:         "data-0",
					SecurityGroupRefs: []string{"sg-a", "sg-b"},
					Primary:           true,
				}},
			},
		}
	}

	newReconciler := func(inventoryClient *mockInventoryClient) *BareMetalInstanceReconciler {
		return &BareMetalInstanceReconciler{
			Client:                            k8sClient,
			Scheme:                            k8sClient.Scheme(),
			InventoryClient:                   inventoryClient,
			NetworkingEnabled:                 true,
			NetworkingNamespace:               testNetworkingNamespace,
			ProvisionPollIntervalDuration:     time.Second,
			ManagementRecheckIntervalDuration: time.Second,
		}
	}

	createBMI := func() {
		Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
		DeferCleanup(func() {
			latest := &v1alpha1.BareMetalInstance{}
			if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(bmi), latest); err == nil {
				latest.Finalizers = nil
				_ = k8sClient.Update(context.Background(), latest)
				_ = k8sClient.Delete(context.Background(), latest)
			}
		})
	}

	createNetworkAttachment := func(finalizers []string) *opv1alpha1.NetworkAttachment {
		apiID := bmi.Labels[BareMetalInstanceUUIDLabel]
		return &opv1alpha1.NetworkAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:       opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, apiID),
				Namespace:  testNetworkingNamespace,
				Finalizers: finalizers,
				Annotations: map[string]string{
					bareMetalInstanceTenantAnnotation: bmi.Annotations[bareMetalInstanceTenantAnnotation],
					bareMetalInstanceOwnerAnnotation:  bmi.Annotations[bareMetalInstanceOwnerAnnotation],
				},
			},
			Spec: opv1alpha1.NetworkAttachmentSpec{
				Target:         opv1alpha1.NetworkAttachmentTarget{Kind: opv1alpha1.NetworkAttachmentTargetBareMetalInstance, ID: apiID},
				Subnet:         "subnet-a",
				Interface:      opv1alpha1.NetworkAttachmentInterface{Type: opv1alpha1.NetworkInterfacePhysical, Name: "data-0", MACAddress: "aa:bb:cc:dd:ee:01"},
				SecurityGroups: []string{"sg-a", "sg-b"},
			},
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		bmi = newBMI()
	})

	It("creates an immutable physical attachment with normalized inventory MAC and tenant metadata", func() {
		createBMI()
		inv := &mockInventoryClient{getHostLogicalPortMACsFunc: func(_ context.Context, hostID string) (map[string]string, error) {
			Expect(hostID).To(Equal("inventory/host-a"))
			return map[string]string{"data-0": "AA-BB-CC-DD-EE-01"}, nil
		}}
		reconciler = newReconciler(inv)

		result, err := reconciler.reconcileNetworking(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
		_, err = reconciler.reconcileNetworking(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())

		na := &opv1alpha1.NetworkAttachment{}
		key := bareMetalNetworkAttachmentKey(testNetworkingNamespace, bmi.Labels[BareMetalInstanceUUIDLabel])
		Expect(k8sClient.Get(ctx, key, na)).To(Succeed())
		Expect(na.Spec.Target).To(Equal(opv1alpha1.NetworkAttachmentTarget{
			Kind: opv1alpha1.NetworkAttachmentTargetBareMetalInstance,
			ID:   bmi.Labels[BareMetalInstanceUUIDLabel],
		}))
		Expect(na.Spec.Subnet).To(Equal("subnet-a"))
		Expect(na.Spec.Interface).To(Equal(opv1alpha1.NetworkAttachmentInterface{
			Type: opv1alpha1.NetworkInterfacePhysical, Name: "data-0", MACAddress: "aa:bb:cc:dd:ee:01",
		}))
		Expect(na.Spec.SecurityGroups).To(ConsistOf("sg-a", "sg-b"))
		Expect(na.Annotations).To(HaveKeyWithValue(bareMetalInstanceTenantAnnotation, "tenant-a"))
		Expect(na.Annotations).To(HaveKeyWithValue(bareMetalInstanceOwnerAnnotation, "BareMetalPool/pool-a"))
		Expect(na.Finalizers).To(ContainElement(BareMetalInstanceNetworkingFinalizer))
	})

	It("reuses an existing immutable attachment without re-reading a changed inventory MAC", func() {
		createBMI()
		na := createNetworkAttachment([]string{BareMetalInstanceNetworkingFinalizer})
		Expect(k8sClient.Create(ctx, na)).To(Succeed())
		inv := &mockInventoryClient{getHostLogicalPortMACsFunc: func(context.Context, string) (map[string]string, error) {
			Fail("inventory MAC should not be re-read for an existing immutable attachment")
			return nil, nil
		}}
		reconciler = newReconciler(inv)

		result, err := reconciler.reconcileNetworking(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
		result, err = reconciler.reconcileNetworking(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Reason).
			To(Equal(v1alpha1.HostConditionReasonProgressing))
	})

	It("projects apply readiness and final failure from the attachment", func() {
		reconciler = newReconciler(nil)
		na := createNetworkAttachment([]string{BareMetalInstanceNetworkingFinalizer})
		bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
			SubnetRef: "subnet-a", Interface: "data-0", Primary: true,
		}}
		na.Status.Conditions = []metav1.Condition{{
			Type: opv1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
			Reason: opv1alpha1.ReasonSucceeded, Message: "physical attachment applied",
		}}
		result := reconciler.projectNetworkAttachmentReady(bmi, na)
		Expect(result.IsZero()).To(BeTrue())
		cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))

		na.Status.Conditions[0].Status = metav1.ConditionFalse
		na.Status.Conditions[0].Reason = opv1alpha1.ReasonFailed
		na.Status.Conditions[0].Message = "switch rejected the port"
		result = reconciler.projectNetworkAttachmentReady(bmi, na)
		Expect(result.IsZero()).To(BeTrue())
		cond = bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonTemplateFailed))
		Expect(cond.Message).To(Equal("switch rejected the port"))
		Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
	})

	It("refuses a same-name attachment with a different tenant", func() {
		createBMI()
		na := createNetworkAttachment([]string{BareMetalInstanceNetworkingFinalizer})
		na.Annotations[bareMetalInstanceTenantAnnotation] = "tenant-b"
		Expect(k8sClient.Create(ctx, na)).To(Succeed())
		reconciler = newReconciler(&mockInventoryClient{})
		_, err := reconciler.reconcileNetworking(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
		_, err = reconciler.reconcileNetworking(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
	})

	It("maps status updates to the BMI's actual Kubernetes name", func() {
		createBMI()
		na := createNetworkAttachment(nil)
		requests := newReconciler(nil).mapNetworkAttachmentToBareMetalInstance(ctx, na)
		Expect(requests).To(Equal([]reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(bmi)}}))
	})

	It("waits for the operator finalizer, removes only its own, and waits for CR removal", func() {
		createBMI()
		controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
		Expect(k8sClient.Update(ctx, bmi)).To(Succeed())
		na := createNetworkAttachment([]string{BareMetalInstanceNetworkingFinalizer, operatorNetworkAttachmentFinalizer})
		Expect(k8sClient.Create(ctx, na)).To(Succeed())
		reconciler = newReconciler(nil)
		Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())

		_, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(na), na)).To(Succeed())
		Expect(na.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(na.Finalizers).To(ContainElements(BareMetalInstanceNetworkingFinalizer, operatorNetworkAttachmentFinalizer))

		controllerutil.RemoveFinalizer(na, operatorNetworkAttachmentFinalizer)
		Expect(k8sClient.Update(ctx, na)).To(Succeed())
		_, done, err = reconciler.reconcileNetworkingDeletion(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(na), na)).To(Satisfy(apierrors.IsNotFound))
		_, done, err = reconciler.reconcileNetworkingDeletion(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
	})

	It("ignores NetworkAttachments outside its configured namespace or for another target kind", func() {
		createBMI()
		na := createNetworkAttachment(nil)
		na.Namespace = "other"
		Expect(newReconciler(nil).mapNetworkAttachmentToBareMetalInstance(ctx, na)).To(BeEmpty())
		na.Namespace = testNetworkingNamespace
		na.Spec.Target.Kind = opv1alpha1.NetworkAttachmentTargetComputeInstance
		Expect(newReconciler(nil).mapNetworkAttachmentToBareMetalInstance(ctx, na)).To(BeEmpty())
	})
})
