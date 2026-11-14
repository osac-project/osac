// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	netutil "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const simulatorDeployment = "osac-provider-simulator"

// Simulator observes the installation-owned native controller manager.
type Simulator struct {
	Kube      kubernetes.Interface
	Namespace string
}

// CheckConfiguration requires one simulator replica targeting the suite namespace,
// then waits for its Deployment and Pod to be ready.
func (environment *Simulator) CheckConfiguration(ctx context.Context) error {
	return wait.PollUntilContextTimeout(
		ctx,
		500*time.Millisecond,
		3*time.Minute,
		true,
		func(ctx context.Context) (bool, error) {
			deployment, err := environment.Kube.AppsV1().
				Deployments(environment.Namespace).
				Get(ctx, simulatorDeployment, metav1.GetOptions{})
			if err != nil {
				return false, simulatorReadError(ctx, err)
			}

			if err := validateSimulatorConfiguration(deployment, environment.Namespace); err != nil {
				return false, err
			}

			slog.DebugContext(ctx, "observed simulator readiness",
				"generation", deployment.Generation,
				"observedGeneration", deployment.Status.ObservedGeneration,
				"replicas", deployment.Status.Replicas,
				"updated", deployment.Status.UpdatedReplicas,
				"available", deployment.Status.AvailableReplicas,
			)

			if deployment.Status.ObservedGeneration < deployment.Generation || deployment.Status.Replicas != 1 ||
				deployment.Status.UpdatedReplicas != 1 ||
				deployment.Status.AvailableReplicas != 1 {
				return false, nil
			}

			pods, err := environment.Kube.CoreV1().
				Pods(environment.Namespace).
				List(ctx, metav1.ListOptions{LabelSelector: "app=" + simulatorDeployment})
			if err != nil {
				return false, simulatorReadError(ctx, err)
			}

			if len(pods.Items) != 1 {
				return false, nil
			}

			pod := &pods.Items[0]
			slog.DebugContext(ctx, "observed simulator Pod",
				"pod", pod.Name,
				"uid", pod.UID,
				"deleting", pod.DeletionTimestamp != nil,
			)

			if pod.DeletionTimestamp != nil {
				return false, nil
			}

			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
					slog.InfoContext(ctx, "simulator ready", "pod", pod.Name, "uid", pod.UID)
					return true, nil
				}
			}

			return false, nil
		},
	)
}

// validateSimulatorConfiguration requires one replica, Recreate strategy and
// a simulator container targeting only the supplied Agent namespace.
func validateSimulatorConfiguration(deployment *appsv1.Deployment, namespace string) error {
	if deployment.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || deployment.Spec.Replicas == nil ||
		*deployment.Spec.Replicas != 1 {
		return fmt.Errorf("provider simulator requires one replica and Recreate strategy")
	}

	containers := deployment.Spec.Template.Spec.Containers
	if len(containers) != 1 || containers[0].Name != "simulator" {
		return fmt.Errorf("unexpected simulator container configuration")
	}

	for _, value := range containers[0].Env {
		if value.Name == "SIMULATED_NAMESPACES" && value.Value == namespace {
			return nil
		}
	}

	return fmt.Errorf("suite requires its dedicated single-hub simulator configuration")
}

// simulatorReadError keeps transient reads within the readiness deadline and surfaces terminal errors.
func simulatorReadError(ctx context.Context, err error) error {
	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) ||
		apierrors.IsServiceUnavailable(err) || netutil.IsTimeout(err) || netutil.IsProbableEOF(err) ||
		netutil.IsConnectionReset(err) || netutil.IsConnectionRefused(err) {
		slog.DebugContext(ctx, "retrying simulator readiness read", "error", err)
		return nil
	}

	return err
}
