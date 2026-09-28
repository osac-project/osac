// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// reconcileNodePoolReplicas preserves the requested bare-metal capacity while Agents are
// still provisioning. NodePools are discovered by the same label selector the ClusterOrder
// controller uses.
func (r *Reconciler) reconcileNodePoolReplicas(
	ctx context.Context, co *v1alpha1.ClusterOrder,
) (ctrl.Result, error) {
	clusterRef := co.Status.ClusterReference
	if clusterRef == nil || clusterRef.Namespace == "" {
		return ctrl.Result{}, nil
	}

	nodePools, err := r.listNodePools(ctx, clusterRef.Namespace, co.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(nodePools.Items) == 0 {
		ctrllog.FromContext(ctx).Info("no NodePool found, requeuing")
		return ctrl.Result{RequeueAfter: agentRequeueInterval}, nil
	}

	replicas := requestedBareMetalWorkersByNodeSet(co)
	return ctrl.Result{}, r.patchNodePoolReplicas(ctx, nodePools, replicas)
}

// requestedBareMetalWorkersByNodeSet preserves each requested NodePool capacity while
// BMaaS workers are still provisioning and their Agents have not registered yet.
func requestedBareMetalWorkersByNodeSet(co *v1alpha1.ClusterOrder) map[string]int64 {
	replicas := make(map[string]int64)
	for _, request := range co.Spec.NodeRequests {
		if request.IsBareMetal() {
			replicas[request.NodeSet] = int64(request.NumberOfNodes)
		}
	}
	return replicas
}

func (r *Reconciler) listNodePools(ctx context.Context, namespace, clusterOrderName string) (*unstructured.UnstructuredList, error) {
	nodePoolList := &unstructured.UnstructuredList{}
	nodePoolList.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "hypershift.openshift.io", Version: "v1beta1", Kind: "NodePoolList",
	})
	if err := r.List(ctx, nodePoolList,
		client.InNamespace(namespace),
		client.MatchingLabels{"osac.openshift.io/clusterorder": clusterOrderName},
	); err != nil {
		return nil, fmt.Errorf("listing nodepools for %s: %w", clusterOrderName, err)
	}
	return nodePoolList, nil
}

func (r *Reconciler) patchNodePoolReplicas(
	ctx context.Context, nodePools *unstructured.UnstructuredList, replicasByNodeSet map[string]int64,
) error {
	log := ctrllog.FromContext(ctx)
	for idx := range nodePools.Items {
		np := &nodePools.Items[idx]
		nodeSet := np.GetLabels()[agentNodeSetLabel]
		replicas, ok := replicasByNodeSet[nodeSet]
		if !ok {
			continue
		}
		currentReplicas, _, _ := unstructured.NestedInt64(np.Object, "spec", "replicas")
		if currentReplicas == replicas {
			continue
		}
		patch := np.DeepCopy()
		if err := unstructured.SetNestedField(patch.Object, replicas, "spec", "replicas"); err != nil {
			return fmt.Errorf("setting nodepool replicas: %w", err)
		}
		if err := r.Patch(ctx, patch, client.MergeFrom(np)); err != nil {
			return fmt.Errorf("patching nodepool %s replicas: %w", np.GetName(), err)
		}
		log.Info("updated NodePool replicas", "nodepool", np.GetName(), "replicas", replicas)
	}
	return nil
}
