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

	"github.com/prometheus/client_golang/prometheus"
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
	osacmetrics "github.com/osac-project/osac/fulfillment-service/internal/metrics"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
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
	logger            *slog.Logger
	connection        *grpc.ClientConn
	hubCache          controllers.HubCache
	metricsRegisterer prometheus.Registerer
}

type function struct {
	logger             *slog.Logger
	grpcClient         *grpc.ClientConn
	hubCache           controllers.HubCache
	workloadClusters   privatev1.WorkloadClustersClient
	watchers           map[string]context.CancelFunc
	watcherCredentials map[string]string
	watcherGenerations map[string]uint64
	watchersLock       sync.Mutex
	readyHubs          map[string]bool
	managedCaches      map[string]cache.SharedIndexInformer
	watchEstablished   map[string]bool
	metrics            *osacmetrics.WorkloadClusterMetrics
	availabilityLock   sync.Mutex
	availability       map[string]string
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

func (b *FunctionBuilder) SetMetricsRegisterer(value prometheus.Registerer) *FunctionBuilder {
	b.metricsRegisterer = value
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
		logger:             b.logger,
		grpcClient:         b.connection,
		hubCache:           b.hubCache,
		workloadClusters:   privatev1.NewWorkloadClustersClient(b.connection),
		watchers:           map[string]context.CancelFunc{},
		watcherCredentials: map[string]string{},
		watcherGenerations: map[string]uint64{},
		readyHubs:          map[string]bool{},
		managedCaches:      map[string]cache.SharedIndexInformer{},
		watchEstablished:   map[string]bool{},
		availability:       map[string]string{},
	}
	if b.metricsRegisterer != nil {
		var metricErr error
		r.metrics, metricErr = osacmetrics.NewWorkloadClusterMetrics(b.metricsRegisterer)
		if metricErr != nil {
			return nil, metricErr
		}
	}
	return r.run, nil
}

func (r *function) run(ctx context.Context, object *privatev1.WorkloadCluster) error {
	if object.GetMetadata().HasDeletionTimestamp() {
		r.updateAvailabilityMetrics(object.GetId(), "")
		return r.releaseWatcherIfUnused(ctx, object.GetSpec().GetMaas().GetSourceHubId())
	}
	old := proto.Clone(object).(*privatev1.WorkloadCluster)
	status := object.GetStatus()
	if status == nil {
		status = &privatev1.WorkloadClusterStatus{}
	}
	entry, err := r.hubCache.Get(ctx, object.GetSpec().GetMaas().GetSourceHubId())
	if err == nil {
		r.ensureWatcher(ctx, object.GetSpec().GetMaas().GetSourceHubId(), entry.CredentialFingerprint)
	}
	if err != nil || !r.isHubReady(object.GetSpec().GetMaas().GetSourceHubId()) {
		setUnavailable(status, reasonHubUnreachable, "Cannot reach hub to validate cluster registration", false)
		if err != nil {
			// Do not return the underlying Hub/Kubernetes error: the generic
			// reconciler logs returned errors and those errors may contain
			// endpoints or other credential-adjacent details.
			err = errors.New("hub credential resolution failed")
		} else {
			err = errors.New("hub watcher is not ready")
		}
	} else {
		setCondition(status, hubReachable, "True", "HubReachable", "Hub is reachable and ready", true)
		err = r.observe(ctx, entry.Client, object, status)
	}
	object.SetStatus(status)
	if !proto.Equal(old.GetStatus(), object.GetStatus()) {
		_, updateErr := r.workloadClusters.Update(ctx, privatev1.WorkloadClustersUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
		}.Build())
		if updateErr != nil {
			return controllers.RequeueAfter(updateErr, time.Second)
		}
	}
	r.recordConditionTransitions(old.GetStatus(), object.GetStatus())
	r.updateAvailabilityMetrics(object.GetId(), availableStatus(object.GetStatus()))
	return err
}

// releaseWatcherIfUnused stops a Hub informer once its last active registration
// has been deleted. The list is intentionally performed through the public
// client so soft-deleted records do not keep a Hub watcher alive.
func (r *function) releaseWatcherIfUnused(ctx context.Context, hubID string) error {
	if hubID == "" {
		return nil
	}
	filter := "this.spec.maas.source_hub_id == " + strconv.Quote(hubID)
	response, err := r.workloadClusters.List(ctx, privatev1.WorkloadClustersListRequest_builder{
		Filter: &filter,
		Limit:  proto.Int32(1),
	}.Build())
	if err != nil {
		r.logger.Warn("Failed to check workload cluster registrations before releasing Hub watcher", slog.String("component", "workload-cluster-controller"), slog.String("operation", "cleanup"), slog.String("reason", "ListFailed"))
		return errors.New("failed to check workload cluster registrations before releasing Hub watcher")
	}
	if len(response.GetItems()) != 0 {
		return nil
	}

	r.watchersLock.Lock()
	defer r.watchersLock.Unlock()
	if cancel, ok := r.watchers[hubID]; ok {
		cancel()
		delete(r.watchers, hubID)
		delete(r.watcherCredentials, hubID)
		delete(r.readyHubs, hubID)
		delete(r.managedCaches, hubID)
		delete(r.watchEstablished, hubID)
	}
	return nil
}

func (r *function) recordConditionTransitions(old, current *privatev1.WorkloadClusterStatus) {
	if r.metrics == nil {
		return
	}
	oldByType := map[string]string{}
	for _, condition := range old.GetConditions() {
		oldByType[condition.GetType()] = condition.GetStatus()
	}
	for _, condition := range current.GetConditions() {
		from := oldByType[condition.GetType()]
		to := condition.GetStatus()
		if from != to {
			r.metrics.ConditionTransitions.WithLabelValues(condition.GetType(), from, to).Inc()
		}
	}
}

func availableStatus(status *privatev1.WorkloadClusterStatus) string {
	for _, condition := range status.GetConditions() {
		if condition.GetType() == availableCondition {
			return condition.GetStatus()
		}
	}
	return ""
}

func (r *function) updateAvailabilityMetrics(id, status string) {
	if r.metrics == nil || id == "" {
		return
	}
	r.availabilityLock.Lock()
	defer r.availabilityLock.Unlock()
	previous := r.availability[id]
	if previous == status {
		return
	}
	switch previous {
	case "True":
		r.metrics.Available.Dec()
	case "False":
		r.metrics.Unavailable.Dec()
	}
	switch status {
	case "True":
		r.metrics.Available.Inc()
	case "False":
		r.metrics.Unavailable.Inc()
	}
	if status == "" {
		delete(r.availability, id)
	} else {
		r.availability[id] = status
	}
}

func (r *function) observe(ctx context.Context, hubClient clnt.Client, object *privatev1.WorkloadCluster, status *privatev1.WorkloadClusterStatus) error {
	managed := &unstructured.Unstructured{}
	managed.SetGroupVersionKind(managedClusterGVK)
	var err error
	if informer := r.managedClusterInformer(object.GetSpec().GetMaas().GetSourceHubId()); informer != nil {
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
	r.watchersLock.Lock()
	defer r.watchersLock.Unlock()
	return r.managedCaches[hubID]
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
