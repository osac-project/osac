/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package workloadcluster

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	maasWorkload           = "maas"
	managedClusterGroup    = "cluster.open-cluster-management.io"
	managedClusterVersion  = "v1"
	managedClusterResource = "managedclusters"

	availableCondition = "Available"
	hubReachable       = "HubReachable"

	reasonHubUnreachable            = "HubUnreachable"
	reasonManagedClusterNotFound    = "ManagedClusterNotFound"
	reasonManagedClusterNotAccepted = "ManagedClusterNotAccepted"
	reasonManagedClusterUnavailable = "ManagedClusterUnavailable"
	reasonClusterIdentityMissing    = "ClusterIdentityMissing"
	reasonClusterIdentityMismatch   = "ClusterIdentityMismatch"
)

var managedClusterGVK = schema.GroupVersionKind{Group: managedClusterGroup, Version: managedClusterVersion, Kind: "ManagedCluster"}

// FunctionBuilder contains the dependencies needed to observe a registered ManagedCluster.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger               *slog.Logger
	grpcClient           *grpc.ClientConn
	hubCache             controllers.HubCache
	workloadClusters     privatev1.WorkloadClustersClient
	watchers             map[string]context.CancelFunc
	watcherCredentials   map[string]string
	watcherGenerations   map[string]uint64
	watcherUseRevisions  map[string]uint64
	watchFailures        map[string]uint64
	watchFailureObserved map[string]bool
	watchFailureAttempts map[string]uint64
	watchHealthy         map[string]bool
	watchAttemptIDs      map[string]uint64
	watchersLock         sync.Mutex
	readyHubs            map[string]bool
	watcherReadyRevision map[string]uint64
	managedCaches        map[string]cache.SharedIndexInformer
	watchEstablished     map[string]bool
	unavailableSignals   map[string]unavailableSignalState
	unavailableSignalID  uint64
	pendingReleases      map[string]watcherReleaseState
	pendingReleaseID     uint64
}

type watcherReleaseState struct {
	id     uint64
	cancel context.CancelFunc
}

// NewFunction creates a WorkloadCluster observation function.
func NewFunction() *FunctionBuilder { return &FunctionBuilder{} }

func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder {
	b.logger = value
	return b
}

func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}

func (b *FunctionBuilder) SetHubCache(value controllers.HubCache) *FunctionBuilder {
	b.hubCache = value
	return b
}

// Build creates a reconciler that observes the Hub's ManagedCluster API.
func (b *FunctionBuilder) Build() (controllers.ReconcilerFunction[*privatev1.WorkloadCluster], error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.connection == nil {
		return nil, errors.New("client is mandatory")
	}
	if b.hubCache == nil {
		return nil, errors.New("hub cache is mandatory")
	}
	r := &function{
		logger:               b.logger,
		grpcClient:           b.connection,
		hubCache:             b.hubCache,
		workloadClusters:     privatev1.NewWorkloadClustersClient(b.connection),
		watchers:             map[string]context.CancelFunc{},
		watcherCredentials:   map[string]string{},
		watcherGenerations:   map[string]uint64{},
		watcherUseRevisions:  map[string]uint64{},
		watchFailures:        map[string]uint64{},
		watchFailureObserved: map[string]bool{},
		watchFailureAttempts: map[string]uint64{},
		watchHealthy:         map[string]bool{},
		watchAttemptIDs:      map[string]uint64{},
		readyHubs:            map[string]bool{},
		watcherReadyRevision: map[string]uint64{},
		managedCaches:        map[string]cache.SharedIndexInformer{},
		watchEstablished:     map[string]bool{},
		unavailableSignals:   map[string]unavailableSignalState{},
		pendingReleases:      map[string]watcherReleaseState{},
	}
	return r.run, nil
}

func (r *function) run(ctx context.Context, object *privatev1.WorkloadCluster) error {
	// WorkloadCluster can represent multiple workload integrations. MaaS is
	// the only supported branch today; keep the dispatch explicit so adding a
	// second integration does not silently reuse MaaS behavior.
	if !containsWorkload(object.GetSpec().GetSupportedWorkloads(), maasWorkload) {
		return nil
	}
	return r.runMaaS(ctx, object)
}

func containsWorkload(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (r *function) runMaaS(ctx context.Context, object *privatev1.WorkloadCluster) error {
	if object.GetMetadata().HasDeletionTimestamp() {
		err := r.releaseWatcherIfUnused(ctx, object.GetSpec().GetMaas().GetSourceHubId())
		if err != nil {
			return controllers.RequeueAfter(err, time.Second)
		}
		return nil
	}
	hubID := object.GetSpec().GetMaas().GetSourceHubId()
	r.cancelPendingWatcherRelease(hubID)
	old := proto.Clone(object).(*privatev1.WorkloadCluster)
	status := object.GetStatus()
	if status == nil {
		status = &privatev1.WorkloadClusterStatus{}
	}
	entry, err := r.hubCache.Get(ctx, object.GetSpec().GetMaas().GetSourceHubId())
	watcherFingerprint := ""
	var watcherErr error
	if err == nil {
		watcherFingerprint, watcherErr = r.ensureMaaSWatcher(ctx, object.GetSpec().GetMaas().GetSourceHubId(), entry.CredentialFingerprint)
	}
	informer, generation, readinessRevision, ready, pending := r.readyMaaSInformer(hubID)
	if err == nil && watcherErr == nil && ready && !r.isMaaSWatcherCredential(hubID, watcherFingerprint) {
		return controllers.RequeueAfter(errors.New("hub watcher credentials changed during observation"), time.Second)
	}
	if err == nil && watcherErr == nil && !ready && pending {
		return nil
	}
	if err != nil || watcherErr != nil || !ready {
		setUnavailable(status, reasonHubUnreachable, "Cannot reach hub to validate cluster registration", false)
		if watcherErr != nil {
			err = controllers.RequeueAfter(errors.New("hub credential resolution failed"), time.Second)
		} else if err != nil {
			// Do not return the underlying Hub/Kubernetes error: the generic
			// reconciler logs returned errors and those errors may contain
			// endpoints or other credential-adjacent details.
			err = controllers.RequeueAfter(errors.New("hub credential resolution failed"), time.Second)
		} else {
			err = errors.New("hub watcher is not ready")
		}
	} else {
		setCondition(status, hubReachable, "True", "HubReachable", "Hub is reachable and ready", true)
		observationErr := r.observeWithInformer(ctx, entry.Client, object, status, informer)
		if !r.isReadyMaaSInformer(hubID, generation, readinessRevision, informer, watcherFingerprint) {
			return controllers.RequeueAfter(errors.New("hub watcher stopped during observation"), time.Second)
		}
		err = observationErr
	}
	object.SetStatus(status)
	if !proto.Equal(old.GetStatus(), object.GetStatus()) {
		updateCtx, cancelUpdate := context.WithTimeout(ctx, 15*time.Second)
		_, updateErr := r.workloadClusters.Update(updateCtx, privatev1.WorkloadClustersUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
		}.Build())
		cancelUpdate()
		if updateErr != nil {
			return controllers.RequeueAfter(updateErr, time.Second)
		}
	}
	return err
}

// releaseWatcherIfUnused stops a Hub informer once its last active registration
// has been deleted. The list is intentionally performed through the private
// client so soft-deleted records do not keep a Hub watcher alive.
func (r *function) releaseWatcherIfUnused(ctx context.Context, hubID string) error {
	if hubID == "" {
		return nil
	}
	err := r.tryReleaseWatcherIfUnused(ctx, hubID)
	if err != nil {
		r.scheduleWatcherReleaseRetry(ctx, hubID)
	}
	return err
}

func (r *function) tryReleaseWatcherIfUnused(ctx context.Context, hubID string) error {
	for {
		r.watchersLock.Lock()
		revision := r.watcherUseRevisions[hubID]
		r.watchersLock.Unlock()

		filter := "this.spec.maas.source_hub_id == " + strconv.Quote(hubID)
		listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		response, err := r.workloadClusters.List(listCtx, privatev1.WorkloadClustersListRequest_builder{
			Filter: &filter,
			Limit:  proto.Int32(1),
		}.Build())
		cancel()
		if err != nil {
			r.logger.Warn("Failed to check workload cluster registrations before releasing Hub watcher", slog.String("component", "workload-cluster-controller"), slog.String("operation", "cleanup"), slog.String("reason", "ListFailed"))
			return errors.New("failed to check workload cluster registrations before releasing Hub watcher")
		}
		if len(response.GetItems()) != 0 {
			return nil
		}

		r.watchersLock.Lock()
		if err := ctx.Err(); err != nil {
			r.watchersLock.Unlock()
			return err
		}
		if revision != r.watcherUseRevisions[hubID] {
			r.watchersLock.Unlock()
			continue
		}
		if cancel, ok := r.watchers[hubID]; ok {
			cancel()
			delete(r.watchers, hubID)
			delete(r.watcherCredentials, hubID)
			delete(r.watchFailures, hubID)
			delete(r.readyHubs, hubID)
			delete(r.managedCaches, hubID)
			delete(r.watchEstablished, hubID)
		}
		r.watchersLock.Unlock()
		return nil
	}
}

func (r *function) scheduleWatcherReleaseRetry(parent context.Context, hubID string) {
	r.watchersLock.Lock()
	if r.pendingReleases == nil {
		r.pendingReleases = make(map[string]watcherReleaseState)
	}
	if _, exists := r.pendingReleases[hubID]; exists {
		r.watchersLock.Unlock()
		return
	}
	r.pendingReleaseID++
	retryID := r.pendingReleaseID
	ctx, cancel := context.WithCancel(parent)
	r.pendingReleases[hubID] = watcherReleaseState{id: retryID, cancel: cancel}
	r.watchersLock.Unlock()

	go func() {
		defer cancel()
		defer func() {
			r.watchersLock.Lock()
			if current, exists := r.pendingReleases[hubID]; exists && current.id == retryID {
				delete(r.pendingReleases, hubID)
			}
			r.watchersLock.Unlock()
		}()
		for attempt := 0; ; attempt++ {
			if err := r.tryReleaseWatcherIfUnused(ctx, hubID); err == nil {
				return
			}
			backoff := time.Duration(1<<min(attempt, 5)) * 250 * time.Millisecond
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func (r *function) cancelPendingWatcherRelease(hubID string) {
	if hubID == "" {
		return
	}
	r.watchersLock.Lock()
	defer r.watchersLock.Unlock()
	if current, exists := r.pendingReleases[hubID]; exists {
		current.cancel()
		delete(r.pendingReleases, hubID)
	}
}

func (r *function) observe(ctx context.Context, hubClient clnt.Client, object *privatev1.WorkloadCluster, status *privatev1.WorkloadClusterStatus) error {
	return r.observeWithInformer(ctx, hubClient, object, status, r.managedClusterInformer(object.GetSpec().GetMaas().GetSourceHubId()))
}

func (r *function) observeWithInformer(ctx context.Context, hubClient clnt.Client, object *privatev1.WorkloadCluster, status *privatev1.WorkloadClusterStatus, informer cache.SharedIndexInformer) error {
	managed := &unstructured.Unstructured{}
	managed.SetGroupVersionKind(managedClusterGVK)
	var err error
	if informer != nil {
		cached, exists, cacheErr := informer.GetStore().GetByKey(object.GetSpec().GetMaas().GetManagedClusterName())
		if cacheErr != nil {
			err = cacheErr
		} else if !exists {
			err = apierrors.NewNotFound(schema.GroupResource{Group: managedClusterGroup, Resource: managedClusterResource}, object.GetSpec().GetMaas().GetManagedClusterName())
		} else if cachedObject, ok := cached.(*unstructured.Unstructured); ok {
			managed = cachedObject.DeepCopy()
		} else {
			err = errors.New("hub informer returned an unexpected ManagedCluster object")
		}
	} else {
		err = hubClient.Get(ctx, clnt.ObjectKey{Name: object.GetSpec().GetMaas().GetManagedClusterName()}, managed)
	}
	if apierrors.IsNotFound(err) {
		setUnavailable(status, reasonManagedClusterNotFound, "Referenced managed cluster not found on the hub", true)
		return nil
	}
	if err != nil {
		setUnavailable(status, reasonManagedClusterUnavailable, "Failed to observe the managed cluster on the hub", false)
		return errors.New("managed cluster observation failed")
	}
	claimValue, extractionErr := clusterIdentity(managed)
	if extractionErr != nil {
		setUnavailable(status, reasonManagedClusterUnavailable, "Managed cluster status is malformed", true)
		return errors.New("managed cluster observation failed")
	}
	if strings.TrimSpace(claimValue) == "" {
		setUnavailable(status, reasonClusterIdentityMissing, "Managed cluster has no stable identity claim", true)
		return nil
	}
	if recorded := status.GetManagedClusterUid(); recorded != "" && recorded != claimValue {
		setUnavailable(status, reasonClusterIdentityMismatch, "Cluster identity changed; administrative action required", true)
		return nil
	}
	accepted, extractionErr := conditionTrue(managed, "HubAcceptedManagedCluster")
	if extractionErr != nil {
		setUnavailable(status, reasonManagedClusterUnavailable, "Managed cluster status is malformed", true)
		return errors.New("managed cluster observation failed")
	}
	if !accepted {
		setUnavailable(status, reasonManagedClusterNotAccepted, "Referenced managed cluster exists but is not accepted by the hub", true)
		return nil
	}
	available, extractionErr := conditionTrue(managed, "ManagedClusterConditionAvailable")
	if extractionErr != nil {
		setUnavailable(status, reasonManagedClusterUnavailable, "Managed cluster status is malformed", true)
		return errors.New("managed cluster observation failed")
	}
	if !available {
		setUnavailable(status, reasonManagedClusterUnavailable, "Referenced managed cluster is accepted but not available", true)
		return nil
	}
	if status.GetManagedClusterUid() == "" {
		status.SetManagedClusterUid(claimValue)
	}
	setCondition(status, availableCondition, "True", "ValidationSucceeded", "Referenced managed cluster is available", true)
	return nil
}

func (r *function) managedClusterInformer(hubID string) cache.SharedIndexInformer {
	informer, _, _, _, _ := r.readyMaaSInformer(hubID)
	return informer
}

func clusterIdentity(object *unstructured.Unstructured) (string, error) {
	claims, _, err := unstructured.NestedSlice(object.Object, "status", "clusterClaims")
	if err != nil {
		return "", errors.New("invalid cluster claims")
	}
	for _, raw := range claims {
		claim, ok := raw.(map[string]interface{})
		if !ok {
			return "", errors.New("invalid cluster claim")
		}
		name, _, err := unstructured.NestedString(claim, "name")
		if err != nil {
			return "", errors.New("invalid cluster claim name")
		}
		if name == "id.k8s.io" {
			value, _, err := unstructured.NestedString(claim, "value")
			if err != nil {
				return "", errors.New("invalid cluster identity claim")
			}
			return value, nil
		}
	}
	return "", nil
}

func conditionTrue(object *unstructured.Unstructured, conditionType string) (bool, error) {
	conditions, _, err := unstructured.NestedSlice(object.Object, "status", "conditions")
	if err != nil {
		return false, errors.New("invalid managed cluster conditions")
	}
	for _, raw := range conditions {
		condition, ok := raw.(map[string]interface{})
		if !ok {
			return false, errors.New("invalid managed cluster condition")
		}
		typeValue, _, err := unstructured.NestedString(condition, "type")
		if err != nil {
			return false, errors.New("invalid managed cluster condition type")
		}
		statusValue, _, err := unstructured.NestedString(condition, "status")
		if err != nil {
			return false, errors.New("invalid managed cluster condition status")
		}
		if typeValue == conditionType && statusValue == "True" {
			return true, nil
		}
	}
	return false, nil
}

func setUnavailable(status *privatev1.WorkloadClusterStatus, reason, message string, reachable bool) {
	if reachable {
		setCondition(status, hubReachable, "True", "HubReachable", "Hub is reachable and ready", true)
	} else {
		// The failed reachability check is still an observation of the current
		// Hub state. Refresh its freshness timestamp even though the condition
		// is False.
		setCondition(status, hubReachable, "False", reasonHubUnreachable, "Hub cannot be reached", true)
	}
	setCondition(status, availableCondition, "False", reason, message, true)
}

func boolStatus(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

func setCondition(status *privatev1.WorkloadClusterStatus, conditionType, value, reason, message string, observed bool) {
	now := timestamppb.Now()
	var condition *privatev1.WorkloadClusterCondition
	for _, candidate := range status.GetConditions() {
		if candidate.GetType() == conditionType {
			condition = candidate
			break
		}
	}
	if condition == nil {
		condition = &privatev1.WorkloadClusterCondition{Type: conditionType}
		status.SetConditions(append(status.GetConditions(), condition))
	}
	transitioned := condition.GetStatus() != value || condition.GetReason() != reason
	if transitioned {
		condition.SetLastTransitionTime(now)
	}
	condition.SetStatus(value)
	condition.SetReason(reason)
	condition.SetMessage(message)
	if observed {
		condition.SetLastObservedTime(now)
	}
}
