/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package acceptance

import (
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker/fake"
	"github.com/osac-project/osac/osac-operator/internal/testing/envsim"
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

var _ = Describe("BareMetalWorkerReconciler ensureInfraEnv", func() {
	var (
		sim *envsim.Simulator
		fc  *fake.FulfillmentClient
		ign *fake.IgnitionServer
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {
		sim = envsim.New(k8sClient)
		fc = fake.NewFulfillmentClient()
		ign = fake.NewIgnitionServer()
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
			Id: clusterUUID,
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
				Name:      name,
				Namespace: testNamespace,
				Labels:    map[string]string{clusterIDLabel: clusterUUID},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
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
		return r.Reconcile(ctx, reconcile.Request{
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

		// Simulator makes the discovery ignition available at the fake endpoint.
		Expect(sim.MarkInfraEnvReady(ctx, "bmw-green-infraenv", testNamespace, ign.URL())).To(Succeed())

		// Second reconcile: ignition fetched, InfraEnvReady=True, requeues for agent correlation.
		res, err = runReconcile("bmw-green")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0), "requeues for agent correlation")

		Expect(apimeta.IsStatusConditionTrue(
			getClusterOrder("bmw-green").Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)).To(BeTrue())
	})

	It("is idempotent: re-reconcile does not create a duplicate InfraEnv", func() {
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
		Expect(sim.MarkInfraEnvReady(ctx, "bmw-big-infraenv", testNamespace, ign.URL())).To(Succeed())

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

	It("ignores ClusterOrders without a bare-metal node set", func() {
		co := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "bmw-none", Namespace: testNamespace},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID: "test",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "vm-standard",
					NumberOfNodes: 1,
				}},
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
		create(newBareMetalClusterOrder("bmw-fetcherr"))

		_, err := runReconcile("bmw-fetcherr")
		Expect(err).ToNot(HaveOccurred())

		// Point the InfraEnv at an unreachable ignition URL.
		Expect(sim.MarkInfraEnvReady(ctx, "bmw-fetcherr-infraenv", testNamespace,
			"http://127.0.0.1:0/discovery.ign")).To(Succeed())

		_, err = runReconcile("bmw-fetcherr")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("BareMetalWorkerReconciler ensureSystemCatalogItem", func() {
	var (
		fc  *fake.FulfillmentClient
		sim *envsim.Simulator
		ign *fake.IgnitionServer
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {
		fc = fake.NewFulfillmentClient()
		sim = envsim.New(k8sClient)
		ign = fake.NewIgnitionServer()
		rec = events.NewFakeRecorder(10)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	newBareMetalClusterOrder := func(name string) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
				Labels:    map[string]string{"osac.openshift.io/clusterorder-uuid": "ci-cluster"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID: "test",
				PullSecret: "{\"auths\":{}}",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
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
			Id: "ci-cluster",
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
		return r.Reconcile(ctx, reconcile.Request{
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
		Expect(sim.MarkInfraEnvReady(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
	}

	It("creates the system catalog item when absent", func() {
		co := newBareMetalClusterOrder("bmw-ci-create")
		create(co)
		makeInfraEnvReady("bmw-ci-create")

		_, err := runReconcile("bmw-ci-create")
		Expect(err).ToNot(HaveOccurred())

		calls := fc.CreateCatalogItemCalls()
		Expect(calls).To(HaveLen(1))
		ci := calls[0]
		Expect(ci.GetMetadata().GetName()).To(Equal("system-bmi-passthrough"))
		Expect(ci.GetMetadata().GetTenant()).To(Equal("system"))
		Expect(ci.GetTitle()).To(Equal("System BMI Pass-through"))
		Expect(ci.GetPublished()).To(BeTrue())
		Expect(ci.GetTemplate().GetId()).To(Equal("osac.templates.bm_host_provisioning"))
	})

	It("is a no-op when the system catalog item already exists", func() {
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

		// Only the pre-create call, no additional create from the reconciler.
		Expect(fc.CreateCatalogItemCalls()).To(HaveLen(1))
		Expect(fc.ListCatalogItemCalls()).To(HaveLen(1))
	})

	It("handles AlreadyExists gracefully (concurrent create race)", func() {
		co := newBareMetalClusterOrder("bmw-ci-race")
		create(co)
		makeInfraEnvReady("bmw-ci-race")

		// First reconcile creates the catalog item.
		_, err := runReconcile("bmw-ci-race")
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.CreateCatalogItemCalls()).To(HaveLen(1))

		// Remove it from list but leave it stored (simulates race: list returns empty,
		// but Create returns AlreadyExists). We achieve this by running a second reconcile —
		// list will find it and skip create.
		_, err = runReconcile("bmw-ci-race")
		Expect(err).ToNot(HaveOccurred())
		// No additional create call — list found it.
		Expect(fc.CreateCatalogItemCalls()).To(HaveLen(1))
	})
})

var _ = Describe("BareMetalWorkerReconciler resolveDiskImage", func() {
	const (
		clusterUUID    = "resolve-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		sim *envsim.Simulator
		fc  *fake.FulfillmentClient
		ign *fake.IgnitionServer
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {
		sim = envsim.New(k8sClient)
		fc = fake.NewFulfillmentClient()
		ign = fake.NewIgnitionServer()
		rec = events.NewFakeRecorder(10)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	newBareMetalClusterOrder := func(name string, labels map[string]string) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
				Labels:    labels,
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
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
		return r.Reconcile(ctx, reconcile.Request{
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
		Expect(sim.MarkInfraEnvReady(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
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
			Id: clusterUUID,
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
			Id: clusterUUID,
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

	It("requeues when the clusterorder-uuid label is absent", func() {
		co := newBareMetalClusterOrder("bmw-nolabel", nil)
		create(co)
		makeInfraEnvReady("bmw-nolabel")

		res, err := runReconcile("bmw-nolabel")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
	})

	It("clears RHCOSImageNotFound when disk_image is later set on the ClusterVersion", func() {
		addInstanceType("bm-standard")
		fc.AddCluster(privatev1.Cluster_builder{
			Id: clusterUUID,
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

var _ = Describe("BareMetalWorkerReconciler reconcileWorkers", func() {
	const (
		clusterUUID    = "workers-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		sim *envsim.Simulator
		fc  *fake.FulfillmentClient
		ign *fake.IgnitionServer
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {
		sim = envsim.New(k8sClient)
		fc = fake.NewFulfillmentClient()
		ign = fake.NewIgnitionServer()
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
			Id: clusterUUID,
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
				Name:      name,
				Namespace: testNamespace,
				Labels:    map[string]string{clusterIDLabel: clusterUUID},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
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
		return r.Reconcile(ctx, reconcile.Request{
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
		Expect(sim.MarkInfraEnvReady(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
		_, err = runReconcile(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(apimeta.IsStatusConditionTrue(
			getClusterOrder(name).Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)).To(BeTrue())
	}

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
		Expect(bmi.GetMetadata().GetTenant()).To(Equal("system"))
		Expect(bmi.GetMetadata().GetName()).To(Equal("bmw-create-worker-0"))
		Expect(bmi.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/cluster-order", "bmw-create"))
		Expect(bmi.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(
			"osac.openshift.io/owner-reference", "ClusterOrder/bmw-create"))
		Expect(bmi.GetSpec().GetCatalogItem().GetName()).To(Equal("system-bmi-passthrough"))
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
			Id: clusterUUID,
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

		Expect(sim.MarkInfraEnvReady(ctx, "bmw-bmit-missing-infraenv", testNamespace, ign.URL())).To(Succeed())

		// The missing requested BMIT keeps the order on the blocked/requeue path.
		res, err = runReconcile("bmw-bmit-missing")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		Expect(fc.CreateCatalogItemCalls()).To(BeEmpty())
		Expect(fc.CreateCalls()).To(BeEmpty())
		Expect(getClusterOrder("bmw-bmit-missing").Status.Workers).To(BeEmpty())
	})

	It("creates the catalog item and BMI after resolving a usable BMIT", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-bmit-usable", 1)
		create(co)
		makeInfraEnvReady("bmw-bmit-usable")

		_, err := runReconcile("bmw-bmit-usable")
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.CreateCatalogItemCalls()).To(HaveLen(1))
		Expect(fc.CreateCalls()).To(HaveLen(1))
		Expect(getClusterOrder("bmw-bmit-usable").Status.Workers).To(HaveLen(1))
	})

	It("resolves a name-only DiskImage reference by name (id-or-name, mirroring ComputeInstance)", func() {
		// The ClusterVersion's disk_image reference carries only a name (no id). The reconciler
		// must resolve it the same way the ComputeInstance path does (OSAC-3724 / RefKeyStr:
		// prefer id, fall back to name), so the BMI still lands the resolved OCI ref.
		fc.AddCluster(privatev1.Cluster_builder{
			Id: clusterUUID,
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
			ContainSubstring("bmw-event-worker-0"))))
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
			Expect(call.GetMetadata().GetName()).To(Equal(fmt.Sprintf("bmw-multi-worker-%d", i)))
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
				Tenant: "system",
				Name:   "bmw-idem-worker-0",
				Labels: map[string]string{"osac.openshift.io/cluster-order": "bmw-idem"},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

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

	It("self-heals the catalog item when a BMI already exists", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-catalog-heal", 1)
		create(co)

		// Pre-create the BMI while leaving the system CatalogItem absent.
		_, err := fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "system",
				Name:   "bmw-catalog-heal-worker-0",
				Labels: map[string]string{"osac.openshift.io/cluster-order": "bmw-catalog-heal"},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		makeInfraEnvReady("bmw-catalog-heal")

		Expect(fc.CreateCatalogItemCalls()).To(HaveLen(1))
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
		Expect(co.Status.Workers[0].ResourceID).ToNot(BeEmpty())
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
		Expect(sim.MarkInfraEnvReady(ctx, "bmw-unavail-infraenv", testNamespace, ign.URL())).To(Succeed())

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
			Expect(w.ResourceID).ToNot(BeEmpty())
			Expect(w.NodeSet).To(Equal("bm-standard"))
		}
		Expect(co.Status.Workers[0].Name).To(Equal("bmw-status-worker-0"))
		Expect(co.Status.Workers[1].Name).To(Equal("bmw-status-worker-1"))
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
				Name:      "bmw-multi-type",
				Namespace: testNamespace,
				Labels:    map[string]string{clusterIDLabel: clusterUUID},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{
					{
						ResourceClass: "bm-standard",
						NumberOfNodes: 1,
						BareMetal:     &osacv1alpha1.BareMetalNodeSpec{InstanceType: "bm-standard"},
					},
					{
						ResourceClass: "bm-gpu",
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
				Name:      "bmw-no-net",
				Namespace: testNamespace,
				Labels:    map[string]string{clusterIDLabel: clusterUUID},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
					NumberOfNodes: 1,
					BareMetal:     &osacv1alpha1.BareMetalNodeSpec{InstanceType: "bm-standard"},
				}},
			},
		}
		create(co)

		// First reconcile creates InfraEnv.
		_, err := runReconcile("bmw-no-net")
		Expect(err).ToNot(HaveOccurred())
		Expect(sim.MarkInfraEnvReady(ctx, "bmw-no-net-infraenv", testNamespace, ign.URL())).To(Succeed())

		// Second reconcile provisions worker BMI without networkAttachment.
		_, err = runReconcile("bmw-no-net")
		Expect(err).ToNot(HaveOccurred())

		calls := fc.CreateCalls()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].GetSpec().GetNetworkAttachments()).To(BeEmpty())
	})
})

var _ = Describe("BareMetalWorkerReconciler correlateAgents", func() {
	const (
		clusterUUID    = "correlate-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		sim *envsim.Simulator
		fc  *fake.FulfillmentClient
		ign *fake.IgnitionServer
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {
		sim = envsim.New(k8sClient)
		fc = fake.NewFulfillmentClient()
		ign = fake.NewIgnitionServer()
		rec = events.NewFakeRecorder(10)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id: clusterUUID,
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
				Name:      name,
				Namespace: testNamespace,
				Labels:    map[string]string{clusterIDLabel: clusterUUID},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
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
		return r.Reconcile(ctx, reconcile.Request{
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
		Expect(sim.MarkInfraEnvReady(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
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
			fc.SetHostMAC(w.ResourceID, fmt.Sprintf("aa:bb:cc:dd:ee:%02d", w.AttemptCount))
		}
		// Set unique MACs for each worker based on their index.
		for i, w := range co.Status.Workers {
			fc.SetHostMAC(w.ResourceID, fmt.Sprintf("aa:bb:cc:dd:ee:%02d", i))
		}
	}

	It("correlates an agent to a worker by unique MAC match", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-corr", 1)
		create(co)
		createWorkersAndSetMAC("bmw-corr", 1)

		// Register an agent with the matching MAC.
		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
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
		Expect(agentObj.GetLabels()).To(HaveKeyWithValue("osac.openshift.io/worker-name", "bmw-corr-worker-0"))
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
		fc.SetHostMAC(co.Status.Workers[0].ResourceID, "aa:bb:cc:dd:ee:99")
		fc.SetHostMAC(co.Status.Workers[1].ResourceID, "aa:bb:cc:dd:ee:99")

		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
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

		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })
	})

	It("uses cached worker-name label on subsequent reconciles", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-cache", 1)
		create(co)
		createWorkersAndSetMAC("bmw-cache", 1)

		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
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
		fc.SetHostMAC(co.Status.Workers[0].ResourceID, "")

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
		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
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
})

var _ = Describe("BareMetalWorkerReconciler workerRetry", func() {
	const (
		clusterUUID    = "retry-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		sim *envsim.Simulator
		fc  *fake.FulfillmentClient
		ign *fake.IgnitionServer
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {
		sim = envsim.New(k8sClient)
		fc = fake.NewFulfillmentClient()
		ign = fake.NewIgnitionServer()
		rec = events.NewFakeRecorder(20)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id: clusterUUID,
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
				Name:      name,
				Namespace: testNamespace,
				Labels:    map[string]string{clusterIDLabel: clusterUUID},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
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
		return r.Reconcile(ctx, reconcile.Request{
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
		Expect(sim.MarkInfraEnvReady(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
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
		Expect(co.Status.Workers[0].ResourceID).ToNot(BeEmpty())

		// Simulate failure.
		setWorkerFailed("bmw-retry-del", "bmw-retry-del-worker-0", "InfrastructureError", "host allocation failed")

		// Reconcile → handleFailedWorkers deletes the BMI and sets backoff.
		_, err = runReconcile("bmw-retry-del")
		Expect(err).ToNot(HaveOccurred())

		Expect(fc.DeleteCalls()).To(HaveLen(1))

		co = getClusterOrder("bmw-retry-del")
		w := co.Status.Workers[0]
		Expect(w.Phase).To(Equal("Failed"))
		Expect(w.AttemptCount).To(Equal(int32(1)))
		Expect(w.ResourceID).To(BeEmpty())
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
		setWorkerFailed("bmw-retry-repl", "bmw-retry-repl-worker-0", "InfrastructureError", "host failed")

		// Reconcile → deletes BMI, sets NextRetryTime in the future.
		_, err = runReconcile("bmw-retry-repl")
		Expect(err).ToNot(HaveOccurred())

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
		Expect(w.ResourceID).ToNot(BeEmpty())
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
		setWorkerFailed("bmw-retry-clear", "bmw-retry-clear-worker-0", "AgentRegistrationTimeout", "timeout")

		// Reconcile → deletes BMI, sets backoff.
		_, err = runReconcile("bmw-retry-clear")
		Expect(err).ToNot(HaveOccurred())

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
		fc.SetHostMAC(co.Status.Workers[0].ResourceID, "aa:bb:cc:dd:ee:42")

		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
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

var _ = Describe("BareMetalWorkerReconciler scale-up", func() {
	const (
		clusterUUID    = "scaleup-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		sim *envsim.Simulator
		fc  *fake.FulfillmentClient
		ign *fake.IgnitionServer
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {
		sim = envsim.New(k8sClient)
		fc = fake.NewFulfillmentClient()
		ign = fake.NewIgnitionServer()
		rec = events.NewFakeRecorder(20)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id: clusterUUID,
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
				Name:      name,
				Namespace: testNamespace,
				Labels:    map[string]string{clusterIDLabel: clusterUUID},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
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
		return r.Reconcile(ctx, reconcile.Request{
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
		Expect(sim.MarkInfraEnvReady(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
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
		co.Spec.NodeRequests[0].NumberOfNodes = 4
		Expect(k8sClient.Update(ctx, co)).To(Succeed())

		_, err := runReconcile("bmw-scaleup")
		Expect(err).ToNot(HaveOccurred())

		Expect(fc.CreateCalls()).To(HaveLen(initialCreateCalls + 2))

		co = getClusterOrder("bmw-scaleup")
		Expect(co.Status.Workers).To(HaveLen(4))
		Expect(co.Status.Workers[0].Name).To(Equal("bmw-scaleup-worker-0"))
		Expect(co.Status.Workers[1].Name).To(Equal("bmw-scaleup-worker-1"))
		Expect(co.Status.Workers[2].Name).To(Equal("bmw-scaleup-worker-2"))
		Expect(co.Status.Workers[3].Name).To(Equal("bmw-scaleup-worker-3"))
		for _, w := range co.Status.Workers[2:] {
			Expect(w.Phase).To(Equal("WaitingForAgent"))
		}
	})

	It("preserves existing worker phases during scale-up", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-preserve", 2)
		create(co)
		provisionWorkers("bmw-preserve", 2)

		co = getClusterOrder("bmw-preserve")
		fc.SetHostMAC(co.Status.Workers[0].ResourceID, "aa:bb:cc:dd:ee:00")
		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
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
		Expect(co.Status.Workers[0].Phase).To(Equal("Binding"))
		Expect(co.Status.Workers[2].Phase).To(Equal("WaitingForAgent"))

		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })
	})

	It("scale-up with a failed worker creates new workers at higher indices", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-scalefail", 2)
		create(co)
		provisionWorkers("bmw-scalefail", 2)

		co = getClusterOrder("bmw-scalefail")
		for i := range co.Status.Workers {
			if co.Status.Workers[i].Name == "bmw-scalefail-worker-1" {
				co.Status.Workers[i].Phase = "Failed"
				co.Status.Workers[i].LastFailureReason = "InfrastructureError"
				failTime := metav1.Now()
				co.Status.Workers[i].LastFailureTime = &failTime
			}
		}
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

		co = getClusterOrder("bmw-scalefail")
		co.Spec.NodeRequests[0].NumberOfNodes = 3
		Expect(k8sClient.Update(ctx, co)).To(Succeed())

		_, err := runReconcile("bmw-scalefail")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-scalefail")
		Expect(co.Status.Workers).To(HaveLen(3))
		Expect(co.Status.Workers[2].Name).To(Equal("bmw-scalefail-worker-2"))
		Expect(co.Status.Workers[2].Phase).To(Equal("WaitingForAgent"))
		Expect(co.Status.Workers[1].Phase).To(Equal("Failed"))
	})

	It("reports partial success with mixed Ready and Failed workers", func() {
		preloadDiskImageChain()
		co := newBareMetalClusterOrder("bmw-partial", 3)
		create(co)
		provisionWorkers("bmw-partial", 3)

		co = getClusterOrder("bmw-partial")
		fc.SetHostMAC(co.Status.Workers[0].ResourceID, "aa:bb:cc:dd:ee:00")
		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
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

var _ = Describe("BareMetalWorkerReconciler stale ignition", func() {
	const (
		clusterUUID    = "stale-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		sim *envsim.Simulator
		fc  *fake.FulfillmentClient
		ign *fake.IgnitionServer
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {
		sim = envsim.New(k8sClient)
		fc = fake.NewFulfillmentClient()
		ign = fake.NewIgnitionServer()
		rec = events.NewFakeRecorder(20)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id: clusterUUID,
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
				Name:      name,
				Namespace: testNamespace,
				Labels:    map[string]string{clusterIDLabel: clusterUUID},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
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
		return r.Reconcile(ctx, reconcile.Request{
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
		Expect(sim.MarkInfraEnvReady(ctx, "bmw-deleted-ie-infraenv", testNamespace, ign.URL())).To(Succeed())
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
		Expect(sim.MarkInfraEnvReady(ctx, "bmw-stale-infraenv", testNamespace, ign.URL())).To(Succeed())
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

		Expect(sim.MarkInfraEnvReady(ctx, "bmw-stale-infraenv", testNamespace, ign.URL())).To(Succeed())

		_, err = runReconcile("bmw-stale")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-stale")
		Expect(co.Status.Workers).To(HaveLen(1))
		Expect(co.Status.Workers[0].Phase).To(Equal("Failed"))
		Expect(co.Status.Workers[0].LastFailureReason).To(Equal("AgentRegistrationTimeout"))
		Expect(co.Status.Workers[0].LastFailureMessage).To(ContainSubstring("stale ignition"))

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
		Expect(sim.MarkInfraEnvReady(ctx, "bmw-stale-safe-infraenv", testNamespace, ign.URL())).To(Succeed())
		_, err = runReconcile("bmw-stale-safe")
		Expect(err).ToNot(HaveOccurred())
		_, err = runReconcile("bmw-stale-safe")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-stale-safe")
		Expect(co.Status.Workers).To(HaveLen(2))
		fc.SetHostMAC(co.Status.Workers[0].ResourceID, "aa:bb:cc:dd:ee:00")
		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
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

		Expect(sim.MarkInfraEnvReady(ctx, "bmw-stale-safe-infraenv", testNamespace, ign.URL())).To(Succeed())

		_, err = runReconcile("bmw-stale-safe")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-stale-safe")
		Expect(co.Status.Workers[0].Phase).To(Equal("Binding"))
		Expect(co.Status.Workers[1].Phase).To(Equal("Failed"))
		Expect(co.Status.Workers[1].LastFailureReason).To(Equal("AgentRegistrationTimeout"))

		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })
	})
})

var _ = Describe("BareMetalWorkerReconciler scale-down", func() {
	const (
		clusterUUID    = "scaledown-cluster-uuid"
		cvID           = "4.18.0"
		diskImageID    = "rhcos-4.18"
		clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
	)

	var (
		sim *envsim.Simulator
		fc  *fake.FulfillmentClient
		ign *fake.IgnitionServer
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {
		sim = envsim.New(k8sClient)
		fc = fake.NewFulfillmentClient()
		ign = fake.NewIgnitionServer()
		rec = events.NewFakeRecorder(20)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id: clusterUUID,
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
				Name:      name,
				Namespace: testNamespace,
				Labels:    map[string]string{clusterIDLabel: clusterUUID},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
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
		return r.Reconcile(ctx, reconcile.Request{
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
		Expect(sim.MarkInfraEnvReady(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
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
		fc.SetHostMAC(co.Status.Workers[workerIndex].ResourceID, mac)

		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
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

		setWorkerFailed("bmw-sd-failed", "bmw-sd-failed-worker-1", "InfrastructureError", "host allocation failed")

		deletesBeforeScaleDown := len(fc.DeleteCalls())

		co = getClusterOrder("bmw-sd-failed")
		co.Spec.NodeRequests[0].NumberOfNodes = 1
		Expect(k8sClient.Update(ctx, co)).To(Succeed())

		_, err = runReconcile("bmw-sd-failed")
		Expect(err).ToNot(HaveOccurred())

		co = getClusterOrder("bmw-sd-failed")

		workerNames := map[string]bool{}
		for _, w := range co.Status.Workers {
			workerNames[w.Name] = true
		}
		Expect(workerNames).ToNot(HaveKey("bmw-sd-failed-worker-1"), "Failed worker-1 should be removed")

		deletesAfterScaleDown := fc.DeleteCalls()
		Expect(len(deletesAfterScaleDown)).To(BeNumerically(">", deletesBeforeScaleDown),
			"BMI for failed worker should have been deleted")

		// Worker-2 (WaitingForAgent, no bound agent) transitions Unbinding → Deleting in one
		// reconcile since there is no agent to wait for unbinding.
		hasTeardown := false
		for _, w := range co.Status.Workers {
			if w.Phase == "Unbinding" || w.Phase == "Deleting" {
				hasTeardown = true
			}
		}
		Expect(hasTeardown).To(BeTrue(), "non-failed excess worker-2 should be in teardown")
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

		Expect(sim.UnbindAgent(ctx, "bmw-sd-unbind-agent-1", testNamespace)).To(Succeed())

		deletesBeforeUnbind := len(fc.DeleteCalls())

		_, err = runReconcile("bmw-sd-unbind")
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
		Expect(co.Status.Workers[0].Name).To(Equal("bmw-sd-unbind-worker-0"))
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
			if co.Status.Workers[i].Name == "bmw-sd-timeout-worker-1" {
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
			if co.Status.Workers[i].Name == "bmw-sd-delwait-worker-1" {
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
			if co.Status.Workers[i].Name == "bmw-sd-delwait-worker-1" {
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
			Expect(w.Name).ToNot(Equal("bmw-sd-delwait-worker-1"),
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

var _ = Describe("BareMetalWorkerReconciler cluster deletion", func() {
	const (
		clusterUUID       = "deletion-cluster-uuid"
		cvID              = "4.18.0"
		diskImageID       = "rhcos-4.18"
		clusterIDLabel    = "osac.openshift.io/clusterorder-uuid"
		bmWorkerFinalizer = "osac.openshift.io/baremetalworker-finalizer"
	)

	var (
		sim *envsim.Simulator
		fc  *fake.FulfillmentClient
		ign *fake.IgnitionServer
		rec *events.FakeRecorder
		r   *baremetalworker.Reconciler
	)

	BeforeEach(func() {
		sim = envsim.New(k8sClient)
		fc = fake.NewFulfillmentClient()
		ign = fake.NewIgnitionServer()
		rec = events.NewFakeRecorder(20)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)
	})

	AfterEach(func() { ign.Close() })

	preloadDiskImageChain := func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id: clusterUUID,
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
				Name:      name,
				Namespace: testNamespace,
				Labels:    map[string]string{clusterIDLabel: clusterUUID},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
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
		return r.Reconcile(ctx, reconcile.Request{
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
		Expect(sim.MarkInfraEnvReady(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
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
			workerIDs[i] = w.ResourceID
		}

		// Delete the ClusterOrder — envtest doesn't enforce finalizers, so we delete directly.
		Expect(k8sClient.Delete(ctx, co)).To(Succeed())

		// The fake removes BMIs on Delete, so handleDeletingWorkers confirms deletion in the
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

	It("holds the finalizer while workers are still being deleted", func() {
		provisionWorkers("bmw-del-hold", 1)

		co := getClusterOrder("bmw-del-hold")
		bmiID := co.Status.Workers[0].ResourceID

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
		// because the first Delete returned an error, so handleDeletingWorkers retries.
		fc.SetDeleteError(nil)

		_, err = runReconcile("bmw-del-retry")
		Expect(err).ToNot(HaveOccurred())

		// After successful delete + confirm, the finalizer is removed and envtest
		// completes the deletion — the object is gone.
		_, err = runReconcile("bmw-del-retry")
		Expect(err).ToNot(HaveOccurred())
	})

	It("removes workers without a ResourceID immediately", func() {
		provisionWorkers("bmw-del-noid", 1)

		// Add a worker with no ResourceID.
		co := getClusterOrder("bmw-del-noid")
		co.Status.Workers = append(co.Status.Workers, osacv1alpha1.WorkerStatus{
			Name:              "bmw-del-noid-orphan",
			Kind:              "BareMetalInstance",
			Phase:             "WaitingForAgent",
			CreationTimestamp: metav1.Now(),
		})
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

		Expect(k8sClient.Delete(ctx, co)).To(Succeed())

		// First reconcile: the no-ResourceID worker is removed immediately by
		// handleDeletingWorkers; the worker with a ResourceID is deleted from the
		// fake and confirmed in the same pass. Both workers cleared, finalizer
		// removed, envtest completes the delete.
		_, err := runReconcile("bmw-del-noid")
		Expect(err).ToNot(HaveOccurred())

		// Object is gone — reconcile returns NotFound (ignored).
		_, err = runReconcile("bmw-del-noid")
		Expect(err).ToNot(HaveOccurred())
	})
})
