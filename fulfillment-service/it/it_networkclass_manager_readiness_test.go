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
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("NetworkClass manager readiness", func() {
	It("reaches READY after both configured managers are discovered", func(ctx context.Context) {
		kubeClient := tool.KubeClient()
		const namespace = "osac"

		By("waiting for the cudn_evpn manager registration")
		Eventually(func(g Gomega) {
			var configMaps corev1.ConfigMapList
			err := kubeClient.List(ctx, &configMaps,
				crclient.InNamespace(namespace),
				crclient.MatchingLabels{networkmanager.LabelK8sManager: "true"},
			)
			g.Expect(err).ToNot(HaveOccurred())

			var cudnEVPN *corev1.ConfigMap
			for i := range configMaps.Items {
				if configMaps.Items[i].Data["name"] == "cudn_evpn" {
					cudnEVPN = &configMaps.Items[i]
					break
				}
			}
			g.Expect(cudnEVPN).ToNot(BeNil())
			g.Expect(cudnEVPN.GetLabels()).To(HaveKeyWithValue(networkmanager.LabelK8sManager, "true"))
			g.Expect(cudnEVPN.Data["capabilities"]).To(Equal("ipv4"))
		}, time.Minute, time.Second).Should(Succeed())

		networkClassesClient := privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		createResponse, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: fmt.Sprintf("test-cudn-evpn-%s", uuid.New())}.Build(),
				Title:         "CUDN EVPN Network Class",
				FabricManager: new("netris"),
				K8SManager:    new("cudn_evpn"),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassID := createResponse.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = networkClassesClient.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{
				Id: networkClassID,
			}.Build())
		})

		Expect(createResponse.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING))

		By("waiting for the NetworkClass to become READY")
		Eventually(func(g Gomega) {
			getResponse, getErr := networkClassesClient.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
				Id: networkClassID,
			}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			g.Expect(getResponse.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
		}, time.Minute, time.Second).Should(Succeed())
	})
})
