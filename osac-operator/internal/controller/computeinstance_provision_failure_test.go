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
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
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
