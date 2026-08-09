/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package projection

import (
	"context"
	"errors"
	"time"
)

var ErrStaleVersion = errors.New("fulfillment_version is stale: stored version > incoming version")

type Store interface {
	Get(ctx context.Context, resourceID string) (*ResourceState, error)
	Upsert(ctx context.Context, state ResourceState) error
	Delete(ctx context.Context, resourceID string) error
	ListBillable(ctx context.Context) ([]ResourceState, error)
	ListAll(ctx context.Context) ([]ResourceState, error)
	UpdateLastHeartbeat(ctx context.Context, resourceIDs []string, at time.Time) error
}
