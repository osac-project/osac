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

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("BareMetalInstance IP Discovery", func() {
	const namespace = "default"

	var (
		ctx        context.Context
		reconciler *BareMetalInstanceReconciler
		bmi        *v1alpha1.BareMetalInstance
		bmiID      string
		attachment *opv1alpha1.NetworkAttachment
	)

	newBMI := func(attachments []v1alpha1.BareMetalNetworkAttachment) *v1alpha1.BareMetalInstance {
		return &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bmiID,
				Namespace: namespace,
				Labels:    map[string]string{bareMetalInstanceIDLabel: bmiID},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				ExternalHostID:     "host-ns/host-123",
				NetworkAttachments: attachments,
			},
		}
	}

	attachments := []v1alpha1.BareMetalNetworkAttachment{{
		SubnetRef: "subnet-1", Interface: "data-0", Primary: true, SecurityGroupRefs: []string{"sg-1"},
	}}

	handoffComplete := func(b *v1alpha1.BareMetalInstance) {
		b.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete, metav1.ConditionTrue, "Succeeded", "done")
	}

	BeforeEach(func() {
		ctx = context.Background()
		bmiID = uuid.NewString()
		attachment = nil
		reconciler = &BareMetalInstanceReconciler{
			Client:                        k8sClient,
			APIReader:                     k8sClient,
			Scheme:                        k8sClient.Scheme(),
			NetworkingEnabled:             true,
			ProvisionPollIntervalDuration: DefaultProvisionPollIntervalDuration,
		}
	})

	AfterEach(func() {
		if attachment != nil {
			_ = k8sClient.Delete(ctx, attachment)
		}
	})

	createAttachment := func(lease *opv1alpha1.NetworkAttachmentDHCPLease, jobs ...opv1alpha1.NetworkJobStatus) {
		attachment = newTestNetworkAttachment(namespace, bmiID, "data-0")
		Expect(k8sClient.Create(ctx, attachment)).To(Succeed())
		attachment.Status.DHCPLease = lease
		setTestNetworkAttachmentReady(ctx, attachment, jobs...)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), attachment)).To(Succeed())
	}

	It("skips when no network attachments are configured", func() {
		bmi = newBMI(nil)
		_, err := reconciler.reconcileIPDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("Skipped"))
	})

	It("waits for the network handoff", func() {
		bmi = newBMI(attachments)
		result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)).To(BeNil())
	})

	It("skips when networking provisioning is disabled", func() {
		bmi = newBMI(attachments)
		handoffComplete(bmi)
		reconciler.NetworkingEnabled = false
		_, err := reconciler.reconcileIPDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("Skipped"))
	})

	It("mirrors the accepted DHCP lease of the NetworkAttachment", func() {
		bmi = newBMI(attachments)
		handoffComplete(bmi)
		createAttachment(&opv1alpha1.NetworkAttachmentDHCPLease{IPAddress: "10.0.1.130", MACAddress: "aa:bb:cc:dd:ee:ff"})

		result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsZero()).To(BeTrue())

		cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(bmi.Status.NetworkAttachmentStatuses).To(Equal([]v1alpha1.BareMetalNetworkAttachmentStatus{{
			SubnetRef: "subnet-1", Interface: "data-0", Primary: true, IPAddress: "10.0.1.130",
		}}))
	})

	It("keeps waiting while the lease query has not succeeded", func() {
		bmi = newBMI(attachments)
		handoffComplete(bmi)
		createAttachment(nil, opv1alpha1.NetworkJobStatus{
			Task: "query_dhcp_lease", Manager: opv1alpha1.NetworkJobManagerFabric, Attempt: 2,
			State: opv1alpha1.NetworkJobStateRetrying, Message: "no DHCP lease yet",
		})

		result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonProgressing))
		Expect(bmi.Status.NetworkAttachmentStatuses).To(BeEmpty())
	})

	It("fails when the lease query failed finally", func() {
		bmi = newBMI(attachments)
		handoffComplete(bmi)
		createAttachment(nil, opv1alpha1.NetworkJobStatus{
			Task: "query_dhcp_lease", Manager: opv1alpha1.NetworkJobManagerFabric, Attempt: 1,
			State: opv1alpha1.NetworkJobStateFailed, Message: "two leases match",
		})

		result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsZero()).To(BeTrue())
		cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonTemplateFailed))
		Expect(cond.Message).To(Equal("two leases match"))
	})

	It("fails when the NetworkAttachment does not exist", func() {
		bmi = newBMI(attachments)
		handoffComplete(bmi)

		_, err := reconciler.reconcileIPDiscovery(ctx, bmi)
		Expect(err).NotTo(HaveOccurred())
		cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
		Expect(cond.Reason).To(Equal(v1alpha1.HostConditionReasonTemplateFailed))
	})
})
