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

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/directclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
)

// remoteClusterAccess holds the Kubernetes access the Metal3 backends use
// for BareMetalHosts and their BMC credential Secrets when
// OSAC_REMOTE_CLUSTER_KUBECONFIG is set. A nil *remoteClusterAccess is local
// mode: every client then resolves through the manager, as before.
type remoteClusterAccess struct {
	// cluster serves the BMH reads a cache may satisfy and owns the REST
	// config the registry backends build their own clients from.
	cluster cluster.Cluster
	// direct reads and writes straight to the outer API server, for BMC
	// Secret writes and deletion confirmation.
	direct directclient.Client
}

// loadRemoteClusterAccess resolves OSAC_REMOTE_CLUSTER_KUBECONFIG. An unset
// variable returns nil access (local mode, unchanged behavior).
//
// The kubeconfig is loaded once at startup and never re-read. This operator
// loads every other configuration that way, and a run's outer kubeconfig is
// stable for the run's lifetime; rotating it requires a pod restart.
func loadRemoteClusterAccess() (*remoteClusterAccess, error) {
	path := os.Getenv(envRemoteClusterKubeconfig)
	if path == "" {
		return nil, nil
	}
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%s is set to a blank path", envRemoteClusterKubeconfig)
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("build config from kubeconfig %q: %w", path, err)
	}

	// The outer cluster serves BareMetalHosts, HardwareData and BMC
	// credential Secrets. No OSAC type resolves there, so the scheme stays
	// this small.
	remoteScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(remoteScheme))
	utilruntime.Must(metal3api.AddToScheme(remoteScheme))

	remoteCluster, err := cluster.New(restConfig, func(o *cluster.Options) {
		o.Scheme = remoteScheme
	})
	if err != nil {
		return nil, fmt.Errorf("create remote cluster from kubeconfig %q: %w", path, err)
	}

	direct, err := directclient.New(restConfig, remoteScheme, remoteCluster.GetRESTMapper())
	if err != nil {
		return nil, fmt.Errorf("create direct client for kubeconfig %q: %w", path, err)
	}

	setupLog.Info("remote cluster mode: BareMetalHosts and their secrets resolve through the outer kubeconfig",
		"kubeconfig", path, "host", restConfig.Host)
	return &remoteClusterAccess{cluster: remoteCluster, direct: direct}, nil
}

// applyRemoteRestConfig points the Kubernetes-reaching backend configs at
// the outer cluster. The metal3 inventory and management constructors build
// their own clients; without this they would dial the local cluster.
func applyRemoteRestConfig(
	inventoryCfg *inventory.Config,
	managementCfg *management.Config,
	remote *remoteClusterAccess,
) {
	if remote == nil {
		return
	}
	restConfig := remote.cluster.GetConfig()
	inventoryCfg.RESTConfig = restConfig
	managementCfg.RESTConfig = restConfig
}

// resolveMetal3Clients returns the clients the Metal3 backends read and
// write BareMetalHosts and their BMC credential Secrets through: the outer
// cluster's in remote mode, the manager's own otherwise.
func resolveMetal3Clients(
	mgr ctrl.Manager,
	remote *remoteClusterAccess,
) (client.Client, directclient.Client, error) {
	if remote != nil {
		return remote.cluster.GetClient(), remote.direct, nil
	}
	if mgr == nil {
		return nil, nil, errors.New("kubernetes manager is required to create Metal3 dependencies")
	}
	// Keep BMC credential Secret access off the cluster-wide informer. The
	// direct client avoids Secret list/watch permissions and limits access to
	// the operations performed by the BMH manager and the credentials writer.
	direct, err := directclient.New(mgr.GetConfig(), mgr.GetScheme(), mgr.GetRESTMapper())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create direct Kubernetes client: %w", err)
	}
	return mgr.GetClient(), direct, nil
}

// startComponents runs the manager and, in remote mode, the outer cluster's
// cache, stopping both when either fails. The remote cluster must run
// alongside the manager: its cached client blocks on the first read until
// Start begins serving informers.
func startComponents(ctx context.Context, remote *remoteClusterAccess, mgr ctrl.Manager) error {
	g, ctx := errgroup.WithContext(ctx)
	if remote != nil {
		g.Go(func() error {
			return ignoreCanceled(remote.cluster.Start(ctx))
		})
	}
	g.Go(func() error {
		return ignoreCanceled(mgr.Start(ctx))
	})
	return g.Wait()
}

// ignoreCanceled returns nil if the error is exactly context.Canceled, the
// expected shutdown path when the errgroup cancels the shared context. Only
// pure cancellation is filtered; mixed or wrapped errors are preserved.
func ignoreCanceled(err error) error {
	if err == context.Canceled {
		return nil
	}
	return err
}
