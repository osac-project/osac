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
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
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
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger           *slog.Logger
	grpcClient       *grpc.ClientConn
	hubCache         controllers.HubCache
	workloadClusters privatev1.WorkloadClustersClient
	watchers         map[string]context.CancelFunc
	watchersLock     sync.Mutex
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
		logger:           b.logger,
		grpcClient:       b.connection,
		hubCache:         b.hubCache,
		workloadClusters: privatev1.NewWorkloadClustersClient(b.connection),
		watchers:         map[string]context.CancelFunc{},
	}
	return r.run, nil
}

func (r *function) run(ctx context.Context, object *privatev1.WorkloadCluster) error {
	if object.GetMetadata().HasDeletionTimestamp() {
		return nil
	}
	old := proto.Clone(object).(*privatev1.WorkloadCluster)
	r.ensureWatcher(ctx, object.GetSpec().GetSourceHubId())
	status := object.GetStatus()
	if status == nil {
		status = &privatev1.WorkloadClusterStatus{}
	}
	entry, err := r.hubCache.Get(ctx, object.GetSpec().GetSourceHubId())
	if err != nil {
		setUnavailable(status, reasonHubUnreachable, "Cannot reach hub to validate cluster registration", false)
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
			return updateErr
		}
	}
	return err
}

func (r *function) observe(ctx context.Context, hubClient clnt.Client, object *privatev1.WorkloadCluster, status *privatev1.WorkloadClusterStatus) error {
	managed := &unstructured.Unstructured{}
	managed.SetGroupVersionKind(managedClusterGVK)
	err := hubClient.Get(ctx, clnt.ObjectKey{Name: object.GetSpec().GetManagedClusterName()}, managed)
	if apierrors.IsNotFound(err) {
		setUnavailable(status, reasonManagedClusterNotFound, "Referenced managed cluster not found on the hub", true)
		return nil
	}
	if err != nil {
		setUnavailable(status, reasonManagedClusterUnavailable, "Failed to observe the managed cluster on the hub", false)
		return errors.New("managed cluster observation failed")
	}
	claimValue := clusterIdentity(managed)
	if strings.TrimSpace(claimValue) == "" {
		setUnavailable(status, reasonClusterIdentityMissing, "Managed cluster has no stable identity claim", true)
		return nil
	}
	if recorded := status.GetManagedClusterUid(); recorded != "" && recorded != claimValue {
		setUnavailable(status, reasonClusterIdentityMismatch, "Cluster identity changed; administrative action required", true)
		return nil
	}
	accepted := conditionTrue(managed, "HubAcceptedManagedCluster")
	if !accepted {
		setUnavailable(status, reasonManagedClusterNotAccepted, "Referenced managed cluster exists but is not accepted by the hub", true)
		return nil
	}
	if !conditionTrue(managed, "ManagedClusterConditionAvailable") {
		setUnavailable(status, reasonManagedClusterUnavailable, "Referenced managed cluster is accepted but not available", true)
		return nil
	}
	if status.GetManagedClusterUid() == "" {
		status.SetManagedClusterUid(claimValue)
	}
	setCondition(status, availableCondition, "True", "ManagedClusterAvailable", "Referenced managed cluster is available", true)
	return nil
}

func clusterIdentity(object *unstructured.Unstructured) string {
	claims, _, _ := unstructured.NestedSlice(object.Object, "status", "clusterClaims")
	for _, raw := range claims {
		claim, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(claim, "name")
		if name == "id.k8s.io" {
			value, _, _ := unstructured.NestedString(claim, "value")
			return value
		}
	}
	return ""
}

func conditionTrue(object *unstructured.Unstructured, conditionType string) bool {
	conditions, _, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, ok := raw.(map[string]interface{})
		if ok && condition["type"] == conditionType && condition["status"] == "True" {
			return true
		}
	}
	return false
}

func setUnavailable(status *privatev1.WorkloadClusterStatus, reason, message string, reachable bool) {
	setCondition(status, hubReachable, boolStatus(reachable), "HubReachable", "Hub is reachable and ready", reachable)
	setCondition(status, availableCondition, "False", reason, message, reachable)
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
	if condition.GetStatus() != value || condition.GetReason() != reason {
		condition.SetLastTransitionTime(now)
	}
	condition.SetStatus(value)
	condition.SetReason(reason)
	condition.SetMessage(message)
	if observed {
		condition.SetLastObservedTime(now)
	}
}
