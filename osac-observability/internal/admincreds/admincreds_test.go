// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package admincreds

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	sourceNamespace = "keycloak"
	destNamespace   = "osac-observability"
)

func testConfig() Config {
	return Config{
		Namespace: destNamespace,
		CABundle: CABundle{
			SourceNamespace: sourceNamespace,
			SourceName:      "ca-bundle",
			SourceKey:       "bundle.pem",
			Name:            "keycloak-ca-bundle",
			Key:             "bundle.pem",
		},
	}
}

func sourceCABundle(value string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "ca-bundle", Namespace: sourceNamespace},
		Data:       map[string]string{"bundle.pem": value},
	}
}

func getConfigMap(t *testing.T, client kubernetes.Interface, namespace, name string) *corev1.ConfigMap {
	t.Helper()
	configMap, err := client.CoreV1().ConfigMaps(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ConfigMap %s/%s: %v", namespace, name, err)
	}
	return configMap
}

func TestReconcileCopiesTheCABundle(t *testing.T) {
	client := fake.NewSimpleClientset(sourceCABundle("ROOT-CA\n"))

	if err := Reconcile(context.Background(), client, testConfig()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getConfigMap(t, client, destNamespace, "keycloak-ca-bundle").Data["bundle.pem"]; got != "ROOT-CA\n" {
		t.Fatalf("copied bundle = %q", got)
	}
}

func TestReconcileUpdatesAChangedCABundle(t *testing.T) {
	client := fake.NewSimpleClientset(
		sourceCABundle("NEW-CA\n"),
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "keycloak-ca-bundle", Namespace: destNamespace},
			Data:       map[string]string{"bundle.pem": "OLD-CA\n"},
		},
	)

	if err := Reconcile(context.Background(), client, testConfig()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getConfigMap(t, client, destNamespace, "keycloak-ca-bundle").Data["bundle.pem"]; got != "NEW-CA\n" {
		t.Fatalf("copied bundle = %q", got)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset(sourceCABundle("ROOT-CA\n"))
	if err := Reconcile(context.Background(), client, testConfig()); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	actions := len(client.Actions())
	if err := Reconcile(context.Background(), client, testConfig()); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	for _, action := range client.Actions()[actions:] {
		if action.GetVerb() == "update" || action.GetVerb() == "create" {
			t.Fatalf("second Reconcile wrote: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
	}
}

func TestReconcileFailsWithoutTheSourceCABundle(t *testing.T) {
	if err := Reconcile(context.Background(), fake.NewSimpleClientset(), testConfig()); err == nil {
		t.Fatalf("expected an error when the source bundle is missing")
	}
}

func TestReconcileRejectsAPartialCABundleConfig(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(cfg *Config) { cfg.CABundle.SourceNamespace = "" },
		func(cfg *Config) { cfg.CABundle.SourceName = "" },
		func(cfg *Config) { cfg.CABundle.SourceKey = "" },
		func(cfg *Config) { cfg.CABundle.Name = "" },
		func(cfg *Config) { cfg.CABundle.Key = "" },
		func(cfg *Config) { cfg.Namespace = "" },
	} {
		cfg := testConfig()
		mutate(&cfg)
		if err := Reconcile(context.Background(), fake.NewSimpleClientset(sourceCABundle("ROOT-CA\n")), cfg); err == nil {
			t.Fatalf("expected a validation error for %+v", cfg)
		}
	}
}
