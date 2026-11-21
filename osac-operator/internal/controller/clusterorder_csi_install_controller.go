/*
Copyright 2025.

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

package controller

import (
	"context"
	"time"

	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// CSIInstallReconciler launches the job that installs the OSAC CSI driver for
// each Ready ClusterOrder.
type CSIInstallReconciler struct {
	client.Client
	apiReader             client.Reader
	ClusterOrderNamespace string
	ProvisioningProvider  provisioning.ProvisioningProvider
	StatusPollInterval    time.Duration
	MaxJobHistory         int
	Enabled               bool
	Recorder              events.EventRecorder
	getAdminKubeconfig    func(context.Context, *v1alpha1.ClusterOrder) ([]byte, error)
}

// NewCSIInstallReconciler creates a reconciler for CSI install jobs.
func NewCSIInstallReconciler(
	k8sClient client.Client,
	apiReader client.Reader,
	namespace string,
	provider provisioning.ProvisioningProvider,
	pollInterval time.Duration,
	enabled bool,
) *CSIInstallReconciler {
	return &CSIInstallReconciler{
		Client:                k8sClient,
		apiReader:             apiReader,
		ClusterOrderNamespace: namespace,
		ProvisioningProvider:  provider,
		StatusPollInterval:    pollInterval,
		MaxJobHistory:         provisioning.DefaultMaxJobHistory,
		Enabled:               enabled,
	}
}

// SetupWithManager registers the namespaced ClusterOrder controller.
func (r *CSIInstallReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return nil
}

// Reconcile launches and tracks the CSI install job of a ClusterOrder.
func (r *CSIInstallReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}
