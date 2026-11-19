/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// TestBillingStubSetupConflict separates a definite Kubernetes rejection of the setup update from an ambiguous
// failure. A conflict did not commit the fixture, so it can be retried from a fresh read or cleaned up through the
// normal reference-checked Secret deletion; a timeout may still commit and keeps the conservative retention covered
// by TestBillingStubFixtureCleanup.
func TestBillingStubSetupConflict(t *testing.T) {
	const secretName = "billing-fixture-setup-conflict-secret"
	for _, tc := range []struct {
		name         string
		conflicts    int
		acknowledged bool
		rejected     bool
		setupUpdates int
	}{
		{
			name: "retries a conflicting setup update from a fresh read", conflicts: 1,
			acknowledged: true, setupUpdates: 2,
		},
		{
			name: "cleans up after exhausted definite setup conflicts", conflicts: 100, rejected: true,
			setupUpdates: minSetupUpdateAttempts,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := t.Context()
			key := billingDeploymentKey()
			replicas := int32(1)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, UID: "deployment", Generation: 1},
				Spec: appsv1.DeploymentSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: billingStubContainer, Image: "original"}}},
				}},
				Status: appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 1, Replicas: 1, AvailableReplicas: 1},
			}
			patch := &billingStubPatch{
				owner:   "fixture-run",
				volumes: []corev1.Volume{{Name: "stub", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secretName}}}},
				mounts:  []corev1.VolumeMount{{Name: "stub", MountPath: "/stub", ReadOnly: true}},
				env:     []corev1.EnvVar{{Name: "OSAC_BILLING_PROVIDER", Value: "m360"}},
			}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: secretName, Namespace: key.Namespace, UID: "secret", Annotations: map[string]string{billingStubOwner: patch.owner},
			}}
			base := fake.NewClientBuilder().WithObjects(deployment, secret).Build()
			remaining, updates := tc.conflicts, 0
			kube := interceptor.NewClient(base, interceptor.Funcs{
				Update: func(ctx context.Context, c crclient.WithWatch, obj crclient.Object, opts ...crclient.UpdateOption) error {
					if _, deployment := obj.(*appsv1.Deployment); !deployment {
						return c.Update(ctx, obj, opts...)
					}
					updates++
					if remaining == 0 {
						return c.Update(ctx, obj, opts...)
					}
					remaining--
					// A concurrent writer changes the Deployment before the API server rejects our stale update.
					latest := &appsv1.Deployment{}
					g.Expect(c.Get(ctx, key, latest)).To(Succeed())
					latest.Spec.Template.Spec.Containers[0].Image = "updated-concurrently"
					g.Expect(c.Update(ctx, latest)).To(Succeed())
					return apierrors.NewConflict(appsv1.Resource("deployments"), key.Name, errors.New("conflict"))
				},
			})

			current := &appsv1.Deployment{}
			g.Expect(kube.Get(ctx, key, current)).To(Succeed())
			g.Expect(patch.apply(current)).To(Succeed())
			err := patch.updateDeployment(ctx, kube, current)
			if tc.acknowledged {
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "setup update error: %v", err)
			}
			g.Expect(patch.deploymentUpdateAcknowledged).To(Equal(tc.acknowledged))
			g.Expect(patch.deploymentSetupRejected).To(Equal(tc.rejected))
			g.Expect(updates).To(Equal(tc.setupUpdates))

			g.Expect(patch.cleanup(ctx, kube, []*corev1.Secret{secret.DeepCopy()})).To(Succeed())
			latest := &appsv1.Deployment{}
			g.Expect(base.Get(ctx, key, latest)).To(Succeed())
			g.Expect(latest.Spec.Template.Annotations).NotTo(HaveKey(billingStubOwner))
			g.Expect(latest.Spec.Template.Spec.Volumes).To(BeEmpty())
			g.Expect(latest.Spec.Template.Spec.Containers[0].Image).To(Equal("updated-concurrently"),
				"a concurrent writer's change must survive setup retry and cleanup")
			g.Expect(apierrors.IsNotFound(base.Get(ctx, crclient.ObjectKeyFromObject(secret), &corev1.Secret{}))).To(BeTrue())
		})
	}
}

// minSetupUpdateAttempts is the number of times RetryOnConflict calls the setup update with the default retry.
const minSetupUpdateAttempts = 5

// This is a plain Go test so selecting it does not start the deployed Ginkgo suite.
func TestBillingStubFixtureCleanup(t *testing.T) {
	const secretName = "billing-fixture-test-secret"
	for _, tc := range []struct {
		name                string
		partialSetup        bool
		setupUpdateTimeout  bool
		commitBeforeCleanup bool
		updateConflict      bool
		readFailure         bool
		alter               func(*appsv1.Deployment)
		fault               string
	}{
		{name: "partial setup with no Deployment update", partialSetup: true},
		{
			name:               "retains the Secret when the setup update commits after cleanup",
			setupUpdateTimeout: true, fault: "setup Deployment update was not acknowledged",
		},
		{
			name:               "deletes the Secret after restoring an unacknowledged setup update that already committed",
			setupUpdateTimeout: true, commitBeforeCleanup: true,
		},
		{name: "restores after a conflict and preserves the concurrent update", updateConflict: true},
		{
			name: "retains the Secret if a writer changed a fixture entry",
			alter: func(d *appsv1.Deployment) {
				d.Spec.Template.Spec.Containers[0].Env[0].Value = "changed"
			},
			fault: "changed concurrently",
		},
		{
			name: "retains the Secret if an unrelated init container references it after restore",
			alter: func(d *appsv1.Deployment) {
				d.Spec.Template.Spec.InitContainers = []corev1.Container{{Name: "init", EnvFrom: []corev1.EnvFromSource{{
					SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}},
				}}}}
			},
			fault: "still references the Secret",
		},
		{
			name: "retains the Secret when the Deployment cannot be read", partialSetup: true,
			readFailure: true, fault: "Deployment read failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := t.Context()
			key := billingDeploymentKey()
			replicas := int32(1)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, UID: "deployment", Generation: 1},
				Spec: appsv1.DeploymentSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: billingStubContainer, Image: "original"}}},
				}},
				Status: appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 1, Replicas: 1, AvailableReplicas: 1},
			}
			patch := billingStubPatch{
				owner:   "fixture-run",
				volumes: []corev1.Volume{{Name: "stub", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secretName}}}},
				mounts:  []corev1.VolumeMount{{Name: "stub", MountPath: "/stub", ReadOnly: true}},
				env:     []corev1.EnvVar{{Name: "OSAC_BILLING_PROVIDER", Value: "m360"}},
			}
			if !tc.partialSetup && !tc.setupUpdateTimeout {
				g.Expect(patch.apply(deployment)).To(Succeed())
				patch.deploymentUID = deployment.UID
				patch.deploymentUpdateAcknowledged = true
			}
			if tc.alter != nil {
				tc.alter(deployment)
			}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: secretName, Namespace: key.Namespace, UID: "secret", Annotations: map[string]string{billingStubOwner: patch.owner},
			}}
			base := fake.NewClientBuilder().WithObjects(deployment, secret).Build()
			var updates, deletes int
			var pendingUpdate *appsv1.Deployment
			kube := interceptor.NewClient(base, interceptor.Funcs{
				Get: func(ctx context.Context, c crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
					deadline, ok := ctx.Deadline()
					g.Expect(ok).To(BeTrue(), "cleanup GET has no deadline")
					g.Expect(time.Until(deadline)).To(BeNumerically("<=", billingStubRequestTimeout))
					if _, deployment := obj.(*appsv1.Deployment); deployment && tc.readFailure {
						return errors.New(tc.fault)
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Update: func(ctx context.Context, c crclient.WithWatch, obj crclient.Object, opts ...crclient.UpdateOption) error {
					if tc.setupUpdateTimeout && pendingUpdate == nil {
						pendingUpdate = obj.(*appsv1.Deployment).DeepCopy()
						return context.DeadlineExceeded
					}
					updates++
					if tc.updateConflict && updates == 1 {
						latest := &appsv1.Deployment{}
						g.Expect(c.Get(ctx, key, latest)).To(Succeed())
						latest.Spec.Template.Spec.Containers[0].Image = "updated-concurrently"
						latest.Spec.Template.Spec.Containers[0].Env = append(latest.Spec.Template.Spec.Containers[0].Env,
							corev1.EnvVar{Name: "UNRELATED", Value: "preserve"})
						g.Expect(c.Update(ctx, latest)).To(Succeed())
					}
					return c.Update(ctx, obj, opts...)
				},
				Delete: func(ctx context.Context, c crclient.WithWatch, obj crclient.Object, opts ...crclient.DeleteOption) error {
					deletes++
					preconditions := (&crclient.DeleteOptions{}).ApplyOptions(opts).Preconditions
					g.Expect(preconditions).NotTo(BeNil())
					g.Expect(preconditions.UID).To(HaveValue(Equal(secret.UID)))
					g.Expect(preconditions.ResourceVersion).To(HaveValue(Equal(obj.GetResourceVersion())))
					return c.Delete(ctx, obj, opts...)
				},
			})

			if tc.setupUpdateTimeout {
				current := &appsv1.Deployment{}
				g.Expect(base.Get(ctx, key, current)).To(Succeed())
				g.Expect(patch.apply(current)).To(Succeed())
				g.Expect(patch.updateDeployment(ctx, kube, current)).To(MatchError(context.DeadlineExceeded))
				if tc.commitBeforeCleanup {
					g.Expect(base.Update(ctx, pendingUpdate)).To(Succeed())
				}
			}
			attempted := secret.DeepCopy()
			if tc.partialSetup {
				// Simulate a create that persisted despite not returning its response.
				attempted.UID = ""
			}
			err := patch.cleanup(ctx, kube, []*corev1.Secret{attempted})
			if tc.setupUpdateTimeout && !tc.commitBeforeCleanup {
				// Commit after cleanup has read the original template and made its deletion decision.
				g.Expect(base.Update(ctx, pendingUpdate)).To(Succeed())
				latest := &appsv1.Deployment{}
				g.Expect(base.Get(ctx, key, latest)).To(Succeed())
				g.Expect(latest.Spec.Template.Spec.Volumes).To(Equal(patch.volumes))
			}
			secretErr := base.Get(ctx, crclient.ObjectKeyFromObject(secret), &corev1.Secret{})
			if tc.fault != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.fault)))
				g.Expect(deletes).To(BeZero())
				g.Expect(secretErr).To(Succeed())
				return
			}
			g.Expect(err).To(Succeed())
			g.Expect(apierrors.IsNotFound(secretErr)).To(BeTrue(), "expected Secret to be deleted, got %v", secretErr)
			if tc.partialSetup {
				g.Expect(updates).To(BeZero())
				return
			}
			latest := &appsv1.Deployment{}
			g.Expect(base.Get(ctx, key, latest)).To(Succeed())
			g.Expect(latest.Spec.Template.Annotations).NotTo(HaveKey(billingStubOwner))
			g.Expect(latest.Spec.Template.Spec.Volumes).To(BeEmpty())
			container := latest.Spec.Template.Spec.Containers[0]
			g.Expect(container.VolumeMounts).To(BeEmpty())
			if tc.updateConflict {
				g.Expect(updates).To(Equal(2))
				g.Expect(container.Image).To(Equal("updated-concurrently"))
				g.Expect(container.Env).To(ConsistOf(corev1.EnvVar{Name: "UNRELATED", Value: "preserve"}))
			} else {
				g.Expect(updates).To(Equal(1))
				g.Expect(container.Image).To(Equal("original"))
				g.Expect(container.Env).To(BeEmpty())
			}
		})
	}
}
