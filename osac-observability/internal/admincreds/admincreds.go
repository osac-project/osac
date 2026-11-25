// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

// Package admincreds provisions what the admin query proxies mount: the
// thanos-admin Keycloak client secret, one cookie secret per proxy, and a copy
// of the trust bundle that verifies the identity provider's certificate. It
// runs as an install/upgrade hook, so every write is a no-op when the desired
// state already holds: changing the client secret on every upgrade would break
// the running proxies, and rotating a cookie secret would log every admin out.
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
// accept (16, 24 or 32).
const CookieSecretBytes = 32

// CookieSecret names one generated Secret and the form its consumer reads.
type CookieSecret struct {
	Name string
	// Base64 stores the value as standard base64, the form ose-oauth-proxy
	// decodes from its --cookie-secret-file. When false the value is unpadded
	// base64url, which oauth2-proxy decodes from its environment variable.
	// Both forms are printable text: an environment variable cannot carry the
	// binary key, because the runtime replaces invalid UTF-8 on the way in.
	Base64 bool
}

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
	// CookieSecrets hold the per-proxy cookie secrets under CookieSecretKey.
	// An existing value is never rotated; the rewrites allowed are re-encoding
	// a legacy standard-base64 value and replacing a binary value, which no
	// consumer can read.
	CookieSecrets   []CookieSecret
	CookieSecretKey string
	// CABundle copies a trust bundle ConfigMap into Namespace. Empty disables
	// the copy.
	CABundle CABundle
	// Random supplies the cookie secret bytes; it defaults to crypto/rand.
	Random io.Reader
}

// CABundle names a trust bundle to copy so the proxies can verify the identity
// provider's certificate. A volume cannot cross namespaces, so the bundle the
// Keycloak chart distributes is copied beside the proxies. Either every field
// is set or none is.
type CABundle struct {
	SourceNamespace string
	SourceName      string
	SourceKey       string
	Name            string
	Key             string
}

func (bundle CABundle) enabled() bool {
	return bundle != CABundle{}
}

func (bundle CABundle) validate() error {
	if !bundle.enabled() {
		return nil
	}
	for name, value := range map[string]string{
		"CABundle.SourceNamespace": bundle.SourceNamespace,
		"CABundle.SourceName":      bundle.SourceName,
		"CABundle.SourceKey":       bundle.SourceKey,
		"CABundle.Name":            bundle.Name,
		"CABundle.Key":             bundle.Key,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required when the CA bundle is copied", name)
		}
	}
	return nil
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
	if len(cfg.CookieSecrets) == 0 {
		return fmt.Errorf("CookieSecrets must list at least one Secret")
	}
	for _, secret := range cfg.CookieSecrets {
		if strings.TrimSpace(secret.Name) == "" {
			return fmt.Errorf("CookieSecrets must not contain an empty name")
		}
	}
	return cfg.CABundle.validate()
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
	for _, secret := range cfg.CookieSecrets {
		if err := reconcileCookieSecret(ctx, client, cfg, secret); err != nil {
			return err
		}
	}
	return reconcileCABundle(ctx, client, cfg)
}

// reconcileCABundle copies the trust bundle into the proxies' namespace. The
// bundle is not secret material, so it is a ConfigMap.
func reconcileCABundle(ctx context.Context, client kubernetes.Interface, cfg Config) error {
	if !cfg.CABundle.enabled() {
		return nil
	}
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

func reconcileCookieSecret(ctx context.Context, client kubernetes.Interface, cfg Config, spec CookieSecret) error {
	secrets := client.CoreV1().Secrets(cfg.Namespace)
	existing, err := secrets.Get(ctx, spec.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		value, genErr := newCookieSecret(cfg.Random, spec)
		if genErr != nil {
			return genErr
		}
		_, createErr := secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: cfg.Namespace},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{cfg.CookieSecretKey: value},
		}, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("create Secret %s/%s: %w", cfg.Namespace, spec.Name, createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Secret %s/%s: %w", cfg.Namespace, spec.Name, err)
	}
	// Never rotate: an active session cookie is encrypted with this value. A
	// value is rewritten only when no consumer can read it as stored, and a
	// printable value is otherwise left alone even if its length is wrong.
	value := existing.Data[cfg.CookieSecretKey]
	switch {
	case len(value) == 0:
		if value, err = newCookieSecret(cfg.Random, spec); err != nil {
			return err
		}
	case !isPrintableASCII(value):
		// The environment path replaces invalid UTF-8, so the proxy reads a
		// different value than the Secret holds.
		if value, err = newCookieSecret(cfg.Random, spec); err != nil {
			return err
		}
	default:
		if migrated := convertLegacyCookieSecret(value, spec); migrated != nil {
			value = migrated
		} else {
			return nil
		}
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	existing.Data[cfg.CookieSecretKey] = value
	if _, err := secrets.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update Secret %s/%s: %w", cfg.Namespace, spec.Name, err)
	}
	return nil
}

// convertLegacyCookieSecret returns the stored value re-encoded for a consumer
// that reads base64url when the value is standard base64 of a usable key. It
// returns nil when the stored value needs no change.
func convertLegacyCookieSecret(value []byte, spec CookieSecret) []byte {
	if spec.Base64 || readsCookieSecret(value) {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(string(value))
	if err != nil || !isCookieSecretLength(len(decoded)) {
		return nil
	}
	return encodeCookieSecret(decoded, spec)
}

// readsCookieSecret reports whether an environment Secret's value is one
// oauth2-proxy accepts: a plain 16, 24 or 32 byte key, or base64url that
// denotes one.
func readsCookieSecret(value []byte) bool {
	if isCookieSecretLength(len(value)) {
		return true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(value))
	return err == nil && isCookieSecretLength(len(decoded))
}

func isCookieSecretLength(n int) bool {
	return n == 16 || n == 24 || n == 32
}

// isPrintableASCII reports whether the value can be carried unchanged through
// an environment variable.
func isPrintableASCII(value []byte) bool {
	if len(value) == 0 {
		return false
	}
	for _, b := range value {
		if b < 0x20 || b > 0x7e {
			return false
		}
	}
	return true
}

// newCookieSecret returns a random cookie secret in the form the consumer
// reads.
func newCookieSecret(random io.Reader, spec CookieSecret) ([]byte, error) {
	raw := make([]byte, CookieSecretBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return nil, fmt.Errorf("generate a cookie secret: %w", err)
	}
	return encodeCookieSecret(raw, spec), nil
}

// encodeCookieSecret returns the printable form each consumer reads: standard
// base64 for the mounted file, unpadded base64url for the environment variable.
// Both decode back to the same key bytes.
func encodeCookieSecret(raw []byte, spec CookieSecret) []byte {
	if spec.Base64 {
		return []byte(base64.StdEncoding.EncodeToString(raw))
	}
	return []byte(base64.RawURLEncoding.EncodeToString(raw))
}
