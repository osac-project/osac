// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"fmt"
	"log/slog"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Environment holds Kubernetes clients for a dedicated installation.
// Application connections and provider controls have separate owners.
type Environment struct {
	Namespace string
	Kube      kubernetes.Interface
	WatchKube kubernetes.Interface
	Client    client.Client
}

// Open uses the selected kubeconfig context without changing installed resources.
func Open(namespace string) (*Environment, error) {
	if namespace == "" {
		return nil, fmt.Errorf("NS is required")
	}

	rules := clientcmd.NewDefaultClientConfigLoadingRules()

	raw, err := rules.Load()
	if err != nil {
		return nil, err
	}

	config, err := clientcmd.NewNonInteractiveClientConfig(*raw, raw.CurrentContext, &clientcmd.ConfigOverrides{}, nil).
		ClientConfig()
	if err != nil {
		return nil, err
	}

	config.Timeout = 20 * time.Second

	kube, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	watchConfig := rest.CopyConfig(config)
	watchConfig.Timeout = 0

	watchKube, err := kubernetes.NewForConfig(watchConfig)
	if err != nil {
		return nil, err
	}

	scheme, err := apiScheme()
	if err != nil {
		return nil, err
	}

	native, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	slog.Info("opened dedicated Kubernetes clients", "context", raw.CurrentContext, "namespace", namespace)

	return &Environment{Namespace: namespace, Kube: kube, WatchKube: watchKube, Client: native}, nil
}
