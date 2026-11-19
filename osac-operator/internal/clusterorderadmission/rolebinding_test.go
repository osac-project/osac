package clusterorderadmission

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	testClusterOrderNamespace = "osac-orders"
	testClusterOrderName      = "cluster-a"
	testClusterNamespace      = "osac-orders-cluster-a"
	testManagerUsername       = "system:serviceaccount:osac-operator:osac-operator"
)

func TestAdminRoleBindingRequiresExistingClusterOrder(t *testing.T) {
	tests := []struct {
		name      string
		order     *v1alpha1.ClusterOrder
		namespace *corev1.Namespace
		mutate    func(*rbacv1.RoleBinding)
		username  string
		operation admissionv1.Operation
		allowed   bool
	}{
		{
			name:      "valid generated binding",
			order:     testClusterOrder(),
			namespace: testNamespace(),
			username:  testManagerUsername,
			operation: admissionv1.Create,
			allowed:   true,
		},
		{
			name:      "valid binding update",
			order:     testClusterOrder(),
			namespace: testNamespace(),
			username:  testManagerUsername,
			operation: admissionv1.Update,
			allowed:   true,
		},
		{
			name:      "missing ClusterOrder",
			namespace: testNamespace(),
			username:  testManagerUsername,
			operation: admissionv1.Create,
		},
		{
			name:      "missing generated namespace",
			order:     testClusterOrder(),
			username:  testManagerUsername,
			operation: admissionv1.Create,
		},
		{
			name:      "binding outside generated namespace",
			order:     testClusterOrder(),
			namespace: testNamespace(),
			mutate: func(binding *rbacv1.RoleBinding) {
				binding.Namespace = "tenant-workload"
				binding.Subjects[0].Namespace = "tenant-workload"
			},
			username:  testManagerUsername,
			operation: admissionv1.Create,
		},
		{
			name:      "wrong RoleBinding name",
			order:     testClusterOrder(),
			namespace: testNamespace(),
			mutate: func(binding *rbacv1.RoleBinding) {
				binding.Name = "unexpected-admin"
			},
			username:  testManagerUsername,
			operation: admissionv1.Create,
		},
		{
			name:      "wrong ClusterOrder label",
			order:     testClusterOrder(),
			namespace: testNamespace(),
			mutate: func(binding *rbacv1.RoleBinding) {
				binding.Labels[clusterOrderNameLabel] = "other-order"
			},
			username:  testManagerUsername,
			operation: admissionv1.Create,
		},
		{
			name:      "wrong ServiceAccount subject",
			order:     testClusterOrder(),
			namespace: testNamespace(),
			mutate: func(binding *rbacv1.RoleBinding) {
				binding.Subjects[0].Name = "attacker"
			},
			username:  testManagerUsername,
			operation: admissionv1.Create,
		},
		{
			name:      "deleting ClusterOrder",
			order:     testClusterOrderDeleting(),
			namespace: testNamespace(),
			username:  testManagerUsername,
			operation: admissionv1.Create,
		},
		{
			name:      "stale generated namespace in ClusterOrder status",
			order:     testClusterOrderWithNamespace("other-namespace"),
			namespace: testNamespace(),
			username:  testManagerUsername,
			operation: admissionv1.Create,
		},
		{
			name:      "unrelated RoleBinding",
			order:     testClusterOrder(),
			namespace: testNamespace(),
			mutate: func(binding *rbacv1.RoleBinding) {
				binding.RoleRef.Name = "view"
			},
			username:  testManagerUsername,
			operation: admissionv1.Create,
			allowed:   true,
		},
		{
			name:      "non-manager admin binding is outside this guard",
			order:     testClusterOrder(),
			namespace: testNamespace(),
			mutate: func(binding *rbacv1.RoleBinding) {
				binding.Subjects[0].Name = "tenant-admin"
			},
			username:  "system:serviceaccount:tenant:tenant-admin",
			operation: admissionv1.Create,
			allowed:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			objects := make([]client.Object, 0, 2)
			if test.order != nil {
				objects = append(objects, test.order)
			}
			if test.namespace != nil {
				objects = append(objects, test.namespace)
			}
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := rbacv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := v1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			handler, err := NewHandler(reader, Config{
				ClusterOrderNamespace: testClusterOrderNamespace,
				ManagerUsername:       testManagerUsername,
			})
			if err != nil {
				t.Fatalf("create handler: %v", err)
			}

			binding := testAdminRoleBinding()
			if test.mutate != nil {
				test.mutate(binding)
			}
			request := testRoleBindingRequest(t, binding, test.username, test.operation)
			response := handler.Handle(context.Background(), request)
			if response.Allowed != test.allowed {
				t.Fatalf("allowed = %t, want %t; response: %+v", response.Allowed, test.allowed, response)
			}
		})
	}
}

func TestAdminRoleBindingFailsClosedWhenClusterOrderLookupFails(t *testing.T) {
	handler, err := NewHandler(failingReader{err: errors.New("API unavailable")}, Config{
		ClusterOrderNamespace: testClusterOrderNamespace,
		ManagerUsername:       testManagerUsername,
	})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	response := handler.Handle(context.Background(), testRoleBindingRequest(
		t, testAdminRoleBinding(), testManagerUsername, admissionv1.Create,
	))
	if response.Allowed {
		t.Fatal("admin RoleBinding was admitted without verifying its ClusterOrder")
	}
}

type failingReader struct {
	client.Reader
	err error
}

func (reader failingReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return reader.err
}

func testClusterOrder() *v1alpha1.ClusterOrder {
	return testClusterOrderWithNamespace(testClusterNamespace)
}

func testClusterOrderWithNamespace(namespace string) *v1alpha1.ClusterOrder {
	return &v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterOrderName,
			Namespace: testClusterOrderNamespace,
		},
		Status: v1alpha1.ClusterOrderStatus{
			ClusterReference: &v1alpha1.ClusterOrderClusterReferenceType{Namespace: namespace},
		},
	}
}

func testClusterOrderDeleting() *v1alpha1.ClusterOrder {
	order := testClusterOrder()
	deletionTimestamp := metav1.Now()
	order.DeletionTimestamp = &deletionTimestamp
	order.Finalizers = []string{"osac.openshift.io/finalizer"}
	return order
}

func testNamespace() *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: testClusterNamespace,
		Labels: map[string]string{
			clusterOrderAppNameLabel: clusterOrderAppName,
			clusterOrderNameLabel:    testClusterOrderName,
		},
	}}
}

func testAdminRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      managerRoleBindingName,
			Namespace: testClusterNamespace,
			Labels: map[string]string{
				clusterOrderAppNameLabel: clusterOrderAppName,
				clusterOrderNameLabel:    testClusterOrderName,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "admin",
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      clusterOrderServiceAccount,
			Namespace: testClusterNamespace,
		}},
	}
}

func testRoleBindingRequest(
	t *testing.T,
	binding *rbacv1.RoleBinding,
	username string,
	operation admissionv1.Operation,
) admission.Request {
	t.Helper()
	encoded, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("encode RoleBinding: %v", err)
	}
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		UID:       types.UID("rolebinding-request"),
		Kind:      metav1.GroupVersionKind{Group: rbacv1.GroupName, Version: "v1", Kind: "RoleBinding"},
		Resource:  metav1.GroupVersionResource{Group: rbacv1.GroupName, Version: "v1", Resource: "rolebindings"},
		Operation: operation,
		UserInfo:  authenticationv1.UserInfo{Username: username},
		Namespace: binding.Namespace,
		Name:      binding.Name,
		Object:    runtime.RawExtension{Raw: encoded},
	}}
}
