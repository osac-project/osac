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
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var agentGVK = schema.GroupVersionKind{
	Group:   "agent-install.openshift.io",
	Version: "v1beta1",
	Kind:    "Agent",
}

const (
	agentClusterOrderLabel   = "osac.openshift.io/clusterorder"
	agentResourceClassLabel  = "osac.openshift.io/resource_class"
	agentServerNameLabel     = "netris.server/name"
	agentClaimedByHypershift = "agent-install.openshift.io/clusterdeployment-namespace"

	defaultAgentNamespace = "hardware-inventory"
)

// reconcileAgentSelection selects available agents for each node set and labels
// them so HyperShift's NodePool can claim them. Returns Requeue if not enough
// agents are available yet.
func (r *ClusterOrderReconciler) reconcileAgentSelection(
	ctx context.Context, instance *v1alpha1.ClusterOrder,
) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	if len(instance.Spec.NodeRequests) == 0 {
		return ctrl.Result{}, nil
	}

	// Check if agents are already selected
	if len(instance.Status.NodeSets) > 0 {
		return ctrl.Result{}, nil
	}

	agentNamespace := r.AgentNamespace
	if agentNamespace == "" {
		agentNamespace = defaultAgentNamespace
	}

	var nodeSets []v1alpha1.NodeSetStatus

	for _, nodeReq := range instance.Spec.NodeRequests {
		agents, err := r.selectAgents(ctx, agentNamespace, instance.Name, nodeReq.ResourceClass, nodeReq.NumberOfNodes)
		if err != nil {
			return ctrl.Result{}, err
		}

		if len(agents) < nodeReq.NumberOfNodes {
			log.Info("not enough agents available, requeueing",
				"resourceClass", nodeReq.ResourceClass,
				"requested", nodeReq.NumberOfNodes,
				"available", len(agents),
			)
			return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
		}

		// Label the selected agents
		for _, agent := range agents {
			if err := r.labelAgent(ctx, agent, instance.Name); err != nil {
				return ctrl.Result{}, fmt.Errorf("labeling agent %s: %w", agent.GetName(), err)
			}
		}

		// Build agent status entries
		var agentStatuses []v1alpha1.AgentStatus
		for _, agent := range agents {
			agentStatuses = append(agentStatuses, v1alpha1.AgentStatus{
				AgentName: agent.GetName(),
				HostName:  agent.GetLabels()[agentServerNameLabel],
			})
		}

		nodeSets = append(nodeSets, v1alpha1.NodeSetStatus{
			Name:   nodeReq.ResourceClass,
			Agents: agentStatuses,
		})
	}

	instance.Status.NodeSets = nodeSets
	log.Info("agent selection complete", "nodeSets", len(nodeSets))
	return ctrl.Result{}, nil
}

// selectAgents lists available agents matching the resource class that are not
// already allocated to a cluster.
func (r *ClusterOrderReconciler) selectAgents(
	ctx context.Context, namespace, clusterOrderName, resourceClass string, count int,
) ([]*unstructured.Unstructured, error) {
	// First check if agents are already labeled for this cluster order
	alreadyLabeled, err := r.listAgentsForClusterOrder(ctx, namespace, clusterOrderName, resourceClass)
	if err != nil {
		return nil, err
	}
	if len(alreadyLabeled) >= count {
		return alreadyLabeled[:count], nil
	}

	// Find available agents (matching resource class, not allocated)
	selector := labels.NewSelector()
	rcReq, err := labels.NewRequirement(agentResourceClassLabel, selection.Equals, []string{resourceClass})
	if err != nil {
		return nil, fmt.Errorf("invalid resource class %q for label selector: %w", resourceClass, err)
	}
	selector = selector.Add(*rcReq)
	noClusterOrder, err := labels.NewRequirement(agentClusterOrderLabel, selection.DoesNotExist, nil)
	if err != nil {
		return nil, fmt.Errorf("building label requirement: %w", err)
	}
	selector = selector.Add(*noClusterOrder)
	noClaimed, err := labels.NewRequirement(agentClaimedByHypershift, selection.DoesNotExist, nil)
	if err != nil {
		return nil, fmt.Errorf("building label requirement: %w", err)
	}
	selector = selector.Add(*noClaimed)

	agentList := &unstructured.UnstructuredList{}
	agentList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   agentGVK.Group,
		Version: agentGVK.Version,
		Kind:    agentGVK.Kind + "List",
	})

	if err := r.Client.List(ctx, agentList,
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return nil, fmt.Errorf("listing available agents for resource class %s: %w", resourceClass, err)
	}

	// Combine already-labeled + newly available, up to count
	result := append(alreadyLabeled, make([]*unstructured.Unstructured, 0, count-len(alreadyLabeled))...)
	for i := range agentList.Items {
		if len(result) >= count {
			break
		}
		result = append(result, &agentList.Items[i])
	}

	return result, nil
}

// listAgentsForClusterOrder returns agents already labeled for this cluster order.
func (r *ClusterOrderReconciler) listAgentsForClusterOrder(
	ctx context.Context, namespace, clusterOrderName, resourceClass string,
) ([]*unstructured.Unstructured, error) {
	matchLabels := map[string]string{
		agentClusterOrderLabel:  clusterOrderName,
		agentResourceClassLabel: resourceClass,
	}

	agentList := &unstructured.UnstructuredList{}
	agentList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   agentGVK.Group,
		Version: agentGVK.Version,
		Kind:    agentGVK.Kind + "List",
	})

	if err := r.Client.List(ctx, agentList,
		client.InNamespace(namespace),
		client.MatchingLabels(matchLabels),
	); err != nil {
		return nil, fmt.Errorf("listing agents for cluster order %s: %w", clusterOrderName, err)
	}

	var result []*unstructured.Unstructured
	for i := range agentList.Items {
		result = append(result, &agentList.Items[i])
	}
	return result, nil
}

// labelAgent sets the clusterorder label on an agent to reserve it.
func (r *ClusterOrderReconciler) labelAgent(
	ctx context.Context, agent *unstructured.Unstructured, clusterOrderName string,
) error {
	agentLabels := agent.GetLabels()
	if agentLabels[agentClusterOrderLabel] == clusterOrderName {
		return nil
	}
	if agentLabels == nil {
		agentLabels = make(map[string]string)
	}
	agentLabels[agentClusterOrderLabel] = clusterOrderName
	agent.SetLabels(agentLabels)
	return r.Client.Update(ctx, agent)
}

// reconcileAgentCleanup removes clusterorder labels from agents allocated to
// this cluster, making them available for future clusters.
func (r *ClusterOrderReconciler) reconcileAgentCleanup(
	ctx context.Context, instance *v1alpha1.ClusterOrder,
) error {
	log := ctrllog.FromContext(ctx)

	agentNamespace := r.AgentNamespace
	if agentNamespace == "" {
		agentNamespace = defaultAgentNamespace
	}

	agentList := &unstructured.UnstructuredList{}
	agentList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   agentGVK.Group,
		Version: agentGVK.Version,
		Kind:    agentGVK.Kind + "List",
	})

	if err := r.Client.List(ctx, agentList,
		client.InNamespace(agentNamespace),
		client.MatchingLabels{agentClusterOrderLabel: instance.Name},
	); err != nil {
		return fmt.Errorf("listing agents for cleanup: %w", err)
	}

	for i := range agentList.Items {
		agent := &agentList.Items[i]
		agentLabels := agent.GetLabels()
		delete(agentLabels, agentClusterOrderLabel)
		agent.SetLabels(agentLabels)
		if err := r.Client.Update(ctx, agent); err != nil {
			return fmt.Errorf("unlabeling agent %s: %w", agent.GetName(), err)
		}
		log.Info("released agent", "agent", agent.GetName())
	}

	return nil
}
