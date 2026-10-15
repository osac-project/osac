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
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

var _ = Describe("BareMetalInstance network cleanup ownership", func() {
	const osacCleanupFinalizer = "osac.openshift.io/baremetalinstance-cleanup"
	var (
		ctx        context.Context
		bmi        *v1alpha1.BareMetalInstance
		eip        *opv1alpha1.ExternalIP
		attachment *opv1alpha1.ExternalIPAttachment
		reconciler *BareMetalInstanceReconciler
		unassigned bool
	)

	BeforeEach(func() {
		ctx = context.Background()
		bmi = &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-bmi-ownership-",
				Namespace:    "default",
				Labels:       map[string]string{"osac.openshift.io/baremetalinstance-uuid": "ownership-test-id"},
				Finalizers:   []string{BareMetalInstanceInventoryFinalizer, BareMetalInstanceManagementFinalizer},
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				ExternalHostID: "host-ownership",
				HostClass:      "test-class",
				TemplateID:     shared.OsacNoopTemplate,
				Selector:       v1alpha1.HostSelectorSpec{HostSelector: map[string]string{"test": "value"}},
			},
		}
		Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
		labels := map[string]string{
			"osac.openshift.io/auto-created":     "true",
			"osac.openshift.io/auto-created-for": "ownership-test-id",
		}
		eip = &opv1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-eip-ownership-", Namespace: bmi.Namespace, Labels: labels},
			Spec:       opv1alpha1.ExternalIPSpec{Pool: "test-pool"},
		}
		Expect(k8sClient.Create(ctx, eip)).To(Succeed())
		id := "ownership-test-id"
		attachment = &opv1alpha1.ExternalIPAttachment{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-eia-ownership-", Namespace: bmi.Namespace, Labels: labels},
			Spec:       opv1alpha1.ExternalIPAttachmentSpec{ExternalIP: eip.Name, BaremetalInstance: &id},
		}
		Expect(k8sClient.Create(ctx, attachment)).To(Succeed())
		unassigned = false
		reconciler = &BareMetalInstanceReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			InventoryClient: &mockInventoryClient{unassignHostFunc: func(_ context.Context, hostID string, _ []string) error {
				Expect(hostID).To(Equal("host-ownership"))
				unassigned = true
				return nil
			}},
			ManagementClient: &mockManagementClient{},
		}
	})

	AfterEach(func() {
		_ = k8sClient.Delete(ctx, attachment)
		_ = k8sClient.Delete(ctx, eip)
		fresh := &v1alpha1.BareMetalInstance{}
		if k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), fresh) == nil {
			fresh.Finalizers = nil
			_ = k8sClient.Update(ctx, fresh)
			_ = k8sClient.Delete(ctx, fresh)
		}
	})

	It("does not arm the OSAC cleanup finalizer when external resources exist", func() {
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bmi)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsZero()).To(BeTrue())
		fresh := &v1alpha1.BareMetalInstance{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), fresh)).To(Succeed())
		Expect(controllerutil.ContainsFinalizer(fresh, osacCleanupFinalizer)).To(BeFalse())
	})

	It("waits on OSAC cleanup, then completes host teardown without changing external resources", func() {
		controllerutil.AddFinalizer(bmi, osacCleanupFinalizer)
		Expect(k8sClient.Update(ctx, bmi)).To(Succeed())
		Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())

		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bmi)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(unassigned).To(BeFalse())
		fresh := &v1alpha1.BareMetalInstance{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), fresh)).To(Succeed())
		Expect(fresh.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseDeleting))
		Expect(fresh.GetFinalizers()).To(ContainElements(
			osacCleanupFinalizer, BareMetalInstanceInventoryFinalizer, BareMetalInstanceManagementFinalizer))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), &opv1alpha1.ExternalIP{})).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), &opv1alpha1.ExternalIPAttachment{})).To(Succeed())

		controllerutil.RemoveFinalizer(fresh, osacCleanupFinalizer)
		Expect(k8sClient.Update(ctx, fresh)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(bmi)})
		Expect(err).NotTo(HaveOccurred())
		Expect(unassigned).To(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), &opv1alpha1.ExternalIP{})).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), &opv1alpha1.ExternalIPAttachment{})).To(Succeed())
	})

	It("does not revive a deleting instance after an in-flight create and resumes cleanup after restart", func() {
		bmi.Spec.TemplateID = "test_template"
		controllerutil.AddFinalizer(bmi, osacCleanupFinalizer)
		Expect(k8sClient.Update(ctx, bmi)).To(Succeed())

		provisionStarted := make(chan struct{})
		releaseProvision := make(chan struct{})
		var startOnce sync.Once
		var providerMu sync.Mutex
		provisionState := opv1alpha1.JobStateRunning
		deprovisionState := opv1alpha1.JobStatePending
		deprovisionStatusFailures := 0
		deprovisionJobTriggers := 0
		provider := &mockProvisioningProvider{
			triggerProvisionFunc: func(ctx context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				startOnce.Do(func() { close(provisionStarted) })
				select {
				case <-releaseProvision:
					return &provisioning.ProvisionResult{
						JobID: "create-job", InitialState: opv1alpha1.JobStateRunning, Message: "create running",
					}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				providerMu.Lock()
				defer providerMu.Unlock()
				return provisioning.ProvisionStatus{JobID: jobID, State: provisionState, Message: "create status"}, nil
			},
			triggerDeprovisionFunc: func(_ context.Context, _ client.Object, jobs []opv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				providerMu.Lock()
				defer providerMu.Unlock()
				latestProvision := provisioning.FindLatestJobByType(jobs, opv1alpha1.JobTypeProvision)
				if latestProvision != nil && !latestProvision.State.IsTerminal() {
					provisionState = opv1alpha1.JobStateCanceled
					return &provisioning.DeprovisionResult{
						Action: provisioning.DeprovisionWaiting,
						ProvisionJobStatus: &provisioning.ProvisionStatus{
							JobID: latestProvision.JobID, State: opv1alpha1.JobStateCanceled, Message: "create canceled",
						},
					}, nil
				}
				deprovisionJobTriggers++
				deprovisionState = opv1alpha1.JobStatePending
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionTriggered, JobID: "cleanup-job", BlockDeletionOnFailure: true,
				}, nil
			},
			getDeprovisionStatusFunc: func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				providerMu.Lock()
				defer providerMu.Unlock()
				if deprovisionStatusFailures == 0 {
					deprovisionStatusFailures++
					return provisioning.ProvisionStatus{}, errors.New("temporary deprovision status failure")
				}
				return provisioning.ProvisionStatus{JobID: jobID, State: deprovisionState, Message: "cleanup status"}, nil
			},
		}
		reconciler = NewBareMetalInstanceReconciler(
			k8sClient, k8sClient.Scheme(), reconciler.InventoryClient, reconciler.ManagementClient,
			provider, nil, nil, nil, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond,
		)

		key := client.ObjectKeyFromObject(bmi)
		reconcileResult := make(chan error, 1)
		go func() {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			reconcileResult <- err
		}()
		select {
		case <-provisionStarted:
		case <-time.After(5 * time.Second):
			Fail("provisioning provider was not called")
		}

		By("accepting deletion while the provider create call is still in flight")
		Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
		close(releaseProvision)
		Expect(<-reconcileResult).NotTo(HaveOccurred())

		latest := &v1alpha1.BareMetalInstance{}
		Expect(k8sClient.Get(ctx, key, latest)).To(Succeed())
		Expect(latest.GetDeletionTimestamp()).NotTo(BeZero())
		Expect(latest.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseDeleting))
		Expect(latest.Status.ProvisioningJobs).To(HaveLen(1))
		Expect(latest.GetFinalizers()).To(ContainElements(
			osacCleanupFinalizer, BareMetalInstanceManagementFinalizer, BareMetalInstanceInventoryFinalizer))

		By("holding provider and host cleanup behind the networking finalizer")
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		providerMu.Lock()
		deprovisionTriggers := deprovisionJobTriggers
		providerMu.Unlock()
		Expect(deprovisionTriggers).To(BeZero())
		Expect(k8sClient.Get(ctx, key, latest)).To(Succeed())
		Expect(latest.GetFinalizers()).To(ContainElement(osacCleanupFinalizer))

		By("starting provider cleanup after networking has finished")
		controllerutil.RemoveFinalizer(latest, osacCleanupFinalizer)
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		result, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		latest = &v1alpha1.BareMetalInstance{}
		Expect(k8sClient.Get(ctx, key, latest)).To(Succeed())
		Expect(latest.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseDeleting))
		Expect(latest.Status.ProvisioningJobs[0].State).To(Equal(opv1alpha1.JobStateCanceled))
		Expect(latest.GetFinalizers()).To(ContainElements(
			BareMetalInstanceManagementFinalizer, BareMetalInstanceInventoryFinalizer))

		By("persisting one deprovision job, then restarting the reconciler after a transient status failure")
		result, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		latest = &v1alpha1.BareMetalInstance{}
		Expect(k8sClient.Get(ctx, key, latest)).To(Succeed())
		Expect(latest.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseDeleting))
		Expect(latest.Status.ProvisioningJobs).To(HaveLen(2))
		Expect(latest.Status.ProvisioningJobs[1].JobID).To(Equal("cleanup-job"))
		Expect(latest.GetFinalizers()).To(ContainElements(
			BareMetalInstanceManagementFinalizer, BareMetalInstanceInventoryFinalizer))

		restartedReconciler := NewBareMetalInstanceReconciler(
			k8sClient, k8sClient.Scheme(), reconciler.InventoryClient, reconciler.ManagementClient,
			provider, nil, nil, nil, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond,
		)
		result, err = restartedReconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		providerMu.Lock()
		deprovisionTriggers = deprovisionJobTriggers
		deprovisionState = opv1alpha1.JobStateSucceeded
		providerMu.Unlock()
		Expect(deprovisionTriggers).To(Equal(1))

		_, err = restartedReconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(unassigned).To(BeTrue())
		Expect(k8sClient.Get(ctx, key, &v1alpha1.BareMetalInstance{})).To(Satisfy(apierrors.IsNotFound))
	})

	It("waits for OSAC cleanup before powering off, offboarding, deprovisioning and releasing the host", func() {
		full := bmi.DeepCopy()
		full.Name = ""
		full.ResourceVersion = ""
		full.UID = ""
		full.Spec.TemplateID = "test_template"
		full.Spec.NetworkAttachments = []v1alpha1.BareMetalNetworkAttachment{{
			SubnetRef: "test-subnet", Interface: "data-0", Primary: true,
		}}
		controllerutil.AddFinalizer(full, BareMetalInstanceNetworkingFinalizer)
		controllerutil.AddFinalizer(full, osacCleanupFinalizer)
		Expect(k8sClient.Create(ctx, full)).To(Succeed())
		defer func() {
			latest := &v1alpha1.BareMetalInstance{}
			if k8sClient.Get(ctx, client.ObjectKeyFromObject(full), latest) == nil {
				latest.Finalizers = nil
				_ = k8sClient.Update(ctx, latest)
				_ = k8sClient.Delete(ctx, latest)
			}
		}()

		poweredOn := true
		powerOffCalls := 0
		networkDeprovisionCalls := 0
		providerDeprovisionCalls := 0
		reconciler.ManagementClient = &mockManagementClient{
			getPowerStateFunc: func(_ context.Context, _ string) (*management.PowerStatus, error) {
				if poweredOn {
					return &management.PowerStatus{State: management.PowerOn}, nil
				}
				return &management.PowerStatus{State: management.PowerOff}, nil
			},
			setPowerStateFunc: func(_ context.Context, _ string, target management.PowerState) error {
				Expect(target).To(Equal(management.PowerOff))
				poweredOn = false
				powerOffCalls++
				return nil
			},
		}
		reconciler.NetworkingProvider = &mockProvisioningProvider{
			triggerDeprovisionFunc: func(_ context.Context, _ client.Object, _ []opv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				Expect(poweredOn).To(BeFalse())
				networkDeprovisionCalls++
				return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "network-job"}, nil
			},
		}
		reconciler.ProvisioningProvider = &mockProvisioningProvider{
			triggerDeprovisionFunc: func(_ context.Context, _ client.Object, _ []opv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				Expect(networkDeprovisionCalls).To(Equal(1))
				providerDeprovisionCalls++
				return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "provider-job"}, nil
			},
		}
		Expect(k8sClient.Delete(ctx, full)).To(Succeed())
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(full)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(poweredOn).To(BeTrue())
		Expect(powerOffCalls).To(Equal(0))
		Expect(networkDeprovisionCalls).To(Equal(0))
		Expect(providerDeprovisionCalls).To(Equal(0))
		Expect(unassigned).To(BeFalse())

		latest := &v1alpha1.BareMetalInstance{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(full), latest)).To(Succeed())
		controllerutil.RemoveFinalizer(latest, osacCleanupFinalizer)
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())

		completed := false
		for range 8 {
			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(full)})
			Expect(err).NotTo(HaveOccurred())
			fresh := &v1alpha1.BareMetalInstance{}
			getErr := k8sClient.Get(ctx, client.ObjectKeyFromObject(full), fresh)
			if apierrors.IsNotFound(getErr) {
				Expect(result.IsZero()).To(BeTrue())
				completed = true
				break
			}
			Expect(getErr).NotTo(HaveOccurred())
			Expect(controllerutil.ContainsFinalizer(fresh, osacCleanupFinalizer)).To(BeFalse())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), &opv1alpha1.ExternalIP{})).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), &opv1alpha1.ExternalIPAttachment{})).To(Succeed())
			if len(fresh.Finalizers) == 1 {
				Expect(result.IsZero()).To(BeTrue())
				completed = true
				break
			}
		}
		Expect(completed).To(BeTrue())
		Expect(powerOffCalls).To(Equal(1))
		Expect(networkDeprovisionCalls).To(Equal(1))
		Expect(providerDeprovisionCalls).To(Equal(1))
		Expect(unassigned).To(BeTrue())
	})
})
