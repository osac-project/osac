/*
Copyright 2026.

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

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var (
	osacNetworkClassIDLabel           = fmt.Sprintf("%s/networkclass-uuid", osacPrefix)
	osacNetworkClassFeedbackFinalizer = fmt.Sprintf("%s/networkclass-feedback", osacPrefix)
)

const networkClassProvisioningDisabledMessage = "Provider networking is disabled; manager resolution was skipped."

// NetworkClassReconciler resolves the managers a NetworkClass selects when
// provisioning is enabled (network manager contract §4.2). With provisioning
// disabled it marks the class Ready without reading manager registrations. It
// runs no AAP job.
type NetworkClassReconciler struct {
	Client              clnt.Client
	APIReader           clnt.Reader
	NetworkingNamespace string
	ProvisioningEnabled bool
	// RegistrationNamespace is the namespace of the manager registrations (the
	// operator's Helm release namespace).
	RegistrationNamespace string
}

// Reconcile resolves a NetworkClass that is not Ready.
func (r *NetworkClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	nc := &v1alpha1.NetworkClass{}
	if err := r.Client.Get(ctx, req.NamespacedName, nc); err != nil {
		return ctrl.Result{}, clnt.IgnoreNotFound(err)
	}
	if nc.Annotations[osacManagementStateAnnotation] == ManagementStateUnmanaged || nc.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}
	if meta.IsStatusConditionTrue(nc.Status.Conditions, v1alpha1.NetworkConditionReady) {
		return ctrl.Result{}, nil
	}
	before := nc.Status.DeepCopy()
	if !r.ProvisioningEnabled {
		nc.Status.NetworkManagers = nil
		nc.Status.NetworkType = ""
		nc.Status.SecurityGroupICMP = nil
		meta.SetStatusCondition(&nc.Status.Conditions, metav1.Condition{
			Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
			Reason:  v1alpha1.ReasonProvisioningDisabled,
			Message: networkClassProvisioningDisabledMessage, ObservedGeneration: nc.Generation,
		})
	} else {
		discovery, err := networkmanager.NewDiscovery(r.APIReader, r.RegistrationNamespace)
		if err != nil {
			return ctrl.Result{}, err
		}
		eastWest := nc.Spec.EastWestCapabilities != nil && nc.Spec.EastWestCapabilities.SupportsEastWestEthernet
		resolution, err := discovery.Resolve(ctx, nc.Spec.FabricManager, nc.Spec.K8sManager, eastWest)
		if err == nil {
			err = checkDefaultRules(nc, resolution)
		}
		if err != nil {
			log.Info("NetworkClass resolution failed", "reason", err.Error())
			nc.Status.NetworkManagers = nil
			nc.Status.NetworkType = ""
			nc.Status.SecurityGroupICMP = nil
			meta.SetStatusCondition(&nc.Status.Conditions, metav1.Condition{
				Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionFalse,
				Reason: v1alpha1.ReasonFailed, Message: err.Error(), ObservedGeneration: nc.Generation,
			})
		} else {
			managers := &v1alpha1.NetworkManagers{Fabric: &v1alpha1.NetworkManagerRef{
				Name: resolution.Fabric.Name, AnsibleRole: resolution.Fabric.AnsibleRole,
			}}
			if resolution.Kubernetes != nil {
				managers.Kubernetes = &v1alpha1.NetworkManagerRef{
					Name: resolution.Kubernetes.Name, AnsibleRole: resolution.Kubernetes.AnsibleRole,
				}
			}
			nc.Status.NetworkManagers = managers
			nc.Status.NetworkType = v1alpha1.NetworkType(resolution.NetworkType)
			nc.Status.SecurityGroupICMP = ptr.To(resolution.SecurityGroupICMP())
			meta.SetStatusCondition(&nc.Status.Conditions, metav1.Condition{
				Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
				Reason: v1alpha1.ReasonSucceeded, ObservedGeneration: nc.Generation,
			})
			log.Info("NetworkClass resolved", "fabric", managers.Fabric.Name, "networkType", nc.Status.NetworkType)
		}
	}
	if equality.Semantic.DeepEqual(before, &nc.Status) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, r.Client.Status().Update(ctx, nc)
}

// checkDefaultRules fails a NetworkClass whose default SecurityGroup rules the managers cannot apply.
func checkDefaultRules(nc *v1alpha1.NetworkClass, resolution *networkmanager.Resolution) error {
	if resolution.SecurityGroupICMP() {
		return nil
	}
	for _, rules := range [][]v1alpha1.SecurityRule{nc.Spec.Defaults.IngressRules, nc.Spec.Defaults.EgressRules} {
		for _, rule := range rules {
			if rule.Protocol == v1alpha1.SecurityGroupProtocolICMP {
				return errors.New("default SecurityGroup rules use protocol icmp but not every " +
					"network manager declares security_group_icmp; use protocol all")
			}
		}
	}
	return nil
}

// registrationChanged reports whether a ConfigMap event adds or removes a
// manager registration: a create or delete of a labelled ConfigMap, or an
// update that adds or removes a manager-type label.
func registrationChanged() predicate.TypedFuncs[*corev1.ConfigMap] {
	labelled := func(obj *corev1.ConfigMap) (bool, bool) {
		labels := obj.GetLabels()
		return labels[networkmanager.LabelFabricManager] == labelValueTrue, labels[networkmanager.LabelK8sManager] == labelValueTrue
	}
	isRegistration := func(obj *corev1.ConfigMap) bool {
		fabric, k8s := labelled(obj)
		return fabric || k8s
	}
	return predicate.TypedFuncs[*corev1.ConfigMap]{
		CreateFunc: func(e event.TypedCreateEvent[*corev1.ConfigMap]) bool { return isRegistration(e.Object) },
		DeleteFunc: func(e event.TypedDeleteEvent[*corev1.ConfigMap]) bool { return isRegistration(e.Object) },
		UpdateFunc: func(e event.TypedUpdateEvent[*corev1.ConfigMap]) bool {
			oldFabric, oldK8s := labelled(e.ObjectOld)
			newFabric, newK8s := labelled(e.ObjectNew)
			return oldFabric != newFabric || oldK8s != newK8s
		},
		GenericFunc: func(event.TypedGenericEvent[*corev1.ConfigMap]) bool { return false },
	}
}

// SetupWithManager registers the reconciler. When provisioning is enabled,
// ConfigMap events in the registration namespace re-queue every NetworkClass
// that is not Ready. The ConfigMaps are watched through a cache limited to
// that namespace, so the operator needs ConfigMap read access only there.
func (r *NetworkClassReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return errors.New("local manager is nil")
	}
	controller := ctrl.NewControllerManagedBy(localMgr).
		Named("networkclass").
		For(&v1alpha1.NetworkClass{}, builder.WithPredicates(NetworkingNamespacePredicate(r.NetworkingNamespace)))
	if !r.ProvisioningEnabled {
		return controller.Complete(r)
	}
	registrations, err := cache.New(localMgr.GetConfig(), cache.Options{
		Scheme:            localMgr.GetScheme(),
		Mapper:            localMgr.GetRESTMapper(),
		DefaultNamespaces: map[string]cache.Config{r.RegistrationNamespace: {}},
	})
	if err != nil {
		return fmt.Errorf("creating the registration ConfigMap cache: %w", err)
	}
	if err := localMgr.Add(registrations); err != nil {
		return err
	}
	return controller.WatchesRawSource(source.Kind(registrations, &corev1.ConfigMap{},
		handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, _ *corev1.ConfigMap) []reconcile.Request {
			return r.notReadyNetworkClasses(ctx, nil)
		}),
		registrationChanged())).Complete(r)
}

func (r *NetworkClassReconciler) notReadyNetworkClasses(ctx context.Context, _ clnt.Object) []reconcile.Request {
	list := &v1alpha1.NetworkClassList{}
	if err := r.Client.List(ctx, list, clnt.InNamespace(r.NetworkingNamespace)); err != nil {
		ctrllog.FromContext(ctx).Error(err, "Failed to list NetworkClasses for a registration change")
		return nil
	}
	var requests []reconcile.Request
	for i := range list.Items {
		if !meta.IsStatusConditionTrue(list.Items[i].Status.Conditions, v1alpha1.NetworkConditionReady) {
			requests = append(requests, reconcile.Request{NamespacedName: clnt.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return requests
}

// NetworkClassFeedbackReconciler copies the NetworkClass resolution to the
// fulfillment record: state READY, FAILED or PENDING, message, and the
// private network_managers, network_type and security_group_icmp capability.
type NetworkClassFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.NetworkClass, *privatev1.NetworkClass]
	networkingNamespace string
}

// NewNetworkClassFeedbackReconciler creates the NetworkClass feedback reconciler.
func NewNetworkClassFeedbackReconciler(hubClient clnt.Client, grpcConn grpc.ClientConnInterface, networkingNamespace string) *NetworkClassFeedbackReconciler {
	ncClient := privatev1.NewNetworkClassesClient(grpcConn)
	r := &NetworkClassFeedbackReconciler{networkingNamespace: networkingNamespace}
	r.bridge = &feedback.Bridge[*v1alpha1.NetworkClass, *privatev1.NetworkClass]{
		Client:    hubClient,
		Finalizer: osacNetworkClassFeedbackFinalizer,
		IDLabel:   osacNetworkClassIDLabel,
		Kind:      "NetworkClass",
		IDKey:     "networkClassID",
		NewObject: func() *v1alpha1.NetworkClass { return &v1alpha1.NetworkClass{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.NetworkClass, error) {
			response, err := ncClient.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			nc := response.GetObject()
			if nc == nil {
				return nil, errors.New("network class response contained nil object")
			}
			if !nc.HasStatus() {
				nc.SetStatus(&privatev1.NetworkClassStatus{})
			}
			return nc, nil
		},
		Save: func(ctx context.Context, remote *privatev1.NetworkClass) error {
			_, err := ncClient.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
				Object: remote,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					feedbackStatusStatePath, feedbackStatusMessagePath,
					"status.network_managers", "status.network_type", "status.security_group_icmp",
				}},
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := ncClient.Signal(ctx, privatev1.NetworkClassesSignalRequest_builder{Id: id}.Build())
			return err
		},
		SyncUpdate: syncNetworkClass,
		SyncDelete: syncNetworkClass,
	}
	return r
}

// SetupWithManager adds the reconciler to the controller manager.
func (r *NetworkClassFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return errors.New("local manager is nil")
	}
	return ctrl.NewControllerManagedBy(localMgr).
		Named("networkclass-feedback").
		For(&v1alpha1.NetworkClass{}, builder.WithPredicates(NetworkingNamespacePredicate(r.networkingNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge.
func (r *NetworkClassFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

func syncNetworkClass(_ context.Context, obj *v1alpha1.NetworkClass, remote *privatev1.NetworkClass) error {
	status := remote.GetStatus()
	ready := meta.FindStatusCondition(obj.Status.Conditions, v1alpha1.NetworkConditionReady)
	switch {
	case ready == nil:
		status.SetState(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING)
		status.ClearMessage()
	case ready.Status == metav1.ConditionTrue:
		status.SetState(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)
		status.ClearMessage()
	default:
		status.SetState(privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED)
		status.SetMessage(sanitizeFeedbackText(ready.Message))
	}
	managers := obj.Status.NetworkManagers
	if managers == nil || managers.Fabric == nil {
		status.ClearNetworkManagers()
	} else {
		remoteManagers := privatev1.NetworkManagers_builder{
			Fabric: privatev1.NetworkManager_builder{Name: managers.Fabric.Name, AnsibleRole: managers.Fabric.AnsibleRole}.Build(),
		}.Build()
		if managers.Kubernetes != nil {
			remoteManagers.SetKubernetes(privatev1.NetworkManager_builder{
				Name: managers.Kubernetes.Name, AnsibleRole: managers.Kubernetes.AnsibleRole,
			}.Build())
		}
		status.SetNetworkManagers(remoteManagers)
	}
	status.SetNetworkType(string(obj.Status.NetworkType))
	if obj.Status.SecurityGroupICMP == nil {
		status.ClearSecurityGroupIcmp()
	} else {
		status.SetSecurityGroupIcmp(*obj.Status.SecurityGroupICMP)
	}
	return nil
}
