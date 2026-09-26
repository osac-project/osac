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
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker/fake"
	"github.com/osac-project/osac/osac-operator/internal/testing/envsim"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

func workerClient() (crclient.Client, *runtime.Scheme) {
	config, err := clientcmd.BuildConfigFromFlags("", connectedConfig.kubeconfig)
	Expect(err).NotTo(HaveOccurred())
	scheme := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	client, err := crclient.New(config, crclient.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	return client, scheme
}

func workerOrder(ctx context.Context, client crclient.Client, cluster *privatev1.Cluster) *v1alpha1.ClusterOrder {
	orders := &v1alpha1.ClusterOrderList{}
	Eventually(func(g Gomega) {
		g.Expect(client.List(ctx, orders, crclient.InNamespace(connectedConfig.namespace), crclient.MatchingLabels{
			"osac.openshift.io/clusterorder-uuid": cluster.GetId(),
		})).To(Succeed())
		g.Expect(orders.Items).To(HaveLen(1), "real fulfillment reconciler must create the order")
	}, 90*time.Second, 500*time.Millisecond).Should(Succeed())
	return &orders.Items[0]
}

var _ = Describe("production worker against real fulfillment", func() {
	It("creates tenant-owned BMIs and status for the real two-type order", func(ctx context.Context) {
		ensureWorkerTemplate(ctx)
		advanceDefaultNetworking(ctx) // Explicit network-readiness simulation, not real networking coverage.
		cluster := createCaaSClusterWithNodeSets(ctx, true)
		client, scheme := workerClient()
		order := workerOrder(ctx, client, cluster)
		defaultFilter := fmt.Sprintf(
			`this.metadata.labels['osac.openshift.io/default'] == 'true' && this.metadata.tenant == %q`, simTenantName)
		subnets, err := privatev1.NewSubnetsClient(fulfillmentConn).List(ctx,
			privatev1.SubnetsListRequest_builder{Filter: &defaultFilter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(subnets.GetItems()).To(HaveLen(1))
		latestCluster, err := fulfillmentClient.GetCluster(ctx, cluster.GetId())
		Expect(err).NotTo(HaveOccurred())
		clusterSubnet := latestCluster.GetSpec().GetNetworkAttachment().GetSubnet()
		defaultSubnet := subnets.GetItems()[0]
		Expect(defaultSubnet.GetMetadata().GetTenant()).To(Equal(simTenantName))
		Expect(defaultSubnet.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/default", "true"))
		vnID := defaultSubnet.GetSpec().GetVirtualNetwork().GetId()
		Expect(vnID).NotTo(BeEmpty())
		vnResponse, err := privatev1.NewVirtualNetworksClient(fulfillmentConn).Get(ctx,
			privatev1.VirtualNetworksGetRequest_builder{Id: vnID}.Build())
		Expect(err).NotTo(HaveOccurred())
		classID := verifySimDefaultFabricManager(ctx, vnResponse.GetObject())
		Expect(clusterSubnet.GetId()).To(Equal(defaultSubnet.GetId()))
		GinkgoWriter.Printf("network boundary: cluster subnet ID=%q name=%q, order subnetRef=%q, "+
			"default subnet ID=%q name=%q state=%s\n",
			clusterSubnet.GetId(), clusterSubnet.GetName(), order.Spec.NetworkAttachment.SubnetRef,
			defaultSubnet.GetId(), defaultSubnet.GetMetadata().GetName(), defaultSubnet.GetStatus().GetState())
		ignition := fake.NewIgnitionServer()
		DeferCleanup(ignition.Close)
		r := baremetalworker.NewReconciler(client, client, scheme,
			baremetalworker.NewFulfillmentClientFromConn(fulfillmentConn),
			baremetalworker.NewIgnitionFetcher(nil), events.NewFakeRecorder(50), connectedConfig.namespace)
		key := crclient.ObjectKeyFromObject(order)
		// Register cleanup after the Cluster fixture so it runs first. The real
		// ownership check currently rejects a deleting order when GetCluster no
		// longer resolves its archived Cluster. Explicitly verify and delete only
		// this spec's BMIs before releasing its deleting order's finalizer.
		// This is test-only cleanup, NOT coverage of production worker teardown.
		DeferCleanup(func(cleanupCtx context.Context) {
			_, err := privatev1.NewClustersClient(fulfillmentConn).Delete(cleanupCtx,
				privatev1.ClustersDeleteRequest_builder{Id: cluster.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred(), "start cleanup of worker-owned Cluster %s", cluster.GetId())
			Eventually(func(g Gomega) {
				latest := &v1alpha1.ClusterOrder{}
				err := client.Get(cleanupCtx, key, latest)
				if apierrors.IsNotFound(err) {
					return
				}
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(latest.DeletionTimestamp.IsZero()).To(BeFalse())
				g.Expect(latest.Labels).To(HaveKeyWithValue("osac.openshift.io/clusterorder-uuid", cluster.GetId()))
				g.Expect(latest.Annotations).To(HaveKeyWithValue("osac.openshift.io/tenant", cluster.GetMetadata().GetTenant()))
				filter := fmt.Sprintf(`this.metadata.labels["osac.openshift.io/cluster-order"] == %q`, key.Name)
				bmis, err := fulfillmentClient.ListBareMetalInstances(cleanupCtx, filter)
				g.Expect(err).NotTo(HaveOccurred())
				byID := make(map[string]*privatev1.BareMetalInstance, len(bmis))
				for _, bmi := range bmis {
					g.Expect(bmi.GetMetadata().GetTenant()).To(Equal(cluster.GetMetadata().GetTenant()))
					g.Expect(bmi.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(
						"osac.openshift.io/owner-reference", "ClusterOrder/"+key.Name))
					byID[bmi.GetId()] = bmi
				}
				for _, worker := range latest.Status.Workers {
					g.Expect(worker.ResourceID).NotTo(BeEmpty(), "refuse to clear finalizer with untracked worker")
					bmi, exists := byID[worker.ResourceID]
					if !exists {
						_, getErr := fulfillmentClient.GetBareMetalInstance(cleanupCtx, worker.ResourceID)
						g.Expect(status.Code(getErr)).To(Equal(codes.NotFound), "untracked worker resource may not be deleted")
						continue
					}
					g.Expect(bmi.GetMetadata().GetName()).To(Equal(worker.Name))
					g.Expect(fulfillmentClient.DeleteBareMetalInstance(cleanupCtx, bmi.GetId())).To(Succeed())
					delete(byID, bmi.GetId())
				}
				g.Expect(byID).To(BeEmpty(), "do not remove finalizer with unexpected BMI")
				remaining, err := fulfillmentClient.ListBareMetalInstances(cleanupCtx, filter)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(remaining).To(BeEmpty(), "do not remove finalizer until owned BMIs are deleted")
				base := latest.DeepCopy()
				if controllerutil.RemoveFinalizer(latest, "osac.openshift.io/baremetalworker-finalizer") {
					g.Expect(client.Patch(cleanupCtx, latest, crclient.MergeFrom(base))).To(Succeed())
				}
			}, 10*time.Second, 500*time.Millisecond).Should(Succeed())
		})
		reconcile := func() ctrl.Result {
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred(), "worker Reconcile for %s", key)
			return result
		}
		reconcile()
		infraEnv := &unstructured.Unstructured{}
		infraEnv.SetAPIVersion("agent-install.openshift.io/v1beta1")
		infraEnv.SetKind("InfraEnv")
		Expect(client.Get(ctx, crclient.ObjectKey{Namespace: key.Namespace, Name: key.Name + "-infraenv"},
			infraEnv)).To(Succeed(), "worker must create discovery InfraEnv")
		Expect(envsim.New(client).MarkInfraEnvReady(ctx, infraEnv.GetName(), key.Namespace, ignition.URL())).To(Succeed())
		// The first post-discovery reconcile is synchronous. Surface API contract
		// failures immediately rather than polling a known permanent failure.
		result := reconcile()
		Eventually(func(g Gomega) {
			latest := &v1alpha1.ClusterOrder{}
			g.Expect(client.Get(ctx, key, latest)).To(Succeed())
			g.Expect(latest.Status.Workers).To(HaveLen(3), "status=%+v result=%+v", latest.Status, result)
		}, 10*time.Second, 500*time.Millisecond).Should(Succeed())
		latest := &v1alpha1.ClusterOrder{}
		Expect(client.Get(ctx, key, latest)).To(Succeed())
		actualTenant, err := fulfillmentClient.GetCluster(ctx, cluster.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(latest.Status.DesiredWorkers).NotTo(BeNil())
		Expect(*latest.Status.DesiredWorkers).To(Equal(int32(3)))
		Expect(latest.Status.CurrentWorkers).NotTo(BeNil())
		Expect(*latest.Status.CurrentWorkers).To(Equal(int32(3)))
		Expect(latest.Status.ReadyWorkers).NotTo(BeNil())
		Expect(*latest.Status.ReadyWorkers).To(Equal(int32(0)))
		wanted := make(map[string]float64)
		for _, nr := range order.Spec.NodeRequests {
			wanted[nr.BareMetal.InstanceType] = float64(nr.NumberOfNodes)
		}
		Expect(wanted).To(HaveLen(2))
		seen := make(map[string]bool)
		for _, worker := range latest.Status.Workers {
			Expect(worker.Phase).To(Equal("WaitingForAgent"))
			Expect(worker.ResourceID).NotTo(BeEmpty())
			Expect(wanted).To(HaveKey(worker.InstanceType))
			Expect(seen).NotTo(HaveKey(worker.Name), "each slot needs a distinct worker")
			seen[worker.Name] = true
		}
		families, err := metrics.Registry.Gather()
		Expect(err).NotTo(HaveOccurred(), "collect in-process worker metrics")
		for _, name := range []string{"osac_caas_worker_desired", "osac_caas_worker_ready"} {
			var samples map[string]float64
			for _, family := range families {
				if family.GetName() != name {
					continue
				}
				samples = make(map[string]float64)
				for _, sample := range family.GetMetric() {
					labels := make(map[string]string)
					for _, label := range sample.GetLabel() {
						labels[label.GetName()] = label.GetValue()
					}
					Expect(labels).To(HaveKeyWithValue("tenant", actualTenant.GetMetadata().GetTenant()))
					Expect(labels).To(HaveKeyWithValue("worker_type", "bare_metal"))
					Expect(wanted).To(HaveKey(labels["instance_type"]))
					samples[labels["instance_type"]] = sample.GetGauge().GetValue()
				}
			}
			Expect(samples).NotTo(BeNil(), "%s must be registered", name)
			Expect(samples).To(HaveLen(2), "no unrelated series in %s", name)
			for instanceType, desired := range wanted {
				value := desired
				if name == "osac_caas_worker_ready" {
					value = 0
				}
				Expect(samples).To(HaveKeyWithValue(instanceType, value), name)
			}
		}
		for _, worker := range latest.Status.Workers {
			bmi, err := fulfillmentClient.GetBareMetalInstance(ctx, worker.ResourceID)
			Expect(err).NotTo(HaveOccurred(), "worker %s", worker.Name)
			Expect(bmi.GetMetadata().GetTenant()).To(Equal(actualTenant.GetMetadata().GetTenant()))
			Expect(bmi.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(
				"osac.openshift.io/owner-reference", "ClusterOrder/"+key.Name))
			Expect(bmi.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/cluster-order", key.Name))
			Expect(bmi.GetSpec().GetInstanceType().GetName()).To(Equal(worker.InstanceType))
			Expect(bmi.GetSpec().GetNetworkAttachments()).To(HaveLen(1))
			Expect(bmi.GetSpec().GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal(defaultSubnet.GetId()),
				"worker BMI must use the tenant default subnet on CUDN class %s", classID)
			Expect(bmi.GetSpec().GetDiskImage().GetId()).NotTo(BeEmpty())
			Expect(bmi.GetSpec().GetUserData()).To(ContainSubstring("\"ignition\""))
		}
		// Retain the old sim suite's real-Postgres UNIQUE-constraint regression
		// (OSAC-3266) on a worker created by the production reconciler.
		first, err := fulfillmentClient.GetBareMetalInstance(ctx, latest.Status.Workers[0].ResourceID)
		Expect(err).NotTo(HaveOccurred())
		_, err = fulfillmentClient.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Name: first.GetMetadata().GetName(), Tenant: actualTenant.GetMetadata().GetTenant(),
				Labels: first.GetMetadata().GetLabels(), Annotations: first.GetMetadata().GetAnnotations(),
			}.Build(),
			Spec: first.GetSpec(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.AlreadyExists),
			"duplicate worker BMI name must be rejected by real DB: %v", err)
	})

	It("rejects a separate wrong-tenant order before creating a BMI", func(ctx context.Context) {
		cluster := createCaaSCluster(ctx)
		client, scheme := workerClient()
		realOrder := workerOrder(ctx, client, cluster)
		name := fixtureName("mismatch")
		negative := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: connectedConfig.namespace,
				Labels:      map[string]string{"osac.openshift.io/clusterorder-uuid": cluster.GetId()},
				Annotations: map[string]string{"osac.openshift.io/tenant": "wrong-caas-tenant"},
			},
			Spec: realOrder.Spec,
		}
		Expect(client.Create(ctx, negative)).To(Succeed())
		DeferCleanup(func(ctx context.Context) {
			latest := &v1alpha1.ClusterOrder{}
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(negative), latest); apierrors.IsNotFound(err) {
				return
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(latest.Finalizers).To(BeEmpty(), "ownership failure must not add a finalizer")
			Expect(client.Delete(ctx, latest)).To(Succeed())
		})
		recorder := events.NewFakeRecorder(5)
		r := baremetalworker.NewReconciler(client, client, scheme,
			baremetalworker.NewFulfillmentClientFromConn(fulfillmentConn),
			baremetalworker.NewIgnitionFetcher(nil), recorder, connectedConfig.namespace)
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: crclient.ObjectKeyFromObject(negative)})
		Expect(err).To(MatchError(ContainSubstring("worker ownership: missing or mismatched ClusterOrder/Cluster tenant")))
		select {
		case event := <-recorder.Events:
			Expect(event).To(ContainSubstring("WorkerOwnershipMismatch"))
		case <-time.After(time.Second):
			Fail("ownership mismatch event was not emitted")
		}
		filter := fmt.Sprintf(`this.metadata.labels["osac.openshift.io/cluster-order"] == %q`, name)
		bmis, err := fulfillmentClient.ListBareMetalInstances(ctx, filter)
		Expect(err).NotTo(HaveOccurred())
		Expect(bmis).To(BeEmpty(), "wrong-tenant order must not create a BMI")
	})
})
