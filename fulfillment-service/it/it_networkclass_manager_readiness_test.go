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

var _ = Describe("NetworkClass manager readiness", func() {
	It("moves from PENDING to FAILED until all managers are registered, then reaches READY", func(ctx context.Context) {
		kubeClient := tool.KubeClient()
		const namespace = "osac"
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
				Title:         "Integration NetworkClass manager readiness",
				FabricManager: &fabricManagerName,
				K8SManager:    &k8sManagerName,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassID := createResponse.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			_, _ = networkClassesClient.Delete(cleanupCtx, privatev1.NetworkClassesDeleteRequest_builder{
				Id: networkClassID,
			}.Build())
			_ = kubeClient.Delete(cleanupCtx, fabricConfigMap)
			_ = kubeClient.Delete(cleanupCtx, k8sConfigMap)
		})

		Expect(createResponse.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING))

		By("registering the fabric manager while the Kubernetes manager is still missing")
		Expect(kubeClient.Create(ctx, fabricConfigMap)).To(Succeed())

		By("waiting for the NetworkClass to report the missing manager")
		Eventually(func(g Gomega) {
			getResponse, getErr := networkClassesClient.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
				Id: networkClassID,
			}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			object := getResponse.GetObject()
			g.Expect(object.GetStatus().GetState()).To(Equal(
				privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED))
			g.Expect(object.GetStatus().GetManagerState()).To(Equal(
				privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED))
			g.Expect(object.GetStatus().GetMessage()).To(ContainSubstring(k8sManagerName))
		}, 2*time.Minute, time.Second).Should(Succeed())

		By("registering the Kubernetes manager")
		Expect(kubeClient.Create(ctx, k8sConfigMap)).To(Succeed())

		By("waiting for the NetworkClass to recover with the capability intersection")
		Eventually(func(g Gomega) {
			getResponse, getErr := networkClassesClient.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
				Id: networkClassID,
			}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			object := getResponse.GetObject()
			g.Expect(object.GetStatus().GetState()).To(Equal(
				privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
			g.Expect(object.GetStatus().GetManagerState()).To(Equal(
				privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
			g.Expect(object.GetStatus().HasMessage()).To(BeFalse())
			g.Expect(object.GetStatus().HasManagerMessage()).To(BeFalse())
			g.Expect(object.GetCapabilities().GetSupportsIpv4()).To(BeTrue())
			g.Expect(object.GetCapabilities().GetSupportsIpv6()).To(BeFalse())
			g.Expect(object.GetCapabilities().GetSupportsDualStack()).To(BeFalse())
		}, 2*time.Minute, time.Second).Should(Succeed())
	})
})
