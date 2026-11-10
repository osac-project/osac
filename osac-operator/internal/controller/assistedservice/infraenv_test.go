// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package assistedservice

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestInfraEnvHelpers(t *testing.T) {
	t.Parallel()

	infraEnv := NewInfraEnvObject()
	if got, want := infraEnv.GroupVersionKind(), InfraEnvGroupVersionKind(); got != want {
		t.Errorf("InfraEnvGroupVersionKind() = %v, want %v", got, want)
	}
	if got, want := InfraEnvName("cluster-a"), "cluster-a-infraenv"; got != want {
		t.Errorf("InfraEnvName() = %q, want %q", got, want)
	}

	url := "https://assisted.example.test:6443/ignition"
	if err := unstructured.SetNestedField(infraEnv.Object, url,
		"status", "bootArtifacts", "discoveryIgnitionURL"); err != nil {
		t.Fatalf("set discovery ignition URL: %v", err)
	}
	got, found, err := DiscoveryIgnitionURL(infraEnv)
	if err != nil {
		t.Fatalf("DiscoveryIgnitionURL() error = %v", err)
	}
	if !found {
		t.Fatal("DiscoveryIgnitionURL() did not find the URL")
	}
	if got != url {
		t.Errorf("DiscoveryIgnitionURL() = %q, want %q", got, url)
	}

	got, found, err = DiscoveryIgnitionURL(NewInfraEnvObject())
	if err != nil {
		t.Fatalf("DiscoveryIgnitionURL() error = %v", err)
	}
	if found || got != "" {
		t.Errorf("DiscoveryIgnitionURL() = (%q, %t), want (empty, false)", got, found)
	}

	_, _, err = DiscoveryIgnitionURL(nil)
	if err == nil {
		t.Fatal("DiscoveryIgnitionURL(nil) error = nil, want an error")
	}
}
