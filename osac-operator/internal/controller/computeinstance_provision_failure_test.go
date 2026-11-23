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
	It("persists atomically across resource-version conflicts and validates the CRD shape", func() {
		name := fmt.Sprintf("provision-failure-%d", time.Now().UnixNano())
		instance := &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID("uid-" + name)},
			Spec:       newTestComputeInstanceSpec("test_template"),
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func() {
			current := &osacv1alpha1.ComputeInstance{}
			if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(instance), current); err == nil {
				Expect(k8sClient.Delete(context.Background(), current)).To(Succeed())
			}
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

		stale := &osacv1alpha1.ComputeInstance{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stale)).To(Succeed())
		newer := stale.DeepCopy()
		newer.Annotations = map[string]string{"test": "new-resource-version"}
		Expect(k8sClient.Update(ctx, newer)).To(Succeed())
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
		aapClient.job.Status = "successful"
		aapClient.job.Artifacts = []byte(`{}`)
		out, err = engine.Run(ctx, stored, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Succeeded()).To(BeTrue())
		Expect(out.LastFailure).To(BeNil())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stored)).To(Succeed())
		Expect(stored.Status.Jobs[0].LastFailure).To(BeNil())
		engine = networktask.New(k8sClient, k8sClient, aapClient, networktask.Config{Organization: "osac"}).WithClock(clock)
		out, err = engine.Run(ctx, stored, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Succeeded()).To(BeTrue())
		Expect(out.LastFailure).To(BeNil())

		partial := stored.DeepCopy()
		partial.Status.Jobs[0].LastFailure = &osacv1alpha1.NetworkJobFailure{Reason: "ProviderReported"}
		Expect(apierrors.IsInvalid(k8sClient.Status().Update(ctx, partial))).To(BeTrue())
		zeroAttempt := stored.DeepCopy()
		zeroAttempt.Status.Jobs[0].LastFailure = storedFailure.DeepCopy()
		zeroAttempt.Status.Jobs[0].LastFailure.Attempt = 0
		Expect(apierrors.IsInvalid(k8sClient.Status().Update(ctx, zeroAttempt))).To(BeTrue())
	})
})
