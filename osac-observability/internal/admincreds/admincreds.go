// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

// Package admincreds copies the trust bundle the Keycloak chart distributes
// into the observability namespace. The admin query gate fetches the OIDC
// discovery document and the JWK set from the identity provider, whose
// certificate is signed by a cluster CA the image does not trust; a volume
// cannot cross namespaces, so the bundle is copied here. The copy is a no-op
// when the desired state already holds, so an upgrade does not restart the
// gate.
package admincreds

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Config names the namespace the gate runs in and the trust bundle to copy.
type Config struct {
	// Namespace is the observability namespace the gate runs in.
	Namespace string
	// CABundle copies a trust bundle ConfigMap into Namespace.
	CABundle CABundle
}

// CABundle names a trust bundle to copy so the gate can verify the identity
// provider's certificate. Every field is required.
type CABundle struct {
	SourceNamespace string
	SourceName      string
	SourceKey       string
	Name            string
	Key             string
}

func (cfg Config) validate() error {
	if strings.TrimSpace(cfg.Namespace) == "" {
		return fmt.Errorf("Namespace is required")
	}
	for name, value := range map[string]string{
		"CABundle.SourceNamespace": cfg.CABundle.SourceNamespace,
		"CABundle.SourceName":      cfg.CABundle.SourceName,
		"CABundle.SourceKey":       cfg.CABundle.SourceKey,
		"CABundle.Name":            cfg.CABundle.Name,
		"CABundle.Key":             cfg.CABundle.Key,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	return nil
}

// Reconcile copies the trust bundle into the gate's namespace.
func Reconcile(ctx context.Context, client kubernetes.Interface, cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	return reconcileCABundle(ctx, client, cfg)
}

// reconcileCABundle copies the trust bundle into the gate's namespace. The
// bundle is not secret material, so it is a ConfigMap.
func reconcileCABundle(ctx context.Context, client kubernetes.Interface, cfg Config) error {
	bundle := cfg.CABundle
	source, err := client.CoreV1().ConfigMaps(bundle.SourceNamespace).Get(ctx, bundle.SourceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read ConfigMap %s/%s: %w", bundle.SourceNamespace, bundle.SourceName, err)
	}
	value := source.Data[bundle.SourceKey]
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("ConfigMap %s/%s has no non-empty %q key",
			bundle.SourceNamespace, bundle.SourceName, bundle.SourceKey)
	}
	configMaps := client.CoreV1().ConfigMaps(cfg.Namespace)
	existing, err := configMaps.Get(ctx, bundle.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, createErr := configMaps.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: bundle.Name, Namespace: cfg.Namespace},
			Data:       map[string]string{bundle.Key: value},
		}, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("create ConfigMap %s/%s: %w", cfg.Namespace, bundle.Name, createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read ConfigMap %s/%s: %w", cfg.Namespace, bundle.Name, err)
	}
	if existing.Data[bundle.Key] == value {
		return nil
	}
	if existing.Data == nil {
		existing.Data = map[string]string{}
	}
	existing.Data[bundle.Key] = value
	if _, err := configMaps.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update ConfigMap %s/%s: %w", cfg.Namespace, bundle.Name, err)
	}
	return nil
}
