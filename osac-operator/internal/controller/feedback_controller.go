/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	ckv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
)

// FeedbackReconciler sends updates to the fulfillment service.
type FeedbackReconciler struct {
	bridge                *feedback.Bridge[*ckv1alpha1.ClusterOrder, *privatev1.Cluster]
	clusterOrderNamespace string
}

// NewFeedbackReconciler creates a reconciler that sends to the fulfillment service updates about cluster orders.
func NewFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, clusterOrderNamespace string) *FeedbackReconciler {
	return &FeedbackReconciler{
		bridge:                newClusterOrderFeedbackBridge(hubClient, privatev1.NewClustersClient(grpcConn)),
		clusterOrderNamespace: clusterOrderNamespace,
	}
}

// SetupWithManager adds the reconciler to the controller manager.
func (r *FeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("clusterorder-feedback").
		For(&ckv1alpha1.ClusterOrder{}, builder.WithPredicates(NamespacePredicate(r.clusterOrderNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge.
func (r *FeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

// newClusterOrderFeedbackBridge creates a Bridge wired to the given clients. Used by both
// the constructor and tests.
func newClusterOrderFeedbackBridge(hubClient clnt.Client, clustersClient privatev1.ClustersClient) *feedback.Bridge[*ckv1alpha1.ClusterOrder, *privatev1.Cluster] {
	return &feedback.Bridge[*ckv1alpha1.ClusterOrder, *privatev1.Cluster]{
		Client:    hubClient,
		Finalizer: osacClusterOrderFeedbackFinalizer,
		IDLabel:   osacClusterOrderIDLabel,
		Kind:      "ClusterOrder",
		IDKey:     "clusterID",
		NewObject: func() *ckv1alpha1.ClusterOrder { return &ckv1alpha1.ClusterOrder{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.Cluster, error) {
			response, err := clustersClient.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			cluster := response.GetObject()
			if cluster == nil {
				return nil, errors.New("cluster response contained nil object")
			}
			if !cluster.HasSpec() {
				cluster.SetSpec(&privatev1.ClusterSpec{})
			}
			if !cluster.HasStatus() {
				cluster.SetStatus(&privatev1.ClusterStatus{})
			}
			return cluster, nil
		},
		Save: func(ctx context.Context, remote *privatev1.Cluster) error {
			_, err := clustersClient.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: remote,
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := clustersClient.Signal(ctx, privatev1.ClustersSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate: newClusterOrderSyncUpdate(hubClient),
		SyncDelete: syncClusterOrderDelete,
	}
}

// newClusterOrderSyncUpdate returns a SyncUpdate function that captures hubClient
// for HostedCluster URL lookups on the Ready phase.
func newClusterOrderSyncUpdate(hubClient clnt.Client) func(context.Context, *ckv1alpha1.ClusterOrder, *privatev1.Cluster) error {
	return func(ctx context.Context, clusterOrder *ckv1alpha1.ClusterOrder, remote *privatev1.Cluster) error {
		syncClusterOrderConditions(ctx, clusterOrder, remote)
		syncClusterOrderPhase(ctx, clusterOrder, remote)
		if err := syncClusterOrderURLs(ctx, hubClient, clusterOrder, remote); err != nil {
			return err
		}
		syncClusterOrderNodeRequests(ctx, clusterOrder, remote)
		syncClusterOrderVIPEndpoints(clusterOrder, remote)
		return nil
	}
}

// syncClusterOrderVIPEndpoints copies MetalLB VIP addresses from ClusterOrder status
// to the Cluster proto. The VIPs are written by the CaaS template and consumed by the
// ExternalIPAttachment controller and the fulfillment-service API surface.
func syncClusterOrderVIPEndpoints(clusterOrder *ckv1alpha1.ClusterOrder, remote *privatev1.Cluster) {
	if clusterOrder.Status.ApiEndpoint != "" {
		remote.GetStatus().SetApiEndpoint(clusterOrder.Status.ApiEndpoint)
	}
	if clusterOrder.Status.IngressEndpoint != "" {
		remote.GetStatus().SetIngressEndpoint(clusterOrder.Status.IngressEndpoint)
	}
}

func syncClusterOrderDelete(ctx context.Context, clusterOrder *ckv1alpha1.ClusterOrder, remote *privatev1.Cluster) error {
	syncClusterOrderConditions(ctx, clusterOrder, remote)
	syncClusterOrderPhase(ctx, clusterOrder, remote)
	syncClusterOrderNodeRequests(ctx, clusterOrder, remote)
	remote.GetStatus().SetState(privatev1.ClusterState_CLUSTER_STATE_DELETING)
	return nil
}

// clusterOrderConditionMappings maps a ClusterOrder condition to the fulfillment
// condition whose status it drives. Each fulfillment condition has exactly one source
// condition, so the derived status never depends on the order of the conditions. The
// fulfillment API has a single PROGRESSING condition, and only "Progressing" drives its
// status (True while the cluster is still being installed, False once it is ready or has
// failed); "ClusterAvailable" drives READY.
//
// The PROGRESSING condition's *reason* and *message* are refined separately, from the
// furthest-advanced installation stage, by applyProgressingStageDetail below.
var clusterOrderConditionMappings = map[string]privatev1.ClusterConditionType{
	ckv1alpha1.ConditionProgressing:      privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING,
	ckv1alpha1.ConditionClusterAvailable: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
}

// clusterOrderProvisioningStages are the ClusterOrder conditions that mark individual
// installation steps, ordered from earliest to furthest-advanced. While the cluster is
// still installing, they do not become their own fulfillment conditions; instead they
// refine the single PROGRESSING condition's reason/message to the furthest step reached
// (see applyProgressingStageDetail). Selecting the furthest stage from this fixed order,
// rather than from the order the conditions happen to appear in the CR status, keeps the
// result deterministic (order-independent).
//
// ControlPlaneAvailable also refines PROGRESSING today; in Epic 2 it additionally gains
// its own orthogonal CONTROL_PLANE_AVAILABLE fulfillment condition. ClusterStorageReady
// is written by the storage controller, which uses the typed
// ClusterOrderConditionClusterStorageReady constant, so we key off that same constant to
// avoid reader/writer drift.
var clusterOrderProvisioningStages = []string{
	ckv1alpha1.ConditionAccepted,
	ckv1alpha1.ConditionControlPlaneCreated,
	ckv1alpha1.ConditionControlPlaneAvailable,
	string(ckv1alpha1.ClusterOrderConditionClusterStorageReady),
}

// clusterOrderUnsurfacedConditions are ClusterOrder conditions we know about but neither
// copy to the fulfillment API nor use to refine PROGRESSING. They are listed so they are
// not reported as unknown:
//   - NamespaceCreated is internal bookkeeping with no tenant-facing meaning.
//   - Deleting is reported through the DELETING state (see syncClusterOrderPhase and
//     syncClusterOrderDelete), not as a condition.
var clusterOrderUnsurfacedConditions = map[string]struct{}{
	ckv1alpha1.ConditionNamespaceCreated: {},
	ckv1alpha1.ConditionDeleting:         {},
}

func syncClusterOrderConditions(ctx context.Context, clusterOrder *ckv1alpha1.ClusterOrder, remote *privatev1.Cluster) {
	log := ctrllog.FromContext(ctx)

	for i := range clusterOrder.Status.Conditions {
		condition := clusterOrder.Status.Conditions[i]
		if protoType, ok := clusterOrderConditionMappings[condition.Type]; ok {
			syncClusterConditionFromCR(remote, protoType, condition)
			continue
		}
		if slices.Contains(clusterOrderProvisioningStages, condition.Type) {
			// An installation-step condition: it refines PROGRESSING's reason/message
			// (handled by applyProgressingStageDetail after this loop), not its own
			// fulfillment condition.
			continue
		}
		if _, ok := clusterOrderUnsurfacedConditions[condition.Type]; ok {
			continue
		}
		// A condition we do not recognise: log it so a newly added ClusterOrder condition
		// is noticed instead of being silently ignored.
		log.Info("Unmapped ClusterOrder condition, will ignore it", "condition", condition.Type)
	}

	applyProgressingStageDetail(clusterOrder, remote)
}

// applyProgressingStageDetail refines the PROGRESSING condition's reason and message to
// the furthest-advanced installation stage that has been reached, while leaving its
// status untouched (the status is single-sourced from "Progressing" in the loop above).
// The reason is the stage condition's name (e.g. "ControlPlaneCreated") and the message
// is that name split into words (e.g. "Control Plane Created").
//
// This only applies while PROGRESSING is True (installation underway). Once the cluster
// is ready or has failed, PROGRESSING is False and keeps the terminal reason/message that
// "Progressing" itself carried, rather than a mid-installation stage.
func applyProgressingStageDetail(clusterOrder *ckv1alpha1.ClusterOrder, remote *privatev1.Cluster) {
	var progressing *privatev1.ClusterCondition
	for _, current := range remote.Status.Conditions {
		if current.Type == privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING {
			progressing = current
			break
		}
	}
	if progressing == nil || progressing.GetStatus() != privatev1.ConditionStatus_CONDITION_STATUS_TRUE {
		return
	}

	trueConditions := trueConditionTypes(clusterOrder)
	furthestStage := ""
	for _, stage := range clusterOrderProvisioningStages {
		if _, ok := trueConditions[stage]; ok {
			furthestStage = stage
		}
	}
	if furthestStage == "" {
		return
	}

	progressing.SetReason(furthestStage)
	progressing.SetMessage(humanizeConditionName(furthestStage))
}

// trueConditionTypes returns the set of ClusterOrder condition types whose status is
// True.
func trueConditionTypes(clusterOrder *ckv1alpha1.ClusterOrder) map[string]struct{} {
	trueConditions := map[string]struct{}{}
	for i := range clusterOrder.Status.Conditions {
		condition := clusterOrder.Status.Conditions[i]
		if condition.Status == metav1.ConditionTrue {
			trueConditions[condition.Type] = struct{}{}
		}
	}
	return trueConditions
}

// humanizeConditionName turns a PascalCase condition name into space-separated words for
// a human-readable message, e.g. "ControlPlaneCreated" -> "Control Plane Created".
// ClusterOrder condition names are simple PascalCase without acronyms, so inserting a
// space before each interior uppercase rune is sufficient.
func humanizeConditionName(name string) string {
	var builder strings.Builder
	for index, runeValue := range name {
		if index > 0 && unicode.IsUpper(runeValue) {
			builder.WriteRune(' ')
		}
		builder.WriteRune(runeValue)
	}
	return builder.String()
}

func syncClusterConditionFromCR(remote *privatev1.Cluster, condType privatev1.ClusterConditionType, condition metav1.Condition) {
	clusterCondition := findClusterCondition(remote, condType)
	oldStatus := clusterCondition.GetStatus()
	newStatus := mapClusterConditionStatus(condition.Status)
	clusterCondition.SetStatus(newStatus)
	clusterCondition.SetReason(condition.Reason)
	clusterCondition.SetMessage(sanitizeFeedbackText(condition.Message))
	if newStatus != oldStatus {
		clusterCondition.SetLastTransitionTime(timestamppb.Now())
	}
}

func mapClusterConditionStatus(status metav1.ConditionStatus) privatev1.ConditionStatus {
	switch status {
	case metav1.ConditionFalse:
		return privatev1.ConditionStatus_CONDITION_STATUS_FALSE
	case metav1.ConditionTrue:
		return privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	default:
		return privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED
	}
}

func syncClusterOrderPhase(ctx context.Context, clusterOrder *ckv1alpha1.ClusterOrder, remote *privatev1.Cluster) {
	log := ctrllog.FromContext(ctx)
	switch clusterOrder.Status.Phase {
	case ckv1alpha1.ClusterOrderPhaseProgressing:
		remote.GetStatus().SetState(privatev1.ClusterState_CLUSTER_STATE_PROGRESSING)
	case ckv1alpha1.ClusterOrderPhaseFailed:
		remote.GetStatus().SetState(privatev1.ClusterState_CLUSTER_STATE_FAILED)
	case ckv1alpha1.ClusterOrderPhaseReady:
		remote.GetStatus().SetState(privatev1.ClusterState_CLUSTER_STATE_READY)
	case ckv1alpha1.ClusterOrderPhaseDeleting:
		remote.GetStatus().SetState(privatev1.ClusterState_CLUSTER_STATE_DELETING)
	default:
		log.Info("Unknown phase, will ignore it", "phase", clusterOrder.Status.Phase)
	}
}

// syncClusterOrderURLs fetches the HostedCluster and populates API/console URLs
// on the Ready phase. Only called on the update path.
func syncClusterOrderURLs(ctx context.Context, hubClient clnt.Client, clusterOrder *ckv1alpha1.ClusterOrder, remote *privatev1.Cluster) error {
	if clusterOrder.Status.Phase != ckv1alpha1.ClusterOrderPhaseReady {
		return nil
	}

	hostedCluster, err := fetchHostedCluster(ctx, hubClient, clusterOrder)
	if err != nil {
		return err
	}
	if hostedCluster == nil {
		return nil
	}

	clusterStatus := remote.GetStatus()
	if apiURL := calculateAPIURL(hostedCluster); apiURL != "" {
		clusterStatus.SetApiUrl(apiURL)
	}
	if consoleURL := calculateConsoleURL(hostedCluster); consoleURL != "" {
		clusterStatus.SetConsoleUrl(consoleURL)
	}
	return nil
}

func syncClusterOrderNodeRequests(ctx context.Context, clusterOrder *ckv1alpha1.ClusterOrder, remote *privatev1.Cluster) {
	log := ctrllog.FromContext(ctx)
	for i := range len(clusterOrder.Status.NodeRequests) {
		nodeRequest := &clusterOrder.Status.NodeRequests[i]

		var nodeSetID string
		for candidateNodeSetID, candidateNodeSet := range remote.GetSpec().GetNodeSets() {
			if candidateNodeSet.GetHostType().GetName() == nodeRequest.ResourceClass {
				nodeSetID = candidateNodeSetID
				break
			}
		}
		if nodeSetID == "" {
			log.Error(nil, "Failed to find a matching node set", "resource_class", nodeRequest.ResourceClass)
			continue
		}

		nodeSets := remote.GetStatus().GetNodeSets()
		if nodeSets == nil {
			nodeSets = map[string]*privatev1.ClusterNodeSet{}
			remote.GetStatus().SetNodeSets(nodeSets)
		}
		nodeSet := nodeSets[nodeSetID]
		if nodeSet == nil {
			nodeSet = privatev1.ClusterNodeSet_builder{
				HostType: privatev1.HostTypeReference_builder{
					Name: nodeRequest.ResourceClass,
				}.Build(),
			}.Build()
			nodeSets[nodeSetID] = nodeSet
		}

		oldValue := nodeSet.GetSize()
		newValue := int32(nodeRequest.NumberOfNodes)
		if newValue != oldValue {
			log.Info("Updating node set size",
				"resource_class", nodeRequest.ResourceClass,
				"old_value", oldValue,
				"new_value", newValue,
			)
			nodeSet.SetSize(newValue)
		}
	}
}

func fetchHostedCluster(ctx context.Context, hubClient clnt.Client, clusterOrder *ckv1alpha1.ClusterOrder) (*hypershiftv1beta1.HostedCluster, error) {
	hostedClusterRef := clusterOrder.Status.ClusterReference
	if hostedClusterRef == nil || hostedClusterRef.Namespace == "" || hostedClusterRef.HostedClusterName == "" {
		return nil, nil
	}
	hostedCluster := &hypershiftv1beta1.HostedCluster{}
	err := hubClient.Get(ctx, clnt.ObjectKey{
		Namespace: hostedClusterRef.Namespace,
		Name:      hostedClusterRef.HostedClusterName,
	}, hostedCluster)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return hostedCluster, nil
}

func calculateAPIURL(hc *hypershiftv1beta1.HostedCluster) string {
	apiEndpoint := hc.Status.ControlPlaneEndpoint
	if apiEndpoint.Host == "" || apiEndpoint.Port == 0 {
		return ""
	}
	return fmt.Sprintf("https://%s:%d", apiEndpoint.Host, apiEndpoint.Port)
}

func calculateConsoleURL(hc *hypershiftv1beta1.HostedCluster) string {
	return fmt.Sprintf(
		"https://console-openshift-console.apps.%s.%s",
		hc.Name, hc.Spec.DNS.BaseDomain,
	)
}

func findClusterCondition(remote *privatev1.Cluster, kind privatev1.ClusterConditionType) *privatev1.ClusterCondition {
	for _, current := range remote.Status.Conditions {
		if current.Type == kind {
			return current
		}
	}
	condition := &privatev1.ClusterCondition{
		Type:   kind,
		Status: privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
	}
	remote.Status.Conditions = append(remote.Status.Conditions, condition)
	return condition
}
