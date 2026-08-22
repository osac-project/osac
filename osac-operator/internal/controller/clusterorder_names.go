package controller

import (
	"fmt"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	subjectKindServiceAccount    string = "ServiceAccount"
	defaultServiceAccountName    string = "osac"
	defaultHostedClusterName     string = "cluster"
	defaultRoleBindingName       string = "osac"
	defaultClusterOrderNamespace string = "osac-orders"
	hubAccessServiceAccountName  string = "hub-access"
	hubAccessRoleBindingName     string = "hub-access"
	hubAccessClusterRoleBaseName string = "hub-access-hosted-clusters"
)

const (
	osacAPIEndpointAnnotation     = osacPrefix + "/api-endpoint"
	osacIngressEndpointAnnotation = osacPrefix + "/ingress-endpoint"
)

var (
	osacClusterOrderNameLabel         string = fmt.Sprintf("%s/clusterorder", osacPrefix)
	osacClusterOrderIDLabel           string = fmt.Sprintf("%s/clusterorder-uuid", osacPrefix)
	osacFinalizer                     string = fmt.Sprintf("%s/finalizer", osacPrefix)
	osacManagementStateAnnotation     string = fmt.Sprintf("%s/management-state", osacPrefix)
	osacClusterOrderFeedbackFinalizer string = fmt.Sprintf("%s/clusterorder-feedback", osacPrefix)
	osacAutoProvisionedLabel          string = fmt.Sprintf("%s/auto-provisioned", osacPrefix)
	osacAutoProvisionedForLabel       string = fmt.Sprintf("%s/auto-provisioned-for", osacPrefix)
)

func generateNamespaceName(instance *v1alpha1.ClusterOrder) string {
	return fmt.Sprintf("%s-%s", instance.GetNamespace(), instance.GetName())
}

// hubAccessClusterRoleName returns the ClusterRole name, matching the
// "{namespace}-" prefix charts/operator/templates/hub-access-clusterrole.yaml
// applies to this cluster-scoped resource.
func (r *ClusterOrderReconciler) hubAccessClusterRoleName() string {
	return fmt.Sprintf("%s-%s", r.ClusterOrderNamespace, hubAccessClusterRoleBaseName)
}
