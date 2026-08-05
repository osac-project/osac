/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package shutdown

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	ossignal "os/signal"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
)

// SequenceBuilder contains the data and logic needed to create a shutdown sequence. Don't create instances of this type
// directly, use the NewSequence function instead.
type SequenceBuilder struct {
	logger  *slog.Logger
	delay   time.Duration
	timeout time.Duration
	actions []shutdownAction
	signals []os.Signal
	exit    func(int)
}

// Sequence is a shutdown sequence. Don't create instances of this type directly, use the NewSequence function instead.
type Sequence struct {
	logger  *slog.Logger
	delay   time.Duration
	timeout time.Duration
	actions []shutdownAction
	exit    func(int)
	lock    *sync.Mutex
	done    chan struct{}
}

// shutdownAction represents a single action in the shutdown sequence,  with a name, priority, object and a function.
//
// The name is used to identify the action in the logs.
//
// Actions can be defined by an object or by a function. The object can be, for example, an HTTP server or a gRPC
// server. For those the shutdown sequence knows how to stop them gracefully. For functions the shutdown sequence
// will just call them.
//
// Higher priority actions are executed before lower priority ones. Actions with the same priority are executed
// simultaneously.
type shutdownAction struct {
	name     string
	priority int
	object   any
	function func(context.Context) error
}

const (
	// DefaultPriority is the default priority. Note that setting the priority to this value (or zero) doesn't mean
	// that the priority will actually be zero, it rather means that the priority will be set to the default. For
	// example, setting the priority of a gRPC server to this value means that the actual priority will be
	// HighPriority
	DefaultPriority = 0

	// LowPriority is the lowest priority, assigned by default to database connection pools, as those are usually
	// the ones that need to be stopped last.
	LowPriority = 1

	// HighPriority is the highest priority, assigned by default to HTTP servers and gRPC servers, as those are
	// usually the ones that need to be stopped first.
	HighPriority = 100
)

// NewSequence creates a builder that can then be used to configure and create a shutdown sequence.
//
// A sequence contains a list of actions, a timeout and an exit function. The list of actions is built from objects
// like HTTP servers and gRPC servers as well as custom functions. Servers are stopped gracefully, functions are just
// called, assuming that they will know how to the things that they manage. When possible it is better to add objects
// instead of custom functions, as the shutdown sequence already knows how to stop them gracefully and in the right
// order.
//
// Actions are grouped by priority and executed from highest to lowest priority. Actions with the same priority are
// executed simultaneously in separate goroutines.
//
// The exit function will be called after all the actions have finished, or when the configured timeout expires,
// whatever happens first.
//
// Functions receive a context as parameter, and return an error. For example, a function that removes a temporary
// directory could be added like this:
//
//	// Perform some initialization task that requires a temporary directory:
//	tmpDir, err := os.MkdirTemp("", "*.tmp")
//	if err != nil {
//		...
//	}
//
//	// Create the shutdown sequence, and remember to remove the temporary directory:
//	sequence, err := shutdown.NewSequence().
//		SetLogger(logger).
//		AddAction("remove-tmp", 0, func (ctx context.Context) error {
//			return os.RemoveAll(tmpDir)
//		}).
//		Build()
//	if err != nil {
//		...
//	}
//
//	// Eventually start the shutdown sequence:
//	sequence.Start(0)
//
// Actions can also be added after the initial creation of the sequence:
//
//	// someFunc receives the shutdown sequence that was configured and created somewhere else.
//	func someFunc(shutdown *shutdown.Sequence) error {
//		// Create a temporary directory:
//		tmpDir, err := os.MkdirTemp("", "*.tmp")
//		if err != nil {
//			return err
//		}
//
//		// Remember to delete the directory during shutdown:
//		sequence.AddAction("remove-tmp", 0, func (ctx context.Context) error {
//			return os.RemoveAll(tmpDir)
//		}
//		...
//	}
//
// The default exit function is os.Exit, and there is usually no need to change it. If needed it can be changed using
// the SetExit method of the builder. For example, in unit tests it is convenient to avoid exiting the process:
//
//	sequence, err := shutdown.NewSequence().
//		SetLogger(logger).
//		SetExit(func (code int) {)).
//		Build()
//	if err != nil {
//		...
//	}
//
// The actions run with a context that has the timeout set with the SetTimeout method of the builder (1 minute by
// default). If the actions take longer than the timeout, the remaining actions will be skipped and the exit function will
// be called anyway, which usually means that the process will exit.
//
// Note that actions are responsible for honouring the timeout set in the context, the sequence can't and will not stop
// such them, because there is no way to do that in Go. It will however call the exit function, and if it is the default
// os.Exit it will kill the process and therefore all goroutines.
//
// When the Start method is called the shutdown sequence will start immediately unless a delay has been specified in
// the configuration (using the SetDelay method of the builder). This is intended for situations where some shutdown
// action is started outside of the control of the shutdown sequence.
//
// The shutdown sequence can also be configured to automatically start when certain signals are received. For example in
// Unix systems you will probably want to start the sequence when the SIGKILL or SIGTERM signals are received:
//
//	sequence, err := shutdown.NewSequence().
//		SetLogger(logger).
//		AddSignals(syscall.SIGKILL, syscall.SIGTERM).
//		Build()
//	if err != nil {
//		...
//	}
func NewSequence() *SequenceBuilder {
	return &SequenceBuilder{
		delay:   0,
		timeout: 1 * time.Minute,
		exit:    os.Exit,
	}
}

// SetLogger sets the logger that the shutdown sequence will use to write messages to the log. This is mandatory.
func (b *SequenceBuilder) SetLogger(value *slog.Logger) *SequenceBuilder {
	b.logger = value
	return b
}

// SetDelay sets the time that the shutdown sequence will be delayed after the Start method is called. It is intended
// for situations where some shutdown actions can't be added to the sequence properly. This is optional and the default
// value is zero.
func (b *SequenceBuilder) SetDelay(value time.Duration) *SequenceBuilder {
	b.delay = value
	return b
}

// SetTimeout sets the maximum time that will pass between the call to the Start method and the call to the exit
// function. Actions that don't complete in that time will simply be ignored. This is optional and the default value is
// one minute.
func (b *SequenceBuilder) SetTimeout(value time.Duration) *SequenceBuilder {
	b.timeout = value
	return b
}

// AddSignal adds a signal that will start the shutdown sequence. This is optional, and by default no signal is used.
func (b *SequenceBuilder) AddSignal(value os.Signal) *SequenceBuilder {
	b.signals = append(b.signals, value)
	return b
}

// AddSignals adds a list of signals that will start the shutdown sequence. This is optional and by default no signal
// is used.
func (b *SequenceBuilder) AddSignals(values ...os.Signal) *SequenceBuilder {
	b.signals = append(b.signals, values...)
	return b
}

// SetExit sets the function that will be called when the shutdown sequence has been completed. The default is to call
// os.Exit. There is usually no need to set this explicitly, it is intended for unit tests where exiting the process
// isn't convenient.
func (b *SequenceBuilder) SetExit(value func(int)) *SequenceBuilder {
	b.exit = value
	return b
}

// AddFunction adds a function that will be executed as a shutdown action. The name is optional and it is used to
// identify the action in the logs. Functions are added with low priority by default (if priority is zero) so they will
// be executed after stopping HTTP and gRPC servers.
func (b *SequenceBuilder) AddFunction(name string, priority int,
	function func(context.Context) error) *SequenceBuilder {
	if priority == 0 {
		priority = LowPriority
	}
	b.actions = append(b.actions, shutdownAction{
		name:     name,
		priority: priority,
		function: function,
	})
	return b
}

// AddHttpServer adds a shutdown action that gracefully shuts down an HTTP server. The name is used to identify the
// server in the logs. HTTP server are added with high priority by default (if priority is zero).
func (b *SequenceBuilder) AddHttpServer(name string, priority int, server *http.Server) *SequenceBuilder {
	if priority == DefaultPriority {
		priority = HighPriority
	}
	b.actions = append(b.actions, shutdownAction{
		name:     name,
		priority: priority,
		object:   server,
	})
	return b
}

// AddGrpcServer adds a shutdown action that gracefully stops a gRPC server. The name is used to identify the server
// in the logs. gRPC servers are added with high priority by default (if priority is zero).
func (b *SequenceBuilder) AddGrpcServer(name string, priority int, server *grpc.Server) *SequenceBuilder {
	if priority == DefaultPriority {
		priority = HighPriority
	}
	b.actions = append(b.actions, shutdownAction{
		name:     name,
		priority: priority,
		object:   server,
	})
	return b
}

// AddListener adds a listener that will be stopped gracefully. The name is used to identify the listener in the logs.
// Listeners are added with high priority by default (if priority is zero). Note that listeners used by HTTP and gRPC
// servers don't need to be added explicitly, as they will be stopped automatically when the servers are stopped.
func (b *SequenceBuilder) AddListener(name string, priority int, listener any) *SequenceBuilder {
	if priority == DefaultPriority {
		priority = HighPriority
	}
	b.actions = append(b.actions, shutdownAction{
		name:     name,
		priority: priority,
		object:   listener,
	})
	return b
}

// AddDatabasePool adds a database connection pool that will be closed gracefully. The name is used to identify the
// pool in the logs. Database pools are added with low priority by default (if priority is zero), as they are usually
// the last things that need to be stopped.
func (b *SequenceBuilder) AddDatabasePool(name string, priority int, pool *pgxpool.Pool) *SequenceBuilder {
	if priority == DefaultPriority {
		priority = LowPriority
	}
	b.actions = append(b.actions, shutdownAction{
		name:     name,
		priority: priority,
		object:   pool,
	})
	return b
}

// AddContext adds a context cancellation function. The name is used to identify the function in the logs. These
// functions are added with low priority by default (if priority is zero) so they will be executed after stopping
// HTTP and gRPC servers.
func (b *SequenceBuilder) AddContext(name string, priority int, cancel context.CancelFunc) *SequenceBuilder {
	if priority == DefaultPriority {
		priority = LowPriority
	}
	b.actions = append(b.actions, shutdownAction{
		name:     name,
		priority: priority,
		object:   cancel,
	})
	return b
}

// Build uses the data stored in the builder to create a new shutdown sequence. Note that this doesn't start the
// shutdown sequence, to do that use the Start method.
func (b *SequenceBuilder) Build() (result *Sequence, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.delay < 0 {
		err = fmt.Errorf(
			"delay must be greater than or equal to zero, but it is %s",
			b.delay,
		)
		return
	}
	if b.timeout < 0 {
		err = fmt.Errorf(
			"timeout must be greater than or equal to zero, but it is %s",
			b.timeout,
		)
		return
	}
	if b.exit == nil {
		err = errors.New("exit function is mandatory")
		return
	}

	// Set the default delay if needed:
	delay := b.delay
	if b.delay <= 0 {
		delay = 0
	}

	// Set the default timeout if needed:
	timeout := b.timeout
	if timeout <= 0 {
		timeout = time.Minute
	}

	// Set the default exit function if needed:
	exit := b.exit
	if exit == nil {
		exit = os.Exit
	}

	// Copy the actions from the builder:
	actions := slices.Clone(b.actions)

	// Create the logger:
	logger := b.logger.With(
		slog.String("logger", "shutdown"),
	)

	// Create and populate the object:
	result = &Sequence{
		logger:  logger,
		delay:   delay,
		timeout: timeout,
		actions: actions,
		exit:    exit,
		lock:    &sync.Mutex{},
		done:    make(chan struct{}),
	}

	// Start the shutdown sequence automatically when one of the configured signals is received:
	signals := make(chan os.Signal, 1)
	for _, signal := range b.signals {
		ossignal.Notify(signals, signal)
	}
	go func() {
		signal := <-signals
		logger.InfoContext(
			context.Background(),
			"Shutdown sequence started by signal",
			slog.String("signal", signal.String()),
		)
		result.Start(0)
	}()

	return
}

// AddFunction adds a function that will be executed as a shutdown action. The is used to identify the function in the
// logs. Functions are added with low priority by default (if priority is zero) so they will be executed after stopping
// HTTP and gRPC servers.
func (s *Sequence) AddFunction(name string, priority int, function func(context.Context) error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if priority == DefaultPriority {
		priority = LowPriority
	}
	s.actions = append(s.actions, shutdownAction{
		name:     name,
		priority: priority,
		function: function,
	})
}

// AddHttpServer adds a shutdown action that gracefully shuts down an HTTP server. The name is used to identify the
// server in the logs. HTTP server are added with high priority by default (if priority is zero).
func (s *Sequence) AddHttpServer(name string, priority int, server *http.Server) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if priority == DefaultPriority {
		priority = HighPriority
	}
	s.actions = append(s.actions, shutdownAction{
		name:     name,
		priority: priority,
		object:   server,
	})
}

// AddGrpcServer adds a shutdown action that gracefully stops a gRPC server. The name is used to identify the server
// in the logs. gRPC servers are added with high priority by default (if priority is zero).
func (s *Sequence) AddGrpcServer(name string, priority int, server *grpc.Server) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if priority == DefaultPriority {
		priority = HighPriority
	}
	s.actions = append(s.actions, shutdownAction{
		name:     name,
		priority: priority,
		object:   server,
	})
}

// AddDatabasePool adds a database connection pool that will be closed gracefully. The name is used to identify the
// pool in the logs. Database pools are added with low priority by default (if priority is zero), as they are usually
// the last things that need to be stopped.
func (s *Sequence) AddDatabasePool(name string, priority int, pool *pgxpool.Pool) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if priority == DefaultPriority {
		priority = LowPriority
	}
	s.actions = append(s.actions, shutdownAction{
		name:     name,
		priority: priority,
		object:   pool,
	})
}

// AddContext adds a context cancellation function. The name is used to identify the function in the logs. These
// functions are added with low priority by default (if priority is zero) so they will be executed after stopping
// HTTP and gRPC servers.
func (s *Sequence) AddContext(name string, priority int, cancel context.CancelFunc) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if priority == DefaultPriority {
		priority = LowPriority
	}
	s.actions = append(s.actions, shutdownAction{
		name:     name,
		priority: priority,
		object:   cancel,
	})
}

// Start starts the shutdown sequence. It will run all the actions grouped by priority from highest to lowest, with
// actions of the same priority running simultaneously. After all actions are completed (or the timeout expires), it
// calls the exit function (by default os.Exit) with the given code.
func (s *Sequence) Start(code int) {
	s.lock.Lock()
	defer s.lock.Unlock()

	// Create a logger with some additional information about the shutdown sequence:
	logger := s.logger.With(
		slog.Int("code", code),
	)
	logger.Info("Shutdown sequence started")

	// Wait a bit before starting the shutdown sequence:
	if s.delay > 0 {
		logger.Info("Shutdown has been requested and will start after the delay")
		time.Sleep(s.delay)
	}

	// Create a context with the configured timeout:
	deadline := time.Now().Add(s.timeout)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	// Add the deadline to the logger:
	logger = logger.With(
		slog.String("deadline", deadline.UTC().Format(time.RFC3339)),
	)

	// Get sorted priorities (highest first):
	var priorities []int
	for _, action := range s.actions {
		if !slices.Contains(priorities, action.priority) {
			priorities = append(priorities, action.priority)
		}
	}
	slices.Sort(priorities)
	slices.Reverse(priorities)

	// Run the actions grouped by priority:
	for _, priority := range priorities {
		// Do not try to run more actions if the context is done already:
		err := ctx.Err()
		if err != nil {
			logger.InfoContext(
				ctx,
				"Remaining shutdown actions aborted due to timeout",
				slog.Any("error", err),
			)
			break
		}

		// Run all actions of this priority group:
		err = s.runPriority(ctx, priority)
		if err != nil {
			logger.ErrorContext(ctx, "Error running shutdown actions for priority group", slog.Any("error", err))
			return
		}
	}

	// Mark the shutdown sequence as done and exit:
	logger.InfoContext(ctx, "Shutdown sequence finished, exiting")
	s.exit(code)
	close(s.done)
}

// Wait waits for the shutdown sequence to complete. Note that usually this will never return because when the shutdown
// is completed the exit function will be called, and that usually means calling os.Exit and therefore terminating the
// process.
func (s *Sequence) Wait() error {
	<-s.done
	return nil
}

// runPriority runs the actions for the given priority. It will return an error only if the context is cancelled, not if
// the actions fail. Those action failures will be logged and ingored..
func (s *Sequence) runPriority(ctx context.Context, priority int) error {
	// Find the actions for this priority:
	var actions []shutdownAction
	for _, action := range s.actions {
		if action.priority == priority {
			actions = append(actions, action)
		}
	}

	// Run the actions:
	var wg sync.WaitGroup
	for _, action := range actions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runAction(ctx, action)
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// runAction runs the given action. Errors will be logged and ignored.
func (s *Sequence) runAction(ctx context.Context, action shutdownAction) {
	logger := s.logger.With(
		slog.String("action", action.name),
	)
	switch {
	case action.function != nil:
		err := action.function(ctx)
		if err != nil {
			logger.ErrorContext(
				ctx,
				"Action function failed",
				slog.Any("error", err),
			)
		} else {
			logger.InfoContext(
				ctx,
				"Action function succeeded",
			)
		}
	case action.object != nil:
		s.stopObject(ctx, action.name, action.object)
	default:
		logger.ErrorContext(
			ctx,
			"Don't know how to run action because it doesn't have an object or a function",
		)
	}
}

// stopObject checks the type of the given object and tries to stop it gracefully.
func (s *Sequence) stopObject(ctx context.Context, name string, object any) {
	switch object := object.(type) {
	case context.CancelFunc:
		object()
	case *http.Server:
		s.stopHttpServer(ctx, name, object)
	case *grpc.Server:
		s.stopGrpcServer(ctx, name, object)
	case net.Listener:
		s.stopListener(ctx, name, object)
	case *pgxpool.Pool:
		s.stopDatabasePool(ctx, name, object)
	default:
		s.logger.ErrorContext(
			ctx,
			"Don't know how to stop object of type",
			slog.String("type", fmt.Sprintf("%T", object)),
		)
	}
}

// stopHttpServer gracefully shuts down an HTTP server stored in the action's object field.
func (s *Sequence) stopHttpServer(ctx context.Context, name string, server *http.Server) {
	logger := s.logger.With(
		slog.String("name", name),
		slog.String("address", server.Addr),
	)
	s.logger.InfoContext(
		ctx,
		"Shutting down HTTP server",
	)
	err := server.Shutdown(ctx)
	if err != nil {
		logger.ErrorContext(
			ctx,
			"Shutdown of HTTP server failed",
			slog.Any("error", err),
		)
	} else {
		logger.InfoContext(
			ctx,
			"HTTP server shut down",
		)
	}
}

// stopGrpcServer gracefully stops a gRPC server stored in the action's object field.
func (s *Sequence) stopGrpcServer(ctx context.Context, name string, server *grpc.Server) {
	logger := s.logger.With(
		slog.String("name", name),
	)
	logger.InfoContext(
		ctx,
		"Shutting down gRPC server",
	)
	server.GracefulStop()
	logger.InfoContext(
		ctx,
		"gRPC server shut down",
	)
}

// stopListener gracefully stops a listener stored in the action's object field.
func (s *Sequence) stopListener(ctx context.Context, name string, listener net.Listener) {
	logger := s.logger.With(
		slog.String("name", name),
		slog.String("address", listener.Addr().String()),
	)
	s.logger.InfoContext(
		ctx,
		"Shutting down listener",
	)
	err := listener.Close()
	if err != nil {
		logger.ErrorContext(
			ctx,
			"Failed to close listener",
			slog.Any("error", err),
		)
	} else {
		logger.InfoContext(
			ctx,
			"Listener shut down",
		)
	}
}

// stopDatabasePool closes a database connection pool stored in the action's object field.
func (s *Sequence) stopDatabasePool(ctx context.Context, name string, pool *pgxpool.Pool) {
	config := pool.Config().ConnConfig
	logger := s.logger.With(
		slog.String("name", name),
		slog.String("host", config.Host),
		slog.Int("port", int(config.Port)),
		slog.String("database", config.Database),
	)
	logger.InfoContext(
		ctx,
		"Closing database connection pool",
	)
	pool.Close()
	logger.InfoContext(
		ctx,
		"Database connection pool closed",
	)
}
