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
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	"sigs.k8s.io/multicluster-runtime/providers/single"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/assistedservice"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// These specs bind the remote-mode split of the cluster controllers: hub
// objects (ClusterOrders, their status and finalizers) stay on the local
// cluster, infrastructure objects (the cluster namespace and its contents,
// the InfraEnv and its pull secret) go to the provider cluster registered as
// "remote". They follow the storage controller's remote-test pattern: a fake
// client behind a single-cluster provider, the real envtest API server as the
// local cluster.

// remoteTestScheme knows the kinds the fake provider cluster serves.
func remoteTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(hypershiftv1beta1.AddToScheme(scheme)).To(Succeed())
	// The fake client resolves unstructured objects through its scheme.
	scheme.AddKnownTypeWithName(
		assistedservice.InfraEnvGroupVersionKind(), &unstructured.Unstructured{})
	return scheme
}

var _ = Describe("ClusterOrder remote-mode split", func() {
	const clusterOrderNamespace = "default"

	newRemoteReconciler := func(remoteClient client.Client) *ClusterOrderReconciler {
		reconciler := NewClusterOrderReconciler(
			k8sClient, k8sClient, k8sClient.Scheme(),
			clusterOrderNamespace, "",
			newControllableProvider(), 100*time.Millisecond, provisioning.DefaultMaxJobHistory,
		)
		reconciler.SetTargetCluster(newRemoteTestMcManager(remoteClient), "remote")
		return reconciler
	}

	newOrder := func(name string) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: clusterOrderNamespace},
			Spec:       osacv1alpha1.ClusterOrderSpec{TemplateID: "test.template"},
		}
	}

	It("creates the cluster namespace and its components on the provider cluster", func() {
		remoteClient := fakeclient.NewClientBuilder().WithScheme(remoteTestScheme()).Build()
		reconciler := newRemoteReconciler(remoteClient)

		const name = "cluster-order-remote-split"
		instance := newOrder(name)
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, instance)).To(Succeed()) })

		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: clusterOrderNamespace},
		})
		Expect(err).NotTo(HaveOccurred())

		// The cluster namespace and everything created inside it are on the
		// provider cluster.
		clusterNamespace := clusterOrderNamespace + "-" + name
		namespace := &corev1.Namespace{}
		Expect(remoteClient.Get(ctx, types.NamespacedName{Name: clusterNamespace}, namespace)).To(Succeed())
		Expect(namespace.Labels[osacClusterOrderNameLabel]).To(Equal(name))

		serviceAccount := &corev1.ServiceAccount{}
		Expect(remoteClient.Get(ctx, types.NamespacedName{
			Name: defaultServiceAccountName, Namespace: clusterNamespace,
		}, serviceAccount)).To(Succeed())
		roleBinding := &rbacv1.RoleBinding{}
		Expect(remoteClient.Get(ctx, types.NamespacedName{
			Name: defaultRoleBindingName, Namespace: clusterNamespace,
		}, roleBinding)).To(Succeed())

		// The local cluster holds only the ClusterOrder.
		localNamespace := &corev1.Namespace{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterNamespace}, localNamespace)
		Expect(err).To(HaveOccurred())

		// The ClusterOrder itself, its finalizer, its status and the recorded
		// namespace reference stay on the local cluster.
		updated := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: name, Namespace: clusterOrderNamespace,
		}, updated)).To(Succeed())
		Expect(updated.Finalizers).To(ContainElement(osacFinalizer))
		Expect(updated.GetClusterReferenceNamespace()).To(Equal(clusterNamespace))
		condition := meta.FindStatusCondition(updated.Status.Conditions, osacv1alpha1.ConditionNamespaceCreated)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
	})

	It("maps control-plane Services to the order through provider HostedClusters", func() {
		remoteClient := fakeclient.NewClientBuilder().WithScheme(remoteTestScheme()).Build()
		reconciler := newRemoteReconciler(remoteClient)

		const name = "cluster-order-remote-service-map"
		instance := newOrder(name)
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, instance)).To(Succeed()) })

		// A kube-apiserver Service in a hosted control-plane namespace on the
		// provider cluster maps back to the local ClusterOrder through the
		// HostedCluster's labels.
		clusterNamespace := clusterOrderNamespace + "-" + name
		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultHostedClusterName,
				Namespace: clusterNamespace,
				Labels:    map[string]string{osacClusterOrderNameLabel: name},
			},
		}
		Expect(remoteClient.Create(ctx, hostedCluster)).To(Succeed())
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-apiserver",
				Namespace: clusterNamespace + "-" + defaultHostedClusterName,
			},
		}
		Expect(remoteClient.Create(ctx, service)).To(Succeed())

		requests := reconciler.mapControlPlaneServiceToCluster(ctx, service)
		Expect(requests).To(ConsistOf(reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: clusterOrderNamespace},
		}))
	})

	It("drives reconciliation from provider watches across two separate envtest API servers", func() {
		By("starting a second independent envtest API server as the provider cluster")
		remoteTestEnv := &envtest.Environment{
			BinaryAssetsDirectory: testEnv.BinaryAssetsDirectory,
			CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "fakes")},
			ErrorIfCRDPathMissing: true,
		}
		remoteCfg, err := remoteTestEnv.Start()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(remoteTestEnv.Stop()).To(Succeed()) })

		remoteKubeconfigPath := filepath.Join(GinkgoT().TempDir(), "remote-envtest.kubeconfig")
		remoteAPIConfig := clientcmdapi.Config{
			Clusters: map[string]*clientcmdapi.Cluster{
				"remote": {
					Server:                   remoteCfg.Host,
					CertificateAuthorityData: remoteCfg.CAData,
					CertificateAuthority:     remoteCfg.CAFile,
					InsecureSkipTLSVerify:    remoteCfg.Insecure,
				},
			},
			AuthInfos: map[string]*clientcmdapi.AuthInfo{
				"remote-user": {
					ClientCertificateData: remoteCfg.CertData,
					ClientCertificate:     remoteCfg.CertFile,
					ClientKeyData:         remoteCfg.KeyData,
					ClientKey:             remoteCfg.KeyFile,
					Token:                 remoteCfg.BearerToken,
				},
			},
			Contexts: map[string]*clientcmdapi.Context{
				"remote": {Cluster: "remote", AuthInfo: "remote-user"},
			},
			CurrentContext: "remote",
		}
		Expect(clientcmd.WriteToFile(remoteAPIConfig, remoteKubeconfigPath)).To(Succeed())
		loadedRemoteCfg, err := clientcmd.BuildConfigFromFlags("", remoteKubeconfigPath)
		Expect(err).NotTo(HaveOccurred())

		remoteScheme := runtime.NewScheme()
		Expect(clientgoscheme.AddToScheme(remoteScheme)).To(Succeed())
		Expect(hypershiftv1beta1.AddToScheme(remoteScheme)).To(Succeed())
		remoteCluster, err := cluster.New(loadedRemoteCfg, func(o *cluster.Options) {
			o.Scheme = remoteScheme
		})
		Expect(err).NotTo(HaveOccurred())

		localMgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:     k8sClient.Scheme(),
			Metrics:    metricsserver.Options{BindAddress: "0"},
			Controller: ctrlconfig.Controller{SkipNameValidation: ptr.To(true)},
		})
		Expect(err).NotTo(HaveOccurred())
		remoteProvider := single.New("remote", remoteCluster)
		mcMgr, err := mcmanager.WithMultiCluster(localMgr, remoteProvider)
		Expect(err).NotTo(HaveOccurred())

		reconciler := NewClusterOrderReconciler(
			localMgr.GetClient(), localMgr.GetAPIReader(), localMgr.GetScheme(),
			"default", "",
			newControllableProvider(), 100*time.Millisecond, provisioning.DefaultMaxJobHistory,
		)
		reconciler.Recorder = localMgr.GetEventRecorder(ClusterOrderControllerName)
		reconciler.SetTargetCluster(mcMgr, multicluster.ClusterName("remote"))
		Expect(reconciler.SetupWithManager(mcMgr)).To(Succeed())

		watchCtx, watchCancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			Expect(remoteCluster.Start(watchCtx)).To(Or(BeNil(), MatchError(context.Canceled)))
		}()
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			Expect(remoteProvider.Start(watchCtx, mcMgr)).To(Or(BeNil(), MatchError(context.Canceled)))
		}()
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			Expect(mcMgr.Start(watchCtx)).To(Or(BeNil(), MatchError(context.Canceled)))
		}()
		DeferCleanup(func() {
			watchCancel()
			wg.Wait()
		})

		remoteK8sClient, err := client.New(loadedRemoteCfg, client.Options{Scheme: remoteScheme})
		Expect(err).NotTo(HaveOccurred())

		By("creating a ClusterOrder on the local cluster and watching its namespace appear on the provider")
		const name = "cluster-order-remote-live-watch"
		instance := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       osacv1alpha1.ClusterOrderSpec{TemplateID: "test.template"},
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, instance)).To(Succeed()) })

		clusterNamespace := "default-" + name
		Eventually(func(g Gomega) {
			namespace := &corev1.Namespace{}
			g.Expect(remoteK8sClient.Get(ctx, types.NamespacedName{Name: clusterNamespace}, namespace)).To(Succeed())
			g.Expect(namespace.Labels[osacClusterOrderNameLabel]).To(Equal(name))
		}, 10*time.Second, 100*time.Millisecond).Should(Succeed())

		By("creating a HostedCluster on the provider cluster and observing the local order react")
		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultHostedClusterName,
				Namespace: clusterNamespace,
				Labels:    map[string]string{osacClusterOrderNameLabel: name},
			},
		}
		Expect(remoteK8sClient.Create(ctx, hostedCluster)).To(Succeed())

		Eventually(func(g Gomega) {
			updated := &osacv1alpha1.ClusterOrder{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, updated)).To(Succeed())
			condition := meta.FindStatusCondition(updated.Status.Conditions, osacv1alpha1.ConditionControlPlaneCreated)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(updated.GetClusterReferenceHostedClusterName()).To(Equal(defaultHostedClusterName))
		}, 10*time.Second, 100*time.Millisecond).Should(Succeed())

		// The local cluster never sees the infrastructure.
		localNamespace := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: clusterNamespace}, localNamespace)).To(HaveOccurred())
	})
})

var _ = Describe("BareMetalWorker remote-mode split", Label("baremetalworker"), func() {
	const (
		clusterOrderNamespace = "default"
		remoteSplitClusterID  = "remote-split-cluster-uuid"
	)

	newRemoteSplitOrder := func(name string) *osacv1alpha1.ClusterOrder {
		return &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: clusterOrderNamespace,
				Labels:    map[string]string{"osac.openshift.io/clusterorder-uuid": remoteSplitClusterID},
				Annotations: map[string]string{
					"osac.openshift.io/tenant": "tenant1",
				},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID: "test",
				PullSecret: `{"auths":{}}`,
				NodeRequests: []osacv1alpha1.NodeRequest{{
					NodeSet:       "workers",
					NumberOfNodes: 1,
					BareMetal:     &osacv1alpha1.BareMetalNodeSpec{InstanceType: "bm-standard"},
				}},
			},
		}
	}

	It("creates the InfraEnv and its pull secret on the provider cluster", func() {
		fulfillment := newWorkerFulfillmentStub()
		fulfillment.AddCluster(privatev1.Cluster_builder{
			Id:       remoteSplitClusterID,
			Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build(),
		}.Build())

		remoteClient := fakeclient.NewClientBuilder().WithScheme(remoteTestScheme()).Build()
		reconciler := baremetalworker.NewReconciler(
			k8sClient, k8sClient, k8sClient.Scheme(),
			fulfillment, baremetalworker.NewIgnitionFetcher(nil),
			events.NewFakeRecorder(10), clusterOrderNamespace,
		)
		reconciler.SetTargetCluster(newRemoteTestMcManager(remoteClient), "remote")

		co := newRemoteSplitOrder("bmw-remote-split")
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, co)).To(Succeed()) })

		// The first reconcile only persists the worker finalizer; the second
		// creates the InfraEnv and the pull secret.
		for range 2 {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: co.Name, Namespace: co.Namespace},
			})
			Expect(err).NotTo(HaveOccurred())
		}

		// The InfraEnv and its pull secret are on the provider cluster, in
		// the ClusterOrder's namespace there.
		infraEnv := assistedservice.NewInfraEnvObject()
		Expect(remoteClient.Get(ctx, types.NamespacedName{
			Name: co.Name + "-infraenv", Namespace: co.Namespace,
		}, infraEnv)).To(Succeed())
		// Remote objects must not carry an owner reference: the owning
		// ClusterOrder lives on the local cluster, so the provider cluster's
		// garbage collector would treat a dangling owner as an orphan and delete
		// the InfraEnv and its pull secret. Ownership is recorded with the
		// cross-cluster correlation markers and teardown deletes the objects.
		Expect(infraEnv.GetOwnerReferences()).To(BeEmpty())
		Expect(infraEnv.GetLabels()).To(HaveKeyWithValue("osac.openshift.io/cluster-order", co.Name))
		Expect(infraEnv.GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/owner-reference", "ClusterOrder/"+co.Name))
		Expect(infraEnv.GetAnnotations()).To(HaveKey("osac.openshift.io/clusterorder-uid"))

		pullSecret := &corev1.Secret{}
		Expect(remoteClient.Get(ctx, types.NamespacedName{
			Name: co.Name + "-pull-secret", Namespace: co.Namespace,
		}, pullSecret)).To(Succeed())
		Expect(pullSecret.GetOwnerReferences()).To(BeEmpty())
		Expect(pullSecret.GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/owner-reference", "ClusterOrder/"+co.Name))

		// Neither object exists on the local cluster.
		localInfraEnv := assistedservice.NewInfraEnvObject()
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name: co.Name + "-infraenv", Namespace: co.Namespace,
		}, localInfraEnv)
		Expect(err).To(HaveOccurred())

		// The ClusterOrder, its worker finalizer and the pending InfraEnv
		// condition stay on the local cluster.
		updated := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: co.Name, Namespace: co.Namespace,
		}, updated)).To(Succeed())
		Expect(updated.Finalizers).To(ContainElement("osac.openshift.io/baremetalworker-finalizer"))
		condition := meta.FindStatusCondition(updated.Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
	})
})
