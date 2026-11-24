// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// testSimulatorDeployment builds the single-writer Deployment checked during suite setup.
func testSimulatorDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: simulatorDeployment, Namespace: "osac", UID: "deployment", Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: new(int32(1)),
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "simulator",
							Env: []corev1.EnvVar{
								{Name: "SIMULATED_NAMESPACES", Value: "osac"},
								{
									Name:      "POD_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}},
								},
							},
						},
					},
				},
			},
		},
	}
}

var _ = Describe("Simulator deployment configuration", func() {
	var deployment *appsv1.Deployment

	BeforeEach(func() {
		deployment = testSimulatorDeployment()
	})

	It("accepts a single writer targeting the selected namespace", func() {
		Expect(validateSimulatorConfiguration(deployment, "osac")).To(Succeed())
	})

	DescribeTable(
		"rejects configurations that break the installation boundary",
		func(change func(*appsv1.Deployment)) {
			change(deployment)
			Expect(validateSimulatorConfiguration(deployment, "osac")).NotTo(Succeed())
		},
		Entry("multiple replicas", func(d *appsv1.Deployment) { d.Spec.Replicas = new(int32(2)) }),
		Entry(
			"overlapping rolling-update pods",
			func(d *appsv1.Deployment) { d.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType },
		),
		Entry(
			"another Agent namespace",
			func(d *appsv1.Deployment) { d.Spec.Template.Spec.Containers[0].Env[0].Value = "other" },
		),
	)
})

var _ = DescribeTable("Simulator readiness read failures", func(ctx SpecContext, failure error, retry bool) {
	err := simulatorReadError(ctx, failure)
	if retry {
		Expect(err).NotTo(HaveOccurred())
	} else {
		Expect(err).To(MatchError(failure))
	}
},
	Entry("request timeout", apierrors.NewTimeoutError("temporary", 1), true),
	Entry("server timeout", apierrors.NewServerTimeout(schema.GroupResource{Resource: "deployments"}, "get", 1), true),
	Entry("throttled", apierrors.NewTooManyRequests("temporary", 1), true),
	Entry("unavailable", apierrors.NewServiceUnavailable("temporary"), true),
	Entry("connection reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, true),
	Entry("connection refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true),
	Entry("unexpected EOF", io.ErrUnexpectedEOF, true),
	Entry("unknown error", errors.New("invalid request"), false),
)

var _ = Describe("Simulator readiness wait", func() {
	var (
		c           *kubefake.Clientset
		environment *Simulator
	)

	BeforeEach(func() {
		deployment := testSimulatorDeployment()
		deployment.Status = appsv1.DeploymentStatus{
			ObservedGeneration: deployment.Generation, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1,
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "osac", Name: "simulator", Labels: map[string]string{"app": simulatorDeployment},
			},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			}},
		}
		c = kubefake.NewClientset(deployment, pod)
		environment = &Simulator{Kube: c, Namespace: "osac"}
	})

	DescribeTable("recovers from a transient read failure", func(ctx SpecContext, verb, resource string) {
		attempts := 0

		c.PrependReactor(verb, resource, func(k8stesting.Action) (bool, runtime.Object, error) {
			attempts++
			if attempts == 1 {
				return true, nil, apierrors.NewServiceUnavailable("temporary")
			}

			return false, nil, nil
		})

		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		Expect(environment.CheckConfiguration(bounded)).To(Succeed())
		Expect(attempts).To(Equal(2))
	},
		Entry("the initial Deployment read", "get", "deployments"),
		Entry("the Pod list", "list", "pods"),
	)

	DescribeTable("surfaces terminal errors immediately", func(ctx SpecContext, verb, resource string) {
		failure := apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "simulator", errors.New("denied"))
		attempts := 0

		c.PrependReactor(verb, resource, func(k8stesting.Action) (bool, runtime.Object, error) {
			attempts++
			return true, nil, failure
		})

		Expect(environment.CheckConfiguration(ctx)).To(MatchError(failure))
		Expect(attempts).To(Equal(1))
	},
		Entry("Deployment authorization", "get", "deployments"),
		Entry("Pod authorization", "list", "pods"),
	)

	It("honors cancellation while transient errors persist", func(ctx SpecContext) {
		c.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewServiceUnavailable("temporary")
		})

		bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()

		Expect(environment.CheckConfiguration(bounded)).To(MatchError(context.DeadlineExceeded))
	})
})
