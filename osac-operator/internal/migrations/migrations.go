/*
Copyright 2025.

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

// Package migrations contains idempotent data migrations that run at operator startup.
package migrations

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const migrationTimeout = 5 * time.Minute

// Migration is a named, idempotent data migration that runs at operator startup.
// Each migration reads its own configuration (e.g. namespace) from environment
// variables so RunAll doesn't need resource-specific parameters.
type Migration struct {
	Name string
	Fn   func(ctx context.Context, c client.Client) error
}

// all is the ordered list of migrations. Append new migrations at the end.
var all = []Migration{
	{Name: "migrate-subnetrefs", Fn: migrateSubnetRefs},
}

// runAll executes all registered migrations in order. Each migration is
// idempotent — safe to re-run on restart. Returns on the first error so the
// operator fails to start and retries on the next restart.
func runAll(ctx context.Context, c client.Client) error {
	log := ctrllog.FromContext(ctx).WithName("migrations")

	ctx, cancel := context.WithTimeout(ctx, migrationTimeout)
	defer cancel()

	for _, m := range all {
		log.Info("Running migration", "name", m.Name)
		if err := m.Fn(ctx, c); err != nil {
			return fmt.Errorf("migration %s failed: %w", m.Name, err)
		}
	}
	return nil
}

// leaderRunnable wraps runAll as a controller-runtime Runnable that requires
// leader election. The manager calls Start once after this instance becomes
// leader, ensuring migrations run on exactly one replica.
type leaderRunnable struct {
	client client.Client
}

func (r *leaderRunnable) Start(ctx context.Context) error {
	return runAll(ctx, r.client)
}

func (r *leaderRunnable) NeedLeaderElection() bool {
	return true
}

// NewRunnable returns a manager.Runnable that executes all migrations once
// after leader election. Register it with mgr.Add().
func NewRunnable(c client.Client) manager.Runnable {
	return &leaderRunnable{client: c}
}
