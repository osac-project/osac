/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package it

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCatalogItemDeletionProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reads, signals := 0, 0
		deleted := false
		probe := catalogItemDeletionProbe(func(context.Context) error {
			reads++
			if deleted {
				return status.Error(codes.NotFound, "archived")
			}
			return nil
		}, func(context.Context) error {
			signals++
			return nil
		})
		ctx := context.Background()
		for i := range 16 {
			done, err := probe(ctx)
			if done || err != nil {
				t.Fatalf("existing resource treated as gone: done=%v err=%v", done, err)
			}
			wantSignals := 1 + i/10
			if signals != wantSignals {
				t.Fatalf("poll %d: signals=%d, want %d", i, signals, wantSignals)
			}
			time.Sleep(200 * time.Millisecond)
		}
		deleted = true
		if done, err := probe(ctx); !done || err != nil || signals != 2 || reads != 17 {
			t.Fatalf("archival observation waits for next signal: done=%v err=%v reads=%d signals=%d", done, err, reads, signals)
		}
	})
}

func TestCatalogItemDeletionProbeErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	probe := catalogItemDeletionProbe(func(context.Context) error {
		called = true
		return nil
	}, func(context.Context) error {
		t.Fatal("cancelled or failed observation must not signal")
		return nil
	})
	if done, err := probe(ctx); done || !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancellation lost: done=%v err=%v called=%v", done, err, called)
	}
	transient := status.Error(codes.Unavailable, "temporarily unavailable")
	probe = catalogItemDeletionProbe(func(context.Context) error { return transient }, func(context.Context) error {
		t.Fatal("failed Get must not signal")
		return nil
	})
	if done, err := probe(context.Background()); done || !errors.Is(err, transient) {
		t.Fatalf("failed observation treated as gone: done=%v err=%v", done, err)
	}
	probe = catalogItemDeletionProbe(func(context.Context) error { return nil }, func(context.Context) error {
		return status.Error(codes.NotFound, "archived between Get and Signal")
	})
	if done, err := probe(context.Background()); !done || err != nil {
		t.Fatalf("concurrent archival not accepted: done=%v err=%v", done, err)
	}
}
