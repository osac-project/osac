/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// diskImageSourceRef is the source URL carried by the fake DiskImage. The reconciler validates
// the ClusterVersion's DiskImage reference via GetDiskImage and passes the canonical reference
// to fulfillment-service, which resolves the source URL when it creates the provider CR.
const diskImageSourceRef = "oci://registry.example.com/rhcos:4.18"

func newInstanceType(name string, fabricPort string, extraPorts ...*privatev1.BareMetalNetworkPortSpec) *privatev1.BareMetalInstanceType {
	ports := append([]*privatev1.BareMetalNetworkPortSpec{
		privatev1.BareMetalNetworkPortSpec_builder{
			Name: fabricPort, Role: "fabric", Type: "Ethernet", Speed: "25Gbps",
		}.Build(),
	}, extraPorts...)
	return privatev1.BareMetalInstanceType_builder{
		Metadata: privatev1.Metadata_builder{Name: name}.Build(),
		Spec: privatev1.BareMetalInstanceTypeSpec_builder{
			Hardware: privatev1.BareMetalHardwareSpec_builder{
				Cpu:          privatev1.BareMetalCPUSpec_builder{Cores: 64, Architecture: "x86_64", ThreadsPerCore: 2}.Build(),
				Memory:       privatev1.BareMetalMemorySpec_builder{TotalGb: 256}.Build(),
				NetworkPorts: ports,
			}.Build(),
			HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
				MatchLabels: map[string]string{"type": name},
			}.Build(),
		}.Build(),
	}.Build()
}

var _ = Describe("BareMetalWorkerReconciler ensureInfraEnv", Label("baremetalworker"), func() {
	var (
		fc  *workerFulfillmentStub
		ign *workerIgnitionEndpoint
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {

		fc = newWorkerFulfillmentStub()
		ign = newWorkerIgnitionEndpoint()
		rec = events.NewFakeRecorder(10)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	const (
		clusterUUID    = "test-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("bm-standard", "data-0"))
	}

	newBareMetalClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNamespace,
				Labels:      map[string]string{clusterIDLabel: clusterUUID},
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{NodeSet: "bm-standard",
					NumberOfNodes: 1,
					BareMetal: &osacv1alpha1.BareMetalNodeSpec{
						InstanceType: "bm-standard",
					},
				}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
	}

	runReconcile := func(name string) (reconcile.Result, error) {
		return driveWorkerCheckpoints(r, fc, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
	}

	getClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		GinkgoHelper()
		co := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, co)).To(Succeed())
		return co
	}

	create := func(co *osacv1alpha1.ClusterOrder) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(co),
				})
			}
			ie := newInfraEnv(co.Name + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})
	}

	It("creates an InfraEnv (late binding, owned by the ClusterOrder) and fetches its ignition [green]", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-green")
		create(co)

		// First reconcile: InfraEnv is created, ignition not ready yet -> requeue.
		res, err := runReconcile("bmw-green")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))

		ie := newInfraEnv("bmw-green-infraenv")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ie), ie)).To(Succeed())
		// Late binding: no clusterRef.
		_, hasClusterRef, _ := unstructured.NestedMap(ie.Object, "spec", "clusterRef")
		Expect(hasClusterRef).To(BeFalse())
		// Owned by the ClusterOrder.
		owners := ie.GetOwnerReferences()
		Expect(owners).To(HaveLen(1))
		Expect(owners[0].Kind).To(Equal("ClusterOrder"))
		Expect(owners[0].Name).To(Equal("bmw-green"))
		// pull secret referenced by convention.
		psName, _, _ := unstructured.NestedString(ie.Object, "spec", "pullSecretRef", "name")
		Expect(psName).To(Equal("bmw-green-pull-secret"))

		// InfraEnvReady is False while ignition is pending.
		cond := apimeta.FindStatusCondition(getClusterOrder("bmw-green").Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)
		Expect(cond).ToNot(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))

		// Fixture makes the discovery ignition available at the fake endpoint.
		Expect(setWorkerIgnitionURL(ctx, "bmw-green-infraenv", testNamespace, ign.URL())).To(Succeed())

		// Second reconcile: ignition fetched, InfraEnvReady=True, requeues for agent correlation.
		res, err = runReconcile("bmw-green")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0), "requeues for agent correlation")

		Expect(apimeta.IsStatusConditionTrue(
			getClusterOrder("bmw-green").Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)).To(BeTrue())
	})

	It("is idempotent: re-reconcile does not create a duplicate InfraEnv", func() {
		preloadDiskImageChain()
		create(newBareMetalClusterOrder("bmw-idem"))

		_, err := runReconcile("bmw-idem")
		Expect(err).ToNot(HaveOccurred())
		_, err = runReconcile("bmw-idem")
		Expect(err).ToNot(HaveOccurred())

		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(infraEnvGVK)
		Expect(k8sClient.List(ctx, list, client.InNamespace(testNamespace))).To(Succeed())
		count := 0
		for i := range list.Items {
			if list.Items[i].GetName() == "bmw-idem-infraenv" {
				count++
			}
		}
		Expect(count).To(Equal(1))
	})

	It("emits DiscoveryIgnitionSizeWarning when ignition exceeds 48KB", func() {
		preloadDiskImageChain()
		create(newBareMetalClusterOrder("bmw-big"))

		_, err := runReconcile("bmw-big")
		Expect(err).ToNot(HaveOccurred())

		ign.SetSize(50 * 1024) // above the 48KB warning threshold
		Expect(setWorkerIgnitionURL(ctx, "bmw-big-infraenv", testNamespace, ign.URL())).To(Succeed())

		_, err = runReconcile("bmw-big")
		Expect(err).ToNot(HaveOccurred())

		var events []string
		for done := false; !done; {
			select {
			case e := <-rec.Events:
				events = append(events, e)
			default:
				done = true
			}
		}
		Expect(events).To(ContainElement(ContainSubstring("DiscoveryIgnitionSizeWarning")))
	})

	It("reports pending evidence when boot artifacts disappear and records a recreated UID without a Ready transition", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-r07-e1")
		create(co)

		// Provision one worker so no further create is due, and let the recorded
		// Ready claim settle.
		_, err := runReconcile("bmw-r07-e1")
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, "bmw-r07-e1-infraenv", testNamespace, ign.URL())).To(Succeed())
		for range 3 {
			_, err = runReconcile("bmw-r07-e1")
			Expect(err).ToNot(HaveOccurred())
		}
		co = getClusterOrder("bmw-r07-e1")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].BareMetalInstance.ID).NotTo(BeEmpty())
		Expect(apimeta.IsStatusConditionTrue(
			co.Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)).To(BeTrue())
		storedUID := co.Annotations["osac.openshift.io/infraenv-uid"]
		Expect(storedUID).NotTo(BeEmpty())

		// The boot artifacts disappear while the recorded claim is still Ready and no
		// create needs the ignition: the condition follows current evidence instead of
		// holding an unverified success.
		ie := newInfraEnv("bmw-r07-e1-infraenv")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ie), ie)).To(Succeed())
		unstructured.RemoveNestedField(ie.Object, "status", "bootArtifacts", "discoveryIgnitionURL")
		Expect(k8sClient.Update(ctx, ie)).To(Succeed())
		creates := len(fc.CreateCalls())
		_, err = runReconcile("bmw-r07-e1")
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-r07-e1")
		cond := apimeta.FindStatusCondition(co.Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)
		Expect(cond).ToNot(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("IgnitionPending"))
		Expect(co.Annotations["osac.openshift.io/infraenv-uid"]).To(Equal(storedUID),
			"clearing evidence is not a recreation")
		Expect(fc.CreateCalls()).To(HaveLen(creates), "missing boot artifacts never authorize a BMI create")

		// Recreating the object advances the recorded UID while the condition stays
		// False: the lookup and UID evidence are never authorized by a condition
		// transition.
		Expect(k8sClient.Delete(ctx, ie)).To(Succeed())
		_, err = runReconcile("bmw-r07-e1")
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, "bmw-r07-e1-infraenv", testNamespace, ign.URL())).To(Succeed())
		_, err = runReconcile("bmw-r07-e1")
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-r07-e1")
		Expect(co.Status.Workers[0].Phase).To(Equal("Failed"), "a waiting worker on stale ignition is failed, not replaced")
		Expect(co.Status.Workers[0].LastFailureReason).To(Equal("AgentRegistrationTimeout"))
		_, err = runReconcile("bmw-r07-e1")
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-r07-e1")
		recreated := newInfraEnv("bmw-r07-e1-infraenv")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(recreated), recreated)).To(Succeed())
		Expect(string(recreated.GetUID())).NotTo(Equal(storedUID))
		Expect(co.Annotations["osac.openshift.io/infraenv-uid"]).To(Equal(string(recreated.GetUID())))
		cond = apimeta.FindStatusCondition(co.Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)
		Expect(cond).ToNot(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue),
			"readiness follows the recreated object's published artifact without needing a create")
		Expect(fc.CreateCalls()).To(HaveLen(creates), "no BMI is created while the stale worker waits for its retry")
	})

	It("ignores ClusterOrders without a bare-metal node set", func() {
		co := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "bmw-none", Namespace: testNamespace},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				NodeRequests: nil,
			},
		}
		create(co)

		res, err := runReconcile("bmw-none")
		Expect(err).ToNot(HaveOccurred())
		Expect(res).To(Equal(reconcile.Result{}))

		ie := newInfraEnv("bmw-none-infraenv")
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(ie), ie)
		Expect(err).To(HaveOccurred(), "no InfraEnv should be created for a non-bare-metal ClusterOrder")
	})

	It("skips a ClusterOrder annotated management-state=unmanaged", func() {
		co := newBareMetalClusterOrder("bmw-unmanaged")
		co.Annotations = map[string]string{"osac.openshift.io/management-state": "unmanaged"}
		create(co)

		res, err := runReconcile("bmw-unmanaged")
		Expect(err).ToNot(HaveOccurred())
		Expect(res).To(Equal(reconcile.Result{}))

		ie := newInfraEnv("bmw-unmanaged-infraenv")
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(ie), ie)
		Expect(err).To(HaveOccurred(), "an unmanaged ClusterOrder must be skipped (no InfraEnv)")
	})

	It("returns an error when the discovery ignition fetch fails", func() {
		preloadDiskImageChain()
		create(newBareMetalClusterOrder("bmw-fetcherr"))

		_, err := runReconcile("bmw-fetcherr")
		Expect(err).ToNot(HaveOccurred())

		// Point the InfraEnv at an unreachable ignition URL.
		Expect(setWorkerIgnitionURL(ctx, "bmw-fetcherr-infraenv", testNamespace,
			"http://127.0.0.1:0/discovery.ign")).To(Succeed())

		_, err = runReconcile("bmw-fetcherr")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("BareMetalWorkerReconciler direct shared worker template", Label("baremetalworker"), func() {
	var (
		fc *workerFulfillmentStub

		ign *workerIgnitionEndpoint
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {
		fc = newWorkerFulfillmentStub()

		ign = newWorkerIgnitionEndpoint()
		rec = events.NewFakeRecorder(10)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	newBareMetalClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNamespace,
				Labels:      map[string]string{"osac.openshift.io/clusterorder-uuid": "ci-cluster"},
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID: "test",
				PullSecret: "{\"auths\":{}}",
				NodeRequests: []osacv1alpha1.NodeRequest{{NodeSet: "bm-standard",
					NumberOfNodes: 1,
					BareMetal: &osacv1alpha1.BareMetalNodeSpec{
						InstanceType: "bm-standard",
					},
				}},
			},
		}
	}

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       "ci-cluster",
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: "ci-version"}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: "ci-version",
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: "ci-disk-image"}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: "ci-disk-image",
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("bm-standard", "data-0"))
	}

	runReconcile := func(name string) (reconcile.Result, error) {
		return driveWorkerCheckpoints(r, fc, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
	}

	create := func(co *osacv1alpha1.ClusterOrder) {
		GinkgoHelper()
		preloadDiskImageChain()
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(co),
				})
			}
			ie := newInfraEnv(co.Name + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})
	}

	makeInfraEnvReady := func(name string) {
		GinkgoHelper()
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
	}

	It("creates a worker directly from the shared template without a system catalog item", func() {
		co := newBareMetalClusterOrder("bmw-ci-create")
		create(co)
		makeInfraEnvReady("bmw-ci-create")

		_, err := runReconcile("bmw-ci-create")
		Expect(err).ToNot(HaveOccurred())

		Expect(fc.CreateCatalogItemCalls()).To(BeEmpty())
		Expect(fc.CreateCalls()).To(HaveLen(1))
		Expect(fc.CreateCalls()[0].GetMetadata().GetTenant()).To(Equal("tenant1"))
		Expect(fc.CreateCalls()[0].GetSpec().GetCatalogItem()).To(BeNil())
		Expect(fc.CreateCalls()[0].GetSpec().GetTemplate().GetId()).To(Equal("osac.templates.bm_host_provisioning"))
		Expect(fc.CreateCalls()[0].GetSpec().GetTemplate().GetShared()).To(BeTrue())
	})

	It("ignores a pre-existing system catalog item", func() {
		co := newBareMetalClusterOrder("bmw-ci-noop")
		create(co)
		makeInfraEnvReady("bmw-ci-noop")

		// Pre-create the catalog item.
		_, err := fc.CreateBareMetalInstanceCatalogItem(ctx, privatev1.BareMetalInstanceCatalogItem_builder{
			Metadata: privatev1.Metadata_builder{Name: "system-bmi-passthrough"}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = runReconcile("bmw-ci-noop")
		Expect(err).ToNot(HaveOccurred())

		// The operator must not use or recreate the obsolete passthrough item.
		Expect(fc.CreateCatalogItemCalls()).To(HaveLen(1))
		Expect(fc.ListCatalogItemCalls()).To(BeEmpty())
		Expect(fc.CreateCalls()).To(HaveLen(1))
		Expect(fc.CreateCalls()[0].GetSpec().GetCatalogItem()).To(BeNil())
	})

	It("does not recreate a system catalog item on repeated reconciliation", func() {
		co := newBareMetalClusterOrder("bmw-ci-race")
		create(co)
		makeInfraEnvReady("bmw-ci-race")

		_, err := runReconcile("bmw-ci-race")
		Expect(err).ToNot(HaveOccurred())
		_, err = runReconcile("bmw-ci-race")
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.CreateCatalogItemCalls()).To(BeEmpty())
		Expect(fc.CreateCalls()).To(HaveLen(1))
	})
})

var _ = Describe("BareMetalWorkerReconciler resolveDiskImage", Label("baremetalworker"), func() {
	const (
		clusterUUID    = "resolve-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		fc  *workerFulfillmentStub
		ign *workerIgnitionEndpoint
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {

		fc = newWorkerFulfillmentStub()
		ign = newWorkerIgnitionEndpoint()
		rec = events.NewFakeRecorder(10)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	newBareMetalClusterOrder := func(name string, labels map[string]string) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNamespace,
				Labels:      labels,
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{NodeSet: "bm-standard",
					NumberOfNodes: 1,
					BareMetal: &osacv1alpha1.BareMetalNodeSpec{
						InstanceType: "bm-standard",
					},
				}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
	}

	runReconcile := func(name string) (reconcile.Result, error) {
		return driveWorkerCheckpoints(r, fc, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
	}

	getClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		GinkgoHelper()
		co := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, co)).To(Succeed())
		return co
	}

	create := func(co *osacv1alpha1.ClusterOrder) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(co),
				})
			}
			ie := newInfraEnv(co.Name + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})
	}

	makeInfraEnvReady := func(name string) {
		GinkgoHelper()
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
		_, err = runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(apimeta.IsStatusConditionTrue(
			getClusterOrder(name).Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)).To(BeTrue())
	}

	addInstanceType := func(name string) {
		fc.AddBareMetalInstanceType(newInstanceType(name, "data-0"))
	}

	It("resolves DiskImage ID from a ClusterVersion with disk_image set", func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		addInstanceType("bm-standard")

		co := newBareMetalClusterOrder("bmw-resolve", map[string]string{clusterIDLabel: clusterUUID})
		create(co)
		makeInfraEnvReady("bmw-resolve")

		_, err := runReconcile("bmw-resolve")
		Expect(err).ToNot(HaveOccurred())

		cond := apimeta.FindStatusCondition(
			getClusterOrder("bmw-resolve").Status.Conditions, osacv1alpha1.ConditionRHCOSImageNotFound)
		Expect(cond).To(BeNil(), "RHCOSImageNotFound should not be set when disk_image is present")
	})

	It("sets RHCOSImageNotFound when ClusterVersion has no disk_image", func() {
		addInstanceType("bm-standard")
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id:   cvID,
			Spec: privatev1.ClusterVersionSpec_builder{}.Build(),
		}.Build())

		co := newBareMetalClusterOrder("bmw-nodisk", map[string]string{clusterIDLabel: clusterUUID})
		create(co)
		makeInfraEnvReady("bmw-nodisk")

		res, err := runReconcile("bmw-nodisk")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		Expect(fc.CreateCalls()).To(BeEmpty())

		cond := apimeta.FindStatusCondition(
			getClusterOrder("bmw-nodisk").Status.Conditions, osacv1alpha1.ConditionRHCOSImageNotFound)
		Expect(cond).ToNot(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	})

	It("fails closed when the clusterorder-uuid label is absent", func() {
		co := newBareMetalClusterOrder("bmw-nolabel", nil)
		create(co)

		_, err := runReconcile("bmw-nolabel")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing Cluster ID"))
		Expect(fc.CreateCalls()).To(BeEmpty())
	})

	It("clears RHCOSImageNotFound when disk_image is later set on the ClusterVersion", func() {
		addInstanceType("bm-standard")
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id:   cvID,
			Spec: privatev1.ClusterVersionSpec_builder{}.Build(),
		}.Build())

		co := newBareMetalClusterOrder("bmw-clear", map[string]string{clusterIDLabel: clusterUUID})
		create(co)
		makeInfraEnvReady("bmw-clear")

		_, err := runReconcile("bmw-clear")
		Expect(err).ToNot(HaveOccurred())
		cond := apimeta.FindStatusCondition(
			getClusterOrder("bmw-clear").Status.Conditions, osacv1alpha1.ConditionRHCOSImageNotFound)
		Expect(cond).ToNot(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))

		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())

		_, err = runReconcile("bmw-clear")
		Expect(err).ToNot(HaveOccurred())

		cond = apimeta.FindStatusCondition(
			getClusterOrder("bmw-clear").Status.Conditions, osacv1alpha1.ConditionRHCOSImageNotFound)
		Expect(cond).ToNot(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
	})
})

var _ = Describe("BareMetalWorkerReconciler reconcileWorkers", Label("baremetalworker"), func() {
	const (
		clusterUUID    = "workers-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		fc  *workerFulfillmentStub
		ign *workerIgnitionEndpoint
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {

		fc = newWorkerFulfillmentStub()
		ign = newWorkerIgnitionEndpoint()
		rec = events.NewFakeRecorder(10)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	addInstanceType := func(name, fabricPort string) {
		mgmt := privatev1.BareMetalNetworkPortSpec_builder{
			Name: "mgmt-0", Role: "management", Type: "Ethernet", Speed: "1Gbps",
		}.Build()
		fc.AddBareMetalInstanceType(newInstanceType(name, fabricPort, mgmt))
	}

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		addInstanceType("bm-standard", "data-0")
	}

	newBareMetalClusterOrder := func(name string, numWorkers int) *osacv1alpha1.ClusterOrder {
		co := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNamespace,
				Labels:      map[string]string{clusterIDLabel: clusterUUID},
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{NodeSet: "bm-standard",
					NumberOfNodes: numWorkers,
					BareMetal: &osacv1alpha1.BareMetalNodeSpec{
						InstanceType: "bm-standard",
					},
				}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
		return co
	}

	runReconcile := func(name string) (reconcile.Result, error) {
		return driveWorkerCheckpoints(r, fc, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
	}

	getClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		GinkgoHelper()
		co := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, co)).To(Succeed())
		return co
	}

	create := func(co *osacv1alpha1.ClusterOrder) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(co),
				})
			}
			ie := newInfraEnv(co.Name + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})
	}

	makeInfraEnvReady := func(name string) {
		GinkgoHelper()
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
		_, err = runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(apimeta.IsStatusConditionTrue(
			getClusterOrder(name).Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)).To(BeTrue())
	}

	It("creates a tenant-owned fulfillment worker BMI with owner annotation", func() {
		preloadDiskImageChain()
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		co := newBareMetalClusterOrder("bmw-tenant-owned", 1)
		co.Annotations = map[string]string{"osac.openshift.io/tenant": "tenant1"}
		create(co)
		makeInfraEnvReady(co.Name)

		_, err := runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.CreateCalls()).To(HaveLen(1))
		bmi := fc.CreateCalls()[0]
		Expect(bmi.GetMetadata().GetTenant()).To(Equal("tenant1"))
		Expect(bmi.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(
			"osac.openshift.io/owner-reference", "ClusterOrder/bmw-tenant-owned"))
		Expect(bmi.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/cluster-order", co.Name))
		Expect(bmi.GetSpec().GetCatalogItem()).To(BeNil())
		Expect(bmi.GetSpec().GetTemplate().GetId()).To(Equal("osac.templates.bm_host_provisioning"))
		Expect(bmi.GetSpec().GetTemplate().GetShared()).To(BeTrue())
		Expect(bmi.GetSpec().GetInstanceType().GetShared()).To(BeTrue())
		Expect(fc.CreateCatalogItemCalls()).To(BeEmpty())
	})

	It("creates BMIs with correct fields for a single-worker node set", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-create", 1)
		create(co)
		makeInfraEnvReady("bmw-create")

		_, err := runReconcile("bmw-create")
		Expect(err).ToNot(HaveOccurred())

		calls := fc.CreateCalls()
		Expect(calls).To(HaveLen(1))

		bmi := calls[0]
		Expect(bmi.GetMetadata().GetTenant()).To(Equal("tenant1"))
		Expect(bmi.GetMetadata().GetName()).To(Equal(getClusterOrder("bmw-create").Status.Workers[0].BareMetalInstance.Name))
		Expect(bmi.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/cluster-order", "bmw-create"))
		Expect(bmi.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(
			"osac.openshift.io/owner-reference", "ClusterOrder/bmw-create"))
		Expect(bmi.GetSpec().GetCatalogItem()).To(BeNil())
		Expect(bmi.GetSpec().GetTemplate().GetShared()).To(BeTrue())
		Expect(bmi.GetSpec().GetInstanceType().GetShared()).To(BeTrue())
		Expect(bmi.GetSpec().GetDiskImage().GetId()).To(Equal(diskImageID))
		Expect(bmi.GetSpec().GetInstanceType().GetName()).To(Equal("bm-standard"))

		userData := bmi.GetSpec().GetUserData()
		Expect(userData).ToNot(BeEmpty())
		Expect(json.Valid([]byte(userData))).To(BeTrue())

		netAttachments := bmi.GetSpec().GetNetworkAttachments()
		Expect(netAttachments).To(HaveLen(1))
		Expect(netAttachments[0].GetSubnet().GetName()).To(Equal("my-subnet"))
		Expect(netAttachments[0].GetSecurityGroups()).To(HaveLen(1))
		Expect(netAttachments[0].GetSecurityGroups()[0].GetName()).To(Equal("sg-default"))
	})

	It("does not create catalog item or BMI when the requested BMIT is not found", func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())

		co := newBareMetalClusterOrder("bmw-bmit-missing", 1)
		create(co)

		// InfraEnv readiness must not create the system catalog item before BMIT resolution.
		res, err := runReconcile("bmw-bmit-missing")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		Expect(fc.CreateCatalogItemCalls()).To(BeEmpty())

		Expect(setWorkerIgnitionURL(ctx, "bmw-bmit-missing-infraenv", testNamespace, ign.URL())).To(Succeed())

		// The missing requested BMIT keeps the order on the blocked/requeue path.
		res, err = runReconcile("bmw-bmit-missing")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		Expect(fc.CreateCatalogItemCalls()).To(BeEmpty())
		Expect(fc.CreateCalls()).To(BeEmpty())
		pending := getClusterOrder("bmw-bmit-missing").Status.Workers
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].BareMetalInstance.ID).To(BeEmpty())
		Expect(pending[0].Phase).To(Equal("Provisioning"))
	})

	It("creates a BMI without a catalog item after resolving a usable BMIT", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-bmit-usable", 1)
		create(co)
		makeInfraEnvReady("bmw-bmit-usable")

		_, err := runReconcile("bmw-bmit-usable")
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.CreateCatalogItemCalls()).To(BeEmpty())
		Expect(fc.CreateCalls()).To(HaveLen(1))
		Expect(getClusterOrder("bmw-bmit-usable").Status.Workers).To(HaveLen(1))
	})

	It("resolves a name-only DiskImage reference by name (id-or-name, mirroring ComputeInstance)", func() {
		// The ClusterVersion's disk_image reference carries only a name (no id). The reconciler
		// must resolve it the same way the ComputeInstance path does (OSAC-3724 / RefKeyStr:
		// prefer id, fall back to name), so the BMI still lands the resolved OCI ref.
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Name: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		addInstanceType("bm-standard", "data-0")

		co := newBareMetalClusterOrder("bmw-nameref", 1)
		create(co)
		makeInfraEnvReady("bmw-nameref")

		_, err := runReconcile("bmw-nameref")
		Expect(err).ToNot(HaveOccurred())

		calls := fc.CreateCalls()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].GetSpec().GetDiskImage().GetId()).To(Equal(diskImageID))
		Expect(fc.GetDiskImageCalls()).To(ContainElement(diskImageID))
	})

	It("emits a WorkerCreated event when a new worker BMI is created", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-event", 1)
		create(co)
		makeInfraEnvReady("bmw-event")

		_, err := runReconcile("bmw-event")
		Expect(err).ToNot(HaveOccurred())

		var events []string
		for done := false; !done; {
			select {
			case e := <-rec.Events:
				events = append(events, e)
			default:
				done = true
			}
		}
		Expect(events).To(ContainElement(SatisfyAll(
			ContainSubstring("WorkerCreated"),
			ContainSubstring(getClusterOrder("bmw-event").Status.Workers[0].BareMetalInstance.Name))))
	})

	It("creates N BMIs for a multi-worker node set", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-multi", 3)
		create(co)
		makeInfraEnvReady("bmw-multi")

		_, err := runReconcile("bmw-multi")
		Expect(err).ToNot(HaveOccurred())

		calls := fc.CreateCalls()
		Expect(calls).To(HaveLen(3))
		for i, call := range calls {
			Expect(call.GetMetadata().GetName()).To(Equal(getClusterOrder("bmw-multi").Status.Workers[i].BareMetalInstance.Name))
		}

		co = getClusterOrder("bmw-multi")
		Expect(co.Status.Workers).To(HaveLen(3))
	})

	It("skips creation when BMI already exists (list-before-create)", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-idem", 1)
		create(co)

		// Pre-create the BMI in the fake BEFORE makeInfraEnvReady, because the second
		// reconcile in makeInfraEnvReady reaches reconcileWorkers.
		_, err := fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant:      "tenant1",
				Name:        "bmw-idem-worker-0",
				Labels:      map[string]string{"osac.openshift.io/cluster-order": "bmw-idem"},
				Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/bmw-idem"},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		reserveExistingWorker(co, "bmw-idem-worker-0")
		makeInfraEnvReady("bmw-idem")

		// makeInfraEnvReady's second reconcile and subsequent reconciles all skip
		// creation because list-before-create finds the pre-existing BMI.
		createCallsBefore := len(fc.CreateCalls())

		_, err = runReconcile("bmw-idem")
		Expect(err).ToNot(HaveOccurred())

		Expect(fc.CreateCalls()).To(HaveLen(createCallsBefore))

		co = getClusterOrder("bmw-idem")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Name).To(Equal("bmw-idem-worker-0"))
		Expect(co.Status.Workers[0].Phase).To(Equal("WaitingForAgent"))
	})

	It("does not create a catalog item when an owned BMI already exists", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-catalog-heal", 1)
		create(co)

		// Pre-create the tenant-owned BMI; no catalog item should be needed.
		_, err := fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant:      "tenant1",
				Name:        "bmw-catalog-heal-worker-0",
				Labels:      map[string]string{"osac.openshift.io/cluster-order": "bmw-catalog-heal"},
				Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/bmw-catalog-heal"},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		reserveExistingWorker(co, "bmw-catalog-heal-worker-0")
		makeInfraEnvReady("bmw-catalog-heal")

		Expect(fc.CreateCatalogItemCalls()).To(BeEmpty())
		Expect(fc.CreateCalls()).To(HaveLen(1), "the existing BMI must not be recreated")
	})

	It("handles AlreadyExists by re-listing and recording the existing BMI", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-exists", 1)
		create(co)
		makeInfraEnvReady("bmw-exists")

		// First reconcile creates the BMI, second finds it via list (idempotent re-reconcile).
		_, err := runReconcile("bmw-exists")
		Expect(err).ToNot(HaveOccurred())

		// First reconcile created it.
		Expect(fc.CreateCalls()).To(HaveLen(1))

		// Second reconcile — list finds it, no new create call.
		_, err = runReconcile("bmw-exists")
		Expect(err).ToNot(HaveOccurred())

		// Still only 1 create call total — second reconcile used list-before-create.
		Expect(fc.CreateCalls()).To(HaveLen(1))

		co = getClusterOrder("bmw-exists")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].BareMetalInstance.ID).ToNot(BeEmpty())
	})

	It("persists opaque BMI references and resumes an interrupted create", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-reference-resume", 1)
		create(co)
		fc.SetCreateError(fmt.Errorf("interrupted create"))
		_, err := runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, co.Name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
		_, err = runReconcile(co.Name)
		Expect(err).To(HaveOccurred())
		co = getClusterOrder(co.Name)
		Expect(co.Status.Workers).To(HaveLen(1))
		reserved := co.Status.Workers[0].BareMetalInstance
		Expect(reserved.Name).To(HaveLen(36))
		Expect(reserved.Name).ToNot(ContainSubstring(co.Name))
		Expect(reserved.ID).To(BeEmpty())
		Expect(co.Status.Workers[0].Phase).To(Equal("Provisioning"))
		fc.SetCreateError(nil)
		_, err = runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder(co.Name)
		Expect(co.Status.Workers[0].BareMetalInstance.Name).To(Equal(reserved.Name))
		Expect(co.Status.Workers[0].BareMetalInstance.ID).ToNot(BeEmpty())
		for _, call := range fc.CreateCalls() {
			Expect(call.GetMetadata().GetName()).To(Equal(reserved.Name))
		}
		_, err = runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.CreateCalls()).To(HaveLen(2), "retry uses the reservation and later reconciles do not create again")
	})

	It("keeps same-hardware NodeSets independent when reordered or scaled", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-reference-membership", 1)
		co.Spec.NodeRequests[0].NodeSet = "compute"
		co.Spec.NodeRequests = append(co.Spec.NodeRequests, osacv1alpha1.NodeRequest{NodeSet: "batch",
			NumberOfNodes: 1, BareMetal: &osacv1alpha1.BareMetalNodeSpec{InstanceType: "bm-standard"},
		})
		create(co)
		makeInfraEnvReady(co.Name)
		co = getClusterOrder(co.Name)
		Expect(co.Status.Workers).To(HaveLen(2))
		original := append([]osacv1alpha1.WorkerStatus(nil), co.Status.Workers...)
		co.Spec.NodeRequests[0].NumberOfNodes = 2
		co.Spec.NodeRequests[0], co.Spec.NodeRequests[1] = co.Spec.NodeRequests[1], co.Spec.NodeRequests[0]
		Expect(k8sClient.Update(ctx, co)).To(Succeed())
		_, err := runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder(co.Name)
		Expect(co.Status.Workers).To(HaveLen(3))
		for _, w := range original {
			Expect(co.Status.Workers).To(ContainElement(w))
		}
		counts := map[string]int{}
		for _, w := range co.Status.Workers {
			Expect(w.InstanceType).To(Equal("bm-standard"))
			counts[w.NodeSet]++
		}
		Expect(counts).To(Equal(map[string]int{"compute": 2, "batch": 1}))
		Expect(fc.CreateCalls()).To(HaveLen(3))
	})

	It("returns an error when BMI creation fails", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-unavail", 1)
		create(co)

		// Set the create error BEFORE makeInfraEnvReady so it is active when
		// the second reconcile reaches reconcileWorkers.
		fc.SetCreateError(fmt.Errorf("connection refused"))

		// First reconcile creates InfraEnv (no fulfillment call yet).
		_, err := runReconcile("bmw-unavail")
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, "bmw-unavail-infraenv", testNamespace, ign.URL())).To(Succeed())

		// Second reconcile fetches ignition, resolves disk image, then tries
		// reconcileWorkers — which fails on Create.
		_, err = runReconcile("bmw-unavail")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("creating BMI"))
	})

	It("updates status.workers with phase WaitingForAgent", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-status", 2)
		create(co)
		makeInfraEnvReady("bmw-status")

		res, err := runReconcile("bmw-status")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0), "requeues for agent correlation")

		co = getClusterOrder("bmw-status")
		Expect(co.Status.Workers).To(HaveLen(2))
		for _, w := range co.Status.Workers {
			Expect(w.Phase).To(Equal("WaitingForAgent"))
			Expect(w.Kind).To(Equal("BareMetalInstance"))
			Expect(w.BareMetalInstance.ID).ToNot(BeEmpty())
			Expect(w.NodeSet).To(Equal("bm-standard"))
		}
		Expect(co.Status.Workers[0].BareMetalInstance.Name).To(Equal(co.Status.Workers[0].Name))
		Expect(co.Status.Workers[1].BareMetalInstance.Name).To(Equal(co.Status.Workers[1].Name))
		Expect(co.Status.Workers[0].Name).ToNot(Equal(co.Status.Workers[1].Name))
	})

	It("enriches BMI network attachment with interface from first fabric port and primary=true", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-enrich", 1)
		create(co)
		makeInfraEnvReady("bmw-enrich")

		_, err := runReconcile("bmw-enrich")
		Expect(err).ToNot(HaveOccurred())

		calls := fc.CreateCalls()
		Expect(calls).To(HaveLen(1))

		netAttachments := calls[0].GetSpec().GetNetworkAttachments()
		Expect(netAttachments).To(HaveLen(1))
		Expect(netAttachments[0].GetSubnet().GetName()).To(Equal("my-subnet"))
		Expect(netAttachments[0].GetSecurityGroups()).To(HaveLen(1))
		Expect(netAttachments[0].GetSecurityGroups()[0].GetName()).To(Equal("sg-default"))
		Expect(netAttachments[0].GetInterface()).To(Equal("data-0"))
		Expect(netAttachments[0].GetPrimary()).To(BeTrue())
	})

	It("resolves different interfaces for different instance types across node sets", func() {
		preloadDiskImageChain()
		addInstanceType("bm-gpu", "gpu-data-0")

		co := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "bmw-multi-type",
				Namespace:   testNamespace,
				Labels:      map[string]string{clusterIDLabel: clusterUUID},
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{
					{NodeSet: "bm-standard",
						NumberOfNodes: 1,
						BareMetal:     &osacv1alpha1.BareMetalNodeSpec{InstanceType: "bm-standard"},
					},
					{NodeSet: "bm-gpu",
						NumberOfNodes: 1,
						BareMetal:     &osacv1alpha1.BareMetalNodeSpec{InstanceType: "bm-gpu"},
					},
				},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
		create(co)
		makeInfraEnvReady("bmw-multi-type")

		_, err := runReconcile("bmw-multi-type")
		Expect(err).ToNot(HaveOccurred())

		calls := fc.CreateCalls()
		Expect(calls).To(HaveLen(2))
		Expect(calls[0].GetSpec().GetNetworkAttachments()[0].GetInterface()).To(Equal("data-0"))
		Expect(calls[1].GetSpec().GetNetworkAttachments()[0].GetInterface()).To(Equal("gpu-data-0"))
	})

	It("provisions BMI with empty network_attachments when networkAttachment is omitted from ClusterOrder", func() {
		preloadDiskImageChain()
		co := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "bmw-no-net",
				Namespace:   testNamespace,
				Labels:      map[string]string{clusterIDLabel: clusterUUID},
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{NodeSet: "bm-standard",
					NumberOfNodes: 1,
					BareMetal:     &osacv1alpha1.BareMetalNodeSpec{InstanceType: "bm-standard"},
				}},
			},
		}
		create(co)

		// First reconcile creates InfraEnv.
		_, err := runReconcile("bmw-no-net")
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, "bmw-no-net-infraenv", testNamespace, ign.URL())).To(Succeed())

		// Second reconcile provisions worker BMI without networkAttachment.
		_, err = runReconcile("bmw-no-net")
		Expect(err).ToNot(HaveOccurred())

		calls := fc.CreateCalls()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].GetSpec().GetNetworkAttachments()).To(BeEmpty())
	})
})

var _ = Describe("BareMetalWorkerReconciler reconcileAgent", Label("baremetalworker"), func() {
	const (
		clusterUUID    = "correlate-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		fc  *workerFulfillmentStub
		ign *workerIgnitionEndpoint
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {

		fc = newWorkerFulfillmentStub()
		ign = newWorkerIgnitionEndpoint()
		rec = events.NewFakeRecorder(10)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("bm-standard", "data-0"))
	}

	newBareMetalClusterOrder := func(name string, numWorkers int) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNamespace,
				Labels:      map[string]string{clusterIDLabel: clusterUUID},
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{NodeSet: "bm-standard",
					NumberOfNodes: numWorkers,
					BareMetal: &osacv1alpha1.BareMetalNodeSpec{
						InstanceType: "bm-standard",
					},
				}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
	}

	runReconcile := func(name string) (reconcile.Result, error) {
		return driveWorkerCheckpoints(r, fc, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
	}

	getClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		GinkgoHelper()
		co := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, co)).To(Succeed())
		return co
	}

	create := func(co *osacv1alpha1.ClusterOrder) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(co),
				})
			}
			ie := newInfraEnv(co.Name + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})
	}

	makeInfraEnvReady := func(name string) {
		GinkgoHelper()
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
	}

	createWorkersAndSetMAC := func(name string, numWorkers int) {
		GinkgoHelper()
		makeInfraEnvReady(name)
		// Reconcile to create BMIs and enter WaitingForAgent.
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())

		co := getClusterOrder(name)
		Expect(co.Status.Workers).To(HaveLen(numWorkers))
		for _, w := range co.Status.Workers {
			Expect(w.Phase).To(Equal("WaitingForAgent"))
			fc.SetHostMAC(w.BareMetalInstance.ID, fmt.Sprintf("aa:bb:cc:dd:ee:%02d", w.AttemptCount))
		}
		// Set unique MACs for each worker based on their index.
		for i, w := range co.Status.Workers {
			fc.SetHostMAC(w.BareMetalInstance.ID, fmt.Sprintf("aa:bb:cc:dd:ee:%02d", i))
		}
	}

	ptrInt32 := func(value int32) *int32 { return &value }

	registerClusterAgent := func(orderName string) *unstructured.Unstructured {
		GinkgoHelper()
		name := orderName + "-agent"
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: name, Namespace: testNamespace, MAC: "aa:bb:cc:dd:ee:00",
		})).To(Succeed())
		agent := &unstructured.Unstructured{}
		agent.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, agent)).To(Succeed())
		agent.SetLabels(map[string]string{"osac.openshift.io/cluster-order": orderName})
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agent) })
		return agent
	}

	installClusterAgent := func(orderName string, agent *unstructured.Unstructured) {
		GinkgoHelper()
		_, err := runReconcile(orderName)
		Expect(err).ToNot(HaveOccurred())
		Expect(getClusterOrder(orderName).Status.Workers[0].Phase).To(Equal("Binding"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(unstructured.SetNestedField(agent.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		_, err = runReconcile(orderName)
		Expect(err).ToNot(HaveOccurred())
		Expect(getClusterOrder(orderName).Status.Workers[0].Phase).To(Equal("Ready"))
	}

	DescribeTable("persists Ready worker demotion after one explicit reconcile",
		func(change, wantPhase string) {
			preloadDiskImageChain()
			name := "bmw-agent-demotion-" + strings.ToLower(change)
			create(newBareMetalClusterOrder(name, 1))
			createWorkersAndSetMAC(name, 1)
			agent := registerClusterAgent(name)
			installClusterAgent(name, agent)
			before := getClusterOrder(name).Status.Workers[0]
			Expect(before.ReadySince).ToNot(BeNil())
			createCount := len(fc.CreateCalls())

			if change == "disappeared" {
				Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
			} else {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
				Expect(unstructured.SetNestedSlice(agent.Object, []interface{}{
					map[string]interface{}{"type": "Installed", "status": change},
				}, "status", "conditions")).To(Succeed())
				// The condition must override the stale installed debug state.
				Expect(k8sClient.Update(ctx, agent)).To(Succeed())
			}
			res, err := runReconcile(name)
			Expect(err).ToNot(HaveOccurred())
			co := getClusterOrder(name)
			Expect(co.Status.Workers).To(HaveLen(1))
			Expect(co.Status.Workers[0].Phase).To(Equal(wantPhase))
			// R09: leaving Ready clears the continuous healthy interval.
			before.Phase = wantPhase
			before.ReadySince = nil
			Expect(co.Status.Workers[0]).To(Equal(before))
			Expect(co.Status.DesiredWorkers).To(Equal(ptrInt32(1)))
			Expect(co.Status.CurrentWorkers).To(Equal(ptrInt32(1)))
			Expect(co.Status.ReadyWorkers).To(Equal(ptrInt32(0)))
			Expect(fc.CreateCalls()).To(HaveLen(createCount))
			if wantPhase == "WaitingForAgent" {
				Expect(res.RequeueAfter).To(Equal(30 * time.Second))
			}
		},
		Entry("Installed=False", "False", "Binding"),
		Entry("Installed=Unknown", "Unknown", "Binding"),
		Entry("Agent disappeared", "disappeared", "WaitingForAgent"),
	)

	It("restarts the healthy interval after a demotion and re-entry", func() {
		preloadDiskImageChain()
		name := "bmw-r09-e4"
		create(newBareMetalClusterOrder(name, 1))
		createWorkersAndSetMAC(name, 1)
		agent := registerClusterAgent(name)
		installClusterAgent(name, agent)
		before := getClusterOrder(name).Status.Workers[0]
		Expect(before.ReadySince).ToNot(BeNil())

		// A demotion clears the continuous healthy interval.
		Expect(unstructured.SetNestedSlice(agent.Object, []interface{}{
			map[string]interface{}{"type": "Installed", "status": "False"},
		}, "status", "conditions")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		demoted := getClusterOrder(name).Status.Workers[0]
		Expect(demoted.Phase).To(Equal("Binding"))
		Expect(demoted.ReadySince).To(BeNil())

		// Re-entry starts a fresh interval instead of inheriting the old one.
		Expect(unstructured.SetNestedSlice(agent.Object, []interface{}{
			map[string]interface{}{"type": "Installed", "status": "True"},
		}, "status", "conditions")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		_, err = runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		ready := getClusterOrder(name).Status.Workers[0]
		Expect(ready.Phase).To(Equal("Ready"))
		Expect(ready.ReadySince).ToNot(BeNil())
		Expect(ready.ReadySince.Time).To(BeTemporally(">=", before.ReadySince.Time))
	})

	DescribeTable("repairs interrupted worker status without rebinding or creating a BMI",
		func(installed bool, wantPhase string) {
			preloadDiskImageChain()
			name := "bmw-agent-recover-" + strings.ToLower(wantPhase)
			create(newBareMetalClusterOrder(name, 1))
			createWorkersAndSetMAC(name, 1)
			co := getClusterOrder(name)
			before := co.Status.Workers[0]
			agent := registerClusterAgent(name)
			// Simulate the Agent patch surviving a crash before worker status was written.
			labels := agent.GetLabels()
			labels["osac.openshift.io/worker-name"] = before.Name
			agent.SetLabels(labels)
			Expect(unstructured.SetNestedMap(agent.Object, map[string]interface{}{
				"name": name, "namespace": testNamespace,
			}, "spec", "clusterDeploymentName")).To(Succeed())
			if installed {
				Expect(unstructured.SetNestedField(agent.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
			}
			Expect(k8sClient.Update(ctx, agent)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
			version := agent.GetResourceVersion()
			createCount := len(fc.CreateCalls())

			_, err := runReconcile(name)
			Expect(err).ToNot(HaveOccurred())
			co = getClusterOrder(name)
			Expect(co.Status.Workers).To(HaveLen(1))
			Expect(co.Status.Workers[0].Phase).To(Equal(wantPhase))
			Expect(co.Status.Workers[0].Name).To(Equal(before.Name))
			Expect(co.Status.Workers[0].BareMetalInstance).To(Equal(before.BareMetalInstance))
			Expect(co.Status.CurrentWorkers).To(Equal(ptrInt32(1)))
			ready := int32(0)
			if installed {
				ready = 1
				Expect(co.Status.Workers[0].ReadySince).ToNot(BeNil())
			}
			Expect(co.Status.ReadyWorkers).To(Equal(&ready))
			Expect(fc.CreateCalls()).To(HaveLen(createCount))
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
			Expect(agent.GetResourceVersion()).To(Equal(version), "status repair must not rebind the Agent")
		},
		Entry("bound Agent", false, "Binding"),
		Entry("installed bound Agent", true, "Ready"),
	)

	It("preserves CAP-Agent handoff readiness and waits for owner-driven detach", func() {
		preloadDiskImageChain()
		name := "bmw-cap-handoff"
		const workerFinalizer = "osac.openshift.io/baremetalworker-finalizer"
		create(newBareMetalClusterOrder(name, 1))
		createWorkersAndSetMAC(name, 1)
		co := getClusterOrder(name)
		co.Status.ClusterReference = &osacv1alpha1.ClusterOrderClusterReferenceType{
			HostedClusterName: name, Namespace: testNamespace + "-" + name,
		}
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())
		agent := registerClusterAgent(name)
		_, err := runReconcile(name) // Initial OSAC correlation/binding.
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder(name)
		before := co.Status.Workers[0]
		Expect(before.Phase).To(Equal("Binding"))
		createCount := len(fc.CreateCalls())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		// CAP-Agent selects the labelled worker and writes its own deployment reference.
		binding := map[string]interface{}{
			"name": name, "namespace": co.Status.ClusterReference.Namespace + "-" + name,
		}
		Expect(unstructured.SetNestedMap(agent.Object, binding, "spec", "clusterDeploymentName")).To(Succeed())
		labels := agent.GetLabels()
		labels["agentMachineRef"] = "machine-0"
		agent.SetLabels(labels)
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		step := func() {
			GinkgoHelper()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(co)})
			Expect(err).ToNot(HaveOccurred())
		}
		step()
		Expect(getClusterOrder(name).Status.Workers[0].Phase).To(Equal("Binding"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(unstructured.SetNestedField(agent.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		version := agent.GetResourceVersion()
		step()
		co = getClusterOrder(name)
		Expect(co.Status.Workers[0].Phase).To(Equal("Ready"))
		Expect(co.Status.Workers[0].BareMetalInstance).To(Equal(before.BareMetalInstance))
		Expect(co.Status.CurrentWorkers).To(Equal(ptrInt32(1)))
		Expect(co.Status.ReadyWorkers).To(Equal(ptrInt32(1)))
		Expect(fc.CreateCalls()).To(HaveLen(createCount))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.GetResourceVersion()).To(Equal(version), "handoff must not rewrite CAP-Agent's binding")

		// Matching OSAC labels cannot authorize a deployment belonging elsewhere.
		Expect(unstructured.SetNestedField(agent.Object, "foreign-hcp", "spec", "clusterDeploymentName", "namespace")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(co)})
		Expect(err).To(MatchError(ContainSubstring("bound to another cluster deployment")))
		Expect(fc.DeleteCalls()).To(BeEmpty())
		Expect(unstructured.SetNestedMap(agent.Object, binding, "spec", "clusterDeploymentName")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())

		Expect(k8sClient.Delete(ctx, co)).To(Succeed())
		step() // Persist retirement intent.
		step() // Observe the still CAP-Agent-bound worker; do not delete it.
		Expect(fc.DeleteCalls()).To(BeEmpty())
		co = getClusterOrder(name)
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Phase).To(Equal("Unbinding"))
		Expect(controllerutil.ContainsFinalizer(co, workerFinalizer)).To(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.GetDeletionTimestamp().IsZero()).To(BeTrue())
		// Only the test simulates CAP-Agent finishing detachment.
		unstructured.RemoveNestedField(agent.Object, "spec", "clusterDeploymentName")
		labels = agent.GetLabels()
		delete(labels, "agentMachineRef")
		agent.SetLabels(labels)
		Expect(unstructured.SetNestedField(agent.Object, "known-unbound", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		step() // Request Agent deletion, retaining the BMI.
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent))).To(BeTrue())
		Expect(fc.DeleteCalls()).To(BeEmpty())
		step() // Authoritative Agent absence permits the BMI delete request.
		Expect(fc.DeleteCalls()).To(Equal([]string{before.BareMetalInstance.ID}))
		step() // Confirm BMI absence and finalize.
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(co), co))).To(BeTrue())
		Expect(fc.CreateCalls()).To(HaveLen(createCount))
	})

	DescribeTable("does not resurrect protected lifecycle workers from installed Agents",
		func(phase string) {
			preloadDiskImageChain()
			name := "bmw-agent-protected-" + strings.ToLower(phase)
			create(newBareMetalClusterOrder(name, 1))
			createWorkersAndSetMAC(name, 1)
			agent := registerClusterAgent(name)
			installClusterAgent(name, agent)
			co := getClusterOrder(name)
			co.Status.Workers[0].Phase = phase
			co.Status.Workers[0].AttemptCount = 2
			co.Status.Workers[0].LastFailureReason = "InfrastructureError"
			now := metav1.Now()
			co.Status.Workers[0].LastFailureTime = &now
			// R09: a protected lifecycle phase is a demotion; no healthy interval remains.
			co.Status.Workers[0].ReadySince = nil
			Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())
			before := co.Status.Workers[0]
			// Keep provider deletion pending. Installed Agents likewise hold Unbinding.
			fc.SetDeleteError(fmt.Errorf("provider deletion pending"))
			_, err := runReconcile(name)
			Expect(err).ToNot(HaveOccurred(), "bound Agent must block cleanup before backend deletion")
			Expect(fc.DeleteCalls()).To(BeEmpty())
			co = getClusterOrder(name)
			Expect(co.Status.Workers).To(ContainElement(before))
			Expect(*co.Status.ReadyWorkers).To(Equal(int32(0)))
			if phase == "Failed" {
				Expect(apimeta.IsStatusConditionTrue(co.Status.Conditions, osacv1alpha1.ConditionWorkersFailed)).To(BeTrue())
			}
		},
		Entry("Failed", "Failed"),
		Entry("Unbinding", "Unbinding"),
		Entry("Deleting", "Deleting"),
	)

	It("correlates an agent to a worker by unique MAC match", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-corr", 1)
		create(co)
		createWorkersAndSetMAC("bmw-corr", 1)

		// Register an agent with the matching MAC.
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: "bmw-corr-agent-0", Namespace: testNamespace, MAC: "aa:bb:cc:dd:ee:00",
		})).To(Succeed())
		// Label the agent with the cluster-order label (simulates the controller's watch filter).
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-corr-agent-0", Namespace: testNamespace}, agentObj)).To(Succeed())
		agentLabels := agentObj.GetLabels()
		if agentLabels == nil {
			agentLabels = make(map[string]string)
		}
		agentLabels["osac.openshift.io/cluster-order"] = "bmw-corr"
		agentObj.SetLabels(agentLabels)
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		// Reconcile — the agent should be correlated.
		_, err := runReconcile("bmw-corr")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-corr")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Phase).To(Equal("Binding"))

		// Verify the agent got the worker-name label and clusterDeploymentName.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-corr-agent-0", Namespace: testNamespace}, agentObj)).To(Succeed())
		Expect(agentObj.GetLabels()).To(HaveKeyWithValue("osac.openshift.io/worker-name", getClusterOrder("bmw-corr").Status.Workers[0].Name))
		Expect(agentObj.GetLabels()).To(HaveKeyWithValue("agentBareMetal", "true"))
		cdName, _, _ := unstructured.NestedString(agentObj.Object, "spec", "clusterDeploymentName", "name")
		Expect(cdName).To(Equal("bmw-corr"))

		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })
	})

	It("stays in WaitingForAgent when no agent matches", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-no-match", 1)
		create(co)
		createWorkersAndSetMAC("bmw-no-match", 1)

		// No agent registered — reconcile should requeue.
		res, err := runReconcile("bmw-no-match")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))

		co = getClusterOrder("bmw-no-match")
		Expect(co.Status.Workers[0].Phase).To(Equal("WaitingForAgent"))
	})

	It("does not bind when multiple BMIs match the same agent MAC", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-ambig", 2)
		create(co)
		createWorkersAndSetMAC("bmw-ambig", 2)

		// Set both BMIs to the same MAC so the agent's MAC matches ambiguously.
		co = getClusterOrder("bmw-ambig")
		fc.SetHostMAC(co.Status.Workers[0].BareMetalInstance.ID, "aa:bb:cc:dd:ee:99")
		fc.SetHostMAC(co.Status.Workers[1].BareMetalInstance.ID, "aa:bb:cc:dd:ee:99")

		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: "bmw-ambig-agent-0", Namespace: testNamespace, MAC: "aa:bb:cc:dd:ee:99",
		})).To(Succeed())
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-ambig-agent-0", Namespace: testNamespace}, agentObj)).To(Succeed())
		agentLabels := agentObj.GetLabels()
		if agentLabels == nil {
			agentLabels = make(map[string]string)
		}
		agentLabels["osac.openshift.io/cluster-order"] = "bmw-ambig"
		agentObj.SetLabels(agentLabels)
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		_, err := runReconcile("bmw-ambig")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-ambig")
		for _, w := range co.Status.Workers {
			Expect(w.Phase).To(Equal("WaitingForAgent"), "ambiguous match should not bind")
		}
		Expect(co.Status.CurrentWorkers).To(Equal(ptrInt32(2)))
		Expect(co.Status.ReadyWorkers).To(Equal(ptrInt32(0)))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agentObj), agentObj)).To(Succeed())
		Expect(agentObj.GetLabels()).ToNot(HaveKey("osac.openshift.io/worker-name"))
		_, bound, err := unstructured.NestedMap(agentObj.Object, "spec", "clusterDeploymentName")
		Expect(err).ToNot(HaveOccurred())
		Expect(bound).To(BeFalse())

		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })
	})

	It("uses cached worker-name label on subsequent reconciles", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-cache", 1)
		create(co)
		createWorkersAndSetMAC("bmw-cache", 1)

		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: "bmw-cache-agent-0", Namespace: testNamespace, MAC: "aa:bb:cc:dd:ee:00",
		})).To(Succeed())
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-cache-agent-0", Namespace: testNamespace}, agentObj)).To(Succeed())
		agentLabels := agentObj.GetLabels()
		if agentLabels == nil {
			agentLabels = make(map[string]string)
		}
		agentLabels["osac.openshift.io/cluster-order"] = "bmw-cache"
		agentObj.SetLabels(agentLabels)
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		// First reconcile: correlates agent.
		_, err := runReconcile("bmw-cache")
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-cache")
		Expect(co.Status.Workers[0].Phase).To(Equal("Binding"))

		// Clear the host MAC to prove correlation doesn't re-run (cached).
		fc.SetHostMAC(co.Status.Workers[0].BareMetalInstance.ID, "")

		// Second reconcile: uses cached label, doesn't re-correlate.
		_, err = runReconcile("bmw-cache")
		Expect(err).ToNot(HaveOccurred())

		// Worker is still Binding (or would advance to Ready once installed).
		co = getClusterOrder("bmw-cache")
		Expect(co.Status.Workers[0].Phase).To(Equal("Binding"))

		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })
	})

	It("updates aggregate worker counts on the ClusterOrder", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-agg", 2)
		create(co)
		createWorkersAndSetMAC("bmw-agg", 2)

		// Correlate only one agent.
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: "bmw-agg-agent-0", Namespace: testNamespace, MAC: "aa:bb:cc:dd:ee:00",
		})).To(Succeed())
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-agg-agent-0", Namespace: testNamespace}, agentObj)).To(Succeed())
		agentLabels := agentObj.GetLabels()
		if agentLabels == nil {
			agentLabels = make(map[string]string)
		}
		agentLabels["osac.openshift.io/cluster-order"] = "bmw-agg"
		agentObj.SetLabels(agentLabels)
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		_, err := runReconcile("bmw-agg")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-agg")
		Expect(co.Status.DesiredWorkers).ToNot(BeNil())
		Expect(*co.Status.DesiredWorkers).To(Equal(int32(2)))
		Expect(co.Status.CurrentWorkers).ToNot(BeNil())
		Expect(*co.Status.CurrentWorkers).To(Equal(int32(2)))
		Expect(co.Status.ReadyWorkers).ToNot(BeNil())
		Expect(*co.Status.ReadyWorkers).To(Equal(int32(0)))

		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })
	})

	// registerAgentWithLabels registers a test Agent and applies the given
	// selector labels, returning the apiserver object.
	registerAgentWithLabels := func(agentName, mac string, labels map[string]string) *unstructured.Unstructured {
		GinkgoHelper()
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: agentName, Namespace: testNamespace, MAC: mac,
		})).To(Succeed())
		agent := &unstructured.Unstructured{}
		agent.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: agentName, Namespace: testNamespace}, agent)).To(Succeed())
		agent.SetLabels(labels)
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agent) })
		return agent
	}
	getAgentByName := func(agentName string) *unstructured.Unstructured {
		GinkgoHelper()
		agent := &unstructured.Unstructured{}
		agent.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: agentName, Namespace: testNamespace}, agent)).To(Succeed())
		return agent
	}

	It("unions both Agent selectors and deduplicates a shared object", func() {
		preloadDiskImageChain()
		const name = "bmw-r06-e1"
		create(newBareMetalClusterOrder(name, 1))
		createWorkersAndSetMAC(name, 1)
		worker := getClusterOrder(name).Status.Workers[0]
		const infraEnvLabel = "infraenvs.agent-install.openshift.io"

		infraOnly := registerAgentWithLabels(name+"-infra-agent", "ff:ff:ff:ff:ff:01",
			map[string]string{infraEnvLabel: name + "-infraenv"})
		// The matching Agent carries only the cluster-order selector: an InfraEnv-only
		// observation would omit it entirely.
		clusterOnly := registerAgentWithLabels(name+"-cluster-agent", "aa:bb:cc:dd:ee:00",
			map[string]string{"osac.openshift.io/cluster-order": name})
		// This object carries both selectors; the union must not count it twice.
		both := registerAgentWithLabels(name+"-both-agent", "ff:ff:ff:ff:ff:02", map[string]string{
			infraEnvLabel: name + "-infraenv", "osac.openshift.io/cluster-order": name,
		})

		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())

		co := getClusterOrder(name)
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Phase).To(Equal("Binding"))
		Expect(getAgentByName(clusterOnly.GetName()).GetLabels()).To(HaveKeyWithValue(
			"osac.openshift.io/worker-name", worker.Name))
		// Every observed Agent survives; the non-matching ones stay unassigned.
		for _, preserved := range []string{infraOnly.GetName(), both.GetName()} {
			Expect(getAgentByName(preserved).GetLabels()).ToNot(HaveKey("osac.openshift.io/worker-name"))
		}
		Expect(fc.DeleteCalls()).To(BeEmpty())
	})

	It("refuses readiness and mutation under ambiguous Agent evidence", func() {
		preloadDiskImageChain()
		const name = "bmw-r06-e2"
		const finalizer = "osac.openshift.io/baremetalworker-finalizer"
		create(newBareMetalClusterOrder(name, 1))
		createWorkersAndSetMAC(name, 1)
		worker := getClusterOrder(name).Status.Workers[0]

		// Two unbound Agents share the worker's MAC: a worker -> several Agents
		// ambiguity that must not bind or patch either Agent.
		first := registerAgentWithLabels(name+"-ambig-a", "aa:bb:cc:dd:ee:00",
			map[string]string{"osac.openshift.io/cluster-order": name})
		second := registerAgentWithLabels(name+"-ambig-b", "aa:bb:cc:dd:ee:00",
			map[string]string{"osac.openshift.io/cluster-order": name})
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		co := getClusterOrder(name)
		Expect(co.Status.Workers[0].Phase).To(Equal("WaitingForAgent"))
		Expect(*co.Status.ReadyWorkers).To(Equal(int32(0)))
		for _, agentName := range []string{first.GetName(), second.GetName()} {
			Expect(getAgentByName(agentName).GetLabels()).ToNot(HaveKey("osac.openshift.io/worker-name"))
		}

		// Two Agents now claim the same worker-name label and report installed: a
		// duplicate established association must not promote readiness or be deleted.
		for _, agentName := range []string{first.GetName(), second.GetName()} {
			agent := getAgentByName(agentName)
			labels := agent.GetLabels()
			labels["osac.openshift.io/worker-name"] = worker.Name
			agent.SetLabels(labels)
			Expect(unstructured.SetNestedField(agent.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
			Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		}
		_, err = runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder(name)
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Phase).ToNot(Equal("Ready"), "duplicate claims must not promote readiness")
		Expect(co.Status.Workers[0].BareMetalInstance).To(Equal(worker.BareMetalInstance))
		Expect(*co.Status.ReadyWorkers).To(Equal(int32(0)))
		Expect(co.Finalizers).To(ContainElement(finalizer))
		Expect(getAgentByName(first.GetName())).ToNot(BeNil())
		Expect(getAgentByName(second.GetName())).ToNot(BeNil())
		Expect(fc.DeleteCalls()).To(BeEmpty())
	})
})

var _ = Describe("BareMetalWorkerReconciler workerRetry", Label("baremetalworker"), func() {
	const (
		clusterUUID    = "retry-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		fc  *workerFulfillmentStub
		ign *workerIgnitionEndpoint
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {

		fc = newWorkerFulfillmentStub()
		ign = newWorkerIgnitionEndpoint()
		rec = events.NewFakeRecorder(20)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("bm-standard", "data-0"))
	}

	newBareMetalClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNamespace,
				Labels:      map[string]string{clusterIDLabel: clusterUUID},
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{NodeSet: "bm-standard",
					NumberOfNodes: 1,
					BareMetal: &osacv1alpha1.BareMetalNodeSpec{
						InstanceType: "bm-standard",
					},
				}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
	}

	runReconcile := func(name string) (reconcile.Result, error) {
		return driveWorkerCheckpoints(r, fc, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
	}

	getClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		GinkgoHelper()
		co := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, co)).To(Succeed())
		return co
	}

	create := func(co *osacv1alpha1.ClusterOrder) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(co),
				})
			}
			ie := newInfraEnv(co.Name + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})
	}

	makeInfraEnvReady := func(name string) {
		GinkgoHelper()
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
	}

	// setWorkerFailed patches a worker's status to Failed with the given reason,
	// simulating any failure path (agent timeout, BMI provisioning error, etc.).
	setWorkerFailed := func(name, workerName, reason, message string) {
		GinkgoHelper()
		co := getClusterOrder(name)
		for i := range co.Status.Workers {
			if co.Status.Workers[i].Name == workerName {
				co.Status.Workers[i].Phase = "Failed"
				co.Status.Workers[i].LastFailureReason = reason
				co.Status.Workers[i].LastFailureMessage = message
				failTime := metav1.Now()
				co.Status.Workers[i].LastFailureTime = &failTime
			}
		}
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())
	}

	It("deletes a failed BMI, increments attemptCount, and sets NextRetryTime", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-retry-del")
		create(co)
		makeInfraEnvReady("bmw-retry-del")

		// Create worker → WaitingForAgent.
		_, err := runReconcile("bmw-retry-del")
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-retry-del")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Phase).To(Equal("WaitingForAgent"))
		Expect(co.Status.Workers[0].BareMetalInstance.ID).ToNot(BeEmpty())

		// Simulate failure.
		setWorkerFailed("bmw-retry-del", co.Status.Workers[0].Name, "InfrastructureError", "host allocation failed")

		// Accepted Delete retains identity; only the next Get confirms absence.
		oldID := co.Status.Workers[0].BareMetalInstance.ID
		_, err = runReconcile("bmw-retry-del")
		Expect(err).ToNot(HaveOccurred())
		Expect(getClusterOrder("bmw-retry-del").Status.Workers[0].BareMetalInstance.ID).To(Equal(oldID))
		_, err = runReconcile("bmw-retry-del")
		Expect(err).ToNot(HaveOccurred())

		Expect(fc.DeleteCalls()).To(HaveLen(1))

		co = getClusterOrder("bmw-retry-del")
		w := co.Status.Workers[0]
		Expect(w.Phase).To(Equal("Failed"))
		Expect(w.AttemptCount).To(Equal(int32(1)))
		Expect(w.BareMetalInstance.ID).To(BeEmpty())
		Expect(w.NextRetryTime).ToNot(BeNil())
	})

	It("creates a replacement BMI when NextRetryTime has passed", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-retry-repl")
		create(co)
		makeInfraEnvReady("bmw-retry-repl")

		// Create worker → WaitingForAgent.
		_, err := runReconcile("bmw-retry-repl")
		Expect(err).ToNot(HaveOccurred())
		initialCreateCalls := len(fc.CreateCalls())

		// Simulate failure.
		setWorkerFailed("bmw-retry-repl", getClusterOrder("bmw-retry-repl").Status.Workers[0].Name, "InfrastructureError", "host failed")

		// Reconcile → deletes BMI, sets NextRetryTime in the future.
		_, err = runReconcile("bmw-retry-repl")
		Expect(err).ToNot(HaveOccurred())

		// Confirm old-incarnation absence and persist the retry checkpoint.
		_, err = runReconcile("bmw-retry-repl")
		Expect(err).ToNot(HaveOccurred())
		Expect(getClusterOrder("bmw-retry-repl").Status.Workers[0].BareMetalInstance.ID).To(BeEmpty())
		// Move NextRetryTime to the past so retry is due.
		co = getClusterOrder("bmw-retry-repl")
		pastTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
		co.Status.Workers[0].NextRetryTime = &pastTime
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

		// Reconcile → creates replacement BMI.
		_, err = runReconcile("bmw-retry-repl")
		Expect(err).ToNot(HaveOccurred())

		Expect(fc.CreateCalls()).To(HaveLen(initialCreateCalls + 1))

		co = getClusterOrder("bmw-retry-repl")
		w := co.Status.Workers[0]
		Expect(w.Phase).To(Equal("WaitingForAgent"))
		Expect(w.BareMetalInstance.ID).ToNot(BeEmpty())
		Expect(w.NextRetryTime).To(BeNil())
		Expect(w.AttemptCount).To(Equal(int32(1)))
	})

	It("clears WorkersFailed condition when replacement reaches Ready", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-retry-clear")
		create(co)
		makeInfraEnvReady("bmw-retry-clear")

		// Create worker → WaitingForAgent.
		_, err := runReconcile("bmw-retry-clear")
		Expect(err).ToNot(HaveOccurred())

		// Simulate failure.
		setWorkerFailed("bmw-retry-clear", getClusterOrder("bmw-retry-clear").Status.Workers[0].Name, "AgentRegistrationTimeout", "timeout")

		// Reconcile → deletes BMI, sets backoff.
		_, err = runReconcile("bmw-retry-clear")
		Expect(err).ToNot(HaveOccurred())

		_, err = runReconcile("bmw-retry-clear") // Confirm absence before scheduling.
		Expect(err).ToNot(HaveOccurred())
		Expect(getClusterOrder("bmw-retry-clear").Status.Workers[0].BareMetalInstance.ID).To(BeEmpty())
		// Set NextRetryTime to past.
		co = getClusterOrder("bmw-retry-clear")
		pastTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
		co.Status.Workers[0].NextRetryTime = &pastTime
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

		// Reconcile → creates replacement BMI.
		_, err = runReconcile("bmw-retry-clear")
		Expect(err).ToNot(HaveOccurred())

		// Now simulate the replacement reaching Ready: register an agent, correlate, install.
		co = getClusterOrder("bmw-retry-clear")
		fc.SetHostMAC(co.Status.Workers[0].BareMetalInstance.ID, "aa:bb:cc:dd:ee:42")

		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: "bmw-retry-clear-agent-0", Namespace: testNamespace, MAC: "aa:bb:cc:dd:ee:42",
		})).To(Succeed())
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-retry-clear-agent-0", Namespace: testNamespace}, agentObj)).To(Succeed())
		agentLabels := agentObj.GetLabels()
		if agentLabels == nil {
			agentLabels = make(map[string]string)
		}
		agentLabels["osac.openshift.io/cluster-order"] = "bmw-retry-clear"
		agentObj.SetLabels(agentLabels)
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		// Reconcile → correlates agent → Binding.
		_, err = runReconcile("bmw-retry-clear")
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-retry-clear")
		Expect(co.Status.Workers[0].Phase).To(Equal("Binding"))

		// Set agent debugInfo.state to "installed" to trigger Binding→Ready.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-retry-clear-agent-0", Namespace: testNamespace}, agentObj)).To(Succeed())
		Expect(unstructured.SetNestedField(agentObj.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		// Reconcile → Binding → Ready.
		_, err = runReconcile("bmw-retry-clear")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-retry-clear")
		Expect(co.Status.Workers[0].Phase).To(Equal("Ready"))
		Expect(co.Status.Workers[0].ReadySince).ToNot(BeNil())
		Expect(co.Status.Workers[0].AttemptCount).To(Equal(int32(1)))

		// WorkersFailed condition is cleared.
		cond := apimeta.FindStatusCondition(co.Status.Conditions, osacv1alpha1.ConditionWorkersFailed)
		if cond != nil {
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		}

		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })
	})
})

var _ = Describe("BareMetalWorkerReconciler scale-up", Label("baremetalworker"), func() {
	const (
		clusterUUID    = "scaleup-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		fc  *workerFulfillmentStub
		ign *workerIgnitionEndpoint
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {

		fc = newWorkerFulfillmentStub()
		ign = newWorkerIgnitionEndpoint()
		rec = events.NewFakeRecorder(20)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("bm-standard", "data-0"))
	}

	newBareMetalClusterOrder := func(name string, numWorkers int) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNamespace,
				Labels:      map[string]string{clusterIDLabel: clusterUUID},
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{NodeSet: "bm-standard",
					NumberOfNodes: numWorkers,
					BareMetal: &osacv1alpha1.BareMetalNodeSpec{
						InstanceType: "bm-standard",
					},
				}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
	}

	runReconcile := func(name string) (reconcile.Result, error) {
		return driveWorkerCheckpoints(r, fc, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
	}

	getClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		GinkgoHelper()
		co := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, co)).To(Succeed())
		return co
	}

	create := func(co *osacv1alpha1.ClusterOrder) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(co),
				})
			}
			ie := newInfraEnv(co.Name + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})
	}

	makeInfraEnvReady := func(name string) {
		GinkgoHelper()
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
	}

	provisionWorkers := func(name string, numWorkers int) {
		GinkgoHelper()
		makeInfraEnvReady(name)
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		co := getClusterOrder(name)
		Expect(co.Status.Workers).To(HaveLen(numWorkers))
	}

	It("creates additional BMIs when NumberOfNodes increases", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-scaleup", 2)
		create(co)
		provisionWorkers("bmw-scaleup", 2)

		initialCreateCalls := len(fc.CreateCalls())

		co = getClusterOrder("bmw-scaleup")
		originalWorkers := append([]osacv1alpha1.WorkerStatus(nil), co.Status.Workers...)
		co.Spec.NodeRequests[0].NumberOfNodes = 4
		Expect(k8sClient.Update(ctx, co)).To(Succeed())

		_, err := runReconcile("bmw-scaleup")
		Expect(err).ToNot(HaveOccurred())

		Expect(fc.CreateCalls()).To(HaveLen(initialCreateCalls + 2))

		co = getClusterOrder("bmw-scaleup")
		Expect(co.Status.Workers).To(HaveLen(4))
		for _, original := range originalWorkers {
			Expect(co.Status.Workers).To(ContainElement(original))
		}
		newNames := make(map[string]bool)
		for _, w := range co.Status.Workers {
			if w.Name == originalWorkers[0].Name || w.Name == originalWorkers[1].Name {
				continue
			}
			Expect(w.Phase).To(Equal("WaitingForAgent"))
			newNames[w.Name] = true
		}
		Expect(newNames).To(HaveLen(2))
	})

	It("preserves existing worker phases during scale-up", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-preserve", 2)
		create(co)
		provisionWorkers("bmw-preserve", 2)

		co = getClusterOrder("bmw-preserve")
		bindingName, otherName := co.Status.Workers[0].Name, co.Status.Workers[1].Name
		fc.SetHostMAC(co.Status.Workers[0].BareMetalInstance.ID, "aa:bb:cc:dd:ee:00")
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: "bmw-preserve-agent-0", Namespace: testNamespace, MAC: "aa:bb:cc:dd:ee:00",
		})).To(Succeed())
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-preserve-agent-0", Namespace: testNamespace}, agentObj)).To(Succeed())
		agentLabels := agentObj.GetLabels()
		if agentLabels == nil {
			agentLabels = make(map[string]string)
		}
		agentLabels["osac.openshift.io/cluster-order"] = "bmw-preserve"
		agentObj.SetLabels(agentLabels)
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		_, err := runReconcile("bmw-preserve")
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-preserve")
		Expect(co.Status.Workers[0].Phase).To(Equal("Binding"))

		co.Spec.NodeRequests[0].NumberOfNodes = 3
		Expect(k8sClient.Update(ctx, co)).To(Succeed())

		_, err = runReconcile("bmw-preserve")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-preserve")
		Expect(co.Status.Workers).To(HaveLen(3))
		Expect(co.Status.Workers).To(ContainElement(SatisfyAll(
			HaveField("Name", bindingName), HaveField("Phase", "Binding"),
		)))
		Expect(co.Status.Workers).To(ContainElement(HaveField("Name", otherName)))
		for _, w := range co.Status.Workers {
			if w.Name == bindingName || w.Name == otherName {
				continue
			}
			Expect(w.Phase).To(Equal("WaitingForAgent"))
		}

		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })
	})

	It("scale-up with a failed worker preserves existing identities", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-scalefail", 2)
		create(co)
		provisionWorkers("bmw-scalefail", 2)

		co = getClusterOrder("bmw-scalefail")
		healthy, failed := co.Status.Workers[0], co.Status.Workers[1]
		co.Status.Workers[1].Phase = "Failed"
		co.Status.Workers[1].LastFailureReason = "InfrastructureError"
		failTime := metav1.Now()
		co.Status.Workers[1].LastFailureTime = &failTime
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

		co = getClusterOrder("bmw-scalefail")
		co.Spec.NodeRequests[0].NumberOfNodes = 3
		Expect(k8sClient.Update(ctx, co)).To(Succeed())

		_, err := runReconcile("bmw-scalefail")
		Expect(err).ToNot(HaveOccurred())
		// The scale-up reservation is independent of the failed worker's provider
		// cleanup; the failed incarnation keeps its recorded name, completes its
		// cleanup only on a fresh authoritative absence and schedules its retry.
		co = getClusterOrder("bmw-scalefail")
		Expect(co.Status.Workers).To(HaveLen(3))
		Expect(co.Status.Workers[1].Name).To(Equal(failed.Name))
		Expect(co.Status.Workers[1].BareMetalInstance.Name).To(Equal(failed.BareMetalInstance.Name))
		Expect(co.Status.Workers[1].Phase).To(Equal("Failed"))
		Expect(co.Status.Workers[1].NextRetryTime).ToNot(BeNil())
		_, err = runReconcile("bmw-scalefail") // The independent reservation still converges.
		Expect(err).ToNot(HaveOccurred())
		_, err = runReconcile("bmw-scalefail")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-scalefail")
		Expect(co.Status.Workers).To(HaveLen(3))
		Expect(co.Status.Workers).To(ContainElement(healthy))
		Expect(co.Status.Workers).To(ContainElement(SatisfyAll(
			HaveField("Name", failed.Name),
			HaveField("BareMetalInstance.Name", failed.BareMetalInstance.Name),
			HaveField("Phase", "Failed"),
		)))
		for _, w := range co.Status.Workers {
			if w.Name == healthy.Name || w.Name == failed.Name {
				continue
			}
			Expect(w.BareMetalInstance.Name).To(Equal(w.Name))
			Expect(w.Phase).To(Equal("WaitingForAgent"))
		}
	})

	It("reports partial success with mixed Ready and Failed workers", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-partial", 3)
		create(co)
		provisionWorkers("bmw-partial", 3)

		co = getClusterOrder("bmw-partial")
		fc.SetHostMAC(co.Status.Workers[0].BareMetalInstance.ID, "aa:bb:cc:dd:ee:00")
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: "bmw-partial-agent-0", Namespace: testNamespace, MAC: "aa:bb:cc:dd:ee:00",
		})).To(Succeed())
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-partial-agent-0", Namespace: testNamespace}, agentObj)).To(Succeed())
		agentLabels := agentObj.GetLabels()
		if agentLabels == nil {
			agentLabels = make(map[string]string)
		}
		agentLabels["osac.openshift.io/cluster-order"] = "bmw-partial"
		agentObj.SetLabels(agentLabels)
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		_, err := runReconcile("bmw-partial")
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-partial")
		Expect(co.Status.Workers[0].Phase).To(Equal("Binding"))

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-partial-agent-0", Namespace: testNamespace}, agentObj)).To(Succeed())
		Expect(unstructured.SetNestedField(agentObj.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		_, err = runReconcile("bmw-partial")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-partial")
		Expect(co.Status.Workers[0].Phase).To(Equal("Ready"))
		Expect(co.Status.Workers[1].Phase).To(Equal("WaitingForAgent"))
		Expect(co.Status.Workers[2].Phase).To(Equal("WaitingForAgent"))

		Expect(co.Status.DesiredWorkers).ToNot(BeNil())
		Expect(*co.Status.DesiredWorkers).To(Equal(int32(3)))
		Expect(co.Status.ReadyWorkers).ToNot(BeNil())
		Expect(*co.Status.ReadyWorkers).To(Equal(int32(1)))

		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })
	})
})

var _ = Describe("BareMetalWorkerReconciler stale ignition", Label("baremetalworker"), func() {
	const (
		clusterUUID    = "stale-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		fc  *workerFulfillmentStub
		ign *workerIgnitionEndpoint
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {

		fc = newWorkerFulfillmentStub()
		ign = newWorkerIgnitionEndpoint()
		rec = events.NewFakeRecorder(20)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("bm-standard", "data-0"))
	}

	newBareMetalClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNamespace,
				Labels:      map[string]string{clusterIDLabel: clusterUUID},
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{NodeSet: "bm-standard",
					NumberOfNodes: 1,
					BareMetal: &osacv1alpha1.BareMetalNodeSpec{
						InstanceType: "bm-standard",
					},
				}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
	}

	runReconcile := func(name string) (reconcile.Result, error) {
		return driveWorkerCheckpoints(r, fc, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
	}

	getClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		GinkgoHelper()
		co := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, co)).To(Succeed())
		return co
	}

	create := func(co *osacv1alpha1.ClusterOrder) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(co),
				})
			}
			ie := newInfraEnv(co.Name + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})
	}

	It("recreates a deleted InfraEnv and resets InfraEnvReady", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-deleted-ie")
		create(co)

		_, err := runReconcile("bmw-deleted-ie")
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, "bmw-deleted-ie-infraenv", testNamespace, ign.URL())).To(Succeed())
		_, err = runReconcile("bmw-deleted-ie")
		Expect(err).ToNot(HaveOccurred())
		Expect(apimeta.IsStatusConditionTrue(
			getClusterOrder("bmw-deleted-ie").Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)).To(BeTrue())

		ie := newInfraEnv("bmw-deleted-ie-infraenv")
		Expect(k8sClient.Delete(ctx, ie)).To(Succeed())

		res, err := runReconcile("bmw-deleted-ie")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))

		co = getClusterOrder("bmw-deleted-ie")
		cond := apimeta.FindStatusCondition(co.Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)
		Expect(cond).ToNot(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))

		newIE := newInfraEnv("bmw-deleted-ie-infraenv")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(newIE), newIE)).To(Succeed())
	})

	It("marks WaitingForAgent workers as Failed after InfraEnv recreation", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-stale")
		create(co)

		_, err := runReconcile("bmw-stale")
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, "bmw-stale-infraenv", testNamespace, ign.URL())).To(Succeed())
		_, err = runReconcile("bmw-stale")
		Expect(err).ToNot(HaveOccurred())

		_, err = runReconcile("bmw-stale")
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-stale")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Phase).To(Equal("WaitingForAgent"))

		Expect(co.Annotations).To(HaveKey("osac.openshift.io/infraenv-uid"))
		oldUID := co.Annotations["osac.openshift.io/infraenv-uid"]
		Expect(oldUID).ToNot(BeEmpty())

		ie := newInfraEnv("bmw-stale-infraenv")
		Expect(k8sClient.Delete(ctx, ie)).To(Succeed())

		_, err = runReconcile("bmw-stale")
		Expect(err).ToNot(HaveOccurred())

		Expect(setWorkerIgnitionURL(ctx, "bmw-stale-infraenv", testNamespace, ign.URL())).To(Succeed())

		originalID := co.Status.Workers[0].BareMetalInstance.ID
		res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "bmw-stale", Namespace: testNamespace}})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(time.Second), "stale-ignition repair is a durable boundary")

		co = getClusterOrder("bmw-stale")
		Expect(co.Annotations["osac.openshift.io/infraenv-uid"]).To(Equal(oldUID), "must not advance past the worker repair")
		Expect(co.Status.Workers[0].BareMetalInstance.ID).To(Equal(originalID))
		Expect(fc.DeleteCalls()).To(BeEmpty(), "must not delete the newly failed worker in the same invocation")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Phase).To(Equal("Failed"))
		Expect(co.Status.Workers[0].LastFailureReason).To(Equal("AgentRegistrationTimeout"))
		Expect(co.Status.Workers[0].LastFailureMessage).To(ContainSubstring("stale ignition"))

		_, err = runReconcile("bmw-stale")
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-stale")
		newUID := co.Annotations["osac.openshift.io/infraenv-uid"]
		Expect(newUID).ToNot(Equal(oldUID))
	})

	It("does not mark Binding or Ready workers as Failed after InfraEnv recreation", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-stale-safe")
		co.Spec.NodeRequests[0].NumberOfNodes = 2
		create(co)

		_, err := runReconcile("bmw-stale-safe")
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, "bmw-stale-safe-infraenv", testNamespace, ign.URL())).To(Succeed())
		_, err = runReconcile("bmw-stale-safe")
		Expect(err).ToNot(HaveOccurred())
		_, err = runReconcile("bmw-stale-safe")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-stale-safe")
		Expect(co.Status.Workers).To(HaveLen(2))
		fc.SetHostMAC(co.Status.Workers[0].BareMetalInstance.ID, "aa:bb:cc:dd:ee:00")
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: "bmw-stale-safe-agent-0", Namespace: testNamespace, MAC: "aa:bb:cc:dd:ee:00",
		})).To(Succeed())
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-stale-safe-agent-0", Namespace: testNamespace}, agentObj)).To(Succeed())
		agentLabels := agentObj.GetLabels()
		if agentLabels == nil {
			agentLabels = make(map[string]string)
		}
		agentLabels["osac.openshift.io/cluster-order"] = "bmw-stale-safe"
		agentObj.SetLabels(agentLabels)
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		_, err = runReconcile("bmw-stale-safe")
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-stale-safe")
		Expect(co.Status.Workers[0].Phase).To(Equal("Binding"))

		ie := newInfraEnv("bmw-stale-safe-infraenv")
		Expect(k8sClient.Delete(ctx, ie)).To(Succeed())

		_, err = runReconcile("bmw-stale-safe")
		Expect(err).ToNot(HaveOccurred())

		Expect(setWorkerIgnitionURL(ctx, "bmw-stale-safe-infraenv", testNamespace, ign.URL())).To(Succeed())

		_, err = runReconcile("bmw-stale-safe")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-stale-safe")
		Expect(co.Status.Workers[0].Phase).To(Equal("Binding"))
		Expect(co.Status.Workers[1].Phase).To(Equal("Failed"))
		Expect(co.Status.Workers[1].LastFailureReason).To(Equal("AgentRegistrationTimeout"))

		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })
	})

	It("persists stale-ignition failures while the image lookup is blocked", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-stale-blocked")
		create(co)

		_, err := runReconcile("bmw-stale-blocked")
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, "bmw-stale-blocked-infraenv", testNamespace, ign.URL())).To(Succeed())
		_, err = runReconcile("bmw-stale-blocked")
		Expect(err).ToNot(HaveOccurred())
		_, err = runReconcile("bmw-stale-blocked")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-stale-blocked")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Phase).To(Equal("WaitingForAgent"))
		oldUID := co.Annotations["osac.openshift.io/infraenv-uid"]
		Expect(oldUID).ToNot(BeEmpty())
		original := co.Status.Workers[0]

		// A create is genuinely pending, but its image input cannot resolve.
		latest := getClusterOrder("bmw-stale-blocked")
		latest.Spec.NodeRequests[0].NumberOfNodes = 2
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		Expect(k8sClient.Delete(ctx, newInfraEnv("bmw-stale-blocked-infraenv"))).To(Succeed())
		_, err = runReconcile("bmw-stale-blocked")
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, "bmw-stale-blocked-infraenv", testNamespace, ign.URL())).To(Succeed())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: "missing-image"}.Build(),
			}.Build(),
		}.Build())
		creates := len(fc.CreateCalls())

		res, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "bmw-stale-blocked", Namespace: testNamespace},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(time.Second), "stale-ignition repair is a durable boundary")

		co = getClusterOrder("bmw-stale-blocked")
		Expect(co.Annotations["osac.openshift.io/infraenv-uid"]).To(Equal(oldUID), "must not advance past the worker repair")
		Expect(co.Status.Workers[0].Phase).To(Equal("Failed"))
		Expect(co.Status.Workers[0].LastFailureReason).To(Equal("AgentRegistrationTimeout"))
		Expect(co.Status.Workers[0].BareMetalInstance).To(Equal(original.BareMetalInstance))
		Expect(fc.CreateCalls()).To(HaveLen(creates), "no create is possible while the image input is blocked")
	})
})

var _ = Describe("BareMetalWorkerReconciler scale-down", Label("baremetalworker"), func() {
	const (
		clusterUUID    = "scaledown-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		fc  *workerFulfillmentStub
		ign *workerIgnitionEndpoint
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {

		fc = newWorkerFulfillmentStub()
		ign = newWorkerIgnitionEndpoint()
		rec = events.NewFakeRecorder(20)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("bm-standard", "data-0"))
	}

	newBareMetalClusterOrder := func(name string, numWorkers int) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNamespace,
				Labels:      map[string]string{clusterIDLabel: clusterUUID},
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{NodeSet: "bm-standard",
					NumberOfNodes: numWorkers,
					BareMetal: &osacv1alpha1.BareMetalNodeSpec{
						InstanceType: "bm-standard",
					},
				}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
	}

	runReconcile := func(name string) (reconcile.Result, error) {
		return driveWorkerCheckpoints(r, fc, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
	}

	getClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		GinkgoHelper()
		co := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, co)).To(Succeed())
		return co
	}

	create := func(co *osacv1alpha1.ClusterOrder) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(co),
				})
			}
			ie := newInfraEnv(co.Name + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})
	}

	makeInfraEnvReady := func(name string) {
		GinkgoHelper()
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
	}

	setWorkerFailed := func(name, workerName, reason, message string) {
		GinkgoHelper()
		co := getClusterOrder(name)
		for i := range co.Status.Workers {
			if co.Status.Workers[i].Name == workerName {
				co.Status.Workers[i].Phase = "Failed"
				co.Status.Workers[i].LastFailureReason = reason
				co.Status.Workers[i].LastFailureMessage = message
				failTime := metav1.Now()
				co.Status.Workers[i].LastFailureTime = &failTime
			}
		}
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())
	}

	registerAndBindAgent := func(coName, agentName string, workerIndex int) *unstructured.Unstructured {
		GinkgoHelper()
		co := getClusterOrder(coName)
		mac := fmt.Sprintf("aa:bb:cc:dd:ee:%02d", workerIndex)
		fc.SetHostMAC(co.Status.Workers[workerIndex].BareMetalInstance.ID, mac)

		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: agentName, Namespace: testNamespace, MAC: mac,
		})).To(Succeed())
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: agentName, Namespace: testNamespace}, agentObj)).To(Succeed())
		agentLabels := agentObj.GetLabels()
		if agentLabels == nil {
			agentLabels = make(map[string]string)
		}
		agentLabels["osac.openshift.io/cluster-order"] = coName
		agentObj.SetLabels(agentLabels)
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		_, err := runReconcile(coName)
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder(coName)
		Expect(co.Status.Workers[workerIndex].Phase).To(Equal("Binding"))

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: agentName, Namespace: testNamespace}, agentObj)).To(Succeed())
		Expect(unstructured.SetNestedField(agentObj.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())

		_, err = runReconcile(coName)
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder(coName)
		Expect(co.Status.Workers[workerIndex].Phase).To(Equal("Ready"))

		return agentObj
	}

	It("removes Failed workers first on scale-down", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-sd-failed", 3)
		create(co)
		makeInfraEnvReady("bmw-sd-failed")

		_, err := runReconcile("bmw-sd-failed")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-failed")
		Expect(co.Status.Workers).To(HaveLen(3))

		failedName := co.Status.Workers[1].Name
		setWorkerFailed("bmw-sd-failed", failedName, "InfrastructureError", "host allocation failed")

		deletesBeforeScaleDown := len(fc.DeleteCalls())

		co = getClusterOrder("bmw-sd-failed")
		co.Spec.NodeRequests[0].NumberOfNodes = 1
		Expect(k8sClient.Update(ctx, co)).To(Succeed())

		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "bmw-sd-failed", Namespace: testNamespace}})
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-failed")

		workerNames := map[string]bool{}
		for _, w := range co.Status.Workers {
			workerNames[w.Name] = true
		}
		Expect(workerNames).To(HaveKey(failedName), "Failed worker must retain its slot until confirmed cleanup")
		Expect(fc.DeleteCalls()).To(HaveLen(deletesBeforeScaleDown), "persist retirement before deleting")
		_, err = runReconcile("bmw-sd-failed") // Request cleanup.
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(HaveLen(deletesBeforeScaleDown + 2))

		// Worker-2 (WaitingForAgent, no bound agent) transitions Unbinding → Deleting in one
		// reconcile since there is no agent to wait for unbinding.
		hasTeardown := false
		for _, w := range co.Status.Workers {
			if w.Phase == "Unbinding" || w.Phase == "Deleting" {
				hasTeardown = true
			}
		}
		Expect(hasTeardown).To(BeTrue(), "non-failed excess worker-2 should be in teardown")
		_, err = runReconcile("bmw-sd-failed") // Confirm both absences.
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-sd-failed")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Name).NotTo(Equal(failedName))
	})

	It("handles Agent unbinding lifecycle and removes worker after BMI deletion confirmed", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-sd-unbind", 2)
		create(co)
		makeInfraEnvReady("bmw-sd-unbind")

		_, err := runReconcile("bmw-sd-unbind")
		Expect(err).ToNot(HaveOccurred())

		agent0 := registerAndBindAgent("bmw-sd-unbind", "bmw-sd-unbind-agent-0", 0)
		agent1 := registerAndBindAgent("bmw-sd-unbind", "bmw-sd-unbind-agent-1", 1)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, agent0)
			_ = k8sClient.Delete(ctx, agent1)
		})

		co = getClusterOrder("bmw-sd-unbind")
		Expect(co.Status.Workers).To(HaveLen(2))
		Expect(co.Status.Workers[0].Phase).To(Equal("Ready"))
		Expect(co.Status.Workers[1].Phase).To(Equal("Ready"))
		retainedName := co.Status.Workers[0].Name

		co.Spec.NodeRequests[0].NumberOfNodes = 1
		Expect(k8sClient.Update(ctx, co)).To(Succeed())

		_, err = runReconcile("bmw-sd-unbind")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-unbind")
		var unbindingWorker *osacv1alpha1.WorkerStatus
		for i := range co.Status.Workers {
			if co.Status.Workers[i].Phase == "Unbinding" {
				unbindingWorker = &co.Status.Workers[i]
			}
		}
		Expect(unbindingWorker).ToNot(BeNil(), "excess worker should be in Unbinding phase")

		Expect(clearWorkerAgentBinding(ctx, "bmw-sd-unbind-agent-1", testNamespace)).To(Succeed())

		deletesBeforeUnbind := len(fc.DeleteCalls())

		_, err = runReconcile("bmw-sd-unbind")
		Expect(err).ToNot(HaveOccurred())

		Expect(fc.DeleteCalls()).To(HaveLen(deletesBeforeUnbind), "Agent deletion must finish before BMI deletion")
		Expect(getClusterOrder("bmw-sd-unbind").Status.Workers).To(ContainElement(HaveField("Phase", "Unbinding")))
		_, err = runReconcile("bmw-sd-unbind") // Re-observe Agent absence, then request BMI deletion.
		Expect(err).ToNot(HaveOccurred())
		co = getClusterOrder("bmw-sd-unbind")
		var deletingWorker *osacv1alpha1.WorkerStatus
		for i := range co.Status.Workers {
			if co.Status.Workers[i].Name == unbindingWorker.Name {
				deletingWorker = &co.Status.Workers[i]
			}
		}
		Expect(deletingWorker).ToNot(BeNil(), "worker should still exist during Deleting phase")
		Expect(deletingWorker.Phase).To(Equal("Deleting"))

		deletesAfterUnbind := fc.DeleteCalls()
		Expect(len(deletesAfterUnbind)).To(BeNumerically(">", deletesBeforeUnbind),
			"BMI should have been deleted")

		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "bmw-sd-unbind-agent-1", Namespace: testNamespace}, agentObj)
		Expect(err).To(HaveOccurred(), "Agent CR should have been deleted")

		_, err = runReconcile("bmw-sd-unbind")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-unbind")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Name).To(Equal(retainedName))
		Expect(co.Status.Workers[0].Phase).To(Equal("Ready"))
	})

	It("unbinding timeout sets AgentUnbindingTimeout reason without replacement", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-sd-timeout", 2)
		create(co)
		makeInfraEnvReady("bmw-sd-timeout")

		_, err := runReconcile("bmw-sd-timeout")
		Expect(err).ToNot(HaveOccurred())

		agent0 := registerAndBindAgent("bmw-sd-timeout", "bmw-sd-timeout-agent-0", 0)
		agent1 := registerAndBindAgent("bmw-sd-timeout", "bmw-sd-timeout-agent-1", 1)
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, agent0)
			_ = k8sClient.Delete(ctx, agent1)
		})

		co = getClusterOrder("bmw-sd-timeout")
		excessName := co.Status.Workers[1].Name
		co.Spec.NodeRequests[0].NumberOfNodes = 1
		Expect(k8sClient.Update(ctx, co)).To(Succeed())

		_, err = runReconcile("bmw-sd-timeout")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-timeout")
		for i := range co.Status.Workers {
			if co.Status.Workers[i].Phase == "Unbinding" {
				pastTime := metav1.NewTime(time.Now().Add(-31 * time.Minute))
				co.Status.Workers[i].LastFailureTime = &pastTime
			}
		}
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

		_, err = runReconcile("bmw-sd-timeout")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-timeout")
		var timedOutWorker *osacv1alpha1.WorkerStatus
		for i := range co.Status.Workers {
			if co.Status.Workers[i].Name == excessName {
				timedOutWorker = &co.Status.Workers[i]
			}
		}
		Expect(timedOutWorker).ToNot(BeNil())
		Expect(timedOutWorker.Phase).To(Equal("Unbinding"), "worker should stay in Unbinding, not transition to Failed")
		Expect(timedOutWorker.LastFailureReason).To(Equal("AgentUnbindingTimeout"))
	})

	It("retains worker in Deleting until BMI deletion is confirmed", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-sd-delwait", 2)
		create(co)
		makeInfraEnvReady("bmw-sd-delwait")

		_, err := runReconcile("bmw-sd-delwait")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-delwait")
		Expect(co.Status.Workers).To(HaveLen(2))

		excessName := co.Status.Workers[1].Name
		co.Status.Workers[1].Phase = "Deleting"
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

		co = getClusterOrder("bmw-sd-delwait")
		co.Spec.NodeRequests[0].NumberOfNodes = 1
		Expect(k8sClient.Update(ctx, co)).To(Succeed())

		fc.SetDeleteError(fmt.Errorf("temporary failure"))

		_, err = runReconcile("bmw-sd-delwait")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-delwait")
		var deletingWorker *osacv1alpha1.WorkerStatus
		for i := range co.Status.Workers {
			if co.Status.Workers[i].Name == excessName {
				deletingWorker = &co.Status.Workers[i]
			}
		}
		Expect(deletingWorker).ToNot(BeNil(), "worker should be retained in Deleting while BMI exists")
		Expect(deletingWorker.Phase).To(Equal("Deleting"))

		fc.SetDeleteError(nil)

		// Reconcile — retries delete (succeeds, removes BMI from map), stays in Deleting.
		_, err = runReconcile("bmw-sd-delwait")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-delwait")
		deletingWorker = nil
		for i := range co.Status.Workers {
			if co.Status.Workers[i].Name == excessName {
				deletingWorker = &co.Status.Workers[i]
			}
		}
		Expect(deletingWorker).ToNot(BeNil(), "worker should still be in Deleting after retry succeeds")
		Expect(deletingWorker.Phase).To(Equal("Deleting"))

		// Reconcile — GetBMI returns NotFound, entry removed.
		_, err = runReconcile("bmw-sd-delwait")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-delwait")
		for _, w := range co.Status.Workers {
			Expect(w.Name).ToNot(Equal(excessName),
				"worker should be removed after BMI deletion confirmed")
		}
	})

	It("requeues when teardown workers exist", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-sd-requeue", 2)
		create(co)
		makeInfraEnvReady("bmw-sd-requeue")

		_, err := runReconcile("bmw-sd-requeue")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-requeue")
		co.Spec.NodeRequests[0].NumberOfNodes = 1
		Expect(k8sClient.Update(ctx, co)).To(Succeed())

		res, err := runReconcile("bmw-sd-requeue")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0), "should requeue while teardown is in progress")
	})
})

var _ = Describe("BareMetalWorkerReconciler cluster deletion", Label("baremetalworker"), func() {
	const (
		clusterUUID       = "deletion-cluster-uuid"
		cvID              = "4.18.0"
		diskImageID       = "rhcos-4.18"
		clusterIDLabel    = "osac.openshift.io/clusterorder-uuid"
		bmWorkerFinalizer = "osac.openshift.io/baremetalworker-finalizer"
	)

	var (
		fc  *workerFulfillmentStub
		ign *workerIgnitionEndpoint
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {

		fc = newWorkerFulfillmentStub()
		ign = newWorkerIgnitionEndpoint()
		rec = events.NewFakeRecorder(20)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterUUID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("bm-standard", "data-0"))
	}

	newBareMetalClusterOrder := func(name string, numWorkers int) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNamespace,
				Labels:      map[string]string{clusterIDLabel: clusterUUID},
				Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{NodeSet: "bm-standard",
					NumberOfNodes: numWorkers,
					BareMetal: &osacv1alpha1.BareMetalNodeSpec{
						InstanceType: "bm-standard",
					},
				}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
	}

	runReconcile := func(name string) (reconcile.Result, error) {
		return driveWorkerCheckpoints(r, fc, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
	}

	getClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		GinkgoHelper()
		co := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, co)).To(Succeed())
		return co
	}

	create := func(co *osacv1alpha1.ClusterOrder) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(co),
				})
			}
			ie := newInfraEnv(co.Name + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})
	}

	makeInfraEnvReady := func(name string) {
		GinkgoHelper()
		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(setWorkerIgnitionURL(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
	}

	provisionWorkers := func(name string, numWorkers int) {
		GinkgoHelper()
		preloadDiskImageChain()
		co := newBareMetalClusterOrder(name, numWorkers)
		create(co)
		makeInfraEnvReady(name)

		_, err := runReconcile(name)
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder(name)
		Expect(co.Status.Workers).To(HaveLen(numWorkers))
		Expect(controllerutil.ContainsFinalizer(co, bmWorkerFinalizer)).To(BeTrue(),
			"finalizer should be added during normal reconciliation")
	}

	It("adds the finalizer during normal reconciliation", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-del-fin", 1)
		create(co)
		makeInfraEnvReady("bmw-del-fin")

		_, err := runReconcile("bmw-del-fin")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-del-fin")
		Expect(controllerutil.ContainsFinalizer(co, bmWorkerFinalizer)).To(BeTrue())
	})

	It("deletes all BMIs on cluster deletion and removes finalizer when confirmed", func() {
		provisionWorkers("bmw-del-all", 2)

		co := getClusterOrder("bmw-del-all")
		workerIDs := make([]string, len(co.Status.Workers))
		for i, w := range co.Status.Workers {
			workerIDs[i] = w.BareMetalInstance.ID
		}

		// Delete the ClusterOrder — envtest doesn't enforce finalizers, so we delete directly.
		Expect(k8sClient.Delete(ctx, co)).To(Succeed())

		// The fake removes BMIs on Delete, so processDeletingWorker confirms deletion in the
		// same reconcile that marks workers Deleting. All workers are removed and the finalizer
		// is cleared in a single pass.
		_, err := runReconcile("bmw-del-all")
		Expect(err).ToNot(HaveOccurred())

		deletes := fc.DeleteCalls()
		for _, id := range workerIDs {
			Expect(deletes).To(ContainElement(id), "should have called Delete for BMI %s", id)
		}

		// After finalizer removal, envtest completes the delete — the object is gone.
		// The reconciler returning NotFound is the expected terminal state.
		_, err = runReconcile("bmw-del-all")
		Expect(err).ToNot(HaveOccurred())
	})

	It("waits for CAP-Agent to unbind a bound worker before deleting its Agent and BMI", func() {
		provisionWorkers("bmw-del-bound", 1)
		co := getClusterOrder("bmw-del-bound")
		worker := co.Status.Workers[0]
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: "bmw-del-bound-agent", Namespace: testNamespace, MAC: "aa:bb:cc:dd:ee:01",
			ClusterDeploymentName: co.Name,
		})).To(Succeed())
		agent := &unstructured.Unstructured{}
		agent.SetGroupVersionKind(agentGVK)
		key := types.NamespacedName{Name: "bmw-del-bound-agent", Namespace: testNamespace}
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
		agent.SetLabels(map[string]string{
			"osac.openshift.io/cluster-order": co.Name,
			"osac.openshift.io/worker-name":   worker.Name,
		})
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agent) })

		Expect(k8sClient.Delete(ctx, co)).To(Succeed())
		_, err := runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(BeEmpty(), "BMI must remain until CAP-Agent unbinds the worker")
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed(), "OSAC must not delete the bound Agent")
		co = getClusterOrder(co.Name)
		Expect(co.Status.Workers[0].Phase).To(Equal("Unbinding"))
		Expect(controllerutil.ContainsFinalizer(co, bmWorkerFinalizer)).To(BeTrue())

		Expect(clearWorkerAgentBinding(ctx, agent.GetName(), testNamespace)).To(Succeed())
		_, err = runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(BeEmpty(), "Agent Delete is not permission to delete BMI in the same invocation")
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, agent))).To(BeTrue())
		_, err = runReconcile(co.Name) // Authoritative old Agent absence.
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(ContainElement(worker.BareMetalInstance.ID))
	})

	It("releases a known-unbound worker only after reclaim and detachment", func() {
		provisionWorkers("bmw-del-known-unbound", 1)
		co := getClusterOrder("bmw-del-known-unbound")
		worker := co.Status.Workers[0]
		key := types.NamespacedName{Name: "bmw-del-known-unbound-agent", Namespace: testNamespace}
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: key.Name, Namespace: key.Namespace, MAC: "aa:bb:cc:dd:ee:02",
			ClusterDeploymentName: co.Name,
		})).To(Succeed())
		agent := &unstructured.Unstructured{}
		agent.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
		agent.SetLabels(map[string]string{
			"osac.openshift.io/cluster-order": co.Name,
			"osac.openshift.io/worker-name":   worker.Name,
			"agentMachineRef":                 "machine-0",
			"clusterdeployment-namespace":     testNamespace,
		})
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agent) })

		Expect(k8sClient.Delete(ctx, co)).To(Succeed())
		_, err := runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(BeEmpty())
		Expect(getClusterOrder(co.Name).Status.Workers[0].Phase).To(Equal("Unbinding"))
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())

		Expect(unstructured.SetNestedField(agent.Object, "reclaiming", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		_, err = runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(BeEmpty(), "BMI must remain while the Agent is reclaiming")
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
		Expect(getClusterOrder(co.Name).Status.Workers[0].Phase).To(Equal("Unbinding"))

		Expect(clearWorkerAgentBinding(ctx, key.Name, key.Namespace)).To(Succeed())
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
		labels := agent.GetLabels()
		delete(labels, "agentMachineRef")
		agent.SetLabels(labels)
		Expect(unstructured.SetNestedField(agent.Object, "reclaiming-rebooting", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		_, err = runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(BeEmpty(), "detachment alone is not enough while reclaiming")
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
		Expect(getClusterOrder(co.Name).Status.Workers[0].Phase).To(Equal("Unbinding"))

		Expect(unstructured.SetNestedField(agent.Object, "known-unbound", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		_, err = runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, agent))).To(BeTrue())
		Expect(fc.DeleteCalls()).To(BeEmpty())
		_, err = runReconcile(co.Name) // Confirm Agent absence before BMI deletion.
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(ContainElement(worker.BareMetalInstance.ID))
		_, err = runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(co), co))).To(BeTrue(),
			"worker finalizer must clear after BMI deletion is confirmed")
	})

	It("holds a known-unbound worker when its Agent still has a binding reference", func() {
		provisionWorkers("bmw-del-stale-binding", 1)
		co := getClusterOrder("bmw-del-stale-binding")
		worker := co.Status.Workers[0]
		key := types.NamespacedName{Name: "bmw-del-stale-binding-agent", Namespace: testNamespace}
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: key.Name, Namespace: key.Namespace, MAC: "aa:bb:cc:dd:ee:03",
			ClusterDeploymentName: co.Name,
		})).To(Succeed())
		agent := &unstructured.Unstructured{}
		agent.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
		agent.SetLabels(map[string]string{
			"osac.openshift.io/cluster-order": co.Name,
			"osac.openshift.io/worker-name":   worker.Name,
		})
		Expect(unstructured.SetNestedField(agent.Object, "known-unbound", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agent) })
		Expect(k8sClient.Delete(ctx, co)).To(Succeed())
		_, err := runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(BeEmpty())
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
		co = getClusterOrder(co.Name)
		Expect(co.Status.Workers[0].Phase).To(Equal("Unbinding"))
		Expect(controllerutil.ContainsFinalizer(co, bmWorkerFinalizer)).To(BeTrue())
	})

	It("holds a known-unbound worker while its AgentMachine reference label remains", func() {
		provisionWorkers("bmw-del-stale-machine", 1)
		co := getClusterOrder("bmw-del-stale-machine")
		worker := co.Status.Workers[0]
		key := types.NamespacedName{Name: "bmw-del-stale-machine-agent", Namespace: testNamespace}
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: key.Name, Namespace: key.Namespace, MAC: "aa:bb:cc:dd:ee:04",
		})).To(Succeed())
		agent := &unstructured.Unstructured{}
		agent.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
		agent.SetLabels(map[string]string{
			"osac.openshift.io/cluster-order": co.Name,
			"osac.openshift.io/worker-name":   worker.Name,
			"agentMachineRef":                 "machine-0",
		})
		Expect(unstructured.SetNestedField(agent.Object, "known-unbound", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agent) })
		Expect(k8sClient.Delete(ctx, co)).To(Succeed())
		_, err := runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(BeEmpty())
		Expect(k8sClient.Get(ctx, key, agent)).To(Succeed())
		co = getClusterOrder(co.Name)
		Expect(co.Status.Workers[0].Phase).To(Equal("Unbinding"))
		Expect(controllerutil.ContainsFinalizer(co, bmWorkerFinalizer)).To(BeTrue())
	})

	It("holds the finalizer while workers are still being deleted", func() {
		provisionWorkers("bmw-del-hold", 1)

		co := getClusterOrder("bmw-del-hold")
		bmiID := co.Status.Workers[0].BareMetalInstance.ID

		// Re-add the BMI to the fake so Get doesn't return NotFound after Delete.
		fc.SetDeleteError(fmt.Errorf("transient API error"))

		Expect(k8sClient.Delete(ctx, co)).To(Succeed())

		_, err := runReconcile("bmw-del-hold")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-del-hold")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Phase).To(Equal("Deleting"))
		Expect(controllerutil.ContainsFinalizer(co, bmWorkerFinalizer)).To(BeTrue(),
			"finalizer must not be removed while workers remain")
		_ = bmiID
	})

	It("retries a failed Delete on the next reconcile", func() {
		provisionWorkers("bmw-del-retry", 1)

		co := getClusterOrder("bmw-del-retry")
		Expect(k8sClient.Delete(ctx, co)).To(Succeed())

		fc.SetDeleteError(fmt.Errorf("transient error"))

		_, err := runReconcile("bmw-del-retry")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-del-retry")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Phase).To(Equal("Deleting"))

		// Clear the error and reconcile again — the BMI was not removed from the fake
		// because the first Delete returned an error, so processDeletingWorker retries.
		fc.SetDeleteError(nil)

		_, err = runReconcile("bmw-del-retry")
		Expect(err).ToNot(HaveOccurred())

		// After successful delete + confirm, the finalizer is removed and envtest
		// completes the deletion — the object is gone.
		_, err = runReconcile("bmw-del-retry")
		Expect(err).ToNot(HaveOccurred())
	})

	It("recovers a created BMI with an unrecorded ID before cluster deletion", func() {
		provisionWorkers("bmw-del-pending-reference", 1)
		co := getClusterOrder("bmw-del-pending-reference")
		bmiID := co.Status.Workers[0].BareMetalInstance.ID
		co.Status.Workers[0].BareMetalInstance.ID = ""
		co.Status.Workers[0].Phase = "Provisioning"
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())
		Expect(k8sClient.Delete(ctx, co)).To(Succeed())
		_, err := runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(ContainElement(bmiID), "finalization must recover the reference rather than orphan the created BMI")
	})

	It("holds finalization rather than inferring a missing BMI name", func() {
		provisionWorkers("bmw-del-missing-name", 1)
		co := getClusterOrder("bmw-del-missing-name")
		recorded := co.Status.Workers[0].BareMetalInstance
		co.Status.Workers[0].BareMetalInstance = osacv1alpha1.BareMetalInstanceReference{}
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())
		Expect(k8sClient.Delete(ctx, co)).To(Succeed())

		_, err := runReconcile(co.Name)
		Expect(err).To(MatchError(ContainSubstring("bareMetalInstance.name is required")))
		co = getClusterOrder(co.Name)
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].BareMetalInstance).To(BeZero())
		Expect(controllerutil.ContainsFinalizer(co, bmWorkerFinalizer)).To(BeTrue())
		Expect(fc.DeleteCalls()).To(BeEmpty())

		// Repair with the recorded reference, not a guess, so normal teardown can finish.
		co.Status.Workers[0].BareMetalInstance = recorded
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())
		_, err = runReconcile(co.Name)
		Expect(err).ToNot(HaveOccurred())
	})

	It("removes workers whose recorded BMI name has no matching instance", func() {
		provisionWorkers("bmw-del-noid", 1)

		// Add an explicitly reserved worker whose BMI was never created.
		co := getClusterOrder("bmw-del-noid")
		co.Status.Workers = append(co.Status.Workers, osacv1alpha1.WorkerStatus{
			Name:              "bmw-del-noid-orphan",
			BareMetalInstance: osacv1alpha1.BareMetalInstanceReference{Name: "never-created-bmi"},
			Kind:              "BareMetalInstance",
			Phase:             "WaitingForAgent",
			CreationTimestamp: metav1.Now(),
		})
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

		Expect(k8sClient.Delete(ctx, co)).To(Succeed())

		// First reconcile: the worker with no BMI ID is removed immediately by
		// processDeletingWorker; the worker with a BMI ID is deleted from the
		// fake and confirmed in the same pass. Both workers cleared, finalizer
		// removed, envtest completes the delete.
		_, err := runReconcile("bmw-del-noid")
		Expect(err).ToNot(HaveOccurred())

		// Object is gone — reconcile returns NotFound (ignored).
		_, err = runReconcile("bmw-del-noid")
		Expect(err).ToNot(HaveOccurred())
	})
})
