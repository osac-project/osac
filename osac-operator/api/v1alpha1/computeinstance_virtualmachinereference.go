package v1alpha1

func (ci *ComputeInstance) SetVirtualMachineReferenceNamespace(name string) {
	ci.EnsureVirtualMachineReference()
	ci.Status.VirtualMachineReference.Namespace = name
}

func (ci *ComputeInstance) SetVirtualMachineReferenceKubeVirtVirtualMachineName(name string) {
	ci.EnsureVirtualMachineReference()
	ci.Status.VirtualMachineReference.KubeVirtVirtualMachineName = name
}

func (ci *ComputeInstance) EnsureVirtualMachineReference() {
	if ci.Status.VirtualMachineReference == nil {
		ci.Status.VirtualMachineReference = &VirtualMachineReferenceType{}
	}
}

func (ci *ComputeInstance) GetVirtualMachineReferenceNamespace() string {
	if ci.Status.VirtualMachineReference == nil {
		return ""
	}
	return ci.Status.VirtualMachineReference.Namespace
}

func (ci *ComputeInstance) GetVirtualMachineReferenceKubeVirtVirtualMachineName() string {
	if ci.Status.VirtualMachineReference == nil {
		return ""
	}
	return ci.Status.VirtualMachineReference.KubeVirtVirtualMachineName
}

func (ci *ComputeInstance) SetTenantReferenceName(name string) {
	ci.EnsureTenantReference()
	ci.Status.TenantReference.Name = name
}

func (ci *ComputeInstance) SetTenantReferenceNamespace(name string) {
	ci.EnsureTenantReference()
	ci.Status.TenantReference.Namespace = name
}

func (ci *ComputeInstance) EnsureTenantReference() {
	if ci.Status.TenantReference == nil {
		ci.Status.TenantReference = &TenantReferenceType{}
	}
}

func (ci *ComputeInstance) GetTenantReferenceName() string {
	if ci.Status.TenantReference == nil {
		return ""
	}
	return ci.Status.TenantReference.Name
}

func (ci *ComputeInstance) GetTenantReferenceNamespace() string {
	if ci.Status.TenantReference == nil {
		return ""
	}
	return ci.Status.TenantReference.Namespace
}

func (ci *ComputeInstance) SetIPAddress(ip string) {
	ci.Status.IPAddress = ip
}

func (ci *ComputeInstance) GetIPAddress() string {
	return ci.Status.IPAddress
}

func (ci *ComputeInstance) SetExternalIPAddress(ip string) {
	ci.Status.ExternalIPAddress = ip
}

func (ci *ComputeInstance) GetExternalIPAddress() string {
	return ci.Status.ExternalIPAddress
}
