/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package install implements the prerequisite discovery and validation
// checks run by `osac install discover` and `osac install validate` against
// a target Hub cluster, before OSAC itself has been installed there.
package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// connectTimeout bounds every request Clients makes, including the
// connectivity probe in LoadClients below. Without it, a kubeconfig
// pointing at an unreachable or black-holed API server has no timeout at
// all: LoadClients would hang, or (for a promptly-refused connection)
// "succeed" and let every single prerequisite check fail independently
// with its own copy of the same dial error.
const connectTimeout = 10 * time.Second

// ErrNoClusterConfig is returned by LoadClients when no kubeconfig can be
// found by any of the usual means and the process isn't running inside a
// cluster either -- i.e. there is simply no cluster to check.
var ErrNoClusterConfig = errors.New(
	"no Kubernetes cluster configuration found: set --kubeconfig, the KUBECONFIG " +
		"environment variable, create ~/.kube/config, or run this command from inside the cluster",
)

// Clients bundles the two client-go client shapes the install checks need: a
// typed clientset for built-in resources (StorageClass, Secret) and a
// dynamic client for resources this package has no generated types for
// (CustomResourceDefinition, ClusterServiceVersion, ClusterVersion, Metal3's
// Provisioning).
type Clients struct {
	Typed   kubernetes.Interface
	Dynamic dynamic.Interface
}

// LoadClients builds Clients using the same kubeconfig resolution as
// oc/kubectl: an explicit kubeconfigPath if non-empty, otherwise the
// KUBECONFIG environment variable, otherwise ~/.kube/config, using whichever
// context is currently active -- falling back to in-cluster configuration
// (the ServiceAccount token/CA mounted into the pod) when none of those
// resolve to a file, so the same binary works unattended inside a
// Kubernetes Job as well as at a human's terminal. This deliberately does
// not use `osac login`'s credentials: that JWT authenticates against
// fulfillment-service's own API, not the Hub cluster's Kubernetes API, which
// is what discovery/validation checks run against before fulfillment-service
// itself has been installed there.
func LoadClients(kubeconfigPath string) (*Clients, error) {
	restConfig, err := loadRESTConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	if restConfig.Timeout == 0 {
		restConfig.Timeout = connectTimeout
	}
	typedClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	// A kubeconfig can resolve and parse fine yet point at a server that
	// simply isn't there (a stale local port-forward, a torn-down cluster).
	// Without this probe, that surfaces as every single prerequisite check
	// independently failing with its own copy of the same connection error
	// instead of one clear message up front.
	if _, err := typedClient.Discovery().ServerVersion(); err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", restConfig.Host, err)
	}
	return &Clients{
		Typed:   typedClient,
		Dynamic: dynamicClient,
	}, nil
}

// loadRESTConfig resolves a kubeconfig itself, rather than handing an
// unresolved path straight to clientcmd, so each failure mode gets its own
// clear message instead of one generic wrapped clientcmd error:
//   - an explicit path that doesn't exist -> a plain "file not found"
//   - a kubeconfig file exists (explicit path, one of KUBECONFIG's
//     colon-separated entries, or ~/.kube/config) but is malformed, or
//     selects a context that doesn't exist -> clientcmd's own error,
//     unchanged, since that detail is the useful part
//   - no kubeconfig anywhere, and not running in-cluster -> ErrNoClusterConfig
func loadRESTConfig(kubeconfigPath string) (*rest.Config, error) {
	switch {
	case kubeconfigPath != "":
		if _, err := os.Stat(kubeconfigPath); err != nil {
			return nil, fmt.Errorf("kubeconfig file not found: %s", kubeconfigPath)
		}
	case !anyFileExists(candidateKubeconfigPaths()):
		// Neither KUBECONFIG's entries nor ~/.kube/config resolve to a real
		// file: try in-cluster configuration before giving up, so the same
		// binary works unattended inside a Job pod.
		if restConfig, err := rest.InClusterConfig(); err == nil {
			return restConfig, nil
		}
		return nil, ErrNoClusterConfig
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = kubeconfigPath
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, &clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}
	return restConfig, nil
}

// candidateKubeconfigPaths returns the files that might hold a kubeconfig
// when no explicit path is given, in priority order: each of KUBECONFIG's
// (possibly multiple, OS-list-separator-joined) entries, then
// ~/.kube/config. Resolved fresh on every call via os.Getenv/os.UserHomeDir
// -- unlike clientcmd's own default loading rules, which resolve the home
// directory once at package init and so can't be redirected by changing
// $HOME afterward (notably in tests).
func candidateKubeconfigPaths() []string {
	var paths []string
	if kubeconfigEnv := os.Getenv("KUBECONFIG"); kubeconfigEnv != "" {
		paths = append(paths, filepath.SplitList(kubeconfigEnv)...)
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".kube", "config"))
	}
	return paths
}

func anyFileExists(paths []string) bool {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}
