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
	"context"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type recordingTenantsServer struct {
	privatev1.UnimplementedTenantsServer
	signals chan string
}

func (s *recordingTenantsServer) Signal(_ context.Context, request *privatev1.TenantsSignalRequest) (*privatev1.TenantsSignalResponse, error) {
	s.signals <- request.GetId()
	return &privatev1.TenantsSignalResponse{}, nil
}

var _ = Describe("Tenant feedback watch", func() {
	It("signals fulfillment after a Tenant status event", func() {
		const (
			tenantID        = "9eb0b1b9-aa21-4e77-aaef-0812693bc40b"
			tenantName      = "feedback-watch-tenant"
			tenantNamespace = "tenant-feedback-watch"
		)

		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tenantNamespace}}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(k8sClient.Delete, ctx, namespace)

		listener := bufconn.Listen(1024 * 1024)
		grpcServer := grpc.NewServer()
		tenantsServer := &recordingTenantsServer{signals: make(chan string, 2)}
		privatev1.RegisterTenantsServer(grpcServer, tenantsServer)
		go func() { _ = grpcServer.Serve(listener) }()
		DeferCleanup(grpcServer.Stop)
		DeferCleanup(listener.Close)

		connection, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(connection.Close)

		managerScheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(managerScheme)).To(Succeed())
		localManager, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:  managerScheme,
			Metrics: metricsserver.Options{BindAddress: "0"},
		})
		Expect(err).NotTo(HaveOccurred())
		manager, err := mcmanager.WithMultiCluster(localManager, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(NewTenantFeedbackReconciler(localManager.GetClient(), connection, tenantNamespace).SetupWithManager(manager)).To(Succeed())

		managerCtx, stopManager := context.WithCancel(ctx)
		managerErrors := make(chan error, 1)
		go func() { managerErrors <- localManager.Start(managerCtx) }()
		DeferCleanup(func() {
			stopManager()
			Eventually(managerErrors, 10*time.Second).Should(Receive(BeNil()))
		})
		Expect(localManager.GetCache().WaitForCacheSync(managerCtx)).To(BeTrue())

		tenant := &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{
			Name: tenantName, Namespace: tenantNamespace,
			Labels: map[string]string{osacTenantIDLabel: tenantID},
		}}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
		Eventually(tenantsServer.signals, 10*time.Second).Should(Receive(Equal(tenantID)))

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName, Namespace: tenantNamespace}, tenant)).To(Succeed())
		tenant.Status.Phase = v1alpha1.TenantPhaseReady
		Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
		Eventually(tenantsServer.signals, 10*time.Second).Should(Receive(Equal(tenantID)))
	})
})
