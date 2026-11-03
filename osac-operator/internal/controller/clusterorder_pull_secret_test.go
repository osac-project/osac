package controller

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	"github.com/osac-project/osac/osac-operator/pkg/pullsecret"
)

var _ = Describe("ClusterOrder reference-only credential lifecycle", func() {
	DescribeTable("deletes without reading source credentials", func(manual bool) {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		order := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "fixture", Namespace: "hub", UID: "fixture-uid", Finalizers: []string{osacFinalizer}},
			Spec: v1alpha1.ClusterOrderSpec{PullSecretRef: &corev1.LocalObjectReference{Name: "missing-source"}}}
		var provider provisioning.ProvisioningProvider
		if manual {
			order.Annotations = map[string]string{osacManagementStateAnnotation: ManagementStateManual}
			provider = noopProvisioningProvider{}
		}
		secretRead := false
		kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(order).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
				if _, secret := object.(*corev1.Secret); secret {
					secretRead = true
					return errors.New("fixture forbidden")
				}
				return underlying.Get(ctx, key, object, options...)
			},
		}).Build()
		reconciler := NewClusterOrderReconciler(kube, kube, scheme, "hub", "", provider, 0, 1)
		_, err := reconciler.handleDelete(context.Background(), reconcile.Request{}, order)
		Expect(err).NotTo(HaveOccurred())
		Expect(secretRead).To(BeFalse())
		Expect(order.Finalizers).NotTo(ContainElement(osacFinalizer))
	}, Entry("no provider", false), Entry("manual management", true))

	It("refuses a foreign namespace before teardown", func() {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		order := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "fixture", Namespace: "hub", UID: "fixture-uid"}}
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "hub-fixture", UID: "foreign-namespace", Labels: commonLabelsFromOrder(order)}}
		kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(order, namespace).Build()
		reconciler := NewClusterOrderReconciler(kube, kube, scheme, "hub", "", nil, 0, 1)
		_, err := reconciler.handleDelete(context.Background(), reconcile.Request{}, order)
		Expect(err).To(MatchError(pullsecret.ErrInvalid))
		Expect(kube.Get(context.Background(), client.ObjectKeyFromObject(namespace), namespace)).To(Succeed())
	})
})
