package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

type fabricOutputProvider struct {
	provisioning.ProvisioningProvider
	reader client.Reader
}

var _ provisioning.ProvisioningProviderWithProvisionOutputs = (*fabricOutputProvider)(nil)

func newFabricOutputProvider(base provisioning.ProvisioningProvider, reader client.Reader) *fabricOutputProvider {
	return &fabricOutputProvider{ProvisioningProvider: base, reader: reader}
}

func (p *fabricOutputProvider) GetProvisionStatusWithExtraVars(
	ctx context.Context, resource client.Object, jobID string,
) (provisioning.ProvisionStatusWithExtraVars, error) {
	status, err := p.ProvisioningProvider.GetProvisionStatus(ctx, resource, jobID)
	if err != nil {
		return provisioning.ProvisionStatusWithExtraVars{}, err
	}
	result := provisioning.ProvisionStatusWithExtraVars{ProvisionStatus: status}
	if status.State != v1alpha1.JobStateSucceeded {
		return result, nil
	}

	subnet, ok := resource.(*v1alpha1.Subnet)
	if !ok {
		return result, fmt.Errorf("fabric output ConfigMap requires a Subnet resource, got %T", resource)
	}
	configMapName := fmt.Sprintf("subnet-%s-fabric-output", subnet.Name)
	configMap := &corev1.ConfigMap{}
	if err := p.reader.Get(ctx, types.NamespacedName{Namespace: subnet.Namespace, Name: configMapName}, configMap); err != nil {
		return result, fmt.Errorf("getting fabric output ConfigMap %s/%s: %w", subnet.Namespace, configMapName, err)
	}

	result.ExtraVars, err = provisioning.ParseFabricOutputConfigMap(configMap.Data)
	if err != nil {
		return result, fmt.Errorf("reading fabric output ConfigMap %s/%s: %w", subnet.Namespace, configMapName, err)
	}
	return result, nil
}
