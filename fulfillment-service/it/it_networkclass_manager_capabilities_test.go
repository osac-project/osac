/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("NetworkClass manager capability discovery", func() {
	It("updates capabilities when manager registrations appear", func(ctx context.Context) {
		const namespace = "osac"
		kubeClient := tool.KubeClient()
		networkClassesClient := privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())

		suffix := uuid.New()
		fabricManagerName := fmt.Sprintf("it-fabric-%s", suffix)
		k8sManagerName := fmt.Sprintf("it-k8s-%s", suffix)
		fabricConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("it-fabric-manager-%s", suffix),
				Namespace: namespace,
				Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
			},
			Data: map[string]string{
				"name":         fabricManagerName,
				"capabilities": "ipv4,ipv6,dualStack",
			},
		}
		k8sConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("it-k8s-manager-%s", suffix),
				Namespace: namespace,
				Labels:    map[string]string{networkmanager.LabelK8sManager: "true"},
			},
			Data: map[string]string{
				"name":         k8sManagerName,
				"capabilities": "ipv4",
			},
		}

		By("creating a NetworkClass before its manager registrations exist")
		createResponse, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: fmt.Sprintf("it-network-class-%s", suffix)}.Build(),
				Title:         "Integration Network Class manager discovery",
				FabricManager: &fabricManagerName,
				K8SManager:    &k8sManagerName,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassID := createResponse.GetObject().GetId()

		DeferCleanup(func(cleanupCtx context.Context) {
			_, deleteErr := networkClassesClient.Delete(cleanupCtx, privatev1.NetworkClassesDeleteRequest_builder{
				Id: networkClassID,
			}.Build())
			if deleteErr != nil {
				GinkgoT().Logf("cleanup: failed to delete NetworkClass %s: %v", networkClassID, deleteErr)
			}
			for _, configMap := range []*corev1.ConfigMap{fabricConfigMap, k8sConfigMap} {
				if deleteErr := kubeClient.Delete(cleanupCtx, configMap); deleteErr != nil {
					GinkgoT().Logf("cleanup: failed to delete ConfigMap %s/%s: %v", configMap.Namespace, configMap.Name, deleteErr)
				}
			}
		})

		By("adding fabric and Kubernetes manager registrations")
		Expect(kubeClient.Create(ctx, fabricConfigMap)).To(Succeed())
		Expect(kubeClient.Create(ctx, k8sConfigMap)).To(Succeed())

		By("waiting for the deployed controller to persist the capability intersection")
		Eventually(func(g Gomega) {
			getResponse, getErr := networkClassesClient.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
				Id: networkClassID,
			}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			capabilities := getResponse.GetObject().GetCapabilities()
			g.Expect(capabilities.GetSupportsIpv4()).To(BeTrue())
			g.Expect(capabilities.GetSupportsIpv6()).To(BeFalse())
			g.Expect(capabilities.GetSupportsDualStack()).To(BeFalse())
		}, 2*time.Minute, time.Second).Should(Succeed())
	})
})
