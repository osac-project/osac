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
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
)

// NetworkClassCapabilitiesReconciler computes NetworkClass.capabilities as the
// intersection of the capabilities declared by its resolved fabric and k8s manager
// ConfigMaps, and writes the result back to the fulfillment service.
//
// NetworkClass has no CRD in this operator, so it can't be watched directly. This
// reconciler instead watches the manager registration ConfigMaps (a k8s-native event
// source) and re-checks every NetworkClass on each relevant ConfigMap event. A
// separate periodic runnable (see NewNetworkClassCapabilitiesSyncRunnable) catches
// NetworkClass-side spec changes (e.g. a different fabricManager/k8sManager
// assignment), which have no k8s-native event source at all.
type NetworkClassCapabilitiesReconciler struct {
	networkClassesClient privatev1.NetworkClassesClient
	resolver             *dispatcher.Resolver
	networkingNamespace  string

	// resyncMu serializes full resyncs between the ConfigMap-triggered Reconcile
	// path and the periodic sync runnable, so the two never race against each
	// other listing/updating the same NetworkClasses.
	resyncMu sync.Mutex
}

// NewNetworkClassCapabilitiesReconciler creates a reconciler that syncs NetworkClass
// capabilities using the given gRPC client and manager resolver.
func NewNetworkClassCapabilitiesReconciler(
	networkClassesClient privatev1.NetworkClassesClient,
	resolver *dispatcher.Resolver,
	networkingNamespace string,
) *NetworkClassCapabilitiesReconciler {
	return &NetworkClassCapabilitiesReconciler{
		networkClassesClient: networkClassesClient,
		resolver:             resolver,
		networkingNamespace:  networkingNamespace,
	}
}

// SetupWithManager adds the reconciler to the controller manager. It watches manager
// registration ConfigMaps in the configured networking namespace.
func (r *NetworkClassCapabilitiesReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("networkclass-capabilities").
		For(&corev1.ConfigMap{}, builder.WithPredicates(managerConfigMapPredicate(r.networkingNamespace))).
		Complete(r)
}

// managerConfigMapPredicate filters ConfigMap events to those in the given namespace
// that carry either the fabric-manager or k8s-manager registration label.
func managerConfigMapPredicate(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj clnt.Object) bool {
		if obj.GetNamespace() != namespace {
			return false
		}
		labels := obj.GetLabels()
		return labels[networkmanager.LabelFabricManager] == "true" || labels[networkmanager.LabelK8sManager] == "true"
	})
}

// Reconcile ignores the triggering request's contents — any relevant ConfigMap event
// re-checks every NetworkClass, since there's no cheap way to know in advance which
// NetworkClasses reference the changed manager.
func (r *NetworkClassCapabilitiesReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, r.resyncAll(ctx)
}

// resyncAll recomputes capabilities for every NetworkClass, waiting for any resync
// already in progress (e.g. a concurrent periodic tick) to finish first. Used by the
// ConfigMap-triggered Reconcile path, where a ConfigMap change must eventually be
// reflected rather than skipped.
func (r *NetworkClassCapabilitiesReconciler) resyncAll(ctx context.Context) error {
	r.resyncMu.Lock()
	defer r.resyncMu.Unlock()
	return r.resyncAllLocked(ctx)
}

// resyncAllLocked recomputes capabilities for every NetworkClass. Callers must hold
// resyncMu. Errors for individual NetworkClasses are logged and joined, but don't stop
// the rest of the pass — a bad manager reference on one NetworkClass shouldn't block
// others from being synced.
func (r *NetworkClassCapabilitiesReconciler) resyncAllLocked(ctx context.Context) error {
	log := ctrllog.FromContext(ctx)

	items, err := listAllNetworkClasses(ctx, r.networkClassesClient)
	if err != nil {
		return fmt.Errorf("listing network classes: %w", err)
	}

	var errs []error
	for _, nc := range items {
		if syncErr := r.syncOne(ctx, nc); syncErr != nil {
			log.Error(syncErr, "failed to sync network class capabilities", "networkClassID", nc.GetId())
			errs = append(errs, syncErr)
		}
	}
	return errors.Join(errs...)
}

// listAllNetworkClasses fetches every NetworkClass from the fulfillment service.
func (r *NetworkClassCapabilitiesReconciler) listAllNetworkClasses(ctx context.Context) ([]*privatev1.NetworkClass, error) {
	return listAllNetworkClasses(ctx, r.networkClassesClient)
}

// syncOne resolves and applies the capability intersection for a single NetworkClass,
// updating it via the fulfillment service only when the computed capabilities differ
// from what's already stored.
func (r *NetworkClassCapabilitiesReconciler) syncOne(ctx context.Context, nc *privatev1.NetworkClass) error {
	log := ctrllog.FromContext(ctx)

	resolved, err := r.resolver.Resolve(ctx, nc.GetId())
	switch {
	case errors.Is(err, dispatcher.ErrNoManagerConfigured):
		log.V(1).Info("network class has no fabricManager or k8sManager set, skipping capabilities sync",
			"networkClassID", nc.GetId())
		return nil
	case networkmanager.IsManagerNotFound(err):
		log.Info("network class references an unregistered manager, skipping capabilities sync",
			"networkClassID", nc.GetId(), "error", err)
		return nil
	case err != nil:
		return fmt.Errorf("resolving managers for network class %q: %w", nc.GetId(), err)
	}

	// Capabilities describe fabric-level networking support, so a NetworkClass
	// with no fabricManager (k8s-only deployment) has none to report.
	if resolved.FabricManager == nil {
		log.V(1).Info("network class has no fabricManager set, skipping capabilities sync",
			"networkClassID", nc.GetId())
		return nil
	}

	newCaps := computeCapabilities(resolved)
	if capabilitiesEqual(newCaps, nc.GetCapabilities()) {
		return nil
	}

	nc.SetCapabilities(newCaps)
	_, err = r.networkClassesClient.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
		Object: nc,
	}.Build())
	if err != nil {
		return fmt.Errorf("updating capabilities for network class %q: %w", nc.GetId(), err)
	}

	log.Info("updated network class capabilities", "networkClassID", nc.GetId(), "capabilities", newCaps)
	return nil
}

// computeCapabilities returns the capability intersection of the resolved fabric and
// k8s managers: a capability is enabled only if the fabric manager declares it and,
// when a k8s manager is configured, the k8s manager declares it too. When no k8s
// manager is configured, the fabric manager's capabilities are used as-is.
//
// NOTE(OSAC-2030): once NetworkClassSpec.disable_capabilities is available in the
// generated client, subtract those capabilities here before returning.
func computeCapabilities(resolved *dispatcher.ResolvedManagers) *privatev1.NetworkClassCapabilities {
	fabric := resolved.FabricManager
	k8s := resolved.K8sManager

	supports := func(capability networkmanager.Capability) bool {
		if !fabric.HasCapability(capability) {
			return false
		}
		return k8s == nil || k8s.HasCapability(capability)
	}

	caps := &privatev1.NetworkClassCapabilities{}
	caps.SetSupportsIpv4(supports(networkmanager.CapabilityIPv4))
	caps.SetSupportsIpv6(supports(networkmanager.CapabilityIPv6))
	caps.SetSupportsDualStack(supports(networkmanager.CapabilityDualStack))
	caps.SetDpuSupport(supports(networkmanager.CapabilityDPUSupport))
	return caps
}

// capabilitiesEqual reports whether two NetworkClassCapabilities have the same
// values. Nil is treated as all-false, matching the generated getters' behavior.
func capabilitiesEqual(a, b *privatev1.NetworkClassCapabilities) bool {
	return a.GetSupportsIpv4() == b.GetSupportsIpv4() &&
		a.GetSupportsIpv6() == b.GetSupportsIpv6() &&
		a.GetSupportsDualStack() == b.GetSupportsDualStack() &&
		a.GetDpuSupport() == b.GetDpuSupport()
}

// networkClassCapabilitiesSyncRunnable periodically re-syncs NetworkClass
// capabilities to catch NetworkClass-side spec changes (e.g. a different
// fabricManager/k8sManager assignment), which have no k8s-native event source.
type networkClassCapabilitiesSyncRunnable struct {
	reconciler *NetworkClassCapabilitiesReconciler
	interval   time.Duration
}

// NewNetworkClassCapabilitiesSyncRunnable returns a manager.Runnable that periodically
// resyncs every NetworkClass's capabilities. Register it with mgr.Add(); it requires
// leader election so only one replica runs the sync loop.
func NewNetworkClassCapabilitiesSyncRunnable(
	reconciler *NetworkClassCapabilitiesReconciler, interval time.Duration,
) manager.Runnable {
	return &networkClassCapabilitiesSyncRunnable{
		reconciler: reconciler,
		interval:   interval,
	}
}

// Start runs the periodic resync loop until the context is canceled.
func (s *networkClassCapabilitiesSyncRunnable) Start(ctx context.Context) error {
	log := ctrllog.FromContext(ctx).WithName("networkclass-capabilities-sync")
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.tick(ctx, log)
		}
	}
}

// tick runs a single periodic resync, skipping it if a resync triggered by the
// ConfigMap-driven Reconcile path is already in progress rather than blocking (which
// would delay observing ctx cancellation until that resync finishes).
func (s *networkClassCapabilitiesSyncRunnable) tick(ctx context.Context, log logr.Logger) {
	if !s.reconciler.resyncMu.TryLock() {
		log.V(1).Info("skipping periodic network class capabilities resync: already in progress")
		return
	}
	defer s.reconciler.resyncMu.Unlock()

	if err := s.reconciler.resyncAllLocked(ctx); err != nil {
		log.Error(err, "periodic network class capabilities resync failed")
	}
}

// NeedLeaderElection ensures the periodic resync loop runs on exactly one replica.
func (s *networkClassCapabilitiesSyncRunnable) NeedLeaderElection() bool {
	return true
}
