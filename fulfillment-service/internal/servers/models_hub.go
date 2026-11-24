/*
Copyright (c) 2026 Red Hat Inc.

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

package servers

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	aiGridManagedClustersResource = schema.GroupVersionResource{
		Group: "cluster.open-cluster-management.io", Version: "v1", Resource: "managedclusters",
	}
	aiGridPoliciesResource = schema.GroupVersionResource{
		Group: "policy.open-cluster-management.io", Version: "v1", Resource: "policies",
	}
)

const (
	aiGridReadyPolicyNamespace = "ai-grid-policies"
	aiGridReadyPolicyName      = "ai-grid-ready"
)

type modelPlacementCluster struct {
	name     string
	location string
}

// listEligibleClusters reads AI Grid membership and provisioning state from
// the registered ACM Hub. It deliberately treats location as display-only:
// clusters with no location label remain eligible.
func (s *ModelsServer) listEligibleClusters(ctx context.Context, hubID string) ([]modelPlacementCluster, error) {
	if s.hubClientProvider == nil {
		return nil, status.Error(codes.FailedPrecondition, "Hub client provider is not configured")
	}
	hub, err := s.hubClientProvider.GetClient(ctx, hubID)
	if err != nil {
		return nil, err
	}
	if hub.Config == nil {
		return nil, status.Errorf(codes.Internal, "AI Grid Hub %q has no Kubernetes client configuration", hubID)
	}
	client, err := dynamic.NewForConfig(hub.Config)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to configure AI Grid Hub access: %v", err)
	}
	managedClusters, err := client.Resource(aiGridManagedClustersResource).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "failed to list ManagedClusters on AI Grid Hub %q: %v", hubID, err)
	}
	provisioned := readAIGridProvisioning(ctx, client, s.logger)
	result := make([]modelPlacementCluster, 0, len(managedClusters.Items))
	for i := range managedClusters.Items {
		item := &managedClusters.Items[i]
		if !isEligibleModelPlacement(item, provisioned) {
			continue
		}
		result = append(result, modelPlacementCluster{name: item.GetName(), location: modelPlacementLocation(item)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

func isEligibleModelPlacement(item *unstructured.Unstructured, provisioned map[string]string) bool {
	labels := item.GetLabels()
	if labels["ai-grid/enabled"] != "true" || provisioned[item.GetName()] != "Compliant" {
		return false
	}
	role := labels["ai-grid/role"]
	return (role == "inference" || role == "combined") && managedClusterAvailable(item)
}

func modelPlacementLocation(item *unstructured.Unstructured) string {
	labels := item.GetLabels()
	if location := labels["location"]; location != "" {
		return location
	}
	return labels["ai-grid/site"]
}

func readAIGridProvisioning(ctx context.Context, client dynamic.Interface, logger *slog.Logger) map[string]string {
	policyPath := envOrDefault("AI_GRID_READY_POLICY", aiGridReadyPolicyNamespace+"/"+aiGridReadyPolicyName)
	namespace, name, found := strings.Cut(policyPath, "/")
	if !found || namespace == "" || name == "" {
		logger.WarnContext(ctx, "AI_GRID_READY_POLICY must be namespace/name", "policy", policyPath)
		return map[string]string{}
	}
	policy, err := client.Resource(aiGridPoliciesResource).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			logger.WarnContext(ctx, "read AI Grid ready policy", "policy", policyPath, "error", err)
		}
		return map[string]string{}
	}
	entries, _, _ := unstructured.NestedSlice(policy.Object, "status", "status")
	result := make(map[string]string, len(entries))
	for _, raw := range entries {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		cluster, _ := entry["clustername"].(string)
		compliance, _ := entry["compliant"].(string)
		if cluster == "" {
			continue
		}
		if compliance != "Compliant" && compliance != "NonCompliant" {
			compliance = "Pending"
		}
		result[cluster] = compliance
	}
	return result
}

func managedClusterAvailable(item *unstructured.Unstructured) bool {
	conditions, _, _ := unstructured.NestedSlice(item.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		conditionType, _ := condition["type"].(string)
		conditionStatus, _ := condition["status"].(string)
		if conditionType == "ManagedClusterConditionAvailable" {
			return conditionStatus == "True"
		}
	}
	return false
}
