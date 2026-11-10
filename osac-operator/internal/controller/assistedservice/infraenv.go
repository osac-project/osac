// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package assistedservice provides shared helpers for Assisted Installer resources.
package assistedservice

import (
	"errors"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	AgentInstallAPIGroup   = "agent-install.openshift.io"
	AgentInstallAPIVersion = "v1beta1"
	InfraEnvNameSuffix     = "-infraenv"
	infraEnvKind           = "InfraEnv"
)

// InfraEnvGroupVersionKind returns the Assisted Installer InfraEnv GVK.
func InfraEnvGroupVersionKind() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group: AgentInstallAPIGroup, Version: AgentInstallAPIVersion, Kind: infraEnvKind,
	}
}

// NewInfraEnvObject creates an unstructured object configured for InfraEnv API operations.
func NewInfraEnvObject() *unstructured.Unstructured {
	infraEnv := &unstructured.Unstructured{}
	infraEnv.SetGroupVersionKind(InfraEnvGroupVersionKind())
	return infraEnv
}

// InfraEnvName returns the deterministic InfraEnv name for a ClusterOrder.
func InfraEnvName(clusterOrderName string) string {
	return clusterOrderName + InfraEnvNameSuffix
}

// DiscoveryIgnitionURL returns the URL published by an InfraEnv, if present.
func DiscoveryIgnitionURL(infraEnv *unstructured.Unstructured) (string, bool, error) {
	if infraEnv == nil {
		return "", false, errors.New("InfraEnv is nil")
	}
	return unstructured.NestedString(infraEnv.Object, "status", "bootArtifacts", "discoveryIgnitionURL")
}
