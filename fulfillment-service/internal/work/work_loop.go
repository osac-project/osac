/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package work

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// LoopBuilder contains the data and logic needed to create a work loop. Don't create instances of this directly, use
// the NewLoop function instead.
type LoopBuilder struct {
	// logger is the logger that will be used to write messages to the log.
	logger *slog.Logger

	// workFunc is the function that will be executed in the loop.
	workFunc func(context.Context) error

	// interval is the interval between executions of the function.
	interval time.Duration

	// name is the name of the loop.
	name string
}

// Loop executes a work function in a loop.
//
// It is intended for situations where a funcion needs to be executed repeatedly, and restarted when it finishes.
//
// To prevent the function from running too often, specially when the function fails, the loop will sleep after running
// the function for the configured interval, minus the time that the function took to run. For example, if the interval
// is 10s and the function ran for 8s, then after running the function, it will sleep for 2s. If the function ran for
// longer than the interval, it will not sleep.
//
// If the work function returns an error, the loop will log the error and continue.
//
// The loop will exit when the context is cancelled.
//
// The loop can be woken up by calling the `Kick` method, which will interrupt the sleep and start the work function
// immediately.
type Loop struct {
	// logger is the logger that will be used to write messages to the log.
	logger *slog.Logger

	// workFunc is the function that will be executed in the loop.
	workFunc func(context.Context) error

	// interval is the interval between executions of the function.
	interval time.Duration

	// last is the time of the last execution of the function.
	last time.Time

	// kick is a channel that will be used to interrupt the sleep and start the function immediately.
	kick chan struct{}
}

// NewLoop creates a builder that can then be used to configure and create a loop.
func NewLoop() *LoopBuilder {
	return &LoopBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *LoopBuilder) SetLogger(value *slog.Logger) *LoopBuilder {
	b.logger = value
	return b
}

// SetWorkFunc sets the function that will be executed in the loop. This is mandatory.
func (b *LoopBuilder) SetWorkFunc(value func(context.Context) error) *LoopBuilder {
	b.workFunc = value
	return b
}

// SetInterval sets the interval between executions of the work function. This is mandatory.
//
// The work function will be executed repeatedly. After each execution, the loop will sleep for the given interval minus
// the time that the work function was running. For example, if the interval is 10s and the work function ran for 8s,
// then after running the work function, it will sleep for 2s. If the work function ran for longer than the interval,
// it will not sleep.
//
// If the work function returns an error, the loop will log the error and continue.
func (b *LoopBuilder) SetInterval(value time.Duration) *LoopBuilder {
	b.interval = value
	return b
}

// SetName sets the name of the loop. This is mandatory.
func (b *LoopBuilder) SetName(value string) *LoopBuilder {
	b.name = value
	return b
}

// Build uses the data stored in the builder to create a new loop.
func (b *LoopBuilder) Build() (result *Loop, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.workFunc == nil {
		err = errors.New("function is mandatory")
		return
	}
	if b.interval <= 0 {
		err = fmt.Errorf("interval should be positive, but it is %s", b.interval)
		return
	}
	if b.name == "" {
		err = errors.New("name is mandatory")
		return
	}

	// Create a logger with the name of the loop:
	logger := b.logger.With(
		slog.String("loop", b.name),
	)

	// Create and populate the object:
	result = &Loop{
		logger:   logger,
		workFunc: b.workFunc,
		interval: b.interval,
		kick:     make(chan struct{}, 1),
	}
	return
}

// Run runs the loop until the context is cancelled.
//
// The work function will be executed repeatedly. After each execution, the loop will sleep for the configured interval
// minus the time that the work function was running. For example, if the interval is 10s and the work function ran for
// 8s, then after running the work function, it will sleep for 2s. If the work function ran for longer than the
// interval, it will not sleep.
//
// The loop will exit when the context is cancelled.
func (l *Loop) Run(ctx context.Context) error {
	for {
		// Capture the start time of the iteration:
		l.last = time.Now()

		// Execute the function:
		err := l.workFunc(ctx)
		if err != nil {
			l.logger.ErrorContext(
				ctx,
				"Work function failed",
				slog.Any("error", err),
			)
		}

		// Check if the context has been cancelled:
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Sleep:
		err = l.sleep(ctx)
		if err != nil {
			return err
		}
	}
}

// Kick interrupts the sleep and starts the work function immediately. If the work function is already running it does
// nothing. Note that it never blocks: it will return immediately regardless of whether the work function is already
// running or not.
func (l *Loop) Kick() {
	select {
	case l.kick <- struct{}{}:
	default:
	}
}

func (l *Loop) sleep(ctx context.Context) error {
	elapsed := time.Since(l.last)
	duration := l.interval - elapsed
	logger := l.logger.With(
		slog.Time("last", l.last),
		slog.Duration("interval", l.interval),
		slog.Duration("elapsed", elapsed),
	)
	if duration <= 0 {
		logger.DebugContext(ctx, "No need to sleep")
		return nil
	}
	l.logger.DebugContext(
		ctx,
		"Sleeping",
		slog.Duration("duration", duration),
	)
	select {
	case <-ctx.Done():
		return context.Canceled
	case <-l.kick:
		l.logger.DebugContext(ctx, "Woken up by kick")
		return nil
	case <-time.After(duration):
		return nil
	}
}
