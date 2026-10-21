/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReconcileSignalScheduler periodically signals a resource while asynchronous cleanup is pending.
type ReconcileSignalScheduler struct {
	logger        *slog.Logger
	retryInterval time.Duration

	mu      sync.Mutex
	pending map[string]struct{}
}

// NewReconcileSignalScheduler creates a scheduler that retries signals at retryInterval until the resource is gone.
func NewReconcileSignalScheduler(logger *slog.Logger, retryInterval time.Duration) *ReconcileSignalScheduler {
	if retryInterval <= 0 {
		panic("retry interval must be positive")
	}
	return &ReconcileSignalScheduler{
		logger:        logger,
		retryInterval: retryInterval,
		pending:       make(map[string]struct{}),
	}
}

// Schedule starts a single signal loop for id. It stops when ctx is canceled or signal returns NotFound.
func (s *ReconcileSignalScheduler) Schedule(
	ctx context.Context,
	id string,
	signal func(context.Context, string) error,
) {
	if ctx.Err() != nil || id == "" || signal == nil {
		return
	}

	s.mu.Lock()
	if _, exists := s.pending[id]; exists {
		s.mu.Unlock()
		return
	}
	s.pending[id] = struct{}{}
	s.mu.Unlock()

	go s.run(ctx, id, signal)
}

func (s *ReconcileSignalScheduler) run(ctx context.Context, id string, signal func(context.Context, string) error) {
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	ticker := time.NewTicker(s.retryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			signalCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := signal(signalCtx, id)
			cancel()
			if status.Code(err) == codes.NotFound {
				return
			}
			if err != nil {
				s.logger.WarnContext(ctx, "Failed to signal resource for cleanup retry",
					slog.String("id", id),
					slog.Any("error", err),
				)
			}
		}
	}
}
