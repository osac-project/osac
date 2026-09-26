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
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func clusterOrderClient() crclient.Client {
	config, err := clientcmd.BuildConfigFromFlags("", connectedConfig.kubeconfig)
	Expect(err).NotTo(HaveOccurred())
	scheme := runtime.NewScheme()
	Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
	client, err := crclient.New(config, crclient.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	return client
}

var _ = Describe("real fulfillment ClusterOrder generation", func() {
	It("preserves distinct instance types and counts for two NodeSets", func(ctx context.Context) {
		cluster := createCaaSClusterWithNodeSets(ctx, true)
		client := clusterOrderClient()
		orders := &osacv1alpha1.ClusterOrderList{}
		Eventually(func(g Gomega) {
			g.Expect(client.List(ctx, orders, crclient.InNamespace(connectedConfig.namespace), crclient.MatchingLabels{
				"osac.openshift.io/clusterorder-uuid": cluster.GetId(),
			})).To(Succeed())
			g.Expect(orders.Items).To(HaveLen(1))
		}, 90*time.Second, time.Second).Should(Succeed())
		Expect(orders.Items[0].Spec.NodeRequests).To(HaveLen(2))
		actual := map[string]int{}
		for _, request := range orders.Items[0].Spec.NodeRequests {
			Expect(request.BareMetal).NotTo(BeNil(), "no legacy resource-class/host-type fallback")
			actual[request.BareMetal.InstanceType] = request.NumberOfNodes
		}
		Expect(actual).To(HaveLen(2))
		for _, nodeSet := range cluster.GetSpec().GetNodeSets() {
			Expect(actual).To(HaveKeyWithValue(nodeSet.GetBaremetalInstanceType().GetName(), int(nodeSet.GetSize())))
		}
	})
	It("generates exactly one tenant-owned order for the real Cluster API object", func(ctx context.Context) {
		cluster := createCaaSCluster(ctx)
		client := clusterOrderClient()
		orders := &osacv1alpha1.ClusterOrderList{}
		Eventually(func(g Gomega) {
			err := client.List(ctx, orders, crclient.InNamespace(connectedConfig.namespace), crclient.MatchingLabels{
				"osac.openshift.io/clusterorder-uuid": cluster.GetId(),
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(orders.Items).To(HaveLen(1), "fulfillment must create the ClusterOrder, not the test")
		}, 90*time.Second, time.Second).Should(Succeed())
		order := &orders.Items[0]
		Expect(order.Annotations).To(HaveKeyWithValue("osac.openshift.io/tenant", cluster.GetMetadata().GetTenant()))
		Expect(order.Labels).To(HaveKeyWithValue("osac.openshift.io/clusterorder-uuid", cluster.GetId()))
		Expect(order.Spec.NodeRequests).To(HaveLen(1))
		Expect(order.Spec.NodeRequests[0].BareMetal).NotTo(BeNil())
		instanceType := cluster.GetSpec().GetNodeSets()["compute"].GetBaremetalInstanceType().GetName()
		Expect(order.Spec.NodeRequests[0].BareMetal.InstanceType).To(Equal(instanceType))
		Expect(order.Spec.NodeRequests[0].NumberOfNodes).To(Equal(1))
	})
})
