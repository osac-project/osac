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
	"strconv"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const csiInstallTestTemplate = "osac-install-cluster-csi"

// csiInstallAAPClient is an AAP client double behind the real AAP provider. It
// records every launch, serves job status from memory and records
// cancellations.
type csiInstallAAPClient struct {
	mu          sync.Mutex
	launches    []aap.LaunchJobTemplateRequest
	jobs        map[string]*aap.Job
	statusPolls []string
	canceled    []string
	templateErr error
	nextID      int
}

func newCSIInstallAAPClient() *csiInstallAAPClient {
	return &csiInstallAAPClient{jobs: map[string]*aap.Job{}}
}

func (c *csiInstallAAPClient) GetTemplate(_ context.Context, name string) (*aap.Template, error) {
	if c.templateErr != nil {
		return nil, c.templateErr
	}
	return &aap.Template{ID: 7, Name: name, Type: aap.TemplateTypeJob}, nil
}

func (c *csiInstallAAPClient) LaunchJobTemplate(_ context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	c.launches = append(c.launches, req)
	c.jobs[strconv.Itoa(c.nextID)] = &aap.Job{ID: c.nextID, Status: "pending"}
	return &aap.LaunchJobTemplateResponse{JobID: c.nextID}, nil
}

func (c *csiInstallAAPClient) LaunchWorkflowTemplate(context.Context, aap.LaunchWorkflowTemplateRequest) (*aap.LaunchWorkflowTemplateResponse, error) {
	return nil, errors.New("unexpected workflow launch")
}

func (c *csiInstallAAPClient) GetJob(_ context.Context, jobID string) (*aap.Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statusPolls = append(c.statusPolls, jobID)
	job, ok := c.jobs[jobID]
	if !ok {
		return nil, &aap.NotFoundError{Resource: "job " + jobID}
	}
	copied := *job
	return &copied, nil
}

func (c *csiInstallAAPClient) CancelJob(_ context.Context, jobID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.canceled = append(c.canceled, jobID)
	return nil
}

func (c *csiInstallAAPClient) setJob(jobID string, job aap.Job) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobs[jobID] = &job
}

func (c *csiInstallAAPClient) launchCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.launches)
}

// staleFirstReader serves the first ClusterOrder read with no CSI install
// jobs, as a lagging read would, and passes later reads through.
type staleFirstReader struct {
	client.Reader
	served bool
}

func (r *staleFirstReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if order, ok := obj.(*osacv1alpha1.ClusterOrder); ok && !r.served {
		r.served = true
		order.Status.CSIInstallJobs = nil
	}
	return nil
}

func findCSIDriverCondition(order *osacv1alpha1.ClusterOrder) *metav1.Condition {
	return apimeta.FindStatusCondition(order.Status.Conditions, string(osacv1alpha1.ClusterOrderConditionCSIDriverReady))
}

var _ = Describe("CSIInstallReconciler", func() {
	const namespace = "default"

	var (
		aapClient  *csiInstallAAPClient
		reconciler *CSIInstallReconciler
		orderName  string
		request    ctrl.Request
	)

	newReconciler := func(reader client.Reader, enabled bool) *CSIInstallReconciler {
		provider := provisioning.NewAAPProvider(aapClient, csiInstallTestTemplate, "")
		r := NewCSIInstallReconciler(k8sClient, reader, namespace, provider, time.Minute, enabled)
		r.getAdminKubeconfig = func(context.Context, *osacv1alpha1.ClusterOrder) ([]byte, error) {
			return []byte("test-kubeconfig"), nil
		}
		return r
	}

	createOrder := func(phase osacv1alpha1.ClusterOrderPhaseType, finalizers ...string) *osacv1alpha1.ClusterOrder {
		order := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:       orderName,
				Namespace:  namespace,
				Finalizers: finalizers,
				Annotations: map[string]string{
					"osac.openshift.io/tenant": "tenant-a",
				},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{TemplateID: "test.template"},
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		DeferCleanup(func() {
			current := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, request.NamespacedName, current); err != nil {
				return
			}
			current.Finalizers = nil
			_ = k8sClient.Update(ctx, current)
			_ = k8sClient.Delete(ctx, current)
		})
		order.Status.Phase = phase
		Expect(k8sClient.Status().Update(ctx, order)).To(Succeed())
		return order
	}

	getOrder := func() *osacv1alpha1.ClusterOrder {
		order := &osacv1alpha1.ClusterOrder{}
		ExpectWithOffset(1, k8sClient.Get(ctx, request.NamespacedName, order)).To(Succeed())
		return order
	}

	reconcile := func() ctrl.Result {
		result, err := reconciler.Reconcile(ctx, request)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		return result
	}

	BeforeEach(func() {
		aapClient = newCSIInstallAAPClient()
		reconciler = newReconciler(k8sClient, true)
		orderName = fmt.Sprintf("csi-install-%d", time.Now().UnixNano())
		request = ctrl.Request{NamespacedName: types.NamespacedName{Name: orderName, Namespace: namespace}}
	})

	It("launches one install job for a Ready ClusterOrder with the add-on job's inputs", func() {
		createOrder(osacv1alpha1.ClusterOrderPhaseReady)

		result := reconcile()
		Expect(result.RequeueAfter).To(Equal(time.Minute))

		Expect(aapClient.launches).To(HaveLen(1))
		launch := aapClient.launches[0]
		Expect(launch.TemplateName).To(Equal(csiInstallTestTemplate))
		Expect(launch.Sensitive).To(BeTrue())
		jobVars, ok := launch.ExtraVars["osac_job_vars"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(jobVars).To(HaveKeyWithValue("admin_kubeconfig", "test-kubeconfig"))
		Expect(jobVars).NotTo(HaveKey("storage_tier_definitions"))
		Expect(jobVars).NotTo(HaveKey("storage_backend_connections"))
		resource, ok := jobVars["resource"].(map[string]any)
		Expect(ok).To(BeTrue())
		metadata, ok := resource["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(metadata).To(HaveKeyWithValue("name", orderName))
		Expect(metadata).To(HaveKeyWithValue("namespace", namespace))

		stored := getOrder()
		Expect(stored.Status.CSIInstallJobs).To(HaveLen(1))
		Expect(stored.Status.CSIInstallJobs[0].JobID).To(Equal("1"))
		Expect(stored.Status.CSIInstallJobs[0].Type).To(Equal(osacv1alpha1.JobTypeProvision))
		condition := findCSIDriverCondition(stored)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(osacv1alpha1.ClusterOrderReasonCSIDriverInstalling))
		Expect(stored.Status.Phase).To(Equal(osacv1alpha1.ClusterOrderPhaseReady))
	})

	It("polls a running job without launching another", func() {
		createOrder(osacv1alpha1.ClusterOrderPhaseReady)
		reconcile()
		aapClient.setJob("1", aap.Job{ID: 1, Status: "running"})

		result := reconcile()
		Expect(result.RequeueAfter).To(Equal(time.Minute))
		reconcile()

		Expect(aapClient.launchCount()).To(Equal(1))
		Expect(aapClient.statusPolls).To(Equal([]string{"1", "1"}))
		stored := getOrder()
		Expect(stored.Status.CSIInstallJobs).To(HaveLen(1))
		Expect(stored.Status.CSIInstallJobs[0].State).To(Equal(osacv1alpha1.JobStateRunning))
		Expect(findCSIDriverCondition(stored).Reason).To(Equal(osacv1alpha1.ClusterOrderReasonCSIDriverInstalling))
	})

	It("sets CSIDriverReady True when the job succeeds and launches nothing more", func() {
		createOrder(osacv1alpha1.ClusterOrderPhaseReady)
		reconcile()
		aapClient.setJob("1", aap.Job{ID: 1, Status: "successful"})

		reconcile()
		result := reconcile()

		Expect(result.RequeueAfter).To(BeZero())
		Expect(aapClient.launchCount()).To(Equal(1))
		stored := getOrder()
		Expect(stored.Status.CSIInstallJobs[0].State).To(Equal(osacv1alpha1.JobStateSucceeded))
		condition := findCSIDriverCondition(stored)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(condition.Reason).To(Equal(osacv1alpha1.ClusterOrderReasonCSIDriverInstalled))
	})

	It("reports a failed job with its message and relaunches only after backoff", func() {
		createOrder(osacv1alpha1.ClusterOrderPhaseReady)
		reconcile()
		aapClient.setJob("1", aap.Job{ID: 1, Status: "failed", ResultTraceback: "helm install failed"})

		result := reconcile()
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		stored := getOrder()
		condition := findCSIDriverCondition(stored)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(osacv1alpha1.ClusterOrderReasonCSIDriverInstallFailed))
		Expect(condition.Message).To(ContainSubstring("helm install failed"))

		result = reconcile()
		Expect(aapClient.launchCount()).To(Equal(1))
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(result.RequeueAfter).To(BeNumerically("<=", provisioning.BackoffBaseDelay))

		stored = getOrder()
		stored.Status.CSIInstallJobs[0].Timestamp = metav1.NewTime(time.Now().Add(-provisioning.BackoffBaseDelay - time.Second))
		Expect(k8sClient.Status().Update(ctx, stored)).To(Succeed())
		reconcile()

		Expect(aapClient.launchCount()).To(Equal(2))
		stored = getOrder()
		Expect(stored.Status.CSIInstallJobs).To(HaveLen(2))
		Expect(stored.Status.CSIInstallJobs[1].JobID).To(Equal("2"))
		Expect(findCSIDriverCondition(stored).Reason).To(Equal(osacv1alpha1.ClusterOrderReasonCSIDriverInstalling))
	})

	It("reports a launch failure as CSIDriverInstallFailed", func() {
		createOrder(osacv1alpha1.ClusterOrderPhaseReady)
		aapClient.templateErr = errors.New("template osac-install-cluster-csi not found")

		_, _ = reconciler.Reconcile(ctx, request)

		Expect(aapClient.launchCount()).To(BeZero())
		condition := findCSIDriverCondition(getOrder())
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(osacv1alpha1.ClusterOrderReasonCSIDriverInstallFailed))
		Expect(condition.Message).To(ContainSubstring("not found"))
	})

	It("does nothing and writes no condition when disabled", func() {
		reconciler = newReconciler(k8sClient, false)
		createOrder(osacv1alpha1.ClusterOrderPhaseReady)

		result := reconcile()

		Expect(result).To(Equal(ctrl.Result{}))
		Expect(aapClient.launchCount()).To(BeZero())
		stored := getOrder()
		Expect(stored.Status.CSIInstallJobs).To(BeEmpty())
		Expect(findCSIDriverCondition(stored)).To(BeNil())
	})

	It("does not launch before the ClusterOrder is Ready", func() {
		createOrder(osacv1alpha1.ClusterOrderPhaseProgressing)

		reconcile()

		Expect(aapClient.launchCount()).To(BeZero())
		Expect(findCSIDriverCondition(getOrder())).To(BeNil())
	})

	It("waits for the hosted cluster kubeconfig before launching", func() {
		reconciler.getAdminKubeconfig = func(context.Context, *osacv1alpha1.ClusterOrder) ([]byte, error) {
			return nil, nil
		}
		createOrder(osacv1alpha1.ClusterOrderPhaseReady)

		result := reconcile()

		Expect(result.RequeueAfter).To(Equal(time.Minute))
		Expect(aapClient.launchCount()).To(BeZero())
		Expect(getOrder().Status.CSIInstallJobs).To(BeEmpty())
	})

	It("re-checks the API server for a non-terminal job before launching", func() {
		createOrder(osacv1alpha1.ClusterOrderPhaseReady)
		reconcile()
		Expect(aapClient.launchCount()).To(Equal(1))

		reconciler = newReconciler(&staleFirstReader{Reader: k8sClient}, true)
		result := reconcile()

		Expect(result.RequeueAfter).To(Equal(time.Minute))
		Expect(aapClient.launchCount()).To(Equal(1))
	})

	It("cancels a non-terminal install job when the ClusterOrder is deleted", func() {
		const holdFinalizer = "osac.openshift.io/test-hold"
		createOrder(osacv1alpha1.ClusterOrderPhaseReady, holdFinalizer)
		reconcile()
		aapClient.setJob("1", aap.Job{ID: 1, Status: "running"})
		reconcile()
		Expect(k8sClient.Delete(ctx, getOrder())).To(Succeed())

		result := reconcile()

		Expect(result).To(Equal(ctrl.Result{}))
		Expect(aapClient.canceled).To(Equal([]string{"1"}))
		Expect(aapClient.launchCount()).To(Equal(1))
		stored := getOrder()
		Expect(stored.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(controllerutil.ContainsFinalizer(stored, holdFinalizer)).To(BeTrue())
	})

	It("does not cancel a finished install job when the ClusterOrder is deleted", func() {
		const holdFinalizer = "osac.openshift.io/test-hold"
		createOrder(osacv1alpha1.ClusterOrderPhaseReady, holdFinalizer)
		reconcile()
		aapClient.setJob("1", aap.Job{ID: 1, Status: "successful"})
		reconcile()
		Expect(k8sClient.Delete(ctx, getOrder())).To(Succeed())

		reconcile()

		Expect(aapClient.canceled).To(BeEmpty())
		Expect(aapClient.launchCount()).To(Equal(1))
	})

	It("skips unmanaged ClusterOrders", func() {
		createOrder(osacv1alpha1.ClusterOrderPhaseReady)
		order := getOrder()
		order.Annotations[osacManagementStateAnnotation] = ManagementStateUnmanaged
		Expect(k8sClient.Update(ctx, order)).To(Succeed())

		reconcile()

		Expect(aapClient.launchCount()).To(BeZero())
	})

	It("registers with the manager and launches when a ClusterOrder becomes Ready", func() {
		const managerNamespace = "csi-install-manager-test"
		localManager, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:  k8sClient.Scheme(),
			Metrics: metricsserver.Options{BindAddress: "0"},
		})
		Expect(err).NotTo(HaveOccurred())
		manager, err := mcmanager.WithMultiCluster(localManager, nil)
		Expect(err).NotTo(HaveOccurred())
		provider := provisioning.NewAAPProvider(aapClient, csiInstallTestTemplate, "")
		watched := NewCSIInstallReconciler(k8sClient, k8sClient, managerNamespace, provider, 10*time.Millisecond, true)
		watched.getAdminKubeconfig = func(context.Context, *osacv1alpha1.ClusterOrder) ([]byte, error) {
			return []byte("test-kubeconfig"), nil
		}
		Expect(watched.SetupWithManager(manager)).To(Succeed())

		startCtx, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		go func() { _ = localManager.Start(startCtx) }()
		Expect(localManager.GetCache().WaitForCacheSync(startCtx)).To(BeTrue())
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: managerNamespace}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, ns) })

		order := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "csi-watched-order", Namespace: managerNamespace},
			Spec:       osacv1alpha1.ClusterOrderSpec{TemplateID: "test.template"},
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, order) })
		order.Status.Phase = osacv1alpha1.ClusterOrderPhaseReady
		Expect(k8sClient.Status().Update(ctx, order)).To(Succeed())

		Eventually(func() int {
			current := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(order), current); err != nil {
				return 0
			}
			return len(current.Status.CSIInstallJobs)
		}, 5*time.Second, 50*time.Millisecond).Should(Equal(1))
	})
})
