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
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

type networkAttachmentCreateErrorClient struct {
	client.Client
	err   error
	calls *int
}

func (c networkAttachmentCreateErrorClient) Create(
	ctx context.Context, obj client.Object, opts ...client.CreateOption,
) error {
	if c.calls != nil {
		(*c.calls)++
	}
	return c.err
}

// networkAttachmentReadRecorder records the keys of NetworkAttachment reads.
type networkAttachmentReadRecorder struct {
	client.Reader
	keys *[]client.ObjectKey
}

func (r networkAttachmentReadRecorder) Get(
	ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption,
) error {
	if _, ok := obj.(*opv1alpha1.NetworkAttachment); ok {
		*r.keys = append(*r.keys, key)
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

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
		na := &opv1alpha1.NetworkAttachment{}
		if err := k8sClient.Get(ctx, attachmentKey(), na); err != nil {
			return
		}
		na.Finalizers = nil
		_ = k8sClient.Update(ctx, na)
		_ = k8sClient.Delete(ctx, na)
	}

	removeBMI := func() {
		latest := &v1alpha1.BareMetalInstance{}
		if k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), latest) == nil {
			latest.Finalizers = nil
			_ = k8sClient.Update(ctx, latest)
			_ = k8sClient.Delete(ctx, latest)
		}
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

	Describe("networkingRequested", func() {
		It("returns false when no network attachments are configured", func() {
			bmi = newBMI(nil)

			Expect(reconciler.networkingRequested(bmi)).To(BeFalse())
		})

		It("returns true when network attachments are configured", func() {
			bmi = newBMI(attachments)
			reconciler.NetworkingEnabled = false

			Expect(reconciler.networkingRequested(bmi)).To(BeTrue())
		})
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

		It("marks provisioning disabled when no attachment was acquired", func() {
			bmi = newBMI(attachments)
			bmi.SetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete,
				metav1.ConditionTrue, "Succeeded", "Provisioning complete")
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			reconciler.NetworkingEnabled = false

			result, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("ProvisioningDisabled"))
			handoff := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)
			Expect(handoff.Status).To(Equal(metav1.ConditionTrue))
			Expect(handoff.Reason).To(Equal("ProvisioningDisabled"))
			Expect(handoff.Message).To(ContainSubstring("handoff"))
			discovery := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
			Expect(discovery.Status).To(Equal(metav1.ConditionTrue))
			Expect(discovery.Reason).To(Equal("ProvisioningDisabled"))
			Expect(discovery.Message).To(ContainSubstring("lease"))
			Expect(bmi.Status.NetworkAttachmentStatuses).To(BeEmpty())
			Expect(reconciler.InventoryClient.(*mockInventoryClient).getHostLogicalPortMACsCalled).To(BeZero())
			_, err = getAttachment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("preserves acquired attachment status when networking is disabled", func() {
			bmi = newBMI(attachments)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
				SubnetRef: "subnet-1", Interface: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff",
			}}
			setNetworkAttachmentsReady(bmi, metav1.ConditionTrue, "Succeeded", "Network attachment is ready")
			reconciler.NetworkingEnabled = false

			result, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			Expect(bmi.Status.NetworkAttachmentStatuses).To(Equal([]v1alpha1.BareMetalNetworkAttachmentStatus{{
				SubnetRef: "subnet-1", Interface: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff",
			}}))
			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal("Succeeded"))
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)).To(BeNil())
			Expect(reconciler.InventoryClient.(*mockInventoryClient).getHostLogicalPortMACsCalled).To(BeZero())
		})

		It("retries a transient create error", func() {
			bmi = newBMI(attachments)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			_, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(networkAttachmentStatusFor(bmi, "subnet-1").MACAddress).To(Equal("aa:bb:cc:dd:ee:ff"))
			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Reason).
				To(Equal(v1alpha1.HostConditionReasonProgressing))
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Message).
				To(Equal(networkAttachmentCreatingMessage))

			reconciler.Client = networkAttachmentCreateErrorClient{
				Client: k8sClient,
				err:    errors.New("create request timed out"),
			}
			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).To(MatchError(ContainSubstring("create request timed out")))
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Reason).
				To(Equal(v1alpha1.HostConditionReasonProgressing), "create intent is kept after a transient Create error")
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
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)).To(BeNil())

			result, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Message).
				To(Equal(networkAttachmentCreatingMessage))
			_, err = getAttachment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "create intent is persisted before Create")

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

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))
			Expect(cond.Message).To(Equal("Waiting for the network attachment"))

			// A second reconcile finds the NetworkAttachment and does not recreate it.
			macs = map[string]string{}
			_, err = reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			cond = bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))
		})

		It("fails the attachment when the inventory has no MAC for the interface", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			macs = map[string]string{"data-1": "aa:bb:cc:dd:ee:01"}
			unassignCalls := 0
			reconciler.InventoryClient.(*mockInventoryClient).unassignHostFunc = func(
				_ context.Context, _, _ string, _ []string,
			) error {
				unassignCalls++
				return nil
			}

			result, err := reconciler.reconcileNetworking(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			cond := bmi.GetStatusCondition(v1alpha1.HostConditionAllocated)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonSelectedPortMACUnavailable))
			Expect(cond.Message).To(ContainSubstring(`"data-0"`))
			Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
			Expect(bmi.Spec.ExternalHostID).To(Equal("host-ns/host-123"))
			Expect(unassignCalls).To(BeZero())
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
			Expect(<-recorder.Events).To(ContainSubstring("Normal NetworkAttached Network attachment is ready"))
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
			Expect(<-recorder.Events).To(ContainSubstring("Warning NetworkAttachmentFailed no free port"))
		})

	})

	Describe("getNetworkAttachmentForDeletion", func() {
		var reads []client.ObjectKey

		BeforeEach(func() {
			reads = nil
			reconciler.APIReader = networkAttachmentReadRecorder{Reader: k8sClient, keys: &reads}
		})

		It("reads the current attachment key", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			current := newTestNetworkAttachment(namespace, bmiID, "data-0")
			Expect(k8sClient.Create(ctx, current)).To(Succeed())

			observed, err := reconciler.getNetworkAttachmentForDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(client.ObjectKeyFromObject(observed)).To(Equal(attachmentKey()))
			Expect(reads).To(Equal([]client.ObjectKey{attachmentKey()}))
		})

		DescribeTable("does not read when BMF owns no network cleanup",
			func(finalizer bool, requested []v1alpha1.BareMetalNetworkAttachment) {
				bmi = newBMI(requested)
				if finalizer {
					controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
				}
				na := newTestNetworkAttachment(namespace, bmiID, "data-0")
				Expect(k8sClient.Create(ctx, na)).To(Succeed())

				observed, err := reconciler.getNetworkAttachmentForDeletion(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(observed).To(BeNil())
				Expect(reads).To(BeEmpty())
			},
			Entry("without the networking finalizer", false, attachments),
			Entry("without requested attachments", true, nil),
		)

		It("reads owned attachments when networking provisioning is disabled", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			reconciler.NetworkingEnabled = false
			na := newTestNetworkAttachment(namespace, bmiID, "data-0")
			Expect(k8sClient.Create(ctx, na)).To(Succeed())

			observed, err := reconciler.getNetworkAttachmentForDeletion(ctx, bmi)
			Expect(err).NotTo(HaveOccurred())
			Expect(client.ObjectKeyFromObject(observed)).To(Equal(attachmentKey()))
			Expect(reads).To(Equal([]client.ObjectKey{attachmentKey()}))
		})
	})

	Describe("reconcileNetworkingDeletion", func() {
		// Observe once and reuse that result for cleanup, as handleDeletion does.
		deletionPass := func() (ctrl.Result, bool, error) {
			observed, err := reconciler.getNetworkAttachmentForDeletion(ctx, bmi)
			if err != nil {
				return ctrl.Result{}, false, err
			}
			return reconciler.reconcileNetworkingDeletion(ctx, bmi, observed)
		}

		It("deletes the NetworkAttachment and waits for the networking controller", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			na := newTestNetworkAttachment(namespace, bmiID, "data-0",
				BareMetalInstanceNetworkingFinalizer, "osac.openshift.io/networkattachment")
			Expect(k8sClient.Create(ctx, na)).To(Succeed())
			naKey := client.ObjectKeyFromObject(na)

			// Delete the observed attachment on this pass.
			result, done, err := deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Reason).
				To(Equal(v1alpha1.HostConditionReasonNetworkDetaching))
			na = &opv1alpha1.NetworkAttachment{}
			err = k8sClient.Get(ctx, naKey, na)
			Expect(err).NotTo(HaveOccurred())
			Expect(na.DeletionTimestamp).NotTo(BeNil())

			// While the networking controller cleans up, BMF keeps its finalizer.
			_, done, err = deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			na = &opv1alpha1.NetworkAttachment{}
			err = k8sClient.Get(ctx, naKey, na)
			Expect(err).NotTo(HaveOccurred())
			Expect(na.Finalizers).To(ContainElement(BareMetalInstanceNetworkingFinalizer))

			// The networking controller finishes and removes its finalizer.
			controllerutil.RemoveFinalizer(na, "osac.openshift.io/networkattachment")
			Expect(k8sClient.Update(ctx, na)).To(Succeed())

			// BMF removes its finalizer from the NetworkAttachment, then from the BMI.
			_, done, err = deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			na = &opv1alpha1.NetworkAttachment{}
			err = k8sClient.Get(ctx, naKey, na)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
				metav1.ConditionTrue, "Succeeded", "Handoff completed")
			bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete,
				metav1.ConditionTrue, "Succeeded", "Lease discovery completed")
			_, done, err = deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
		})

		It("records NetworkDetaching before removing the attachment finalizer", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			na := newTestNetworkAttachment(namespace, bmiID, "data-0", BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, na)).To(Succeed())
			naKey := client.ObjectKeyFromObject(na)
			Expect(k8sClient.Delete(ctx, na)).To(Succeed())
			setNetworkAttachmentsReady(bmi, metav1.ConditionTrue, "Succeeded", "Network attachment is ready")
			bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
				metav1.ConditionTrue, "Succeeded", "Handoff completed")
			bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete,
				metav1.ConditionTrue, "Succeeded", "Lease discovery completed")

			result, done, err := deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(result.RequeueAfter).To(Equal(networkRetryInterval))
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Reason).
				To(Equal(v1alpha1.HostConditionReasonNetworkDetaching))
			na = &opv1alpha1.NetworkAttachment{}
			Expect(k8sClient.Get(ctx, naKey, na)).To(Succeed())
			Expect(na.Finalizers).To(ContainElement(BareMetalInstanceNetworkingFinalizer))

			_, done, err = deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())

			_, done, err = deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
		})

		It("completes at once when no NetworkAttachment exists", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			_, done, err := deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
		})

		It("is done when the networking finalizer is absent", func() {
			bmi = newBMI(attachments)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			_, done, err := deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
		})

		It("retains ownership when create may still produce an attachment", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
				SubnetRef: "subnet-1", Interface: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff",
			}}
			setNetworkAttachmentsReady(bmi, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonProgressing, "pending attachment creation")

			createCalls := 0
			reconciler.Client = networkAttachmentCreateErrorClient{
				Client: k8sClient, err: errors.New("create request timed out"), calls: &createCalls,
			}
			_, err := reconciler.reconcileNetworkAttachmentCreation(ctx, bmi, &attachments[0], attachmentKey())
			Expect(err).To(HaveOccurred())
			Expect(createCalls).To(Equal(1))

			reconciler.Client = k8sClient
			result, done, err := deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse(), "uncertain create retains network cleanup ownership")
			Expect(result.RequeueAfter).To(Equal(networkRetryInterval))
			Expect(createCalls).To(Equal(1), "deletion must not retry Create")
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())
		})

		It("releases ownership after a definitive create rejection", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
				SubnetRef: "subnet-1", Interface: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff",
			}}
			createCalls := 0
			reconciler.Client = networkAttachmentCreateErrorClient{
				Client: k8sClient,
				err:    apierrors.NewBadRequest("malformed NetworkAttachment"),
				calls:  &createCalls,
			}

			for range 3 {
				_, err := reconciler.reconcileNetworkAttachmentCreation(ctx, bmi, &attachments[0], attachmentKey())
				Expect(err).NotTo(HaveOccurred())
			}
			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonNetworkAttachmentFailed))
			Expect(cond.Message).To(Equal(networkAttachmentCreateRejectedMessage))
			Expect(cond.ObservedGeneration).To(Equal(bmi.Generation))

			_, err := reconciler.reconcileNetworkAttachmentCreation(ctx, bmi, &attachments[0], attachmentKey())
			Expect(err).NotTo(HaveOccurred())
			Expect(createCalls).To(Equal(1), "a rejected request is not retried for the same BMI generation")

			reconciler.Client = k8sClient
			_, done, err := deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
		})

		It("retries create after Forbidden until authorization is restored", func() {
			bmi = newBMI(attachments)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
				SubnetRef: "subnet-1", Interface: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff",
			}}
			createCalls := 0
			reconciler.Client = networkAttachmentCreateErrorClient{
				Client: k8sClient,
				err:    apierrors.NewForbidden(schema.GroupResource{Resource: "networkattachments"}, "test", errors.New("denied")),
				calls:  &createCalls,
			}

			result, err := reconciler.reconcileNetworkAttachmentCreation(ctx, bmi, &attachments[0], attachmentKey())
			Expect(err).NotTo(HaveOccurred())
			Expect(createCalls).To(Equal(0), "create intent is persisted before Create")
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Message).
				To(Equal(networkAttachmentCreatingMessage))

			result, err = reconciler.reconcileNetworkAttachmentCreation(ctx, bmi, &attachments[0], attachmentKey())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(networkRetryInterval))
			cond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))
			Expect(cond.Message).To(ContainSubstring("authorization"))
			Expect(createCalls).To(Equal(1))

			result, err = reconciler.reconcileNetworkAttachmentCreation(ctx, bmi, &attachments[0], attachmentKey())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(networkRetryInterval))
			Expect(createCalls).To(Equal(2), "authorization failures are retried without a generation bump")

			reconciler.Client = k8sClient
			result, err = reconciler.reconcileNetworkAttachmentCreation(ctx, bmi, &attachments[0], attachmentKey())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(createCalls).To(Equal(2))
			_, err = getAttachment()
			Expect(err).NotTo(HaveOccurred())
		})

		It("completes delete when the tenant annotation is missing", func() {
			bmi = newBMI(attachments)
			delete(bmi.Annotations, tenantAnnotation)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
				SubnetRef: "subnet-1", Interface: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff",
			}}

			result, err := reconciler.reconcileNetworkAttachmentCreation(ctx, bmi, &attachments[0], attachmentKey())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady).Reason).
				To(Equal(v1alpha1.HostConditionReasonTemplateFailed))

			_, done, err := deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
		})

		It("cleans up a late-appearing attachment after an uncertain create", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
				SubnetRef: "subnet-1", Interface: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff",
			}}
			setNetworkAttachmentsReady(bmi, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonProgressing, "Waiting for the network attachment")

			// First pass observes NotFound while create may still commit.
			result, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(result.RequeueAfter).To(Equal(networkRetryInterval))
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeTrue())

			na := newTestNetworkAttachment(namespace, bmiID, "data-0", BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, na)).To(Succeed())
			naKey := client.ObjectKeyFromObject(na)

			result, done, err = deletionPass()
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			stored := &opv1alpha1.NetworkAttachment{}
			Expect(k8sClient.Get(ctx, naKey, stored)).To(Succeed())
			Expect(stored.DeletionTimestamp).NotTo(BeNil())
		})

		It("finishes network cleanup when a previously applied attachment is already gone", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			setNetworkAttachmentsReady(bmi, metav1.ConditionTrue, "Succeeded", "Network attachment is ready")

			_, done, err := reconciler.reconcileNetworkingDeletion(ctx, bmi, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
		})

		It("releases ownership when only the selected MAC is recorded", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
				SubnetRef: "subnet-1", Interface: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff",
			}}

			_, done, err := deletionPass()

			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue(), "MAC alone means Create was never sent")
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
		})

		It("releases ownership after the create visibility window expires", func() {
			bmi = newBMI(attachments)
			controllerutil.AddFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
				SubnetRef: "subnet-1", Interface: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff",
			}}
			setNetworkAttachmentsReady(bmi, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonProgressing, networkAttachmentCreatingMessage)
			ready := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
			ready.LastTransitionTime = metav1.NewTime(time.Now().Add(-networkAttachmentCreateVisibilityTimeout - time.Second))

			_, done, err := deletionPass()

			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue(), "uncertain create finishes after the visibility window")
			Expect(controllerutil.ContainsFinalizer(bmi, BareMetalInstanceNetworkingFinalizer)).To(BeFalse())
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
