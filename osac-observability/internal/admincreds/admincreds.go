// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

// Package admincreds provisions the Secrets the admin query proxies mount: the
// thanos-admin Keycloak client secret and one cookie secret per proxy. It runs
// as an install/upgrade hook, so every write is a no-op when the desired state
// already holds: changing the client secret on every upgrade would break the
// running proxies, and rotating a cookie secret would log every admin out.
package admincreds

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// CookieSecretBytes is the cookie secret length both authentication proxies
// accept (16, 24 or 32). The value is stored base64-encoded, which the proxies
// decode back to this many bytes.
const CookieSecretBytes = 32

// Config names the source and destination objects and the keys inside them.
type Config struct {
	// KeycloakNamespace and KeycloakSecretName locate the Secret the Keycloak
	// provisioning writes; KeycloakKey names the client inside it.
	KeycloakNamespace  string
	KeycloakSecretName string
	KeycloakKey        string
	// Namespace is the observability namespace the proxies run in.
	Namespace string
	// ClientSecretName holds the copied Keycloak client secret under
	// ClientSecretKey.
	ClientSecretName string
	ClientSecretKey  string
	// CookieSecretNames hold the per-proxy cookie secrets under
	// CookieSecretKey. An existing non-empty value is never replaced.
	CookieSecretNames []string
	CookieSecretKey   string
	// Random supplies the cookie secret bytes; it defaults to crypto/rand.
	Random io.Reader
}

func (cfg Config) validate() error {
	for name, value := range map[string]string{
		"KeycloakNamespace":  cfg.KeycloakNamespace,
		"KeycloakSecretName": cfg.KeycloakSecretName,
		"KeycloakKey":        cfg.KeycloakKey,
		"Namespace":          cfg.Namespace,
		"ClientSecretName":   cfg.ClientSecretName,
		"ClientSecretKey":    cfg.ClientSecretKey,
		"CookieSecretKey":    cfg.CookieSecretKey,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if len(cfg.CookieSecretNames) == 0 {
		return fmt.Errorf("CookieSecretNames must list at least one Secret")
	}
	for _, name := range cfg.CookieSecretNames {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("CookieSecretNames must not contain an empty name")
		}
	}
	return nil
}

// Reconcile copies the Keycloak client secret and ensures every cookie secret
// exists with a value.
func Reconcile(ctx context.Context, client kubernetes.Interface, cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}

	source, err := client.CoreV1().Secrets(cfg.KeycloakNamespace).Get(ctx, cfg.KeycloakSecretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read Secret %s/%s: %w", cfg.KeycloakNamespace, cfg.KeycloakSecretName, err)
	}
	value := strings.TrimSpace(string(source.Data[cfg.KeycloakKey]))
	if value == "" {
		return fmt.Errorf("Secret %s/%s has no non-empty %q key",
			cfg.KeycloakNamespace, cfg.KeycloakSecretName, cfg.KeycloakKey)
	}

	if err := reconcileClientSecret(ctx, client, cfg, []byte(value)); err != nil {
		return err
	}
	for _, name := range cfg.CookieSecretNames {
		if err := reconcileCookieSecret(ctx, client, cfg, name); err != nil {
			return err
		}
	}
	return nil
}

func reconcileClientSecret(ctx context.Context, client kubernetes.Interface, cfg Config, value []byte) error {
	secrets := client.CoreV1().Secrets(cfg.Namespace)
	existing, err := secrets.Get(ctx, cfg.ClientSecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, createErr := secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: cfg.ClientSecretName, Namespace: cfg.Namespace},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{cfg.ClientSecretKey: value},
		}, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("create Secret %s/%s: %w", cfg.Namespace, cfg.ClientSecretName, createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Secret %s/%s: %w", cfg.Namespace, cfg.ClientSecretName, err)
	}
	if bytes.Equal(existing.Data[cfg.ClientSecretKey], value) {
		return nil
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	existing.Data[cfg.ClientSecretKey] = value
	if _, err := secrets.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update Secret %s/%s: %w", cfg.Namespace, cfg.ClientSecretName, err)
	}
	return nil
}

func reconcileCookieSecret(ctx context.Context, client kubernetes.Interface, cfg Config, name string) error {
	secrets := client.CoreV1().Secrets(cfg.Namespace)
	existing, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		value, genErr := newCookieSecret(cfg.Random)
		if genErr != nil {
			return genErr
		}
		_, createErr := secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cfg.Namespace},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{cfg.CookieSecretKey: value},
		}, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("create Secret %s/%s: %w", cfg.Namespace, name, createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Secret %s/%s: %w", cfg.Namespace, name, err)
	}
	if len(existing.Data[cfg.CookieSecretKey]) > 0 {
		// Never rotate: an active session cookie is encrypted with this value.
		return nil
	}
	value, err := newCookieSecret(cfg.Random)
	if err != nil {
		return err
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	existing.Data[cfg.CookieSecretKey] = value
	if _, err := secrets.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update Secret %s/%s: %w", cfg.Namespace, name, err)
	}
	return nil
}

// newCookieSecret returns a base64-encoded random cookie secret.
func newCookieSecret(random io.Reader) ([]byte, error) {
	raw := make([]byte, CookieSecretBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return nil, fmt.Errorf("generate a cookie secret: %w", err)
	}
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(raw)))
	base64.StdEncoding.Encode(encoded, raw)
	return encoded, nil
}
