/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package echo

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/osac-project/osac-metering/adapters"
)

type echoAdapter struct {
	store     *EventStore
	submitted atomic.Int64
	flushed   atomic.Int64
}

// NewAdapter creates an echo provider adapter that records events in store.
func NewAdapter(store *EventStore) adapters.ProviderAdapter {
	return &echoAdapter{store: store}
}

func (a *echoAdapter) Name() string { return "echo" }

func (a *echoAdapter) Submit(_ context.Context, event adapters.MeteringEvent) error {
	fmt.Printf("[SUBMIT] id=%-36s type=%-30s topic=%-30s partition=%d offset=%d\n",
		event.CloudEvent.ID(),
		event.CloudEvent.Type(),
		event.Topic,
		event.Partition,
		event.Offset,
	)
	a.store.add(event)
	a.submitted.Add(1)
	return nil
}

func (a *echoAdapter) Flush(_ context.Context) (adapters.SubmitResult, error) {
	n := a.flushed.Add(1)
	total := a.submitted.Load()
	fmt.Printf("[FLUSH]  #%d — %d events submitted so far\n", n, total)
	return adapters.SubmitResult{Idempotent: true}, nil
}

func (a *echoAdapter) HealthCheck(_ context.Context) error { return nil }

func (a *echoAdapter) Close() error {
	fmt.Printf("[CLOSE]  total events submitted: %d, total flushes: %d\n",
		a.submitted.Load(), a.flushed.Load())
	return nil
}
