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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("LVMS Volume lifecycle", Ordered, func() {
	BeforeAll(func() {
		// Only the Kubernetes resource contract is exercised; TopoLVM is simulated.
		crdOptions := envtest.CRDInstallOptions{CRDs: []*apiextensionsv1.CustomResourceDefinition{{
			ObjectMeta: metav1.ObjectMeta{Name: "logicalvolumes.topolvm.io"},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: logicalVolumeGroup,
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural: logicalVolumeResource, Singular: "logicalvolume", Kind: logicalVolumeKind,
				},
				Scope: apiextensionsv1.ClusterScoped,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
					Name: logicalVolumeVersion, Served: true, Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"spec": {Type: "object", Required: []string{"name", "nodeName", "size"},
								Properties: map[string]apiextensionsv1.JSONSchemaProps{
									"name": {Type: "string"}, "nodeName": {Type: "string"},
									"size": {Type: "string"}, "deviceClass": {Type: "string"},
								}},
							"status": {Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"volumeID": {Type: "string"}, "code": {Type: "integer"}, "message": {Type: "string"},
							}},
						},
					}},
					Subresources: &apiextensionsv1.CustomResourceSubresources{Status: &apiextensionsv1.CustomResourceSubresourceStatus{}},
				}},
			},
		}}}
		_, err := envtest.InstallCRDs(cfg, crdOptions)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(envtest.UninstallCRDs(cfg, crdOptions)).To(Succeed()) })
	})

	DescribeTable("persists generated identity and replaces a terminating LogicalVolume",
		func(accessMode v1alpha1.VolumeAccessMode) {
			testCtx := context.Background()
			volume := &v1alpha1.Volume{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "lvms-envtest-", Namespace: "default",
					Annotations: map[string]string{osacTenantKey: "tenant-a"},
					Finalizers:  []string{osacVolumeFinalizer},
				},
				Spec: v1alpha1.VolumeSpec{
					StorageTier: "local", SizeGiB: 1, AccessMode: accessMode,
					Topology: &v1alpha1.VolumeTopology{Segments: map[string]string{nodeTopologySegment: "worker-1"}},
				},
			}
			Expect(k8sClient.Create(testCtx, volume)).To(Succeed())
			key := client.ObjectKeyFromObject(volume)
			getVolume := func() *v1alpha1.Volume {
				current := &v1alpha1.Volume{}
				ExpectWithOffset(1, k8sClient.Get(testCtx, key, current)).To(Succeed())
				return current
			}
			getLogicalVolume := func(name string) *unstructured.Unstructured {
				current := &unstructured.Unstructured{}
				current.SetGroupVersionKind(logicalVolumeGVK)
				ExpectWithOffset(1, k8sClient.Get(testCtx, client.ObjectKey{Name: name}, current)).To(Succeed())
				return current
			}
			listLogicalVolumes := func() []unstructured.Unstructured {
				list := &unstructured.UnstructuredList{}
				list.SetGroupVersionKind(logicalVolumeGVK.GroupVersion().WithKind("LogicalVolumeList"))
				ExpectWithOffset(1, k8sClient.List(testCtx, list, client.MatchingLabels{logicalVolumeUUIDLabel: string(volume.UID)})).To(Succeed())
				return list.Items
			}
			DeferCleanup(func() {
				for _, item := range listLogicalVolumes() {
					item.SetFinalizers(nil)
					Expect(k8sClient.Update(testCtx, &item)).To(Succeed())
					Expect(client.IgnoreNotFound(k8sClient.Delete(testCtx, &item))).To(Succeed())
				}
				current := &v1alpha1.Volume{}
				if err := k8sClient.Get(testCtx, key, current); apierrors.IsNotFound(err) {
					return
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
				current.SetFinalizers(nil)
				Expect(k8sClient.Update(testCtx, current)).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(testCtx, current))).To(Succeed())
			})
			volume.Status.Provider = lvmsProvider
			volume.Status.Protocol = v1alpha1.VolumeProtocolBlock
			Expect(k8sClient.Status().Update(testCtx, volume)).To(Succeed())
			staleVolume := volume.DeepCopy()
			reconciler := &VolumeReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), VolumeNamespace: key.Namespace,
				mgr:                testMcManager,
				VendorProvisioners: VendorProvisionerRegistry{lvmsProvider: NewLvmsVendorProvisioner(k8sClient, k8sClient)},
			}
			reconcileVolume := func() {
				_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: key}})
				ExpectWithOffset(1, err).NotTo(HaveOccurred())
			}

			reconcileVolume()
			pending := getVolume()
			Expect(pending.Status.Phase).To(Equal(v1alpha1.VolumePhaseProgressing))
			originalName := pending.Status.VendorContext[logicalVolumeNameContextKey]
			Expect(originalName).To(HavePrefix("pvc-"))
			original := getLogicalVolume(originalName)
			Expect(original.GetGenerateName()).To(Equal("pvc-"))
			Expect(pending.Status.VendorContext[logicalVolumeResourceUIDContextKey]).To(Equal(string(original.GetUID())))
			Expect(pending.Status.VendorContext[logicalVolumeSourceUIDContextKey]).To(Equal(string(volume.UID)))
			Expect(original.GetAnnotations()[osacTenantKey]).To(Equal("tenant-a"))
			Expect(original.GetAnnotations()[logicalVolumeOwnerAnnotation]).To(Equal(volume.Name))
			_, hasDeviceClass, err := unstructured.NestedString(original.Object, "spec", "deviceClass")
			Expect(err).NotTo(HaveOccurred())
			Expect(hasDeviceClass).To(BeFalse())
			reconciler.Client = &staleVolumeClient{Client: k8sClient, volume: staleVolume}
			reconcileVolume()
			Expect(listLogicalVolumes()).To(HaveLen(1))
			Expect(getVolume().Status.VendorContext[logicalVolumeNameContextKey]).To(Equal(originalName))
			reconciler.Client = k8sClient

			original.SetFinalizers([]string{"topolvm.io/logicalvolume"})
			Expect(k8sClient.Update(testCtx, original)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, original)).To(Succeed())
			reconcileVolume()
			Expect(getVolume().Status.VendorContext).To(BeEmpty())
			reconcileVolume()
			replacementName := getVolume().Status.VendorContext[logicalVolumeNameContextKey]
			Expect(replacementName).NotTo(Equal(originalName))
			replacement := getLogicalVolume(replacementName)
			Expect(replacement.GetUID()).NotTo(Equal(original.GetUID()))
			Expect(listLogicalVolumes()).To(HaveLen(2))
			Expect(getLogicalVolume(originalName).GetDeletionTimestamp().IsZero()).To(BeFalse())

			Expect(unstructured.SetNestedField(replacement.Object, string(replacement.GetUID()), "status", "volumeID")).To(Succeed())
			Expect(k8sClient.Status().Update(testCtx, replacement)).To(Succeed())
			reconcileVolume()
			ready := getVolume()
			Expect(ready.Status.Phase).To(Equal(v1alpha1.VolumePhaseReady))
			Expect(ready.Status.VendorVolumeID).To(Equal(string(replacement.GetUID())))

			Expect(k8sClient.Delete(testCtx, ready)).To(Succeed())
			reconcileVolume()
			reconcileVolume()
			Expect(getVolume().Status.Phase).To(Equal(v1alpha1.VolumePhaseDeleted))
			Expect(k8sClient.Get(testCtx, client.ObjectKey{Name: replacementName}, replacement)).To(Satisfy(apierrors.IsNotFound))
			Expect(getLogicalVolume(originalName).GetUID()).To(Equal(original.GetUID()))
			reconcileVolume()
			Expect(k8sClient.Get(testCtx, key, &v1alpha1.Volume{})).To(Satisfy(apierrors.IsNotFound))
		},
		Entry("ReadWriteOnce", v1alpha1.VolumeAccessModeReadWriteOnce),
		Entry("ReadWriteOncePod", v1alpha1.VolumeAccessModeReadWriteOncePod),
	)
})
