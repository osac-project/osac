// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package harness connects tests to the existing local CaaS installation.
// It owns Kubernetes connections, installation checks, inventory observations
// and Ignition I/O. Suite fixtures register hosts through the host model and reuse
// Fulfillment IT clients; the awx package owns execution evidence.
//
// The installer owns deployment. Suite fixtures own application prerequisites,
// correlation and reuse checks; specs own behavior and outcome assertions.
package harness
