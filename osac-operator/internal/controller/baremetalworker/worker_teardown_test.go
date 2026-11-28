// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/assistedservice"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type teardownReadClient struct {
	*workerReadClient
	returned *privatev1.BareMetalInstance
	deletes  int
}

func (f *teardownReadClient) GetBareMetalInstance(ctx context.Context, id string) (*privatev1.BareMetalInstance, error) {
	if f.returned != nil {
		f.gets++
		return f.returned, nil
	}
	return f.bmiObservationClient.GetBareMetalInstance(ctx, id)
}
func (f *teardownReadClient) DeleteBareMetalInstance(context.Context, string) error {
	f.deletes++
	return nil
}

var _ = Describe("teardown uses one fresh owned read without inventing absence", func() {
	for _, state := range []string{"present", "absent", "error", "foreign", "wrong-id"} {
		It(state, func() {
			r, fc, co := workerReadHarness()
			provider := &teardownReadClient{workerReadClient: fc}
			r.fulfillment = provider
			w := co.Status.Workers[0]
			w.Phase = workerPhaseDeleting
			provider.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
			switch state {
			case "absent":
				provider.returned = nil
				fc.getErr = status.Error(codes.NotFound, "gone")
			case "error":
				provider.returned = nil
				fc.getErr = status.Error(codes.Internal, "unknown")
			case "foreign":
				provider.returned.GetMetadata().SetTenant("foreign")
			case "wrong-id":
				provider.returned.SetId("different-id")
			}
			observed := indexWorkerBMIs(nil)
			observed.agents = &unstructured.UnstructuredList{}
			kept := r.reconcileTeardownWorkers(context.Background(), co, []v1alpha1.WorkerStatus{w})
			wantDeletes := 0
			if state == "present" {
				wantDeletes = 1
			}
			Expect(provider.gets).To(Equal(1), "gets=%d deletes=%d, want 1/%d", provider.gets, provider.deletes, wantDeletes)
			Expect(provider.deletes).To(Equal(wantDeletes), "gets=%d deletes=%d, want 1/%d", provider.gets, provider.deletes, wantDeletes)
			Expect((len(kept) == 0)).To(Equal((state == "absent")), "worker removal without confirmed NotFound: %v", kept)
		})
	}
})

type agentListCountingClient struct {
	client.Client
	agentLists int
}

func (c *agentListCountingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if agents, ok := list.(*unstructured.UnstructuredList); ok && agents.GetKind() == "AgentList" {
		c.agentLists++
	}
	return c.Client.List(ctx, list, opts...)
}

var _ = It("retains the finalizer when another writer appends a worker", func() {
	r, fc, co := workerReadHarness()
	fc.getErr = status.Error(codes.NotFound, "confirmed deleted")
	co.Finalizers = []string{bmWorkerFinalizer}
	Expect(r.Update(context.Background(), co)).To(Succeed())
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	kube := r.Client
	appended := newWorkerStatus("standard", "standard", "concurrent", "", workerPhaseProvisioning)
	c := &bmiConflictClient{Client: kube, beforePatch: func() {
		latest := co.DeepCopy()
		Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest)).To(Succeed())
		latest.Status.Workers = append(latest.Status.Workers, appended)
		Expect(kube.Status().Update(context.Background(), latest)).To(Succeed())
	}}
	r.Client = c
	o := indexWorkerBMIs(nil)
	o.agents = &unstructured.UnstructuredList{}
	res, err := r.handleClusterDeletion(context.Background(), co)
	Expect(apierrors.IsConflict(err)).To(BeTrue(), "result=%+v err=%v, want one-shot conflict", res, err)
	Expect(res.IsZero()).To(BeTrue(), "result=%+v err=%v, want one-shot conflict", res, err)
	latest := co.DeepCopy()
	Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(co), latest)).To(Succeed())
	Expect(latest.Finalizers).To(HaveLen(1), "removed finalizer with appended worker: finalizers=%v result=%v", latest.Finalizers, res)
	Expect(latest.Finalizers[0]).To(Equal(bmWorkerFinalizer), "removed finalizer with appended worker: finalizers=%v result=%v", latest.Finalizers, res)
	Expect(res.IsZero()).To(BeTrue(), "removed finalizer with appended worker: finalizers=%v result=%v", latest.Finalizers, res)
	Expect(latest.Status.Workers).To(HaveLen(2), "workers=%v", latest.Status.Workers)
	Expect(latest.Status.Workers[1].Name).To(Equal(appended.Name), "workers=%v", latest.Status.Workers)
})

var _ = It("retains a failed worker slot during scale-down", func() {
	r, base, co := workerReadHarness()
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	w := co.Status.Workers[0]
	w.Phase = workerPhaseFailed
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	kept := r.handleScaleDown(context.Background(), co, nil, []v1alpha1.WorkerStatus{w})
	Expect(kept).To(HaveLen(1), "retirement must precede external cleanup: workers=%+v deletes=%d", kept, fc.deletes)
	Expect(kept[0].BareMetalInstance).To(Equal(w.BareMetalInstance), "retirement must precede external cleanup: workers=%+v deletes=%d", kept, fc.deletes)
	Expect(kept[0].Phase).To(Equal(workerPhaseUnbinding), "retirement must precede external cleanup: workers=%+v deletes=%d", kept, fc.deletes)
	Expect(fc.deletes).To(Equal(0), "retirement must precede external cleanup: workers=%+v deletes=%d", kept, fc.deletes)
})

var _ = Describe("Worker retention when cleanup evidence is unknown", func() {
	for _, scenario := range []string{"outage", "denied", "foreign-tenant", "foreign-owner", "idless"} {
		It(scenario, func() {
			r, base, co := workerReadHarness()
			fc := &teardownReadClient{workerReadClient: base}
			r.fulfillment = fc
			w := co.Status.Workers[0]
			w.Phase = workerPhaseDeleting
			switch scenario {
			case "outage":
				base.getErr = status.Error(codes.Unavailable, "outage")
			case "denied":
				base.getErr = status.Error(codes.PermissionDenied, "denied")
			case "foreign-tenant", "foreign-owner":
				fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
				if scenario == "foreign-tenant" {
					fc.returned.GetMetadata().SetTenant("foreign")
				} else {
					fc.returned.GetMetadata().SetAnnotations(map[string]string{ownerReferenceAnnotation: "ClusterOrder/foreign"})
				}
			case "idless":
				w.BareMetalInstance.ID = ""
			}
			o := indexWorkerBMIs(nil)
			o.agents = &unstructured.UnstructuredList{}
			kept := r.reconcileTeardownWorkers(context.Background(), co, []v1alpha1.WorkerStatus{w})
			Expect(kept).To(HaveLen(1), "unknown released: workers=%+v deletes=%d", kept, fc.deletes)
			Expect(kept[0].BareMetalInstance).To(Equal(w.BareMetalInstance), "unknown released: workers=%+v deletes=%d", kept, fc.deletes)
			Expect(fc.deletes).To(Equal(0), "unknown released: workers=%+v deletes=%d", kept, fc.deletes)
		})
	}
})

var _ = It("avoids another Agent List for teardown during stable convergence", func() {
	r, _, co := workerReadHarness()
	c := &agentListCountingClient{Client: r.Client}
	r.Client = c
	_, err := r.reconcileWorkerTeardown(context.Background(), co)
	Expect(err).NotTo(HaveOccurred())
	Expect(c.agentLists).To(BeZero(), "unnecessary teardown Agent lists=%d", c.agentLists)
})

// fakeTargetManager is the multicluster seam the reconciler needs: one provider
// cluster backed by a fake client, which also serves as the uncached reader.
type fakeTargetManager struct {
	mcmanager.Manager
	target cluster.Cluster
}

func (m *fakeTargetManager) GetCluster(context.Context, multicluster.ClusterName) (cluster.Cluster, error) {
	return m.target, nil
}

type fakeTargetCluster struct {
	cluster.Cluster
	client client.Client
}

func (c *fakeTargetCluster) GetClient() client.Client    { return c.client }
func (c *fakeTargetCluster) GetAPIReader() client.Reader { return c.client }

func remoteTeardownScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	scheme.AddKnownTypeWithName(assistedservice.InfraEnvGroupVersionKind(), &unstructured.Unstructured{})
	return scheme
}

// remoteTeardownHarness returns a reconciler wired to a provider fake client and
// the ClusterOrder incarnation the objects are expected to carry.
func remoteTeardownHarness() (*Reconciler, client.Client, *v1alpha1.ClusterOrder) {
	GinkgoHelper()
	scheme := remoteTeardownScheme()
	provider := clientfake.NewClientBuilder().WithScheme(scheme).Build()
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
		Name: "teardown", Namespace: "test", UID: "order-uid",
	}}
	r := &Reconciler{scheme: scheme}
	r.SetTargetCluster(&fakeTargetManager{
		target: &fakeTargetCluster{client: provider},
	}, "remote")
	return r, provider, co
}

// remoteTeardownInfraEnv is the provider-cluster object shape the worker creates
// in remote mode: correlation markers, never an owner reference.
func remoteTeardownInfraEnv(co *v1alpha1.ClusterOrder, orderUID string) *unstructured.Unstructured {
	infraEnv := assistedservice.NewInfraEnvObject()
	infraEnv.SetName(assistedservice.InfraEnvName(co.Name))
	infraEnv.SetNamespace(co.Namespace)
	infraEnv.SetUID("infraenv-uid")
	infraEnv.SetLabels(map[string]string{clusterOrderLabel: co.Name})
	infraEnv.SetAnnotations(map[string]string{
		ownerReferenceAnnotation:  "ClusterOrder/" + co.Name,
		clusterOrderUIDAnnotation: orderUID,
	})
	return infraEnv
}

func remoteTeardownPullSecret(co *v1alpha1.ClusterOrder, orderUID string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        co.Name + pullSecretNameSuffix,
			Namespace:   co.Namespace,
			UID:         "pull-secret-uid",
			Labels:      map[string]string{clusterOrderLabel: co.Name},
			Annotations: map[string]string{ownerReferenceAnnotation: "ClusterOrder/" + co.Name, clusterOrderUIDAnnotation: orderUID},
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
}

var _ = Describe("remote infrastructure teardown", func() {
	It("deletes the provider-cluster InfraEnv and pull secret, then confirms absence", func() {
		ctx := context.Background()
		r, provider, co := remoteTeardownHarness()
		infraEnv := remoteTeardownInfraEnv(co, "order-uid")
		secret := remoteTeardownPullSecret(co, "order-uid")
		Expect(provider.Create(ctx, infraEnv)).To(Succeed())
		Expect(provider.Create(ctx, secret)).To(Succeed())

		// A Delete request is pending, not completion.
		gone, err := r.deleteRemoteInfraEnv(ctx, co)
		Expect(err).NotTo(HaveOccurred())
		Expect(gone).To(BeFalse())

		// Idempotent: the second pass confirms the objects are gone.
		gone, err = r.deleteRemoteInfraEnv(ctx, co)
		Expect(err).NotTo(HaveOccurred())
		Expect(gone).To(BeTrue())

		err = provider.Get(ctx, client.ObjectKeyFromObject(infraEnv), assistedservice.NewInfraEnvObject())
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "provider InfraEnv survives teardown: err=%v", err)
		Expect(apierrors.IsNotFound(provider.Get(ctx, client.ObjectKeyFromObject(secret), &corev1.Secret{}))).To(BeTrue())
	})

	It("confirms absence immediately when nothing was created", func() {
		r, _, co := remoteTeardownHarness()
		gone, err := r.deleteRemoteInfraEnv(context.Background(), co)
		Expect(err).NotTo(HaveOccurred())
		Expect(gone).To(BeTrue())
	})

	It("never deletes a same-name object that is not this order's", func() {
		ctx := context.Background()
		r, provider, co := remoteTeardownHarness()
		foreign := remoteTeardownInfraEnv(co, "order-uid")
		foreign.SetLabels(nil)
		foreign.SetAnnotations(nil)
		Expect(provider.Create(ctx, foreign)).To(Succeed())

		_, err := r.deleteRemoteInfraEnv(ctx, co)
		Expect(err).To(MatchError(ContainSubstring("not owned by ClusterOrder")))
		Expect(provider.Get(ctx, client.ObjectKeyFromObject(foreign), assistedservice.NewInfraEnvObject())).To(Succeed())
	})

	It("deletes an object that still carries this order's controller reference", func() {
		ctx := context.Background()
		r, provider, co := remoteTeardownHarness()
		legacy := remoteTeardownInfraEnv(co, "")
		legacy.SetLabels(nil)
		legacy.SetAnnotations(nil)
		controller := true
		legacy.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: v1alpha1.GroupVersion.String(), Kind: "ClusterOrder",
			Name: co.Name, UID: co.UID, Controller: &controller,
		}})
		Expect(provider.Create(ctx, legacy)).To(Succeed())

		gone, err := r.deleteRemoteInfraEnv(ctx, co)
		Expect(err).NotTo(HaveOccurred())
		Expect(gone).To(BeFalse())
		gone, err = r.deleteRemoteInfraEnv(ctx, co)
		Expect(err).NotTo(HaveOccurred())
		Expect(gone).To(BeTrue())
	})

	It("never deletes an object left by an earlier order incarnation", func() {
		ctx := context.Background()
		r, provider, co := remoteTeardownHarness()
		previous := remoteTeardownPullSecret(co, "previous-uid")
		Expect(provider.Create(ctx, previous)).To(Succeed())

		_, err := r.deleteRemoteInfraEnv(ctx, co)
		Expect(err).To(MatchError(ContainSubstring("not owned by ClusterOrder")))
		Expect(provider.Get(ctx, client.ObjectKeyFromObject(previous), &corev1.Secret{})).To(Succeed())
	})

	It("leaves local-mode objects to the local garbage collector", func() {
		ctx := context.Background()
		scheme := remoteTeardownScheme()
		local := clientfake.NewClientBuilder().WithScheme(scheme).Build()
		r := &Reconciler{Client: local, apiReader: local, scheme: scheme}
		co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			Name: "teardown", Namespace: "test", UID: "order-uid",
		}}
		// Local mode keeps the controller reference; the objects are not
		// deleted by the worker.
		infraEnv := assistedservice.NewInfraEnvObject()
		infraEnv.SetName(assistedservice.InfraEnvName(co.Name))
		infraEnv.SetNamespace(co.Namespace)
		Expect(controllerutil.SetControllerReference(co, infraEnv, scheme)).To(Succeed())
		Expect(local.Create(ctx, infraEnv)).To(Succeed())

		gone, err := r.deleteRemoteInfraEnv(ctx, co)
		Expect(err).NotTo(HaveOccurred())
		Expect(gone).To(BeTrue())
		Expect(local.Get(ctx, client.ObjectKeyFromObject(infraEnv), assistedservice.NewInfraEnvObject())).To(Succeed())
	})
})
