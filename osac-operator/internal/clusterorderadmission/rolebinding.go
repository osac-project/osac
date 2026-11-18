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

// Package clusterorderadmission validates the operator's delegated ClusterOrder RBAC.
package clusterorderadmission

import (
	"context"
	"encoding/json"
	"fmt"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	// RoleBindingValidationPath is the manager webhook path for delegated admin bindings.
	RoleBindingValidationPath = "/validate-clusterorder-admin-rolebinding"

	managerRoleBindingName     = "osac"
	clusterOrderServiceAccount = "osac"
	clusterOrderNameLabel      = "osac.openshift.io/clusterorder"
	clusterOrderAppNameLabel   = "app.kubernetes.io/name"
	clusterOrderAppName        = "osac-operator"
)

// Config identifies the manager and namespace that own ClusterOrders.
type Config struct {
	ClusterOrderNamespace string
	ManagerUsername       string
}

// Handler permits the operator's admin RoleBinding only in the generated namespace
// of an existing ClusterOrder, and only for that namespace's osac account.
type Handler struct {
	reader                client.Reader
	clusterOrderNamespace string
	managerUsername       string
}

// NewHandler constructs a fail-closed ClusterOrder RoleBinding validator.
func NewHandler(reader client.Reader, config Config) (*Handler, error) {
	if reader == nil || config.ClusterOrderNamespace == "" || config.ManagerUsername == "" {
		return nil, fmt.Errorf("ClusterOrder namespace, manager identity, and Kubernetes reader are required")
	}
	return &Handler{
		reader:                reader,
		clusterOrderNamespace: config.ClusterOrderNamespace,
		managerUsername:       config.ManagerUsername,
	}, nil
}

// Handle allows unrelated RoleBindings and validates admin bindings requested by
// the operator manager before Kubernetes persists them.
func (h *Handler) Handle(ctx context.Context, request admission.Request) admission.Response {
	if request.UserInfo.Username != h.managerUsername ||
		(request.Operation != admissionv1.Create && request.Operation != admissionv1.Update) ||
		request.Resource.Group != rbacv1.GroupName || request.Resource.Resource != "rolebindings" {
		return admission.Allowed("")
	}

	binding := &rbacv1.RoleBinding{}
	if err := json.Unmarshal(request.Object.Raw, binding); err != nil {
		return admission.Denied("OSAC admin RoleBinding is invalid")
	}
	if binding.RoleRef.Kind != "ClusterRole" || binding.RoleRef.Name != "admin" {
		return admission.Allowed("")
	}
	if !h.authorizedAdminBinding(ctx, request, binding) {
		return admission.Denied("OSAC may bind admin only to the service account for an existing ClusterOrder")
	}
	return admission.Allowed("")
}

func (h *Handler) authorizedAdminBinding(
	ctx context.Context,
	request admission.Request,
	binding *rbacv1.RoleBinding,
) bool {
	if request.Namespace == "" || request.Name != managerRoleBindingName ||
		binding.Namespace != request.Namespace || binding.Name != managerRoleBindingName ||
		binding.RoleRef.APIGroup != rbacv1.GroupName ||
		binding.Labels[clusterOrderAppNameLabel] != clusterOrderAppName ||
		len(binding.Subjects) != 1 {
		return false
	}

	clusterOrderName := binding.Labels[clusterOrderNameLabel]
	if clusterOrderName == "" {
		return false
	}
	clusterOrder := &v1alpha1.ClusterOrder{}
	if err := h.reader.Get(ctx, types.NamespacedName{
		Namespace: h.clusterOrderNamespace,
		Name:      clusterOrderName,
	}, clusterOrder); err != nil {
		return false
	}
	if !clusterOrder.DeletionTimestamp.IsZero() {
		return false
	}

	// The ClusterOrder controller generates the namespace from the order's
	// namespace and immutable name before it creates the account and binding.
	expectedNamespace := fmt.Sprintf("%s-%s", h.clusterOrderNamespace, clusterOrder.Name)
	if binding.Namespace != expectedNamespace {
		return false
	}
	if clusterOrder.GetClusterReferenceNamespace() != "" &&
		clusterOrder.GetClusterReferenceNamespace() != expectedNamespace {
		return false
	}
	if !h.namespaceBelongsToOrder(ctx, expectedNamespace, clusterOrder.Name) {
		return false
	}

	subject := binding.Subjects[0]
	return subject.Kind == "ServiceAccount" && subject.APIGroup == "" &&
		subject.Name == clusterOrderServiceAccount && subject.Namespace == expectedNamespace
}

func (h *Handler) namespaceBelongsToOrder(ctx context.Context, namespaceName, clusterOrderName string) bool {
	namespace := &corev1.Namespace{}
	if err := h.reader.Get(ctx, types.NamespacedName{Name: namespaceName}, namespace); err != nil {
		return false
	}
	return namespace.Labels[clusterOrderNameLabel] == clusterOrderName &&
		namespace.Labels[clusterOrderAppNameLabel] == clusterOrderAppName &&
		namespace.DeletionTimestamp.IsZero()
}
