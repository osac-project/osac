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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker/fake"
	"github.com/osac-project/osac/osac-operator/internal/testing/envsim"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// These tests intentionally do not trust the fake List filter: it returns every BMI.
// Each foreign BMI carries matching, but editable, correlation metadata.
var _ = Describe("BareMetalWorker tenant safety", func() {
	const (
		clusterID = "tenant-safety-cluster"
		tenant    = "tenant1"
		cvID      = "4.18.0"
		imageID   = "rhcos-4.18"
		finalizer = "osac.openshift.io/baremetalworker-finalizer"
	)
	var (
		fc  *fake.FulfillmentClient
		ign *fake.IgnitionServer
		sim *envsim.Simulator
		r   *baremetalworker.Reconciler
	)
	BeforeEach(func() {
		fc = fake.NewFulfillmentClient()
		ign = fake.NewIgnitionServer()
		sim = envsim.New(k8sClient)
		r = baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), events.NewFakeRecorder(20), testNamespace)
		fc.AddCluster(privatev1.Cluster_builder{
			Id:       clusterID,
			Metadata: privatev1.Metadata_builder{Tenant: tenant}.Build(),
			Spec:     privatev1.ClusterSpec_builder{Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build()}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID, Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: imageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: imageID, Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY, SourceRef: diskImageSourceRef,
			}.Build(),
		}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("bm-standard", "data-0"))
	})
	AfterEach(func() { ign.Close() })

	order := func(name string, count int) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: testNamespace,
				Labels:      map[string]string{"osac.openshift.io/clusterorder-uuid": clusterID},
				Annotations: map[string]string{"osac.openshift.io/tenant": tenant},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID: "test", PullSecret: `{"auths":{}}`, SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests:      []osacv1alpha1.NodeRequest{{NumberOfNodes: count, BareMetal: &osacv1alpha1.BareMetalNodeSpec{InstanceType: "bm-standard"}}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{SubnetRef: "my-subnet", SecurityGroupRefs: []string{"sg-default"}},
			},
		}
	}
	getOrder := func(name string) *osacv1alpha1.ClusterOrder {
		GinkgoHelper()
		co := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, co)).To(Succeed())
		return co
	}
	run := func(name string) (reconcile.Result, error) {
		return r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: name}})
	}
	createOrder := func(co *osacv1alpha1.ClusterOrder) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		// Never reconcile an invalid/foreign status during cleanup: remove only this test's K8s objects.
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest) == nil {
				if len(latest.Finalizers) > 0 {
					latest.Finalizers = nil
					_ = k8sClient.Update(ctx, latest)
				}
				if latest.DeletionTimestamp.IsZero() {
					_ = k8sClient.Delete(ctx, latest)
				}
			}
			_ = k8sClient.Delete(ctx, newInfraEnv(co.Name+"-infraenv"))
		})
	}
	ready := func(name string) {
		GinkgoHelper()
		_, err := run(name)
		Expect(err).ToNot(HaveOccurred())
		Expect(sim.MarkInfraEnvReady(ctx, name+"-infraenv", testNamespace, ign.URL())).To(Succeed())
	}
	foreign := func(orderName, bmiName string) string {
		GinkgoHelper()
		bmi, err := fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "tenant2", Name: bmiName,
				Labels:      map[string]string{"osac.openshift.io/cluster-order": orderName},
				Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + orderName},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		return bmi.GetId()
	}
	setStatus := func(name, workerName, bmiID, phase string) {
		GinkgoHelper()
		co := getOrder(name)
		co.Status.Workers = []osacv1alpha1.WorkerStatus{{
			Kind: "BareMetalInstance", Name: workerName, NodeSet: "bm-standard", Phase: phase,
			ResourceID: bmiID, CreationTimestamp: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())
	}
	assertForeignUntouched := func(name, bmiID, workerName string) {
		GinkgoHelper()
		Expect(fc.DeleteCalls()).ToNot(ContainElement(bmiID))
		stored, err := fc.GetBareMetalInstance(ctx, bmiID)
		Expect(err).ToNot(HaveOccurred())
		Expect(stored.GetMetadata().GetTenant()).To(Equal("tenant2"))
		co := getOrder(name)
		Expect(co.Status.Workers).To(ContainElement(SatisfyAll(
			HaveField("Name", workerName), HaveField("ResourceID", bmiID))),
			"foreign status ID must not be cleared, adopted or marked deleted")
	}

	for _, tc := range []struct {
		name   string
		mutate func(*osacv1alpha1.ClusterOrder)
	}{
		{"missing order ID", func(co *osacv1alpha1.ClusterOrder) { delete(co.Labels, "osac.openshift.io/clusterorder-uuid") }},
		{"unknown order ID", func(co *osacv1alpha1.ClusterOrder) {
			co.Labels["osac.openshift.io/clusterorder-uuid"] = "unknown-cluster"
		}},
		{"missing order tenant", func(co *osacv1alpha1.ClusterOrder) { delete(co.Annotations, "osac.openshift.io/tenant") }},
		{"mismatched order tenant", func(co *osacv1alpha1.ClusterOrder) { co.Annotations["osac.openshift.io/tenant"] = "tenant2" }},
	} {
		It("rejects "+tc.name+" before privileged worker creation", func() {
			name := "safety-" + strings.ToLower(strings.ReplaceAll(tc.name, " ", "-"))
			co := order(name, 1)
			tc.mutate(co)
			createOrder(co)
			_, err := run(name)
			Expect(err).To(HaveOccurred(), "invalid order identity must report a failure, not silently requeue")
			Expect(fc.CreateCalls()).To(BeEmpty())
			Expect(fc.CreateCatalogItemCalls()).To(BeEmpty())
			Expect(getOrder(name).Status.Workers).To(BeEmpty())
		})
	}

	It("rejects missing authoritative Cluster tenant before worker creation", func() {
		fc.AddCluster(privatev1.Cluster_builder{
			Id: clusterID, Spec: privatev1.ClusterSpec_builder{Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build()}.Build(),
		}.Build())
		co := order("safety-empty-cluster-tenant", 1)
		createOrder(co)
		_, _ = run(co.Name)
		Expect(fc.CreateCalls()).To(BeEmpty())
		Expect(fc.CreateCatalogItemCalls()).To(BeEmpty())
	})

	It("does not adopt another tenant's same-named worker with a matching order label", func() {
		co := order("safety-foreign-name", 1)
		createOrder(co)
		id := foreign(co.Name, co.Name+"-worker-0")
		ready(co.Name)
		_, _ = run(co.Name)
		Expect(fc.DeleteCalls()).ToNot(ContainElement(id))
		stored, err := fc.GetBareMetalInstance(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		Expect(stored.GetMetadata().GetTenant()).To(Equal("tenant2"))
		for _, w := range getOrder(co.Name).Status.Workers {
			Expect(w.ResourceID).ToNot(Equal(id), "must not adopt a foreign BMI from an unfiltered List")
		}
	})

	It("retries a failed worker whose prior BMI has already disappeared", func() {
		co := order("safety-missing-failed-bmi", 1)
		createOrder(co)
		ready(co.Name)
		setStatus(co.Name, co.Name+"-worker-0", "deleted-bmi-id", "Failed")
		_, err := run(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.DeleteCalls()).To(BeEmpty(), "already absent BMI must not be deleted by stale ID")
		workers := getOrder(co.Name).Status.Workers
		Expect(workers).To(HaveLen(1))
		Expect(workers[0].ResourceID).To(BeEmpty(), "stale ID must not block retry indefinitely")
		Expect(workers[0].AttemptCount).To(Equal(int32(1)))
	})

	for _, phase := range []string{"Failed", "Deleting", "Finalizing"} {
		It("does not delete another tenant's status ID on "+phase, func() {
			name := "safety-foreign-" + strings.ToLower(phase)
			co := order(name, 1)
			createOrder(co)
			ready(name)
			workerName := name + "-worker-0"
			id := foreign(name, workerName)
			statusPhase := phase
			if phase == "Finalizing" {
				statusPhase = "WaitingForAgent"
			}
			setStatus(name, workerName, id, statusPhase)
			if phase == "Finalizing" {
				co = getOrder(name)
				Expect(co.Finalizers).To(ContainElement(finalizer))
				Expect(k8sClient.Delete(ctx, co)).To(Succeed())
			}
			_, _ = run(name)
			assertForeignUntouched(name, id, workerName)
		})
	}

	It("does not delete a same-tenant BMI with a different worker name from status", func() {
		co := order("safety-wrong-worker-name", 1)
		createOrder(co)
		co = getOrder(co.Name)
		co.Finalizers = append(co.Finalizers, finalizer)
		Expect(k8sClient.Update(ctx, co)).To(Succeed())
		bmi, err := fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: tenant, Name: "other-worker",
				Labels:      map[string]string{"osac.openshift.io/cluster-order": co.Name},
				Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + co.Name},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		workerName := co.Name + "-worker-0"
		setStatus(co.Name, workerName, bmi.GetId(), "Deleting")
		Expect(k8sClient.Delete(ctx, getOrder(co.Name))).To(Succeed())
		_, _ = run(co.Name)
		Expect(fc.DeleteCalls()).ToNot(ContainElement(bmi.GetId()))
		_, err = fc.GetBareMetalInstance(ctx, bmi.GetId())
		Expect(err).ToNot(HaveOccurred())
		Expect(getOrder(co.Name).Status.Workers).To(ContainElement(HaveField("ResourceID", bmi.GetId())))
	})

	It("does not delete a same-tenant BMI owned by another order", func() {
		co := order("safety-wrong-owner", 1)
		createOrder(co)
		co = getOrder(co.Name)
		co.Finalizers = append(co.Finalizers, finalizer)
		Expect(k8sClient.Update(ctx, co)).To(Succeed())
		name := co.Name + "-worker-0"
		bmi, err := fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: tenant, Name: name,
				Labels:      map[string]string{"osac.openshift.io/cluster-order": "another-order"},
				Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/another-order"},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		setStatus(co.Name, name, bmi.GetId(), "Deleting")
		Expect(k8sClient.Delete(ctx, getOrder(co.Name))).To(Succeed())
		_, _ = run(co.Name)
		Expect(fc.DeleteCalls()).ToNot(ContainElement(bmi.GetId()))
		_, err = fc.GetBareMetalInstance(ctx, bmi.GetId())
		Expect(err).ToNot(HaveOccurred())
		Expect(getOrder(co.Name).Status.Workers).To(ContainElement(HaveField("ResourceID", bmi.GetId())))
	})

	It("reuses an owned worker after listing while ignoring unrelated BMIs", func() {
		co := order("safety-owned-recovery", 1)
		createOrder(co)
		bmi, err := fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: tenant, Name: co.Name + "-worker-0",
				Labels:      map[string]string{"osac.openshift.io/cluster-order": co.Name},
				Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + co.Name},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		foreign(co.Name, "some-other-worker")
		ready(co.Name)
		_, err = run(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(getOrder(co.Name).Status.Workers).To(ContainElement(HaveField("ResourceID", bmi.GetId())))
		Expect(fc.DeleteCalls()).To(BeEmpty())
		Expect(fc.CreateCalls()).To(HaveLen(2), "re-list should reuse the owned BMI, not create a third")
	})

	It("checks an owned BMI during AlreadyExists re-list recovery", func() {
		co := order("safety-owned-race", 1)
		createOrder(co)
		bmi, err := fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: tenant, Name: co.Name + "-worker-0",
				Labels:      map[string]string{"osac.openshift.io/cluster-order": co.Name},
				Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + co.Name},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		ready(co.Name)
		fc.SetListEmptyOnce()
		fc.SetCreateError(status.Error(codes.AlreadyExists, "concurrent owned create"))
		_, err = run(co.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.ListCalls()).To(HaveLen(2), "must re-list after AlreadyExists")
		Expect(getOrder(co.Name).Status.Workers).To(ContainElement(HaveField("ResourceID", bmi.GetId())))
	})

	It("does not adopt a foreign BMI during AlreadyExists re-list recovery", func() {
		co := order("safety-foreign-race", 1)
		createOrder(co)
		id := foreign(co.Name, co.Name+"-worker-0")
		ready(co.Name)
		fc.SetListEmptyOnce()
		fc.SetCreateError(status.Error(codes.AlreadyExists, "concurrent foreign create"))
		_, _ = run(co.Name)
		Expect(fc.ListCalls()).To(HaveLen(2), "must re-list after AlreadyExists")
		Expect(fc.DeleteCalls()).ToNot(ContainElement(id))
		stored, err := fc.GetBareMetalInstance(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		Expect(stored.GetMetadata().GetTenant()).To(Equal("tenant2"))
		for _, w := range getOrder(co.Name).Status.Workers {
			Expect(w.ResourceID).ToNot(Equal(id), "must not adopt another tenant's BMI after a create race")
		}
	})

	for _, reason := range []string{"missing", "non-shared"} {
		It("fails worker creation when the shared direct template is "+reason, func() {
			co := order("safety-template-"+reason, 1)
			createOrder(co)
			ready(co.Name)
			fc.SetCreateError(status.Error(codes.InvalidArgument, "shared template "+reason))
			_, err := run(co.Name)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("shared template " + reason))
			Expect(fc.CreateCalls()).To(HaveLen(1))
			Expect(fc.CreateCalls()[0].GetSpec().GetTemplate().GetShared()).To(BeTrue())
			Expect(fc.CreateCatalogItemCalls()).To(BeEmpty())
			Expect(getOrder(co.Name).Status.Workers).To(BeEmpty())
		})
	}

	It("does not correlate a foreign status ID during rebuild", func() {
		co := order("safety-foreign-rebuild", 1)
		createOrder(co)
		id := foreign(co.Name, "some-other-worker")
		setStatus(co.Name, co.Name+"-worker-0", id, "WaitingForAgent")
		_, err := run(co.Name)
		Expect(err).To(HaveOccurred(), "foreign status ID must be rejected before rebuild or agent correlation")
		Expect(fc.DeleteCalls()).ToNot(ContainElement(id))
		Expect(getOrder(co.Name).Status.Workers).To(ContainElement(HaveField("ResourceID", id)),
			"foreign status must remain visible for safe manual recovery, not be rebound or dropped")
	})

	It("does not delete an ID during finalization when the order has no Cluster ID", func() {
		co := order("safety-missing-id-finalizer", 1)
		delete(co.Labels, "osac.openshift.io/clusterorder-uuid")
		createOrder(co)
		co = getOrder(co.Name)
		co.Finalizers = append(co.Finalizers, finalizer)
		Expect(k8sClient.Update(ctx, co)).To(Succeed())
		name := co.Name + "-worker-0"
		id := foreign(co.Name, name)
		setStatus(co.Name, name, id, "Deleting")
		Expect(k8sClient.Delete(ctx, getOrder(co.Name))).To(Succeed())
		_, _ = run(co.Name)
		assertForeignUntouched(co.Name, id, name)
	})

	It("does not delete a foreign status ID when the order tenant disagrees during finalization", func() {
		co := order("safety-mismatched-finalizer", 1)
		co.Annotations["osac.openshift.io/tenant"] = "tenant2"
		createOrder(co)
		// A terminating order can have persisted status from an earlier reconcile.
		co = getOrder(co.Name)
		co.Finalizers = append(co.Finalizers, finalizer)
		Expect(k8sClient.Update(ctx, co)).To(Succeed())
		name := co.Name + "-worker-0"
		id := foreign(co.Name, name)
		setStatus(co.Name, name, id, "Deleting")
		Expect(k8sClient.Delete(ctx, getOrder(co.Name))).To(Succeed())
		_, _ = run(co.Name)
		assertForeignUntouched(co.Name, id, name)
	})
})
