// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"fmt"
	"slices"

	"k8s.io/client-go/rest"
)

// Config supplies the connection and native namespace scopes.
type Config struct {
	RESTConfig        *rest.Config
	AgentNamespaces   []string
	SettingsNamespace string
	Metal3Namespace   string
}

// validate requires a connection and namespace scopes.
// Run checks these settings before opening clients or starting controllers.
func (c Config) validate() error {
	if c.RESTConfig == nil || c.SettingsNamespace == "" || len(c.AgentNamespaces) == 0 {
		return fmt.Errorf("cluster connection, settings namespace and Agent namespaces are required")
	}

	if slices.Contains(c.AgentNamespaces, "") {
		return fmt.Errorf("empty Agent namespace")
	}

	if c.Metal3Namespace == "" {
		return fmt.Errorf("Metal3 inventory namespace is required")
	}

	return nil
}
