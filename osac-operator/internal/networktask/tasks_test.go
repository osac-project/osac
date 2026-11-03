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

package networktask_test

import (
	"testing"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

func TestValidateTaskForManager(t *testing.T) {
	tests := []struct {
		name       string
		task       string
		manager    v1alpha1.NetworkJobManager
		managerRef *v1alpha1.NetworkManagerRef
		wantErr    bool
	}{
		{"k8s_only ExternalIP allocation", networktask.CreateExternalIP, v1alpha1.NetworkJobManagerKubernetes, &v1alpha1.NetworkManagerRef{Name: "k8s_only", ExternalIPContractVersion: "v1"}, false},
		{"k8s_only ExternalIP pool release", networktask.DeleteExternalIPPool, v1alpha1.NetworkJobManagerKubernetes, &v1alpha1.NetworkManagerRef{Name: "k8s_only", ExternalIPContractVersion: "v1"}, false},
		{"other Kubernetes manager cannot allocate ExternalIP", networktask.CreateExternalIP, v1alpha1.NetworkJobManagerKubernetes, &v1alpha1.NetworkManagerRef{Name: "cudn_evpn", ExternalIPContractVersion: "v1"}, true},
		{"k8s_only without v1 cannot allocate ExternalIP", networktask.CreateExternalIP, v1alpha1.NetworkJobManagerKubernetes, &v1alpha1.NetworkManagerRef{Name: "k8s_only"}, true},
		{"k8s_only cannot attach ExternalIP", networktask.AttachExternalIP, v1alpha1.NetworkJobManagerKubernetes, &v1alpha1.NetworkManagerRef{Name: "k8s_only", ExternalIPContractVersion: "v1"}, true},
		{"Fabric ExternalIP allocation", networktask.CreateExternalIP, v1alpha1.NetworkJobManagerFabric, &v1alpha1.NetworkManagerRef{Name: "netris"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := networktask.ValidateTaskForManager(tt.task, tt.manager, tt.managerRef)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateTaskForManager() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
