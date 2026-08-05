package controller

import (
	"context"
	"fmt"
	"maps"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac-operator/api/v1alpha1"
)

func (r *ClusterOrderReconciler) newNamespace(ctx context.Context, instance *v1alpha1.ClusterOrder) (*appResource, error) {
	log := ctrllog.FromContext(ctx)

	var namespaceList corev1.NamespaceList
	var namespaceName string

	if err := r.List(ctx, &namespaceList, labelSelectorFromInstance(instance)); err != nil {
		log.Error(err, "failed to list namespaces")
		return nil, err
	}

	if len(namespaceList.Items) > 1 {
		return nil, fmt.Errorf("found multiple matching namespaces for %s", instance.GetName())
	}

	if len(namespaceList.Items) == 0 {
		namespaceName = generateNamespaceName(instance)
		if namespaceName == "" {
			return nil, fmt.Errorf("failed to generate namespace name")
		}
	} else {
		namespaceName = namespaceList.Items[0].GetName()
	}

	namespace := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   namespaceName,
			Labels: commonLabelsFromOrder(instance),
		},
	}

	mutateFn := func() error {
		ensureCommonLabels(instance, namespace)
		instance.SetClusterReferenceNamespace(namespaceName)
		return nil
	}

	return &appResource{
		namespace,
		mutateFn,
	}, nil
}

func (r *ClusterOrderReconciler) newServiceAccount(ctx context.Context, instance *v1alpha1.ClusterOrder) (*appResource, error) {
	namespaceName := instance.GetClusterReferenceNamespace()
	if namespaceName == "" {
		return nil, fmt.Errorf("unable to retrieve required information from spec.clusterReference")
	}

	serviceAccountName := defaultServiceAccountName

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: namespaceName,
			Labels:    commonLabelsFromOrder(instance),
		},
	}

	mutateFn := func() error {
		ensureCommonLabels(instance, sa)
		instance.SetClusterReferenceServiceAccountName(serviceAccountName)
		return nil
	}

	return &appResource{
		sa,
		mutateFn,
	}, nil
}

func (r *ClusterOrderReconciler) newAdminRoleBinding(ctx context.Context, instance *v1alpha1.ClusterOrder) (*appResource, error) {
	namespaceName := instance.GetClusterReferenceNamespace()
	serviceAccountName := instance.GetClusterReferenceServiceAccountName()
	if namespaceName == "" || serviceAccountName == "" {
		return nil, fmt.Errorf("unable to retrieve required information from spec.clusterReference")
	}

	roleBindingName := defaultRoleBindingName

	subjects := []rbacv1.Subject{
		{
			Kind:      subjectKindServiceAccount,
			Name:      serviceAccountName,
			Namespace: namespaceName,
		},
	}

	roleref := rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     "ClusterRole",
		Name:     "admin",
	}

	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleBindingName,
			Namespace: namespaceName,
			Labels:    commonLabelsFromOrder(instance),
		},
	}

	mutateFn := func() error {
		instance.SetClusterReferenceRoleBindingName(roleBindingName)
		ensureCommonLabels(instance, roleBinding)
		roleBinding.Subjects = subjects
		roleBinding.RoleRef = roleref
		return nil
	}

	return &appResource{
		roleBinding,
		mutateFn,
	}, nil
}

func (r *ClusterOrderReconciler) newHubAccessRoleBinding(ctx context.Context, instance *v1alpha1.ClusterOrder) (*appResource, error) {
	namespaceName := instance.GetClusterReferenceNamespace()
	if namespaceName == "" {
		return nil, fmt.Errorf("unable to retrieve required information from spec.clusterReference")
	}

	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hubAccessRoleBindingName,
			Namespace: namespaceName,
			Labels:    commonLabelsFromOrder(instance),
		},
	}

	mutateFn := func() error {
		ensureCommonLabels(instance, roleBinding)
		roleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      subjectKindServiceAccount,
				Name:      hubAccessServiceAccountName,
				Namespace: r.ClusterOrderNamespace,
			},
		}
		roleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     r.hubAccessClusterRoleName(),
		}
		return nil
	}

	return &appResource{
		roleBinding,
		mutateFn,
	}, nil
}

func ensureCommonLabels(instance *v1alpha1.ClusterOrder, obj client.Object) {
	requiredLabels := commonLabelsFromOrder(instance)
	objLabels := obj.GetLabels()
	if objLabels == nil {
		objLabels = make(map[string]string)
	}
	maps.Copy(objLabels, requiredLabels)
	obj.SetLabels(objLabels)
}

func commonLabelsFromOrder(instance *v1alpha1.ClusterOrder) map[string]string {
	key := client.ObjectKeyFromObject(instance)
	return map[string]string{
		"app.kubernetes.io/name":  osacAppName,
		osacClusterOrderNameLabel: key.Name,
	}
}
