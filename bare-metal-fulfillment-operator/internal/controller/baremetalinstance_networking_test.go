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
	"errors"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// newTestNetworkAttachment returns the physical NetworkAttachment of interface
// iface of the BareMetalInstance with API ID bmiID.
func newTestNetworkAttachment(namespace, bmiID, iface string, finalizers ...string) *opv1alpha1.NetworkAttachment {
	return &opv1alpha1.NetworkAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, bmiID, iface),
			Namespace:  namespace,
			Finalizers: finalizers,
		},
		Spec: opv1alpha1.NetworkAttachmentSpec{
			Target: opv1alpha1.NetworkAttachmentTarget{Kind: opv1alpha1.NetworkAttachmentTargetBareMetalInstance, ID: bmiID},
			Subnet: "subnet-1",
			Interface: opv1alpha1.NetworkAttachmentInterface{
				Type: opv1alpha1.NetworkInterfacePhysical, Name: iface, MACAddress: "aa:bb:cc:dd:ee:ff",
			},
			SecurityGroups: []string{"sg-1"},
		},
	}
}

// setTestNetworkAttachmentReady marks a NetworkAttachment Ready and records jobs.
func setTestNetworkAttachmentReady(ctx context.Context, na *opv1alpha1.NetworkAttachment, jobs ...opv1alpha1.NetworkJobStatus) {
	meta.SetStatusCondition(&na.Status.Conditions, metav1.Condition{
		Type: opv1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue, Reason: "Succeeded",
	})
	na.Status.Jobs = jobs
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, na)).To(Succeed())
}

type networkAttachmentCreateErrorClient struct {
	client.Client
	err error
}

func (c networkAttachmentCreateErrorClient) Create(
	ctx context.Context, obj client.Object, opts ...client.CreateOption,
) error {
	if _, ok := obj.(*opv1alpha1.NetworkAttachment); ok {
		return c.err
	}
	return c.Client.Create(ctx, obj, opts...)
}

var _ = Describe("BareMetalInstance Networking", func() {
	const namespace = "default"

	var (
		ctx        context.Context
		reconciler *BareMetalInstanceReconciler
		bmi        *v1alpha1.BareMetalInstance
		bmiID      string
		macs       map[string]string
		macsErr    error
	)

	newBMI := func(attachments []v1alpha1.BareMetalNetworkAttachment) *v1alpha1.BareMetalInstance {
		return &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-bmi-net-",
				Namespace:    namespace,
				Labels:       map[string]string{bareMetalInstanceIDLabel: bmiID},
				Annotations:  map[string]string{tenantAnnotation: "tenant-1"},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				ExternalHostID:   "host-ns/host-123",
				ExternalHostName: "host-123",
				Selector: v1alpha1.HostSelectorSpec{
					HostSelector: map[string]string{"test": "value"},
				},
				HostClass:          "metal3",
				TemplateID:         "noop",
				NetworkAttachments: attachments,
			},
		}
	}

	attachmentKey := func() client.ObjectKey {
		return client.ObjectKey{
			Namespace: namespace,
			Name:      "baremetalinstance-" + bmiID + "-data-0",
		}
	}

	getAttachment := func() (*opv1alpha1.NetworkAttachment, error) {
		na := &opv1alpha1.NetworkAttachment{}
		return na, k8sClient.Get(ctx, attachmentKey(), na)
	}

	removeAttachment := func() {
		if bmi == nil {
			return
		}
		keys := []client.ObjectKey{
			attachmentKey(),
			{Namespace: namespace, Name: opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, bmiID, "")},
		}
		for _, key := range keys {
			na := &opv1alpha1.NetworkAttachment{}
			if err := k8sClient.Get(ctx, key, na); apierrors.IsNotFound(err) {
				continue
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			na.Finalizers = nil
			Expect(k8sClient.Update(ctx, na)).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, na))).To(Succeed())
		}
	}

	removeBMI := func() {
		if bmi == nil {
			return
		}
		latest := &v1alpha1.BareMetalInstance{}
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), latest)
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		latest.Finalizers = nil
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, latest))).To(Succeed())
	}

	attachments := []v1alpha1.BareMetalNetworkAttachment{{
		SubnetRef: "subnet-1", Interface: "data-0", Primary: true, SecurityGroupRefs: []string{"sg-1"},
	}}

	BeforeEach(func() {
		ctx = context.Background()
		bmiID = uuid.NewString()
		macs = map[string]string{"data-0": "AA-BB-CC-DD-EE-FF"}
		macsErr = nil
		reconciler = &BareMetalInstanceReconciler{
			Client:    k8sClient,
			APIReader: k8sClient,
			Scheme:    k8sClient.Scheme(),
			InventoryClient: &mockInventoryClient{
				getHostLogicalPortMACsFunc: func(_ context.Context, _ string) (map[string]string, error) {
					return macs, macsErr
				},
			},
			NetworkingEnabled:             true,
			ProvisionPollIntervalDuration: DefaultProvisionPollIntervalDuration,
		}
	})

	AfterEach(func() {
		removeAttachment()
		removeBMI()
	})

	Describe("reconcileNetworking", func() {
		It("skips when no network attachments are configured", func() {
			bmi = newBMI(nil)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			result, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("Skipped"))
		})

		It("skips when networking provisioning is disabled", func() {
			bmi = newBMI(attachments)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			reconciler.NetworkingEnabled = false

			result, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonProvisioningDisabled))
			_, err = getAttachment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("adds the finalizer, then creates the physical NetworkAttachment once", func() {
			bmi = newBMI(attachments)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			_, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())
			_, err = getAttachment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the finalizer is persisted before the NetworkAttachment exists")

			result, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(networkAttachmentStatusFor(bmi, "subnet-1").MACAddress).To(Equal("aa:bb:cc:dd:ee:ff"))
			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonNetworkAttachmentNotCreated))

			result, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			na, err := getAttachment()
			Expect(err).NotTo(HaveOccurred())
			Expect(na.Finalizers).To(ConsistOf(BareMetalInstanceNetworkingFinalizer))
			Expect(na.Annotations).To(HaveKeyWithValue(tenantAnnotation, "tenant-1"))
			Expect(na.Spec.Target).To(Equal(opv1alpha1.NetworkAttachmentTarget{
				Kind: opv1alpha1.NetworkAttachmentTargetBareMetalInstance, ID: bmiID,
			}))
			Expect(na.Spec.Subnet).To(Equal("subnet-1"))
			Expect(na.Spec.SecurityGroups).To(Equal([]string{"sg-1"}))
			Expect(na.Spec.Interface).To(Equal(opv1alpha1.NetworkAttachmentInterface{
				Type: opv1alpha1.NetworkInterfacePhysical, Name: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff",
			}))

			cond = bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))

			// A second reconcile finds the NetworkAttachment and does not recreate it.
			macs = map[string]string{}
			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			cond = bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))
		})

		It("records a rejected first create as known not to be applied", func() {
			bmi = newBMI(attachments)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			_, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred()) // Add the networking finalizer.
			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred()) // Record the selected interface MAC.

			createErr := apierrors.NewForbidden(
				schema.GroupResource{Resource: "networkattachments"}, "attachment",
				errors.New("access denied"),
			)
			reconciler.Client = networkAttachmentCreateErrorClient{Client: k8sClient, err: createErr}

			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).To(MatchError(ContainSubstring("access denied")))
			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonNetworkAttachmentNotCreated))
			Expect(cond.Message).To(ContainSubstring("NetworkAttachment creation was rejected"))
			Expect(networkAttachmentMayHaveBeenApplied(bmi)).To(BeFalse())
			Expect(networkAttachmentCreateMayBeInFlight(bmi)).To(BeFalse())
		})

		It("keeps a rejected retry uncertain when an earlier create may still be in flight", func() {
			bmi = newBMI(attachments)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			_, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred()) // Add the networking finalizer.
			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred()) // Record the selected interface MAC.
			setNetworkAttachmentsReady(bmi, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonNetworkAttachmentCreating, "Previous request may be in flight")
			Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())

			createErr := apierrors.NewForbidden(
				schema.GroupResource{Resource: "networkattachments"}, "attachment",
				errors.New("access denied"),
			)
			reconciler.Client = networkAttachmentCreateErrorClient{Client: k8sClient, err: createErr}

			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).To(MatchError(ContainSubstring("access denied")))
			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonNetworkAttachmentCreating))
			Expect(networkAttachmentCreateMayBeInFlight(bmi)).To(BeTrue())
		})

		It("retains ownership when a rejected replacement follows an applied attachment", func() {
			bmi = newBMI(attachments)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			_, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred()) // Add the networking finalizer.
			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred()) // Record the selected interface MAC.
			setNetworkAttachmentsReady(bmi, metav1.ConditionTrue, "Succeeded", "Previously applied")
			Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())

			createErr := apierrors.NewForbidden(
				schema.GroupResource{Resource: "networkattachments"}, "attachment",
				errors.New("access denied"),
			)
			reconciler.Client = networkAttachmentCreateErrorClient{Client: k8sClient, err: createErr}

			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).To(MatchError(ContainSubstring("access denied")))
			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonNetworkAttachmentMissing))
			Expect(networkAttachmentMayHaveBeenApplied(bmi)).To(BeTrue())
			Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())

			deletingBMI := &v1alpha1.BareMetalInstance{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), deletingBMI)).To(Succeed())

			result, done, err := reconciler.reconcileNetworkingDeletion(ctx, deletingBMI)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(result.RequeueAfter).To(Equal(NetworkOffboardPollInterval))
			Expect(controllerutil.ContainsFinalizer(deletingBMI, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())
		})

		It("does not create an attachment after deletion begins", func() {
			bmi = newBMI(attachments)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			_, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(networkAttachmentStatusFor(bmi, "subnet-1").MACAddress).To(Equal("aa:bb:cc:dd:ee:ff"))
			Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())

			Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())

			result, err := reconciler.reconcileNetworking(ctx, bmi)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(NetworkOffboardPollInterval))
			_, err = getAttachment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("releases the host when the inventory has no MAC for the interface", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			setNetworkAttachmentsReady(bmi, metav1.ConditionTrue, "Succeeded", "Previously ready")
			macs = map[string]string{"data-1": "aa:bb:cc:dd:ee:01"}

			result, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionAllocated)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonSelectedPortMACUnavailable))
			Expect(cond.Message).To(ContainSubstring(`"data-0"`))
			Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Reason).
				To(Equal("Succeeded"), "preserve prior network history")
			Expect(bmi.Spec.ExternalHostID).To(BeEmpty(),
				"release the host when the selected-port MAC is unavailable")
			_, err = getAttachment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("treats a single attachment without primary as the primary one", func() {
			bmi = newBMI([]v1alpha1.BareMetalNetworkAttachment{{
				SubnetRef: "subnet-1", Interface: "data-0", SecurityGroupRefs: []string{"sg-1"},
			}})
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			_, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			na, err := getAttachment()
			Expect(err).NotTo(HaveOccurred())
			Expect(na.Spec.Interface.Name).To(Equal("data-0"))
		})

		It("retries when the inventory backend fails", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			macsErr = errors.New("netbox unavailable")

			_, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).To(MatchError(ContainSubstring("netbox unavailable")))
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)).To(BeNil())
			_, err = getAttachment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("fails the attachment when the BareMetalInstance has no tenant", func() {
			bmi = newBMI(attachments)
			delete(bmi.Annotations, tenantAnnotation)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			result, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonTemplateFailed))
			Expect(cond.Message).To(ContainSubstring(tenantAnnotation))
			_, err = getAttachment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("fails the attachment when the interface name is not a DNS label", func() {
			bmi = newBMI([]v1alpha1.BareMetalNetworkAttachment{{SubnetRef: "subnet-1", Interface: "Data_0", Primary: true}})
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			result, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonTemplateFailed))
			Expect(cond.Message).To(ContainSubstring(`"Data_0"`))
		})

		It("mirrors a Ready NetworkAttachment", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			na := newTestNetworkAttachment(namespace, bmiID, "data-0", BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, na)).To(Succeed())
			setTestNetworkAttachmentReady(ctx, na)
			recorder := events.NewFakeRecorder(1)
			reconciler.EventRecorder = recorder

			result, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Eventually(recorder.Events).Should(Receive(ContainSubstring("Normal NetworkAttached Network attachment is ready")))
		})

		It("fails when the NetworkAttachment failed", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			na := newTestNetworkAttachment(namespace, bmiID, "data-0", BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, na)).To(Succeed())
			meta.SetStatusCondition(&na.Status.Conditions, metav1.Condition{
				Type: opv1alpha1.NetworkConditionReady, Status: metav1.ConditionFalse,
				Reason: opv1alpha1.ReasonFailed, Message: "no free port",
			})
			Expect(k8sClient.Status().Update(ctx, na)).To(Succeed())
			recorder := events.NewFakeRecorder(1)
			reconciler.EventRecorder = recorder

			result, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonNetworkAttachmentFailed))
			Expect(cond.Message).To(Equal("no free port"))
			Eventually(recorder.Events).Should(Receive(ContainSubstring("Warning NetworkAttachmentFailed no free port")))
		})

		It("reuses a legacy BMI-only NetworkAttachment and records its MAC", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			legacy := newTestNetworkAttachment(namespace, bmiID, "data-0", BareMetalInstanceNetworkingFinalizer)
			legacy.Name = opv1alpha1.NetworkAttachmentName(opv1alpha1.NetworkAttachmentTargetBareMetalInstance, bmiID, "")
			Expect(k8sClient.Create(ctx, legacy)).To(Succeed())

			result, err := reconciler.reconcileNetworking(ctx, bmi)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(networkAttachmentStatusFor(bmi, "subnet-1").MACAddress).To(Equal("aa:bb:cc:dd:ee:ff"))
			_, err = getAttachment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "legacy attachment is reused without a suffixed duplicate")
		})
	})

	Describe("reconcileNetworkingDeletion", func() {
		It("deletes the NetworkAttachment and waits for the networking controller", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			na := newTestNetworkAttachment(namespace, bmiID, "data-0",
				BareMetalInstanceNetworkingFinalizer, "osac.openshift.io/networkattachment")
			Expect(k8sClient.Create(ctx, na)).To(Succeed())

			// Persist cleanup intent before requesting NetworkAttachment deletion.
			result, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			na, err = getAttachment()
			Expect(err).NotTo(HaveOccurred())
			Expect(na.DeletionTimestamp).To(BeNil())
			Expect(networkAttachmentCleanupStarted(bmi)).To(BeTrue())
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Reason).
				To(Equal(v1alpha1.HostConditionReasonNetworkDetaching))

			// The next pass requests deletion after the cleanup intent is durable.
			_, done, err = reconciler.reconcileNetworkingDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			na, err = getAttachment()
			Expect(err).NotTo(HaveOccurred())
			Expect(na.DeletionTimestamp).NotTo(BeNil())

			// While the networking controller cleans up, BMF keeps its finalizer.
			_, done, err = reconciler.reconcileNetworkingDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			na, err = getAttachment()
			Expect(err).NotTo(HaveOccurred())
			Expect(na.Finalizers).To(ContainElement(BareMetalInstanceNetworkingFinalizer))

			// The networking controller finishes and removes its finalizer.
			controllerutil.RemoveFinalizer(na, "osac.openshift.io/networkattachment")
			Expect(k8sClient.Update(ctx, na)).To(Succeed())

			// BMF removes its finalizer from the NetworkAttachment, then from the BMI.
			_, done, err = reconciler.reconcileNetworkingDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			_, err = getAttachment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			_, done, err = reconciler.reconcileNetworkingDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
		})

		It("completes at once when no NetworkAttachment exists", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			_, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
		})

		It("retains host ownership if an applied attachment is missing before cleanup starts", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			setNetworkAttachmentsReady(bmi, metav1.ConditionTrue, "Succeeded", "Network attachment ready")

			result, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(result.RequeueAfter).To(Equal(NetworkOffboardPollInterval))
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())
			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonNetworkAttachmentMissing))
			Expect(cond.Message).To(ContainSubstring("retaining host ownership"))
		})

		It("recognizes the legacy cleanup reason", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			na := newTestNetworkAttachment(namespace, bmiID, "data-0", BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, na)).To(Succeed())
			setNetworkAttachmentsReady(bmi, metav1.ConditionFalse,
				"NetworkAttachmentCleanupStarted", "Cleanup started by an older BMF version")
			Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())

			_, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(networkAttachmentCleanupStarted(bmi)).To(BeTrue())
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Reason).
				To(Equal("NetworkAttachmentCleanupStarted"))
			na, err = getAttachment()
			Expect(err).NotTo(HaveOccurred())
			Expect(na.DeletionTimestamp).NotTo(BeNil())
		})

		DescribeTable("retains host ownership if a possibly applied attachment is missing",
			func(reason string) {
				bmi = newBMI(attachments)
				controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				setNetworkAttachmentsReady(bmi, metav1.ConditionFalse, reason, "Network attachment may have been applied")

				result, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeFalse())
				Expect(result.RequeueAfter).To(Equal(NetworkOffboardPollInterval))
				Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())
				cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
				Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonNetworkAttachmentMissing))
			},
			Entry("legacy Progressing state after create", v1alpha1.HostConditionReasonProgressing),
			Entry("failed networking apply", v1alpha1.HostConditionReasonNetworkAttachmentFailed),
		)

		DescribeTable("releases host ownership when creation is known not to have happened",
			func(reason string) {
				bmi = newBMI(attachments)
				controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				setNetworkAttachmentsReady(bmi, metav1.ConditionFalse, reason, "NetworkAttachment was not created")

				_, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeTrue())
				Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
			},
			Entry("current marker", v1alpha1.HostConditionReasonNetworkAttachmentNotCreated),
			Entry("legacy MAC recording reason", "NetworkAttachmentRecordingMAC"),
			Entry("legacy rejected create reason", "NetworkAttachmentCreateRejected"),
			Entry("legacy skipped creation reason", "NetworkAttachmentCreationSkipped"),
		)

		It("waits for an uncertain create and deletes both current and legacy names", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			setNetworkAttachmentsReady(bmi, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonNetworkAttachmentCreating, "Create request may be in flight")

			result, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(result.RequeueAfter).To(Equal(NetworkOffboardPollInterval))
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())

			suffixed := newTestNetworkAttachment(namespace, bmiID, "data-0", BareMetalInstanceNetworkingFinalizer)
			legacy := suffixed.DeepCopy()
			legacy.Name = opv1alpha1.NetworkAttachmentName(
				opv1alpha1.NetworkAttachmentTargetBareMetalInstance, bmiID, "")
			Expect(k8sClient.Create(ctx, suffixed)).To(Succeed())
			Expect(k8sClient.Create(ctx, legacy)).To(Succeed())

			for range 5 {
				_, done, err = reconciler.reconcileNetworkingDeletion(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				if done {
					break
				}
			}
			Expect(done).To(BeTrue())
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
			_, err = getAttachment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
			legacyKey := client.ObjectKey{Namespace: namespace, Name: legacy.Name}
			Expect(k8sClient.Get(ctx, legacyKey, &opv1alpha1.NetworkAttachment{})).To(Satisfy(apierrors.IsNotFound))
		})

		It("is done when the networking finalizer is absent", func() {
			bmi = newBMI(attachments)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			_, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
		})
	})
})

var _ = Describe("normalizeMAC", func() {
	It("accepts colon and dash separators in any case", func() {
		Expect(normalizeMAC("AA:BB:CC:DD:EE:FF")).To(Equal("aa:bb:cc:dd:ee:ff"))
		Expect(normalizeMAC("aa-bb-cc-dd-ee-ff")).To(Equal("aa:bb:cc:dd:ee:ff"))
	})

	It("rejects malformed addresses", func() {
		for _, mac := range []string{"", "aa:bb:cc", "zz:bb:cc:dd:ee:ff", "aa:bb:cc:dd:ee:ff:00:11"} {
			_, err := normalizeMAC(mac)
			Expect(err).To(HaveOccurred(), mac)
		}
	})
})
