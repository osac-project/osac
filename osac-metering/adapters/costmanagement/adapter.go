/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package costmanagement

import (
	"context"

	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/adapters"
)

type adapter struct {
	client *client
}

// NewAdapterWithConfig creates a Cost Management provider adapter from an
// explicit ClientConfig, supporting custom paths, token files, and private CA
// bundles for both single-cluster and two-cluster deployments.
func NewAdapterWithConfig(cfg ClientConfig) (adapters.ProviderAdapter, error) {
	cl, err := newClientFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &adapter{client: cl}, nil
}

// NewAdapter creates a Cost Management provider adapter using a static Bearer
// token (which may be empty when authentication is handled by the network path).
func NewAdapter(baseURL, token string, logger logr.Logger) adapters.ProviderAdapter {
	return &adapter{
		client: newClient(baseURL, token, "", logger),
	}
}

// NewAdapterFromTokenFile creates a Cost Management provider adapter that reads
// its Bearer token from tokenFile on every request so Kubernetes Secret rotation
// is observed without restarting the pod.
func NewAdapterFromTokenFile(baseURL, tokenFile string, logger logr.Logger) adapters.ProviderAdapter {
	return &adapter{
		client: newClient(baseURL, "", tokenFile, logger),
	}
}

func (a *adapter) Name() string { return "cost-management" }

// Submit translates an OSAC CloudEvent into the locked Cost Management contract
// and delivers it synchronously to POST /api/v1/events. Synchronous delivery
// lets the shared Runner route rejected records individually to their tenant
// DLQ and prevents pre-flush deduplication loss.
func (a *adapter) Submit(ctx context.Context, event adapters.MeteringEvent) error {
	payload, err := TranslateEvent(event.CloudEvent)
	if err != nil {
		return err
	}
	return a.client.post(ctx, payload)
}

func (a *adapter) Flush(_ context.Context) (adapters.SubmitResult, error) {
	return adapters.SubmitResult{Idempotent: true}, nil
}

func (a *adapter) HealthCheck(ctx context.Context) error {
	return a.client.healthCheck(ctx)
}

func (a *adapter) Close() error { return nil }
