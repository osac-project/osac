/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package m360

import (
	"context"

	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/adapters"
)

type m360Adapter struct {
	client *m360Client
}

// NewAdapter creates an M360 provider adapter for the given API configuration.
func NewAdapter(baseURL, apiVersion, apiKey string, logger logr.Logger) adapters.ProviderAdapter {
	client := newM360Client(baseURL, apiVersion, apiKey)
	client.logger = logger
	return &m360Adapter{client: client}
}

func (a *m360Adapter) Name() string { return "m360" }

func (a *m360Adapter) Submit(ctx context.Context, event adapters.MeteringEvent) error {
	endpoint, payload, err := translateEvent(event.CloudEvent)
	if err != nil {
		return err
	}
	return a.client.post(ctx, endpoint, payload)
}

func (a *m360Adapter) Flush(_ context.Context) (adapters.SubmitResult, error) {
	return adapters.SubmitResult{Idempotent: true}, nil
}

func (a *m360Adapter) HealthCheck(ctx context.Context) error {
	return a.client.healthCheck(ctx)
}

func (a *m360Adapter) Close() error { return nil }
