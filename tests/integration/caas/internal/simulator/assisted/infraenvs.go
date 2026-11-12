// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package assisted

import (
	"context"
	"fmt"
	"time"

	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const infraEnvFinalizer = "infraenv.agent-install.openshift.io/ai-deprovision"

// InfraEnvReconciler publishes the installed HTTP fixture as Assisted's discovery input.
// The HTTP server owns the bytes; OSAC fetches and validates them.
type InfraEnvReconciler struct {
	Client       client.Client
	APIReader    client.Reader
	Scope        kube.Scope
	DiscoveryURL func(context.Context, *assistedv1.InfraEnv) (string, error)
}

// Reconcile validates discovery inputs and publishes the artifact and image condition.
// The finalizer represents backend deregistration, which has no external work here.
func (r *InfraEnvReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !r.Scope.Contains(req.Namespace) {
		return ctrl.Result{}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	infra := &assistedv1.InfraEnv{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, infra); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if infra.DeletionTimestamp != nil {
		return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, infra, infraEnvFinalizer)
	}

	if err := kube.AddFinalizer(ctx, r.Client, infra, infraEnvFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	endpoint, err := r.DiscoveryURL(ctx, infra)
	if err != nil {
		return ctrl.Result{}, err
	}

	if endpoint == "" {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	next := infra.DeepCopy()

	var inputError error

	if infra.Spec.PullSecretRef == nil {
		inputError = fmt.Errorf("discovery pull secret reference is required")
	} else {
		inputError = validPullSecret(ctx, r.APIReader, infra.Namespace, infra.Spec.PullSecretRef.Name)
	}

	condition := conditionsv1.Condition{
		Type:    assistedv1.ImageCreatedCondition,
		Status:  corev1.ConditionTrue,
		Reason:  assistedv1.ImageCreatedReason,
		Message: "Image has been created",
	}
	if inputError != nil {
		condition.Status = corev1.ConditionFalse
		condition.Reason = assistedv1.ImageCreationErrorReason
		condition.Message = inputError.Error()
		next.Status.BootArtifacts.DiscoveryIgnitionURL = ""
	} else {
		next.Status.BootArtifacts.DiscoveryIgnitionURL = endpoint
	}

	if ref := infra.Spec.ClusterRef; ref != nil {
		if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name},
			&hivev1.ClusterDeployment{}); err != nil {
			inputError = err
			condition.Status = corev1.ConditionUnknown
			condition.Reason = assistedv1.MissingClusterDeploymentReason
			condition.Message = assistedv1.MissingClusterDeploymentReference + ": " + err.Error()
			next.Status.BootArtifacts.DiscoveryIgnitionURL = infra.Status.BootArtifacts.DiscoveryIgnitionURL
		}
	}

	conditionsv1.SetStatusConditionNoHeartbeat(&next.Status.Conditions, condition)

	if !equality.Semantic.DeepEqual(infra.Status, next.Status) {
		if err := r.Client.Status().Patch(ctx, next,
			client.MergeFromWithOptions(infra, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
	}

	if condition.Reason == assistedv1.MissingClusterDeploymentReason {
		return ctrl.Result{RequeueAfter: time.Second}, client.IgnoreNotFound(inputError)
	}

	return ctrl.Result{}, inputError
}
