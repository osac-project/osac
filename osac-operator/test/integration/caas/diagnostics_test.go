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

package caas

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

func logFixtureDiagnostics(ctx context.Context, cluster *privatev1.Cluster) {
	id, tenant := cluster.GetId(), cluster.GetMetadata().GetTenant()
	latest, err := fulfillmentClient.GetCluster(ctx, id)
	if err == nil {
		fmt.Fprintf(GinkgoWriter, "CaaS cluster %s tenant=%s state=%s conditions=%v\n",
			id, tenant, latest.GetStatus().GetState(), latest.GetStatus().GetConditions())
	} else {
		fmt.Fprintf(GinkgoWriter, "CaaS cluster %s tenant=%s read error: %v\n", id, tenant, err)
	}
	bmis, err := fulfillmentClient.ListBareMetalInstances(ctx, "")
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "CaaS BMI list error: %v\n", err)
	} else {
		for _, bmi := range bmis {
			owner := bmi.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]
			if bmi.GetMetadata().GetTenant() == tenant && owner != "" {
				fmt.Fprintf(GinkgoWriter, "CaaS BMI %s name=%s tenant=%s owner=%s state=%s\n",
					bmi.GetId(), bmi.GetMetadata().GetName(), tenant, owner, bmi.GetStatus().GetState())
			}
		}
	}
	config, err := clientcmd.BuildConfigFromFlags("", connectedConfig.kubeconfig)
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "CaaS Kubernetes diagnostic config error: %v\n", err)
		return
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "CaaS Kubernetes diagnostic client error: %v\n", err)
		return
	}
	orderResource := schema.GroupVersionResource{
		Group: "osac.openshift.io", Version: "v1alpha1", Resource: "clusterorders",
	}
	orders, err := client.Resource(orderResource).Namespace(connectedConfig.namespace).List(ctx,
		metav1.ListOptions{LabelSelector: "osac.openshift.io/clusterorder-uuid=" + id})
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "CaaS ClusterOrder list error: %v\n", err)
		return
	}
	fmt.Fprintf(GinkgoWriter, "CaaS ClusterOrders for %s: %d\n", id, len(orders.Items))
	for _, order := range orders.Items {
		fmt.Fprintf(GinkgoWriter, "CaaS order %s tenant=%s status=%v\n",
			order.GetName(), order.GetAnnotations()["osac.openshift.io/tenant"], order.Object["status"])
		events, eventErr := kubeClient.CoreV1().Events(connectedConfig.namespace).List(ctx,
			metav1.ListOptions{FieldSelector: "involvedObject.name=" + order.GetName()})
		if eventErr != nil {
			fmt.Fprintf(GinkgoWriter, "CaaS order event list error: %v\n", eventErr)
			continue
		}
		for _, event := range events.Items {
			fmt.Fprintf(GinkgoWriter, "CaaS order event %s: %s\n", event.Reason, event.Message)
		}
	}
}
