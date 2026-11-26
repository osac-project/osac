package controller

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	events "k8s.io/client-go/tools/events"
	clocktesting "k8s.io/utils/clock/testing"
	kubevirtv1 "kubevirt.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type checkpointAAP struct {
	job aap.Job
}

func (a *checkpointAAP) LaunchJobTemplate(context.Context, aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
	a.job = aap.Job{ID: 101, Status: "pending"}
	return &aap.LaunchJobTemplateResponse{JobID: a.job.ID}, nil
}

func (a *checkpointAAP) GetJob(_ context.Context, _ string) (*aap.Job, error) {
	job := a.job
	return &job, nil
}

func (*checkpointAAP) CancelJob(context.Context, string) error { return nil }

func (*checkpointAAP) ListJobsByLabel(context.Context, string) ([]aap.Job, error) { return nil, nil }

func (*checkpointAAP) EnsureLabel(context.Context, string, string) (int, error) { return 1, nil }

var _ = Describe("ComputeInstance provision failure checkpoint", func() {

	It("round trips and clears a complete public failure while rejecting incomplete status", func() {
		instance := &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("public-failure-%d", time.Now().UnixNano()), Namespace: "default"},
			Spec:       newTestComputeInstanceSpec("test_template"),
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, instance)).To(Succeed()) })
		observed := metav1.NewTime(time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC))
		failure := &osacv1alpha1.ComputeInstanceProvisioningFailure{
			Reason: "ProviderReported", Message: "Provisioning failed.", Attempt: 2, ObservedAt: &observed,
		}
		instance.Status.LastProvisioningFailure = failure
		Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		stored := &osacv1alpha1.ComputeInstance{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stored)).To(Succeed())
		Expect(stored.Status.LastProvisioningFailure).NotTo(BeNil())
		Expect(stored.Status.LastProvisioningFailure.Reason).To(Equal(failure.Reason))
		Expect(stored.Status.LastProvisioningFailure.Message).To(Equal(failure.Message))
		Expect(stored.Status.LastProvisioningFailure.Attempt).To(Equal(failure.Attempt))
		Expect(stored.Status.LastProvisioningFailure.ObservedAt.Time.Equal(observed.Time)).To(BeTrue())
		copied := stored.DeepCopy()
		copied.Status.LastProvisioningFailure.ObservedAt.Time = observed.Add(time.Hour)
		Expect(stored.Status.LastProvisioningFailure.ObservedAt.Time.Equal(observed.Time)).To(BeTrue())
		for _, mutate := range []func(*osacv1alpha1.ComputeInstanceProvisioningFailure){
			func(f *osacv1alpha1.ComputeInstanceProvisioningFailure) { f.Reason = "" },
			func(f *osacv1alpha1.ComputeInstanceProvisioningFailure) { f.Reason = "Unknown" },
			func(f *osacv1alpha1.ComputeInstanceProvisioningFailure) { f.Message = "" },
			func(f *osacv1alpha1.ComputeInstanceProvisioningFailure) { f.Attempt = 0 },
			func(f *osacv1alpha1.ComputeInstanceProvisioningFailure) { f.ObservedAt = nil },
		} {
			invalid := stored.DeepCopy()
			mutate(invalid.Status.LastProvisioningFailure)
			Expect(apierrors.IsInvalid(k8sClient.Status().Update(ctx, invalid))).To(BeTrue())
		}
		stored.Status.LastProvisioningFailure = nil
		Expect(k8sClient.Status().Update(ctx, stored)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stored)).To(Succeed())
		Expect(stored.Status.LastProvisioningFailure).To(BeNil())
	})
	It("clears an old configuration failure during retry without resetting backoff", func() {
		By("creating the instance and arranging the provision engine")
		instance := &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("retry-config-%d", time.Now().UnixNano()), Namespace: "default"},
			Spec:       newTestComputeInstanceSpec("test_template"),
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			Expect(k8sClient.Delete(cleanupCtx, instance)).To(Succeed())
		})
		instance.Status.DesiredConfigVersion = "old"
		instance.Status.ProvisionConfigVersion = "old"
		Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		clock := clocktesting.NewFakeClock(time.Now().Truncate(time.Second))
		aapClient := &checkpointAAP{}
		engine := networktask.New(k8sClient, k8sClient, aapClient, networktask.Config{Organization: "osac"}).WithClock(clock)
		r := &ComputeInstanceReconciler{Client: k8sClient, Engine: engine, ProvisionTemplate: "provision"}
		_, err := r.handleProvisioning(ctx, instance, "default", true)
		Expect(err).NotTo(HaveOccurred())
		By("recording a retryable provisioning failure")
		aapClient.job.Status = "failed"
		aapClient.job.Artifacts = []byte(`{"osac_error":{"message":"private","retryable":true}}`)
		failed, err := r.handleProvisioning(ctx, instance, "default", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(failed.LastFailure).NotTo(BeNil())
		By("rejecting a stale checkpoint-clearing write without changing stored state")
		clock.Step(time.Second)
		stale := instance.DeepCopy()
		instance.Annotations = map[string]string{"test": "new-resource-version"}
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		stale.Status.DesiredConfigVersion = "new"
		err = r.handleDesiredConfigVersion(ctx, stale)
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		Expect(instance.Status.Jobs[0].LastFailure).NotTo(BeNil())
		Expect(instance.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateRetrying))
		By("invalidating the previous configuration failure from fresh state")
		Expect(r.handleDesiredConfigVersion(ctx, instance)).To(Succeed())
		out, err := r.handleProvisioning(ctx, instance, "default", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Retrying()).To(BeTrue())
		Expect(out.LastFailure).To(BeNil())
		Expect(out.RequeueAfter).To(Equal(failed.RequeueAfter - time.Second))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		Expect(instance.Status.Jobs[0].LastFailure).To(BeNil())
		By("launching the next attempt with the original retry schedule")
		clock.Step(out.RequeueAfter)
		out, err = r.handleProvisioning(ctx, instance, "default", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Entry.State).To(Equal(osacv1alpha1.NetworkJobStateRunning))
		Expect(out.Entry.Attempt).To(Equal(int32(2)))
		Expect(out.LastFailure).To(BeNil())
		By("recording a retryable provisioning failure")
		aapClient.job.Status = "failed"
		aapClient.job.Artifacts = []byte(`{"osac_error":{"message":"private","retryable":true}}`)
		out, err = r.handleProvisioning(ctx, instance, "default", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.RequeueAfter).To(Equal(networktask.Backoff("provision", 2, 2)))
	})

	It("clears a cancelled provision checkpoint only when its configuration changes", func() {
		By("creating the instance and arranging the provision engine")
		instance := &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("cancel-config-%d", time.Now().UnixNano()), Namespace: "default"},
			Spec:       newTestComputeInstanceSpec("test_template"),
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			Expect(k8sClient.Delete(cleanupCtx, instance)).To(Succeed())
		})
		instance.Status.DesiredConfigVersion = "old"
		instance.Status.ProvisionConfigVersion = "old"
		Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		clock := clocktesting.NewFakeClock(time.Now().Truncate(time.Second))
		aapClient := &checkpointAAP{}
		engine := networktask.New(k8sClient, k8sClient, aapClient, networktask.Config{Organization: "osac"}).WithClock(clock)
		r := &ComputeInstanceReconciler{Client: k8sClient, Engine: engine, ProvisionTemplate: "provision"}
		_, err := r.handleProvisioning(ctx, instance, "default", true)
		Expect(err).NotTo(HaveOccurred())
		By("recording a retryable provisioning failure")
		aapClient.job.Status = "failed"
		aapClient.job.Artifacts = []byte(`{"osac_error":{"message":"private","retryable":true}}`)
		failed, err := r.handleProvisioning(ctx, instance, "default", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(failed.LastFailure).NotTo(BeNil())
		clock.Step(failed.RequeueAfter)
		_, err = r.handleProvisioning(ctx, instance, "default", true)
		Expect(err).NotTo(HaveOccurred())
		By("holding a cancelled retry with its same-configuration checkpoint")
		aapClient.job.Status = "canceled"
		cancelled, err := r.handleProvisioning(ctx, instance, "default", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(cancelled.Entry.State).To(Equal(osacv1alpha1.NetworkJobStateCancelled))
		Expect(cancelled.LastFailure).NotTo(BeNil())
		Expect(cancelled.LastFailure.Reason).To(Equal(failed.LastFailure.Reason))
		Expect(cancelled.LastFailure.Message).To(Equal(failed.LastFailure.Message))
		Expect(cancelled.LastFailure.Attempt).To(Equal(failed.LastFailure.Attempt))
		Expect(cancelled.LastFailure.ObservedAt.Time.Equal(failed.LastFailure.ObservedAt.Time)).To(BeTrue())
		held, err := r.handleProvisioning(ctx, instance, "default", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(held.LastFailure).NotTo(BeNil())
		Expect(held.LastFailure.Reason).To(Equal(failed.LastFailure.Reason))
		Expect(held.LastFailure.Message).To(Equal(failed.LastFailure.Message))
		Expect(held.LastFailure.Attempt).To(Equal(failed.LastFailure.Attempt))
		Expect(held.LastFailure.ObservedAt.Time.Equal(failed.LastFailure.ObservedAt.Time)).To(BeTrue())
		By("invalidating the previous configuration failure from fresh state")
		Expect(r.handleDesiredConfigVersion(ctx, instance)).To(Succeed())
		held, err = r.handleProvisioning(ctx, instance, "default", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(held.Entry.State).To(Equal(osacv1alpha1.NetworkJobStateCancelled))
		Expect(held.LastFailure).To(BeNil())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		Expect(instance.Status.Jobs[0].LastFailure).To(BeNil())
		By("launching the changed configuration without the previous failure")
		out, err := r.handleProvisioning(ctx, instance, "default", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Entry.State).To(Equal(osacv1alpha1.NetworkJobStateRunning))
		Expect(out.Entry.Attempt).To(Equal(int32(3)))
		Expect(out.LastFailure).To(BeNil())
		Expect(instance.Status.ProvisionConfigVersion).To(Equal(instance.Status.DesiredConfigVersion))
		By("recording a retryable provisioning failure")
		aapClient.job.Status = "failed"
		aapClient.job.Artifacts = []byte(`{"osac_error":{"message":"private","retryable":true}}`)
		out, err = r.handleProvisioning(ctx, instance, "default", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.RequeueAfter).To(Equal(networktask.Backoff("provision", 2, 3)))
	})

	It("persists atomically across resource-version conflicts and validates the CRD shape", func() {
		name := fmt.Sprintf("provision-failure-%d", time.Now().UnixNano())
		By("creating the instance and arranging the provision engine")
		instance := &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID("uid-" + name)},
			Spec:       newTestComputeInstanceSpec("test_template"),
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			current := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(cleanupCtx, client.ObjectKeyFromObject(instance), current)
			if apierrors.IsNotFound(err) {
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(cleanupCtx, current)).To(Succeed())
		})

		clock := clocktesting.NewFakeClock(time.Date(2026, 10, 1, 12, 0, 0, 123000000, time.UTC))
		aapClient := &checkpointAAP{}
		engine := networktask.New(k8sClient, k8sClient, aapClient, networktask.Config{
			TemplatePrefix: "osac", Organization: "osac", ProvisioningEnabled: true,
		}).WithClock(clock)
		req := networktask.Request{
			Task: "provision", Manager: osacv1alpha1.NetworkJobManagerNone,
			Kind: "ComputeInstance", Template: "provision", Accept: networktask.AcceptNoOutput,
		}
		out, err := engine.Run(ctx, instance, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Entry.State).To(Equal(osacv1alpha1.NetworkJobStateRunning))

		By("conflicting the failure write and verifying atomic persistence")
		stale := &osacv1alpha1.ComputeInstance{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stale)).To(Succeed())
		newer := stale.DeepCopy()
		newer.Annotations = map[string]string{"test": "new-resource-version"}
		Expect(k8sClient.Update(ctx, newer)).To(Succeed())
		By("recording a retryable provisioning failure")
		aapClient.job.Status = "failed"
		aapClient.job.Artifacts = []byte(`{"osac_error":{"message":"private provider detail","retryable":true}}`)

		conflicted, err := engine.Run(ctx, stale, req)
		Expect(conflicted.Entry.Task).To(BeEmpty())
		Expect(conflicted.LastFailure).To(BeNil())
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		stored := &osacv1alpha1.ComputeInstance{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stored)).To(Succeed())
		Expect(stored.Status.Jobs).To(HaveLen(1))
		Expect(stored.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateRunning))
		Expect(stored.Status.Jobs[0].LastFailure).To(BeNil())

		out, err = engine.Run(ctx, stored, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Retrying()).To(BeTrue())
		Expect(out.LastFailure).NotTo(BeNil())
		Expect(out.LastFailure.Reason).To(Equal("ProviderReported"))
		Expect(out.LastFailure.Message).To(Equal("Provisioning failed."))
		Expect(out.LastFailure.Attempt).To(Equal(int32(1)))
		Expect(out.LastFailure.ObservedAt.Time.Equal(clock.Now().UTC().Truncate(time.Second))).To(BeTrue())
		Expect(out.Entry.Message).To(ContainSubstring("private provider detail"))

		stored = &osacv1alpha1.ComputeInstance{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stored)).To(Succeed())
		storedFailure := stored.Status.Jobs[0].LastFailure
		Expect(storedFailure.Reason).To(Equal(out.LastFailure.Reason))
		Expect(storedFailure.Message).To(Equal(out.LastFailure.Message))
		Expect(storedFailure.Attempt).To(Equal(out.LastFailure.Attempt))
		Expect(storedFailure.ObservedAt.Time.Equal(out.LastFailure.ObservedAt.Time)).To(BeTrue())

		By("replaying the stored checkpoint across restart and retry launch")
		// Recreate the engine from stored state, then advance through a retry.
		engine = networktask.New(k8sClient, k8sClient, aapClient, networktask.Config{Organization: "osac"}).WithClock(clock)
		replay, err := engine.Run(ctx, stored, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(replay.Retrying()).To(BeTrue())
		Expect(replay.LastFailure.ObservedAt.Time.Equal(storedFailure.ObservedAt.Time)).To(BeTrue())
		clock.Step(replay.RequeueAfter)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stored)).To(Succeed())
		running, err := engine.Run(ctx, stored, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(running.Entry.State).To(Equal(osacv1alpha1.NetworkJobStateRunning))
		Expect(running.Entry.Attempt).To(Equal(int32(2)))
		Expect(running.LastFailure.Attempt).To(Equal(int32(1)))
		Expect(running.LastFailure.ObservedAt.Time.Equal(storedFailure.ObservedAt.Time)).To(BeTrue())
		By("preserving the committed checkpoint when a later status update conflicts")
		// A caller's later projection may conflict; the checkpoint stays stored.
		staleProjection := stored.DeepCopy()
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stored)).To(Succeed())
		stored.Annotations["test"] = "after-launch"
		Expect(k8sClient.Update(ctx, stored)).To(Succeed())
		Expect(apierrors.IsConflict(k8sClient.Status().Update(ctx, staleProjection))).To(BeTrue())
		engine = networktask.New(k8sClient, k8sClient, aapClient, networktask.Config{Organization: "osac"}).WithClock(clock)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stored)).To(Succeed())
		aapClient.job.Status = "running"
		replay, err = engine.Run(ctx, stored, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(replay.LastFailure.Attempt).To(Equal(storedFailure.Attempt))
		Expect(replay.LastFailure.Reason).To(Equal(storedFailure.Reason))
		Expect(replay.LastFailure.Message).To(Equal(storedFailure.Message))
		Expect(replay.LastFailure.ObservedAt.Time.Equal(storedFailure.ObservedAt.Time)).To(BeTrue())
		By("clearing the failure on success and replaying successful state")
		aapClient.job.Status = "successful"
		aapClient.job.Artifacts = []byte(`{}`)
		out, err = engine.Run(ctx, stored, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Succeeded()).To(BeTrue())
		Expect(out.LastFailure).To(BeNil())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stored)).To(Succeed())
		Expect(stored.Status.Jobs[0].LastFailure).To(BeNil())
		staleSuccessProjection := stored.DeepCopy()
		stored.Annotations["test"] = "after-success"
		Expect(k8sClient.Update(ctx, stored)).To(Succeed())
		Expect(apierrors.IsConflict(k8sClient.Status().Update(ctx, staleSuccessProjection))).To(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stored)).To(Succeed())
		engine = networktask.New(k8sClient, k8sClient, aapClient, networktask.Config{Organization: "osac"}).WithClock(clock)
		out, err = engine.Run(ctx, stored, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Succeeded()).To(BeTrue())
		Expect(out.LastFailure).To(BeNil())

		By("rejecting invalid checkpoint shapes at the Kubernetes API")
		partial := stored.DeepCopy()
		partial.Status.Jobs[0].LastFailure = &osacv1alpha1.NetworkJobFailure{Reason: "ProviderReported"}
		Expect(apierrors.IsInvalid(k8sClient.Status().Update(ctx, partial))).To(BeTrue())
		zeroAttempt := stored.DeepCopy()
		zeroAttempt.Status.Jobs[0].LastFailure = storedFailure.DeepCopy()
		zeroAttempt.Status.Jobs[0].LastFailure.Attempt = 0
		Expect(apierrors.IsInvalid(k8sClient.Status().Update(ctx, zeroAttempt))).To(BeTrue())
	})
})

type projectionConflictClient struct {
	client.Client
	when func(*osacv1alpha1.ComputeInstance) bool
}

type projectionConflictWriter struct {
	client.SubResourceWriter
	owner *projectionConflictClient
}

func (c *projectionConflictClient) Status() client.SubResourceWriter {
	return projectionConflictWriter{SubResourceWriter: c.Client.Status(), owner: c}
}

func (w projectionConflictWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	ci, ok := obj.(*osacv1alpha1.ComputeInstance)
	if ok && w.owner.when != nil && w.owner.when(ci) {
		w.owner.when = nil
		fresh := &osacv1alpha1.ComputeInstance{}
		Expect(w.owner.Client.Get(ctx, client.ObjectKeyFromObject(ci), fresh)).To(Succeed())
		if fresh.Annotations == nil {
			fresh.Annotations = map[string]string{}
		}
		fresh.Annotations["test-conflict"] = fresh.ResourceVersion
		Expect(w.owner.Client.Update(ctx, fresh)).To(Succeed())
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

var _ = Describe("ComputeInstance public retry projection", func() {
	var instance *osacv1alpha1.ComputeInstance
	var fake *wlFakeAAP
	var clock *clocktesting.FakeClock
	var reconciler *ComputeInstanceReconciler
	var writes *projectionConflictClient
	var key types.NamespacedName

	reload := func() {
		Expect(k8sClient.Get(ctx, key, instance)).To(Succeed())
	}
	recreate := func() {
		reconciler = newTestCIReconcilerWithFake(fake, "", "default", "", time.Second)
		reconciler.Client = writes
		reconciler.Engine = networktask.New(writes, k8sClient, fake, networktask.Config{
			Organization: "osac", PollInterval: time.Second,
		}).WithClock(clock)
	}
	run := func() (ctrl.Result, error) {
		return reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: key}})
	}
	reconcileOK := func() ctrl.Result {
		result, err := run()
		Expect(err).NotTo(HaveOccurred())
		reload()
		return result
	}
	expectReason := func(reason string) {
		condition := instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(reason))
		Expect(condition.Message).NotTo(ContainSubstring("private-provider-text"))
	}
	BeforeEach(func() {
		name := fmt.Sprintf("retry-projection-%d", time.Now().UnixNano())
		createReadyTenant(ctx, "default", name)
		DeferCleanup(func() { deleteTenantInNamespace(ctx, "default", name) })
		instance = &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Annotations: map[string]string{osacTenantKey: name}},
			Spec:       newTestComputeInstanceSpec("test_template"),
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		key = client.ObjectKeyFromObject(instance)
		DeferCleanup(func() {
			fresh := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, key, fresh)
			if apierrors.IsNotFound(err) {
				return
			}
			Expect(err).NotTo(HaveOccurred())
			fresh.Finalizers = nil
			Expect(k8sClient.Update(ctx, fresh)).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, fresh))).To(Succeed())
		})
		fake = newWLFakeAAP()
		clock = clocktesting.NewFakeClock(time.Now().Truncate(time.Second))
		writes = &projectionConflictClient{Client: k8sClient}
		recreate()
		reconcileOK()
	})

	DescribeTable("suppresses saved retry progress in manual mode and resumes in managed mode", func(running bool) {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		result := reconcileOK()
		wantReason := "RetryScheduled"
		if running {
			clock.Step(result.RequeueAfter)
			reconcileOK()
			wantReason = "RetryRunning"
		}
		expectReason(wantReason)
		failure := instance.Status.LastProvisioningFailure.DeepCopy()
		jobs := instance.DeepCopy().Status.Jobs
		instance.Annotations[osacComputeInstanceManagementStateAnnotation] = ManagementStateManual
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		clock.Step(time.Hour)
		recreate()
		for range 2 {
			reconcileOK()
			expectReason(osacv1alpha1.ReasonWaitingForVM)
			Expect(instance.Status.LastProvisioningFailure).To(Equal(failure))
			Expect(instance.Status.Jobs).To(Equal(jobs))
		}
		delete(instance.Annotations, osacComputeInstanceManagementStateAnnotation)
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		result = reconcileOK()
		if !running {
			// A recreated engine restores the retry wait from the saved job.
			expectReason("RetryScheduled")
			clock.Step(result.RequeueAfter)
			reconcileOK()
		}
		expectReason("RetryRunning")
		Expect(instance.Status.LastProvisioningFailure).To(Equal(failure))
		if running {
			Expect(instance.Status.Jobs).To(Equal(jobs))
		} else {
			Expect(instance.Status.Jobs[0].Attempt).To(Equal(jobs[0].Attempt + 1))
		}
	},
		Entry("scheduled retry", false),
		Entry("running retry", true),
	)

	DescribeTable("recovers saved failure details in manual mode after a summary conflict", func(stale bool) {
		attempt := int32(1)
		if stale {
			fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
			result := reconcileOK()
			clock.Step(result.RequeueAfter)
			reconcileOK()
			clock.Step(time.Second)
			attempt = 2
		}
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text-latest","retryable":true}}`)
		writes.when = func(ci *osacv1alpha1.ComputeInstance) bool {
			return ci.Status.LastProvisioningFailure != nil && ci.Status.LastProvisioningFailure.Attempt == attempt
		}
		_, err := run()
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		reload()
		checkpoint := instance.Status.Jobs[0].LastFailure.DeepCopy()
		Expect(checkpoint.Attempt).To(Equal(attempt))
		if stale {
			Expect(instance.Status.LastProvisioningFailure.Attempt).To(Equal(int32(1)))
		} else {
			Expect(instance.Status.LastProvisioningFailure).To(BeNil())
		}
		jobs := instance.DeepCopy().Status.Jobs
		instance.Annotations[osacComputeInstanceManagementStateAnnotation] = ManagementStateManual
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		recreate()
		for range 2 {
			reconcileOK()
			expectReason(osacv1alpha1.ReasonWaitingForVM)
			failure := instance.Status.LastProvisioningFailure
			Expect(failure).NotTo(BeNil())
			Expect(failure.Reason).To(Equal(checkpoint.Reason))
			Expect(failure.Message).To(Equal(checkpoint.Message))
			Expect(failure.Attempt).To(Equal(checkpoint.Attempt))
			Expect(failure.ObservedAt.Time.Equal(checkpoint.ObservedAt.Time)).To(BeTrue())
			Expect(instance.Status.Jobs).To(Equal(jobs))
			Expect(fake.tasks()).To(HaveLen(int(attempt)))
		}
	},
		Entry("empty summary", false),
		Entry("stale summary", true),
	)

	It("preserves failure details after a manual configuration change with a ready tenant", func() {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		reconcileOK()
		failure := instance.Status.LastProvisioningFailure.DeepCopy()
		jobs := instance.DeepCopy().Status.Jobs
		instance.Annotations[osacComputeInstanceManagementStateAnnotation] = ManagementStateManual
		restart := metav1.NewTime(clock.Now().Add(time.Second))
		instance.Spec.RestartRequestedAt = &restart
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		recreate()
		for range 2 {
			reconcileOK()
			Expect(instance.Status.DesiredConfigVersion).NotTo(Equal(instance.Status.ProvisionConfigVersion))
			expectReason(osacv1alpha1.ReasonWaitingForVM)
			Expect(instance.Status.LastProvisioningFailure).To(Equal(failure))
			Expect(instance.Status.Jobs).To(Equal(jobs))
			Expect(fake.tasks()).To(HaveLen(1))
		}
	})

	It("replays the latest failure and successful clearing across real projection conflicts and restart", func() {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		result := reconcileOK()
		expectReason("RetryScheduled")
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		first := instance.Status.LastProvisioningFailure.DeepCopy()
		Expect(first.Attempt).To(Equal(int32(1)))
		Expect(first.Message).To(Equal("Provisioning failed."))
		Expect(instance.Status.Jobs[0].Message).To(ContainSubstring("private-provider-text"))
		clock.Step(result.RequeueAfter)
		reconcileOK()
		expectReason("RetryRunning")
		Expect(instance.Status.LastProvisioningFailure.Attempt).To(Equal(int32(1)))
		clock.Step(time.Second)
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text-second","retryable":true}}`)
		writes.when = func(ci *osacv1alpha1.ComputeInstance) bool {
			return ci.Status.LastProvisioningFailure != nil && ci.Status.LastProvisioningFailure.Attempt == 2
		}
		_, err := run()
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		reload()
		checkpoint := instance.Status.Jobs[0].LastFailure.DeepCopy()
		Expect(checkpoint.Attempt).To(Equal(int32(2)))
		Expect(instance.Status.LastProvisioningFailure.Attempt).To(Equal(int32(1)))
		recreate()
		result = reconcileOK()
		expectReason("RetryScheduled")
		Expect(instance.Status.LastProvisioningFailure.Attempt).To(Equal(checkpoint.Attempt))
		Expect(instance.Status.LastProvisioningFailure.ObservedAt.Time.Equal(checkpoint.ObservedAt.Time)).To(BeTrue())
		Expect(checkpoint.ObservedAt.After(first.ObservedAt.Time)).To(BeTrue())
		remote := privatev1.ComputeInstance_builder{Status: privatev1.ComputeInstanceStatus_builder{}.Build()}.Build()
		Expect(syncComputeInstanceUpdate(ctx, instance, remote)).To(Succeed())
		Expect(remote.GetStatus().GetLastProvisioningFailure()).NotTo(BeNil())
		Expect(remote.GetStatus().GetLastProvisioningFailure().GetAttempt()).To(Equal(checkpoint.Attempt))
		Expect(remote.GetStatus().GetLastProvisioningFailure().GetObservedAt().AsTime().Equal(checkpoint.ObservedAt.Time)).To(BeTrue())
		clock.Step(result.RequeueAfter)
		writes.when = func(ci *osacv1alpha1.ComputeInstance) bool {
			c := ci.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned)
			return c != nil && c.Reason == "RetryRunning"
		}
		_, err = run()
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		reload()
		Expect(instance.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateRunning))
		recreate()
		reconcileOK()
		expectReason("RetryRunning")
		Expect(instance.Status.Jobs[0].Attempt).To(Equal(int32(3)))
		Expect(instance.Status.LastProvisioningFailure.ObservedAt.Time.Equal(checkpoint.ObservedAt.Time)).To(BeTrue())
		fake.finish(computeInstanceProvisionTask, "successful", `{}`)
		writes.when = func(ci *osacv1alpha1.ComputeInstance) bool { return ci.Status.LastProvisioningFailure == nil }
		_, err = run()
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		reload()
		Expect(instance.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateSucceeded))
		Expect(instance.Status.Jobs[0].LastFailure).To(BeNil())
		Expect(instance.Status.LastProvisioningFailure).NotTo(BeNil())
		recreate()
		reconcileOK()
		expectReason(osacv1alpha1.ReasonWaitingForVM)
		Expect(instance.Status.LastProvisioningFailure).To(BeNil())
	})

	DescribeTable("replays persisted success clearing before tenant readiness", func(manual bool) {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		result := reconcileOK()
		clock.Step(result.RequeueAfter)
		reconcileOK()
		fake.finish(computeInstanceProvisionTask, "successful", `{}`)
		writes.when = func(ci *osacv1alpha1.ComputeInstance) bool { return ci.Status.LastProvisioningFailure == nil }
		_, err := run()
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		reload()
		Expect(instance.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateSucceeded))
		Expect(instance.Status.Jobs[0].LastFailure).To(BeNil())
		Expect(instance.Status.LastProvisioningFailure).NotTo(BeNil())
		entry := instance.Status.Jobs[0].DeepCopy()
		if manual {
			instance.Annotations[osacComputeInstanceManagementStateAnnotation] = ManagementStateManual
			Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		}
		tenant := &osacv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: instance.Annotations[osacTenantKey], Namespace: "default"}, tenant)).To(Succeed())
		tenant.Status.Phase = osacv1alpha1.TenantPhaseProgressing
		Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
		recreate()
		result = reconcileOK()
		expectReason(osacv1alpha1.ReasonTenantNotReady)
		Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))
		Expect(instance.Status.Jobs[0]).To(Equal(*entry))
		Expect(fake.tasks()).To(HaveLen(2))
		if manual {
			Expect(instance.Status.LastProvisioningFailure).NotTo(BeNil())
		} else {
			Expect(instance.Status.LastProvisioningFailure).To(BeNil())
		}
	},
		Entry("managed replay after conflict", false),
		Entry("manual exclusion after conflict", true),
	)

	DescribeTable("retains current failure history before tenant readiness", func(running bool) {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		result := reconcileOK()
		if running {
			clock.Step(result.RequeueAfter)
			reconcileOK()
		}
		observed := instance.Status.LastProvisioningFailure.ObservedAt.DeepCopy()
		entry := instance.Status.Jobs[0].DeepCopy()
		tenant := &osacv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: instance.Annotations[osacTenantKey], Namespace: "default"}, tenant)).To(Succeed())
		tenant.Status.Phase = osacv1alpha1.TenantPhaseProgressing
		Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
		reconcileOK()
		Expect(instance.Status.LastProvisioningFailure).NotTo(BeNil())
		Expect(instance.Status.LastProvisioningFailure.ObservedAt.Time.Equal(observed.Time)).To(BeTrue())
		Expect(instance.Status.Jobs[0]).To(Equal(*entry))
	},
		Entry("retry backoff", false),
		Entry("running retry", true),
	)

	DescribeTable("invalidates only obsolete managed diagnostics before the tenant-readiness gate", func(changeSpec, manual, checkpointCleared, wantFailure bool) {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		reconcileOK()
		entry := instance.Status.Jobs[0].DeepCopy()
		if checkpointCleared {
			instance.Status.Jobs[0].LastFailure = nil
			Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		}
		if changeSpec {
			restart := metav1.NewTime(clock.Now().Add(time.Second))
			instance.Spec.RestartRequestedAt = &restart
		}
		if manual {
			instance.Annotations[osacComputeInstanceManagementStateAnnotation] = ManagementStateManual
		}
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		tenant := &osacv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: instance.Annotations[osacTenantKey], Namespace: "default"}, tenant)).To(Succeed())
		tenant.Status.Phase = osacv1alpha1.TenantPhaseProgressing
		Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
		result := reconcileOK()
		expectReason(osacv1alpha1.ReasonTenantNotReady)
		Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))
		Expect(instance.Status.Jobs[0].State).To(Equal(entry.State))
		Expect(instance.Status.Jobs[0].Attempt).To(Equal(entry.Attempt))
		Expect(instance.Status.Jobs[0].JobID).To(Equal(entry.JobID))
		Expect(instance.Status.Jobs[0].Message).To(Equal(entry.Message))
		Expect(fake.tasks()).To(HaveLen(1))
		if wantFailure {
			Expect(instance.Status.LastProvisioningFailure).NotTo(BeNil())
			Expect(instance.Status.Jobs[0].LastFailure).NotTo(BeNil())
		} else {
			Expect(instance.Status.LastProvisioningFailure).To(BeNil())
			Expect(instance.Status.Jobs[0].LastFailure).To(BeNil())
		}
	},
		Entry("changed configuration", true, false, false, false),
		Entry("public clearing replay", true, false, true, false),
		Entry("same configuration", false, false, false, true),
		Entry("manual management", true, true, false, true),
	)

	It("retries obsolete diagnostic clearing after a real conflict while the tenant is unready", func() {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		reconcileOK()
		restart := metav1.NewTime(clock.Now().Add(time.Second))
		instance.Spec.RestartRequestedAt = &restart
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		tenant := &osacv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: instance.Annotations[osacTenantKey], Namespace: "default"}, tenant)).To(Succeed())
		tenant.Status.Phase = osacv1alpha1.TenantPhaseProgressing
		Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
		writes.when = func(ci *osacv1alpha1.ComputeInstance) bool { return ci.Status.Jobs[0].LastFailure == nil }
		_, err := run()
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		reload()
		Expect(instance.Status.Jobs[0].LastFailure).NotTo(BeNil())
		Expect(instance.Status.LastProvisioningFailure).NotTo(BeNil())
		recreate()
		reconcileOK()
		Expect(instance.Status.Jobs[0].LastFailure).To(BeNil())
		Expect(instance.Status.LastProvisioningFailure).To(BeNil())
		Expect(fake.tasks()).To(HaveLen(1))
	})

	It("reports retry progress without repeating an unchanged storage-gate event", func() {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		result := reconcileOK()
		clock.Step(result.RequeueAfter)
		tenant := &osacv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: instance.Annotations[osacTenantKey], Namespace: "default"}, tenant)).To(Succeed())
		tenant.SetStatusCondition(osacv1alpha1.TenantConditionStorageReady, metav1.ConditionFalse,
			osacv1alpha1.TenantReasonNotFound, "Storage is not ready")
		Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
		recorder := events.NewFakeRecorder(10)
		reconciler.Recorder = recorder
		reconcileOK()
		expectReason("RetryScheduled")
		Expect(instance.Status.LastProvisioningFailure).NotTo(BeNil())
		Eventually(recorder.Events, time.Second, 10*time.Millisecond).Should(Receive(ContainSubstring(eventReasonTenantNotReady)))
		reconcileOK()
		expectReason("RetryScheduled")
		Expect(recorder.Events).To(BeEmpty())
		Expect(fake.tasks()).To(HaveLen(1))
	})

	It("preserves the storage gate for a new configuration after terminal failure", func() {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":false}}`)
		reconcileOK()
		expectReason(osacv1alpha1.ReasonProvisioningFailed)
		restart := metav1.NewTime(clock.Now().Add(time.Second))
		instance.Spec.RestartRequestedAt = &restart
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		tenant := &osacv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: instance.Annotations[osacTenantKey], Namespace: "default"}, tenant)).To(Succeed())
		tenant.SetStatusCondition(osacv1alpha1.TenantConditionStorageReady, metav1.ConditionFalse,
			osacv1alpha1.TenantReasonNotFound, "Storage is not ready")
		Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
		reconcileOK()
		expectReason(osacv1alpha1.ReasonTenantNotReady)
		Expect(instance.Status.LastProvisioningFailure).To(BeNil())
		Expect(fake.tasks()).To(HaveLen(1))
	})

	It("clears previous-input diagnostics without resetting retry policy when configuration changes", func() {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		result := reconcileOK()
		Expect(instance.Status.LastProvisioningFailure).NotTo(BeNil())
		restart := metav1.NewTime(clock.Now().Add(time.Second))
		instance.Spec.RestartRequestedAt = &restart
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		updated := reconcileOK()
		expectReason("RetryScheduled")
		Expect(instance.Status.LastProvisioningFailure).To(BeNil())
		Expect(instance.Status.Jobs[0].LastFailure).To(BeNil())
		Expect(updated.RequeueAfter).To(Equal(result.RequeueAfter))
		clock.Step(updated.RequeueAfter)
		reconcileOK()
		expectReason(osacv1alpha1.ReasonWaitingForVM)
		Expect(instance.Status.LastProvisioningFailure).To(BeNil())
	})

	It("clears retry and failure while deletion waits for normal cleanup", func() {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		reconcileOK()
		Expect(instance.Status.LastProvisioningFailure).NotTo(BeNil())
		Expect(k8sClient.Delete(ctx, instance)).To(Succeed())
		reconcileOK()
		expectReason(osacv1alpha1.ReasonDeleting)
		Expect(instance.Status.LastProvisioningFailure).To(BeNil())
		Expect(instance.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseDeleting))
	})

	It("clears retry after success when the existing VM has no printable status yet", func() {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		result := reconcileOK()
		clock.Step(result.RequeueAfter)
		reconcileOK()
		expectReason("RetryRunning")
		vm := &kubevirtv1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{
			Name: instance.Name, Namespace: "default",
			Labels: map[string]string{osacComputeInstanceNameLabel: instance.Name},
		}}
		Expect(k8sClient.Create(ctx, vm)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, vm))).To(Succeed()) })
		Eventually(func() error {
			return testMcManager.GetLocalManager().GetClient().Get(ctx, client.ObjectKeyFromObject(vm), &kubevirtv1.VirtualMachine{})
		}, 5*time.Second, 10*time.Millisecond).Should(Succeed())
		fake.finish(computeInstanceProvisionTask, "successful", `{}`)
		reconcileOK()
		expectReason(osacv1alpha1.ReasonWaitingForVM)
		Expect(instance.Status.LastProvisioningFailure).To(BeNil())
		Expect(instance.Status.Jobs[0].LastFailure).To(BeNil())
	})

	It("does not publish a failure when the engine's checkpoint write conflicts", func() {
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		writes.when = func(ci *osacv1alpha1.ComputeInstance) bool {
			return ci.Status.Jobs[0].State == osacv1alpha1.NetworkJobStateRetrying
		}
		_, err := run()
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		reload()
		Expect(instance.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateRunning))
		Expect(instance.Status.Jobs[0].LastFailure).To(BeNil())
		Expect(instance.Status.LastProvisioningFailure).To(BeNil())
		expectReason(osacv1alpha1.ReasonWaitingForVM)
	})

	DescribeTable("keeps snapshot-free legacy progress conservative", func(state osacv1alpha1.NetworkJobState, attempt int32, want string) {
		reload()
		instance.Status.Jobs[0].State = state
		instance.Status.Jobs[0].Attempt = attempt
		instance.Status.Jobs[0].LastFailure = nil
		Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		recreate()
		result := reconcileOK()
		expectReason(want)
		Expect(instance.Status.LastProvisioningFailure).To(BeNil())
		if state == osacv1alpha1.NetworkJobStateRetrying {
			clock.Step(result.RequeueAfter)
			reconcileOK()
			expectReason(osacv1alpha1.ReasonWaitingForVM)
			Expect(instance.Status.LastProvisioningFailure).To(BeNil())
		}
		fake.finish(computeInstanceProvisionTask, "failed", `{"osac_error":{"message":"private-provider-text","retryable":true}}`)
		result = reconcileOK()
		expectReason("RetryScheduled")
		Expect(instance.Status.LastProvisioningFailure).NotTo(BeNil())
		observed := instance.Status.LastProvisioningFailure.ObservedAt.DeepCopy()
		clock.Step(result.RequeueAfter)
		reconcileOK()
		recreate()
		reconcileOK()
		expectReason("RetryRunning")
		Expect(instance.Status.LastProvisioningFailure.ObservedAt.Time.Equal(observed.Time)).To(BeTrue())
	},
		Entry("initial running", osacv1alpha1.NetworkJobStateRunning, int32(1), osacv1alpha1.ReasonWaitingForVM),
		Entry("configuration rerun", osacv1alpha1.NetworkJobStateRunning, int32(3), osacv1alpha1.ReasonWaitingForVM),
		Entry("cancelled relaunch", osacv1alpha1.NetworkJobStateCancelled, int32(2), osacv1alpha1.ReasonWaitingForVM),
		Entry("backoff", osacv1alpha1.NetworkJobStateRetrying, int32(2), "RetryScheduled"),
	)

	DescribeTable("reports final job failure safely without scheduling a retry", func(jobStatus, artifacts, reason string) {
		fake.finish(computeInstanceProvisionTask, jobStatus, artifacts)
		reconcileOK()
		expectReason(osacv1alpha1.ReasonProvisioningFailed)
		Expect(instance.Status.LastProvisioningFailure).NotTo(BeNil())
		Expect(instance.Status.LastProvisioningFailure.Reason).To(Equal(reason))
		Expect(instance.Status.Jobs[0].State).To(Equal(osacv1alpha1.NetworkJobStateFailed))
		Expect(instance.GetStatusCondition(osacv1alpha1.ComputeInstanceConditionProvisioned).Message).To(Equal("Provisioning failed; no retry is scheduled."))
	},
		Entry("nonretryable provider", "failed", `{"osac_error":{"message":"private-provider-text","retryable":false}}`, "ProviderReported"),
		Entry("missing error", "failed", `{}`, "InvalidResult"),
		Entry("malformed error", "failed", `{"osac_error":true}`, "InvalidResult"),
		Entry("unexpected output", "successful", `{"osac_result":{"unexpected":true}}`, "ResultRejected"),
	)
})
