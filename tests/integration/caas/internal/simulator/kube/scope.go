// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package kube

import "slices"

// Scope lists the Agent namespaces that this simulator serves.
type Scope struct{ AgentNamespaces []string }

// Contains reports whether namespace is in the configured Agent namespace list.
func (s Scope) Contains(namespace string) bool { return slices.Contains(s.AgentNamespaces, namespace) }
