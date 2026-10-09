/*
Copyright 2025.

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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

type addOnOperatorProviderStub struct {
	triggeredOperators []string
	jobStatuses        map[string]provisioning.ProvisionStatus
	nextJobID          int
	adminKubeconfig    string
	triggerError       error
	returnEmptyJobID   bool
	canceledJobIDs     []string
	cancelError        error
	statusErrors       map[string]error
	beforeTrigger      func(string)
}

func newAddOnOperatorProviderStub() *addOnOperatorProviderStub {
	return &addOnOperatorProviderStub{
		jobStatuses:  map[string]provisioning.ProvisionStatus{},
		statusErrors: map[string]error{},
	}
}

func (p *addOnOperatorProviderStub) TriggerProvision(ctx context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
	operatorName := provisioning.AddOnOperatorNameFromContext(ctx)
	if operatorName == "" {
		return nil, fmt.Errorf("missing add-on operator name")
	}
	p.adminKubeconfig = provisioning.AdminKubeconfigFromContext(ctx)
	if p.triggerError != nil {
		return nil, p.triggerError
	}
	if p.beforeTrigger != nil {
		p.beforeTrigger(operatorName)
	}
	p.nextJobID++
	jobID := fmt.Sprintf("addon-job-%d", p.nextJobID)
	p.triggeredOperators = append(p.triggeredOperators, operatorName)
	if p.returnEmptyJobID {
		return &provisioning.ProvisionResult{
			InitialState: osacv1alpha1.JobStatePending,
			Message:      "queued",
		}, nil
	}
	p.jobStatuses[jobID] = provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   osacv1alpha1.JobStatePending,
		Message: "queued",
	}
	return &provisioning.ProvisionResult{
		JobID:        jobID,
		InitialState: osacv1alpha1.JobStatePending,
		Message:      "queued",
	}, nil
}

func (p *addOnOperatorProviderStub) GetProvisionStatus(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	if err := p.statusErrors[jobID]; err != nil {
		return provisioning.ProvisionStatus{}, err
	}
	return p.jobStatuses[jobID], nil
}

func (p *addOnOperatorProviderStub) TriggerDeprovision(_ context.Context, _ client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
	return nil, fmt.Errorf("unexpected deprovision dispatch")
}

func (p *addOnOperatorProviderStub) GetDeprovisionStatus(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
	return provisioning.ProvisionStatus{}, fmt.Errorf("unexpected deprovision status poll")
}

func (p *addOnOperatorProviderStub) CancelJob(_ context.Context, jobID string) error {
	p.canceledJobIDs = append(p.canceledJobIDs, jobID)
	return p.cancelError
}

func (p *addOnOperatorProviderStub) Name() string { return "add-on-test" }

func (p *addOnOperatorProviderStub) setJobStatus(jobID string, status provisioning.ProvisionStatus) {
	status.JobID = jobID
	p.jobStatuses[jobID] = status
}

func (p *addOnOperatorProviderStub) setStatusError(jobID string, err error) {
	p.statusErrors[jobID] = err
}

type nonCancellingAddOnOperatorProvider struct {
	provider *addOnOperatorProviderStub
}

func (p *nonCancellingAddOnOperatorProvider) TriggerProvision(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
	return p.provider.TriggerProvision(ctx, resource)
}

func (p *nonCancellingAddOnOperatorProvider) GetProvisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	return p.provider.GetProvisionStatus(ctx, resource, jobID)
}

func (p *nonCancellingAddOnOperatorProvider) TriggerDeprovision(ctx context.Context, resource client.Object, jobs []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
	return p.provider.TriggerDeprovision(ctx, resource, jobs)
}

func (p *nonCancellingAddOnOperatorProvider) GetDeprovisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	return p.provider.GetDeprovisionStatus(ctx, resource, jobID)
}

func (p *nonCancellingAddOnOperatorProvider) Name() string { return p.provider.Name() }

var _ = Describe("AddOnOperatorReconciler", func() {
	const namespace = "default"
	ctx := context.Background()

	var (
		k8sClient     client.Client
		provider      *addOnOperatorProviderStub
		reconciler    *AddOnOperatorReconciler
		eventRecorder *events.FakeRecorder
	)

	BeforeEach(func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		k8sClient = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&osacv1alpha1.ClusterOrder{}).
			Build()
		provider = newAddOnOperatorProviderStub()
		eventRecorder = events.NewFakeRecorder(20)
		reconciler = NewAddOnOperatorReconciler(k8sClient, k8sClient, namespace, provider, time.Minute)
		reconciler.Recorder = eventRecorder
		reconciler.getAdminKubeconfig = func(context.Context, *osacv1alpha1.ClusterOrder) ([]byte, error) {
			return []byte("test-kubeconfig"), nil
		}
	})

	newOrder := func(name string, phase osacv1alpha1.ClusterOrderPhaseType, operators ...string) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:     "test.template",
				AddOnOperators: operators,
			},
			Status: osacv1alpha1.ClusterOrderStatus{Phase: phase},
		}
	}

	getOrder := func(name string) *osacv1alpha1.ClusterOrder {
		order := &osacv1alpha1.ClusterOrder{}
		ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, order)).To(Succeed())
		return order
	}

	It("does not dispatch until the ClusterOrder is Ready", func() {
		order := newOrder("not-ready", osacv1alpha1.ClusterOrderPhaseProgressing, "cert-manager")
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		Expect(provider.triggeredOperators).To(BeEmpty())

		order = getOrder(order.Name)
		order.Status.Phase = osacv1alpha1.ClusterOrderPhaseReady
		Expect(k8sClient.Status().Update(ctx, order)).To(Succeed())

		_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		Expect(provider.triggeredOperators).To(Equal([]string{"cert-manager"}))

		stored := getOrder(order.Name)
		Expect(stored.Status.AddOnOperatorJobs).To(HaveLen(1))
		Expect(stored.Status.AddOnOperatorJobs[0].Name).To(Equal("cert-manager"))
		Expect(stored.Status.AddOnOperatorJobs[0].State).To(Equal(osacv1alpha1.JobStatePending))
		Expect(stored.Status.Phase).To(Equal(osacv1alpha1.ClusterOrderPhaseReady))
	})

	It("persists each launched job before launching the next operator", func() {
		order := newOrder("durable-launch", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager", "gpu-operator")
		provider.beforeTrigger = func(operatorName string) {
			if operatorName != "gpu-operator" {
				return
			}
			stored := getOrder(order.Name)
			Expect(stored.Status.AddOnOperatorJobs).To(HaveLen(1))
			Expect(stored.Status.AddOnOperatorJobs[0].Name).To(Equal("cert-manager"))
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		Expect(provider.triggeredOperators).To(Equal([]string{"cert-manager", "gpu-operator"}))
	})

	It("skips unmanaged ClusterOrders", func() {
		order := newOrder("unmanaged", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		order.Annotations = map[string]string{osacManagementStateAnnotation: ManagementStateUnmanaged}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		Expect(provider.triggeredOperators).To(BeEmpty())
	})

	It("marks successful operators installed without redispatching them", func() {
		order := newOrder("success", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}}

		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		jobID := getOrder(order.Name).Status.AddOnOperatorJobs[0].JobID
		provider.setJobStatus(jobID, provisioning.ProvisionStatus{State: osacv1alpha1.JobStateSucceeded, Message: "installed"})

		result, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		stored := getOrder(order.Name)
		Expect(stored.Status.AddOnOperatorJobs[0].State).To(Equal(osacv1alpha1.JobStateSucceeded))
		Expect(stored.Status.AddOnOperatorJobs[0].Message).To(BeEmpty())
		condition := findAddOnOperatorCondition(stored)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))

		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(provider.triggeredOperators).To(Equal([]string{"cert-manager"}))
	})

	It("retries only failed operators and keeps the main phase Ready", func() {
		order := newOrder("independent", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager", "gpu-operator")
		order.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{{
			JobID: "cluster-job", Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateSucceeded,
			Timestamp: metav1.Now(),
		}}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}}

		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		stored := getOrder(order.Name)
		Expect(stored.Status.AddOnOperatorJobs).To(HaveLen(2))
		firstJobID := stored.Status.AddOnOperatorJobs[0].JobID
		secondJobID := stored.Status.AddOnOperatorJobs[1].JobID
		provider.setJobStatus(firstJobID, provisioning.ProvisionStatus{
			State:        osacv1alpha1.JobStateFailed,
			Message:      "installation failed",
			ErrorDetails: strings.Repeat("traceback ", 1000),
		})
		provider.setJobStatus(secondJobID, provisioning.ProvisionStatus{State: osacv1alpha1.JobStateSucceeded, Message: "installed"})

		result, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		stored = getOrder(order.Name)
		Expect(stored.Status.Phase).To(Equal(osacv1alpha1.ClusterOrderPhaseReady))
		Expect(stored.Status.AddOnOperatorJobs[0].State).To(Equal(osacv1alpha1.JobStateFailed))
		Expect(len(stored.Status.AddOnOperatorJobs[0].Message)).To(BeNumerically("<=", maxAddOnOperatorJobMessageLength))
		Expect(stored.Status.AddOnOperatorJobs[0].Message).NotTo(ContainSubstring("traceback"))
		Expect(stored.Status.AddOnOperatorJobs[1].State).To(Equal(osacv1alpha1.JobStateSucceeded))
		condition := findAddOnOperatorCondition(stored)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Message).To(ContainSubstring("cert-manager"))

		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(provider.triggeredOperators).To(Equal([]string{"cert-manager", "gpu-operator"}))

		stored = getOrder(order.Name)
		stored.Status.AddOnOperatorJobs[0].Timestamp = metav1.NewTime(time.Now().Add(-provisioning.BackoffMaxDelay))
		Expect(k8sClient.Status().Update(ctx, stored)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(provider.triggeredOperators).To(Equal([]string{"cert-manager", "gpu-operator", "cert-manager"}))
		stored = getOrder(order.Name)
		Expect(stored.Status.AddOnOperatorJobs).To(HaveLen(3))
		Expect(findAddOnOperatorCondition(stored).Status).To(Equal(metav1.ConditionFalse))
		retryJobID := stored.Status.AddOnOperatorJobs[2].JobID
		provider.setJobStatus(retryJobID, provisioning.ProvisionStatus{State: osacv1alpha1.JobStateSucceeded, Message: "installed"})
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		stored = getOrder(order.Name)
		Expect(findAddOnOperatorCondition(stored).Status).To(Equal(metav1.ConditionTrue))
	})

	It("records provider trigger failures as failed attempts", func() {
		order := newOrder("trigger-failure", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		provider.triggerError = errors.New("AAP unavailable")
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		stored := getOrder(order.Name)
		Expect(stored.Status.AddOnOperatorJobs).To(HaveLen(1))
		Expect(stored.Status.AddOnOperatorJobs[0].JobID).To(BeEmpty())
		Expect(stored.Status.AddOnOperatorJobs[0].State).To(Equal(osacv1alpha1.JobStateFailed))
		Expect(stored.Status.AddOnOperatorJobs[0].Message).To(ContainSubstring("AAP unavailable"))
		Expect(findAddOnOperatorCondition(stored).Status).To(Equal(metav1.ConditionFalse))
	})

	It("requeues an admin kubeconfig error without recording an attempt", func() {
		order := newOrder("kubeconfig-failure", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		reconciler.getAdminKubeconfig = func(context.Context, *osacv1alpha1.ClusterOrder) ([]byte, error) {
			return nil, errors.New("kubeconfig secret unavailable")
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		stored := getOrder(order.Name)
		Expect(stored.Status.AddOnOperatorJobs).To(BeEmpty())
		Expect(findAddOnOperatorCondition(stored)).To(BeNil())
		Expect(provider.triggeredOperators).To(BeEmpty())
	})

	It("requeues when the admin kubeconfig is unavailable without recording an attempt", func() {
		order := newOrder("missing-kubeconfig", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		reconciler.getAdminKubeconfig = func(context.Context, *osacv1alpha1.ClusterOrder) ([]byte, error) {
			return nil, nil
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		stored := getOrder(order.Name)
		Expect(stored.Status.AddOnOperatorJobs).To(BeEmpty())
		Expect(findAddOnOperatorCondition(stored)).To(BeNil())
		Expect(provider.triggeredOperators).To(BeEmpty())
	})

	It("records a warning event when the admin kubeconfig is unavailable", func() {
		order := newOrder("missing-kubeconfig-event", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		reconciler.getAdminKubeconfig = func(context.Context, *osacv1alpha1.ClusterOrder) ([]byte, error) {
			return nil, nil
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		Eventually(eventRecorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeWarning),
			ContainSubstring("AddOnOperatorKubeconfigUnavailable"),
		)))
	})

	It("records an empty provider job ID as a failed attempt", func() {
		order := newOrder("empty-job-id", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		provider.returnEmptyJobID = true
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		stored := getOrder(order.Name)
		Expect(stored.Status.AddOnOperatorJobs).To(HaveLen(1))
		Expect(stored.Status.AddOnOperatorJobs[0].JobID).To(BeEmpty())
		Expect(stored.Status.AddOnOperatorJobs[0].State).To(Equal(osacv1alpha1.JobStateFailed))
	})

	It("marks purged AAP jobs failed so the operator can retry", func() {
		order := newOrder("purged-job", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}}

		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		jobID := getOrder(order.Name).Status.AddOnOperatorJobs[0].JobID
		provider.setStatusError(jobID, &aap.NotFoundError{Resource: "job " + jobID})

		result, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		stored := getOrder(order.Name)
		Expect(stored.Status.AddOnOperatorJobs[0].State).To(Equal(osacv1alpha1.JobStateFailed))
		Expect(stored.Status.AddOnOperatorJobs[0].Message).To(Equal("AAP job was purged before completion"))
	})

	It("cancels every active add-on job and removes its deletion finalizer immediately", func() {
		order := newOrder("delete-active-addon", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		order.Finalizers = []string{osacPrefix + "/addon-operator"}
		deletionTimestamp := metav1.Now()
		order.Status.AddOnOperatorJobs = []osacv1alpha1.AddOnOperatorJobStatus{{
			Name: "cert-manager",
			JobStatus: osacv1alpha1.JobStatus{
				JobID:     "older-active-addon-job",
				Type:      osacv1alpha1.JobTypeProvision,
				State:     osacv1alpha1.JobStateRunning,
				Timestamp: deletionTimestamp,
			},
		}, {
			Name: "cert-manager",
			JobStatus: osacv1alpha1.JobStatus{
				JobID:     "newer-active-addon-job",
				Type:      osacv1alpha1.JobTypeProvision,
				State:     osacv1alpha1.JobStatePending,
				Timestamp: metav1.NewTime(deletionTimestamp.Add(time.Second)),
			},
		}, {
			Name: "removed-from-spec",
			JobStatus: osacv1alpha1.JobStatus{
				JobID:     "removed-active-addon-job",
				Type:      osacv1alpha1.JobTypeProvision,
				State:     osacv1alpha1.JobStateRunning,
				Timestamp: deletionTimestamp,
			},
		}}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		order.DeletionTimestamp = &deletionTimestamp

		result, err := reconciler.reconcileDeletion(ctx, order)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		stored := getOrder(order.Name)
		Expect(stored.Finalizers).NotTo(ContainElement(osacPrefix + "/addon-operator"))
		Expect(provider.canceledJobIDs).To(ConsistOf(
			"older-active-addon-job",
			"newer-active-addon-job",
			"removed-active-addon-job",
		))
	})

	It("does not poll active jobs during deletion", func() {
		order := newOrder("delete-status-error", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		order.Finalizers = []string{osacAddOnOperatorFinalizer}
		deletionTimestamp := metav1.Now()
		order.DeletionTimestamp = &deletionTimestamp
		order.Status.AddOnOperatorJobs = []osacv1alpha1.AddOnOperatorJobStatus{{
			Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "active-addon-job", State: osacv1alpha1.JobStateRunning, Timestamp: deletionTimestamp,
			},
		}}
		provider.setStatusError("active-addon-job", errors.New("AAP temporarily unavailable"))
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		result, err := reconciler.reconcileDeletion(ctx, order)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		stored := getOrder(order.Name)
		Expect(stored.Finalizers).NotTo(ContainElement(osacAddOnOperatorFinalizer))
		Expect(provider.canceledJobIDs).To(Equal([]string{"active-addon-job"}))
	})

	It("removes the deletion finalizer when cancellation fails", func() {
		order := newOrder("delete-cancel-error", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		order.Finalizers = []string{osacAddOnOperatorFinalizer}
		deletionTimestamp := metav1.Now()
		order.DeletionTimestamp = &deletionTimestamp
		order.Status.AddOnOperatorJobs = []osacv1alpha1.AddOnOperatorJobStatus{{
			Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "active-addon-job", State: osacv1alpha1.JobStateRunning, Timestamp: deletionTimestamp,
			},
		}}
		provider.setJobStatus("active-addon-job", provisioning.ProvisionStatus{State: osacv1alpha1.JobStateRunning})
		provider.cancelError = errors.New("AAP cancellation failed")
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		result, err := reconciler.reconcileDeletion(ctx, order)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		stored := getOrder(order.Name)
		Expect(stored.Finalizers).NotTo(ContainElement(osacAddOnOperatorFinalizer))
		Eventually(eventRecorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeWarning),
			ContainSubstring("AddOnOperatorCancellationFailed"),
		)))
	})

	It("removes the deletion finalizer when the provider cannot cancel jobs", func() {
		order := newOrder("delete-no-canceler", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		order.Finalizers = []string{osacAddOnOperatorFinalizer}
		deletionTimestamp := metav1.Now()
		order.DeletionTimestamp = &deletionTimestamp
		order.Status.AddOnOperatorJobs = []osacv1alpha1.AddOnOperatorJobStatus{{
			Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "active-addon-job", State: osacv1alpha1.JobStateRunning, Timestamp: deletionTimestamp,
			},
		}}
		provider.setJobStatus("active-addon-job", provisioning.ProvisionStatus{State: osacv1alpha1.JobStateRunning})
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		nonCancellingProvider := &nonCancellingAddOnOperatorProvider{provider: provider}
		nonCancellingReconciler := NewAddOnOperatorReconciler(k8sClient, k8sClient, namespace, nonCancellingProvider, time.Minute)
		nonCancellingReconciler.Recorder = eventRecorder

		result, err := nonCancellingReconciler.reconcileDeletion(ctx, order)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		stored := getOrder(order.Name)
		Expect(stored.Finalizers).NotTo(ContainElement(osacAddOnOperatorFinalizer))
		Expect(provider.canceledJobIDs).To(BeEmpty())
		Eventually(eventRecorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeWarning),
			ContainSubstring("AddOnOperatorCancellationUnavailable"),
		)))
	})

	It("does not poll or rewrite job status before completing deletion", func() {
		order := newOrder("delete-purged-job", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		order.Finalizers = []string{osacAddOnOperatorFinalizer}
		deletionTimestamp := metav1.Now()
		order.DeletionTimestamp = &deletionTimestamp
		order.Status.AddOnOperatorJobs = []osacv1alpha1.AddOnOperatorJobStatus{{
			Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "purged-addon-job", State: osacv1alpha1.JobStateRunning, Timestamp: deletionTimestamp,
			},
		}}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		result, err := reconciler.reconcileDeletion(ctx, order)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		stored := getOrder(order.Name)
		Expect(stored.Finalizers).NotTo(ContainElement(osacAddOnOperatorFinalizer))
		Expect(stored.Status.AddOnOperatorJobs[0].State).To(Equal(osacv1alpha1.JobStateRunning))
		Expect(provider.canceledJobIDs).To(Equal([]string{"purged-addon-job"}))
	})

	It("preserves concurrent finalizers when adding the add-on finalizer", func() {
		order := newOrder("concurrent-add-finalizer", osacv1alpha1.ClusterOrderPhaseProgressing, "cert-manager")
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		storageFinalizer := "osac.openshift.io/storage"
		injected := false
		conflictClient := fake.NewClientBuilder().
			WithScheme(k8sClient.Scheme()).
			WithStatusSubresource(&osacv1alpha1.ClusterOrder{}).
			WithObjects(order).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if !injected {
						injected = true
						latest := &osacv1alpha1.ClusterOrder{}
						Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), latest)).To(Succeed())
						latest.Finalizers = append(latest.Finalizers, storageFinalizer)
						Expect(c.Update(ctx, latest)).To(Succeed())
					}
					return c.Patch(ctx, obj, patch, opts...)
				},
			}).Build()
		reconciler := NewAddOnOperatorReconciler(conflictClient, conflictClient, namespace, provider, time.Minute)
		reconciler.getAdminKubeconfig = func(context.Context, *osacv1alpha1.ClusterOrder) ([]byte, error) {
			return []byte("test-kubeconfig"), nil
		}

		_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		stored := &osacv1alpha1.ClusterOrder{}
		Expect(conflictClient.Get(ctx, client.ObjectKeyFromObject(order), stored)).To(Succeed())
		Expect(stored.Finalizers).To(ContainElements(storageFinalizer, osacAddOnOperatorFinalizer))
	})

	It("preserves concurrent finalizers when removing the add-on finalizer", func() {
		order := newOrder("concurrent-remove-finalizer", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		order.Finalizers = []string{osacAddOnOperatorFinalizer, "osac.openshift.io/storage"}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		deletionTimestamp := metav1.Now()
		order.DeletionTimestamp = &deletionTimestamp
		feedbackFinalizer := "osac.openshift.io/feedback"
		injected := false
		conflictClient := fake.NewClientBuilder().
			WithScheme(k8sClient.Scheme()).
			WithObjects(order).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if !injected {
						injected = true
						latest := &osacv1alpha1.ClusterOrder{}
						Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), latest)).To(Succeed())
						latest.Finalizers = append(latest.Finalizers, feedbackFinalizer)
						Expect(c.Update(ctx, latest)).To(Succeed())
					}
					return c.Patch(ctx, obj, patch, opts...)
				},
			}).Build()
		reconciler := NewAddOnOperatorReconciler(conflictClient, conflictClient, namespace, provider, time.Minute)

		_, err := reconciler.reconcileDeletion(ctx, order)
		Expect(err).NotTo(HaveOccurred())
		stored := &osacv1alpha1.ClusterOrder{}
		Expect(conflictClient.Get(ctx, client.ObjectKeyFromObject(order), stored)).To(Succeed())
		Expect(stored.Finalizers).To(ContainElements("osac.openshift.io/storage", feedbackFinalizer))
		Expect(stored.Finalizers).NotTo(ContainElement(osacAddOnOperatorFinalizer))
	})

	It("retries add-on status patches after a resource version conflict", func() {
		order := newOrder("status-conflict", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		order.Status.Conditions = []metav1.Condition{{
			Type: osacv1alpha1.ConditionProgressing, Status: metav1.ConditionFalse,
			Reason: "Provisioned", LastTransitionTime: metav1.Now(),
		}}
		baseClient := fake.NewClientBuilder().
			WithScheme(k8sClient.Scheme()).
			WithStatusSubresource(&osacv1alpha1.ClusterOrder{}).
			WithObjects(order).
			Build()
		injected := false
		conflictClient := interceptor.NewClient(baseClient, interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if subResourceName == "status" && !injected {
					injected = true
					latest := &osacv1alpha1.ClusterOrder{}
					Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), latest)).To(Succeed())
					latest.Status.Conditions = append(latest.Status.Conditions, metav1.Condition{
						Type: osacv1alpha1.ConditionDeleting, Status: metav1.ConditionFalse,
						Reason: "StillActive", LastTransitionTime: metav1.Now(),
					})
					Expect(c.Status().Update(ctx, latest)).To(Succeed())
					return apierrors.NewConflict(schema.GroupResource{Group: "osac.openshift.io", Resource: "clusterorders"}, latest.Name, errors.New("injected conflict"))
				}
				return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		})
		reconciler := NewAddOnOperatorReconciler(conflictClient, conflictClient, namespace, provider, time.Minute)
		computed := order.Status
		computed.AddOnOperatorJobs = []osacv1alpha1.AddOnOperatorJobStatus{{
			Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "addon-job-1", State: osacv1alpha1.JobStateRunning, Timestamp: metav1.Now(),
			},
		}}
		computed.Conditions = append(computed.Conditions, metav1.Condition{
			Type: string(osacv1alpha1.ClusterOrderConditionAddOnOperatorsReady), Status: metav1.ConditionFalse,
			Reason: addOnOperatorsReadyReason, LastTransitionTime: metav1.Now(),
		})

		Expect(reconciler.patchAddOnStatusWithRetry(ctx, client.ObjectKeyFromObject(order), nil, computed)).To(Succeed())
		stored := &osacv1alpha1.ClusterOrder{}
		Expect(conflictClient.Get(ctx, client.ObjectKeyFromObject(order), stored)).To(Succeed())
		Expect(stored.Status.AddOnOperatorJobs).To(HaveLen(1))
		Expect(apimeta.FindStatusCondition(stored.Status.Conditions, string(osacv1alpha1.ClusterOrderConditionProgressing))).NotTo(BeNil())
		Expect(apimeta.FindStatusCondition(stored.Status.Conditions, string(osacv1alpha1.ClusterOrderConditionAddOnOperatorsReady))).NotTo(BeNil())
	})

	It("retries terminal job status persistence after a resource version conflict", func() {
		order := newOrder("terminal-status-conflict", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		order.Status.AddOnOperatorJobs = []osacv1alpha1.AddOnOperatorJobStatus{{
			Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "addon-job-1", State: osacv1alpha1.JobStateRunning, Timestamp: metav1.Now(),
			},
		}}
		baseClient := fake.NewClientBuilder().
			WithScheme(k8sClient.Scheme()).
			WithStatusSubresource(&osacv1alpha1.ClusterOrder{}).
			WithObjects(order).
			Build()
		injected := false
		conflictClient := interceptor.NewClient(baseClient, interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if subResourceName == "status" && !injected {
					injected = true
					latest := &osacv1alpha1.ClusterOrder{}
					Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), latest)).To(Succeed())
					latest.Status.Conditions = append(latest.Status.Conditions, metav1.Condition{
						Type: osacv1alpha1.ConditionDeleting, Status: metav1.ConditionFalse,
						Reason: "StillActive", LastTransitionTime: metav1.Now(),
					})
					Expect(c.Status().Update(ctx, latest)).To(Succeed())
					return apierrors.NewConflict(schema.GroupResource{Group: "osac.openshift.io", Resource: "clusterorders"}, latest.Name, errors.New("injected conflict"))
				}
				return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		})
		reconciler := NewAddOnOperatorReconciler(conflictClient, conflictClient, namespace, provider, time.Minute)

		status := provisioning.ProvisionStatus{JobID: "addon-job-1", State: osacv1alpha1.JobStateSucceeded, Message: "installed"}
		Expect(reconciler.persistAddOnOperatorJobStatusWithRetry(ctx, client.ObjectKeyFromObject(order), "cert-manager", "addon-job-1", status)).To(Succeed())
		stored := &osacv1alpha1.ClusterOrder{}
		Expect(conflictClient.Get(ctx, client.ObjectKeyFromObject(order), stored)).To(Succeed())
		Expect(stored.Status.AddOnOperatorJobs[0].State).To(Equal(osacv1alpha1.JobStateSucceeded))
		Expect(apimeta.FindStatusCondition(stored.Status.Conditions, string(osacv1alpha1.ConditionDeleting))).NotTo(BeNil())
	})

	It("bounds add-on history while preserving the latest attempt", func() {
		order := newOrder("bounded-history", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		reconciler.MaxJobHistory = 2
		old := metav1.Now()
		old.Time = old.Time.Add(-2 * provisioning.BackoffMaxDelay)
		order.Status.AddOnOperatorJobs = []osacv1alpha1.AddOnOperatorJobStatus{
			{Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{JobID: "old-1", Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, Timestamp: old}},
			{Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{JobID: "old-2", Type: osacv1alpha1.JobTypeProvision, State: osacv1alpha1.JobStateFailed, Timestamp: metav1.NewTime(old.Time.Add(time.Minute))}},
		}
		provider.setJobStatus("old-2", provisioning.ProvisionStatus{JobID: "old-2", State: osacv1alpha1.JobStateFailed})
		Expect(k8sClient.Create(ctx, order)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: order.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		stored := getOrder(order.Name)
		Expect(stored.Status.AddOnOperatorJobs).To(HaveLen(3))
		Expect(stored.Status.AddOnOperatorJobs).To(ContainElement(HaveField("JobID", Equal("old-1"))))
		Expect(stored.Status.AddOnOperatorJobs).To(ContainElement(HaveField("JobID", Equal("old-2"))))
		Expect(stored.Status.AddOnOperatorJobs).To(ContainElement(HaveField("JobID", Equal("addon-job-1"))))
	})

	It("preserves active add-on jobs when trimming history", func() {
		active := metav1.Now()
		jobs := []osacv1alpha1.AddOnOperatorJobStatus{
			{Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "active-job", Type: osacv1alpha1.JobTypeProvision,
				State: osacv1alpha1.JobStateRunning, Timestamp: active,
			}},
			{Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "newer-terminal-job", Type: osacv1alpha1.JobTypeProvision,
				State: osacv1alpha1.JobStateSucceeded, Timestamp: metav1.NewTime(active.Add(time.Second)),
			}},
		}

		trimmed := trimAddOnOperatorJobs(jobs, 1)

		Expect(trimmed).To(ConsistOf(jobs[0], jobs[1]))
	})

	It("uses the later record when add-on job timestamps tie", func() {
		timestamp := metav1.Now()
		jobs := []osacv1alpha1.AddOnOperatorJobStatus{
			{Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "first-job", State: osacv1alpha1.JobStateFailed, Timestamp: timestamp,
			}},
			{Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "second-job", State: osacv1alpha1.JobStateSucceeded, Timestamp: timestamp,
			}},
		}

		Expect(latestAddOnOperatorJob(jobs, "cert-manager").JobID).To(Equal("second-job"))
	})

	It("canonicalizes JobID-less failure timestamps for API round trips", func() {
		order := newOrder("canonical-failure-timestamp", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		result := reconciler.recordAddOnOperatorFailure(order, "cert-manager", "AAP unavailable")
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		computed := order.Status.AddOnOperatorJobs[0]
		Expect(computed.Timestamp.Time.Nanosecond()).To(BeZero())

		persisted := computed
		persisted.Timestamp = metav1.NewTime(computed.Timestamp.Time.UTC().Truncate(time.Second))

		Expect(findAddOnOperatorJobIndex([]osacv1alpha1.AddOnOperatorJobStatus{persisted}, computed)).To(Equal(0))
	})

	It("uses the same later-tie rule when computing readiness", func() {
		order := newOrder("condition-timestamp-tie", osacv1alpha1.ClusterOrderPhaseReady, "cert-manager")
		timestamp := metav1.Now().Rfc3339Copy()
		order.Status.AddOnOperatorJobs = []osacv1alpha1.AddOnOperatorJobStatus{
			{Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "failed-job", State: osacv1alpha1.JobStateFailed, Timestamp: timestamp,
			}},
			{Name: "cert-manager", JobStatus: osacv1alpha1.JobStatus{
				JobID: "successful-job", State: osacv1alpha1.JobStateSucceeded, Timestamp: timestamp,
			}},
		}

		Expect(updateAddOnOperatorsReadyCondition(order)).To(BeTrue())
		condition := findAddOnOperatorCondition(order)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
	})
})

func findAddOnOperatorCondition(order *osacv1alpha1.ClusterOrder) *metav1.Condition {
	return apimeta.FindStatusCondition(order.Status.Conditions, string(osacv1alpha1.ClusterOrderConditionAddOnOperatorsReady))
}
