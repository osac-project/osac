// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package simulator starts controllers that supply selected HyperShift, Cluster API,
// CAP-Agent, Assisted and Metal3/BMO responses for integration tests. Each controller
// reads and updates its component's Kubernetes resources. The controllers resume from those
// resources after a restart. They keep no separate record of test progress.
//
// The selected profile lets installation finish, fail or wait, or holds control-plane
// availability or host release. Tests register host inventory and connect application
// requests to provider resources. These controllers model provider responses only.
// They do not boot hosts, register guest Nodes or run a guest API server.
package simulator
