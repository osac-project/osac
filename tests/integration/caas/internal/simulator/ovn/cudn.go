// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package ovn simulates only the CUDN and VTEP status boundary used by the
// production Kubernetes Manager tasks. It does not model OVN traffic.
package ovn

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const (
	group        = "k8s.ovn.org"
	version      = "v1"
	cudnKind     = "ClusterUserDefinedNetwork"
	vtepKind     = "VTEP"
	managerLabel = "osac.openshift.io/k8s-manager"
	fixtureName  = "caas-it-networking-fixture"
	heldCUDNsKey = "notReadyCudns"
)

var (
	cudnGVK = schema.GroupVersionKind{Group: group, Version: version, Kind: cudnKind}
	vtepGVK = schema.GroupVersionKind{Group: group, Version: version, Kind: vtepKind}
)

// ResourceStatus contains the status conditions exposed by the OVN fixture APIs.
type ResourceStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// CUDN is the small typed API shape needed to watch OVN CUDNs and report status.
type CUDN struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Status            ResourceStatus `json:"status,omitzero"`
}

// DeepCopyObject returns a detached CUDN copy for Kubernetes clients.
func (object *CUDN) DeepCopyObject() runtime.Object {
	if object == nil {
		return nil
	}

	copy := *object
	object.DeepCopyInto(&copy.ObjectMeta)
	copy.Status.Conditions = append([]metav1.Condition(nil), object.Status.Conditions...)

	return &copy
}

// CUDNList is the list shape registered for the CUDN API.
type CUDNList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CUDN `json:"items"`
}

// DeepCopyObject returns a detached CUDN list for Kubernetes clients.
func (list *CUDNList) DeepCopyObject() runtime.Object {
	if list == nil {
		return nil
	}

	copy := *list

	copy.ListMeta = *list.DeepCopy()

	copy.Items = make([]CUDN, len(list.Items))
	for index := range list.Items {
		copy.Items[index] = *list.Items[index].DeepCopyObject().(*CUDN)
	}

	return &copy
}

// VTEP is the small typed API shape needed to watch and accept the configured VTEP.
type VTEP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Status            ResourceStatus `json:"status,omitzero"`
}

// DeepCopyObject returns a detached VTEP copy for Kubernetes clients.
func (object *VTEP) DeepCopyObject() runtime.Object {
	if object == nil {
		return nil
	}

	copy := *object
	object.DeepCopyInto(&copy.ObjectMeta)
	copy.Status.Conditions = append([]metav1.Condition(nil), object.Status.Conditions...)

	return &copy
}

// VTEPList is the list shape registered for the VTEP API.
type VTEPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []VTEP `json:"items"`
}

// DeepCopyObject returns a detached VTEP list for Kubernetes clients.
func (list *VTEPList) DeepCopyObject() runtime.Object {
	if list == nil {
		return nil
	}

	copy := *list

	copy.ListMeta = *list.DeepCopy()

	copy.Items = make([]VTEP, len(list.Items))
	for index := range list.Items {
		copy.Items[index] = *list.Items[index].DeepCopyObject().(*VTEP)
	}

	return &copy
}

// AddToScheme registers the minimal typed OVN API shapes used by this fixture.
func AddToScheme(scheme *runtime.Scheme) error {
	scheme.AddKnownTypeWithName(cudnGVK, &CUDN{})
	scheme.AddKnownTypeWithName(cudnGVK.GroupVersion().WithKind(cudnKind+"List"), &CUDNList{})
	scheme.AddKnownTypeWithName(vtepGVK, &VTEP{})
	scheme.AddKnownTypeWithName(vtepGVK.GroupVersion().WithKind(vtepKind+"List"), &VTEPList{})
	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Group: group, Version: version})

	return nil
}

// CUDNReconciler reports the accepted and ready status normally written by OVN-Kubernetes.
type CUDNReconciler struct {
	Client            client.Client
	APIReader         client.Reader
	SettingsNamespace string
}

// Reconcile updates only CUDNs created for OSAC's production Kubernetes Manager.
func (r *CUDNReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	object := CUDNObject()
	if err := r.APIReader.Get(ctx, req.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if object.GetDeletionTimestamp() != nil || object.GetLabels()[managerLabel] != "cudn_evpn" {
		return ctrl.Result{}, nil
	}

	held, err := r.isHeld(ctx, object.GetName())
	if err != nil {
		return ctrl.Result{}, err
	}

	status, reason, message := metav1.ConditionTrue, "FixtureReady", "Accepted by the Kind OVN fixture."
	if held {
		status, reason, message = metav1.ConditionFalse, "FixtureNotReady", "Held by the Kind OVN fixture."
	}

	wanted := make([]metav1.Condition, 0, 3)
	for _, kind := range []string{"TransportAccepted", "NetworkCreated", "NetworkAllocationSucceeded"} {
		wanted = append(wanted, condition(kind, status, reason, message, object.GetGeneration(), object.Status.Conditions))
	}

	if equality.Semantic.DeepEqual(object.Status.Conditions, wanted) {
		return ctrl.Result{}, nil
	}

	next := object.DeepCopyObject().(*CUDN)
	next.Status.Conditions = wanted
	if err := r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(object, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// isHeld reports whether the fixture settings hold this CUDN in a not-ready state.
func (r *CUDNReconciler) isHeld(ctx context.Context, name string) (bool, error) {
	settings := &corev1.ConfigMap{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: r.SettingsNamespace, Name: fixtureName}, settings)
	if apierrors.IsNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("get OVN fixture settings: %w", err)
	}

	for heldName := range strings.SplitSeq(settings.Data[heldCUDNsKey], ",") {
		if strings.TrimSpace(heldName) == name {
			return true, nil
		}
	}

	return false, nil
}

// cudnsForFixtureConfig requeues managed CUDNs when their fixture settings change.
func (r *CUDNReconciler) cudnsForFixtureConfig(ctx context.Context, object client.Object) []reconcile.Request {
	if object.GetNamespace() != r.SettingsNamespace || object.GetName() != fixtureName {
		return nil
	}

	cudns := &CUDNList{}
	if err := r.APIReader.List(ctx, cudns); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "list CUDNs after OVN fixture settings changed")
		return nil
	}

	requests := make([]reconcile.Request, 0, len(cudns.Items))
	for index := range cudns.Items {
		cudn := &cudns.Items[index]
		if cudn.GetLabels()[managerLabel] == "cudn_evpn" {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(cudn)})
		}
	}

	return requests
}

// VTEPReconciler marks the configured tenant VTEP accepted by the simulated hub.
type VTEPReconciler struct {
	Client    client.Client
	APIReader client.Reader
}

// Reconcile records the external OVN controller's accepted VTEP status.
func (r *VTEPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	object := VTEPObject()
	if err := r.APIReader.Get(ctx, req.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if object.GetName() != "tenant-vtep" || object.GetDeletionTimestamp() != nil {
		return ctrl.Result{}, nil
	}

	wanted := []metav1.Condition{condition("Accepted", metav1.ConditionTrue,
		"FixtureAccepted", "Accepted by the Kind OVN fixture.", object.GetGeneration(), object.Status.Conditions)}
	if equality.Semantic.DeepEqual(object.Status.Conditions, wanted) {
		return ctrl.Result{}, nil
	}

	next := object.DeepCopyObject().(*VTEP)
	next.Status.Conditions = wanted

	return ctrl.Result{}, r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(object, client.MergeFromWithOptimisticLock{}))
}

// SetupWithManager installs the two external API status simulators.
func SetupWithManager(mgr ctrl.Manager, apiReader client.Reader, settingsNamespace string) error {
	cudn := &CUDNReconciler{
		Client: mgr.GetClient(), APIReader: apiReader, SettingsNamespace: settingsNamespace,
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		Named("kind-ovn-cudn-status").
		For(CUDNObject()).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(cudn.cudnsForFixtureConfig)).
		WithOptions(kube.ControllerOptions()).
		Complete(cudn); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("kind-ovn-vtep-status").
		For(VTEPObject()).
		WithOptions(kube.ControllerOptions()).
		Complete(&VTEPReconciler{Client: mgr.GetClient(), APIReader: apiReader})
}

// CUDNObject returns a typed CUDN object for Kubernetes clients.
func CUDNObject() *CUDN {
	return &CUDN{TypeMeta: metav1.TypeMeta{APIVersion: cudnGVK.GroupVersion().String(), Kind: cudnKind}}
}

// VTEPObject returns a typed VTEP object for Kubernetes clients.
func VTEPObject() *VTEP {
	return &VTEP{TypeMeta: metav1.TypeMeta{APIVersion: vtepGVK.GroupVersion().String(), Kind: vtepKind}}
}

// condition keeps a stable transition timestamp while its status remains unchanged.
func condition(
	kind string,
	status metav1.ConditionStatus,
	reason, message string,
	generation int64,
	existing []metav1.Condition,
) metav1.Condition {
	transition := metav1.Now()

	for _, previous := range existing {
		if previous.Type == kind && previous.Status == status {
			transition = previous.LastTransitionTime
			break
		}
	}

	return metav1.Condition{
		Type: kind, Status: status, Reason: reason, Message: message,
		ObservedGeneration: generation, LastTransitionTime: transition,
	}
}
