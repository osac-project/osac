package v1alpha1

func (co *ClusterOrder) SetClusterReferenceNamespace(name string) {
	co.EnsureClusterReference()
	co.Status.ClusterReference.Namespace = name
}

func (co *ClusterOrder) SetClusterReferenceServiceAccountName(name string) {
	co.EnsureClusterReference()
	co.Status.ClusterReference.ServiceAccountName = name
}

func (co *ClusterOrder) SetClusterReferenceRoleBindingName(name string) {
	co.EnsureClusterReference()
	co.Status.ClusterReference.RoleBindingName = name
}

func (co *ClusterOrder) SetClusterReferenceHostedClusterName(name string) {
	co.EnsureClusterReference()
	co.Status.ClusterReference.HostedClusterName = name
}

func (co *ClusterOrder) EnsureClusterReference() {
	if co.Status.ClusterReference == nil {
		co.Status.ClusterReference = &ClusterOrderClusterReferenceType{}
	}
}

func (co *ClusterOrder) GetClusterReferenceNamespace() string {
	if co.Status.ClusterReference == nil {
		return ""
	}
	return co.Status.ClusterReference.Namespace
}

func (co *ClusterOrder) GetClusterReferenceServiceAccountName() string {
	if co.Status.ClusterReference == nil {
		return ""
	}
	return co.Status.ClusterReference.ServiceAccountName
}

func (co *ClusterOrder) GetClusterReferenceRoleBindingName() string {
	if co.Status.ClusterReference == nil {
		return ""
	}
	return co.Status.ClusterReference.RoleBindingName
}

func (co *ClusterOrder) GetClusterReferenceHostedClusterName() string {
	if co.Status.ClusterReference == nil {
		return ""
	}
	return co.Status.ClusterReference.HostedClusterName
}
