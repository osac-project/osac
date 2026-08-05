/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TxManager is a database transaction manager. It knows how to start transactions.
//
//go:generate mockgen -destination=database_tx_manager_mock.go -package=database . TxManager
type TxManager interface {
	// Begin starts a new transaction.
	Begin(ctx context.Context) (Tx, error)

	// Run starts a new transaction, executes the given task function, and then commits the transaction if the task
	// completes without error and without panic. If the task returns an error, panics, or marks the transaction for
	// rollback via ReportError, the transaction is rolled back instead.
	//
	// The task must be a function whose first parameter is either context.Context or Tx. Any additional parameters
	// are passed via args. When the first parameter is context.Context, the transaction is stored in it and can be
	// retrieved with TxFromContext. When the first parameter is Tx, the transaction is passed directly.
	//
	// If the last return value of the task implements the error interface, it will be used to determine whether to
	// commit or rollback. Functions with no return values or without a trailing error are also accepted (they
	// commit unless a panic or ReportError occurs).
	Run(ctx context.Context, task any, args ...any) error
}

// TxManagerBuilder is a builder responsible for constructing database transaction managers. Don't create instances of
// this type directly, use the NewTxManager function instead.
type TxManagerBuilder struct {
	logger *slog.Logger
	pool   *pgxpool.Pool
}

// txManager is responsible for managing database transactions. It provides functionality to interact with a PostgreSQL
// connection pool and logs transaction-related operations using the provided logger.
type txManager struct {
	logger *slog.Logger
	pool   *pgxpool.Pool
}

// NewTxManager creates a builder that can then be used to initializa a new transaction manager.
func NewTxManager() *TxManagerBuilder {
	return &TxManagerBuilder{}
}

// SetLogger sets the logger that the transaction manager will use to write to the log. This is mandatory.
func (b *TxManagerBuilder) SetLogger(value *slog.Logger) *TxManagerBuilder {
	b.logger = value
	return b
}

// SetPool sets the database connection pool that the transaction manager will use to create transactions. This is
// mandatory.
func (b *TxManagerBuilder) SetPool(value *pgxpool.Pool) *TxManagerBuilder {
	b.pool = value
	return b
}

// Build uses the information stored in the builder to create a new transaction manager.
func (b *TxManagerBuilder) Build() (result TxManager, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.pool == nil {
		err = errors.New("database connection pool is mandatory")
		return
	}

	// Create and populate the object:
	result = &txManager{
		logger: b.logger,
		pool:   b.pool,
	}
	return
}

// Begin starts a new transaction. Note that the created transaction is lazy in the sense that it will not create a real
// database transaction till one of the Query or Exec methods is called.
func (m *txManager) Begin(ctx context.Context) (tx Tx, err error) {
	tx = &managedTx{
		manager: m,
	}
	return
}

// Run starts a transaction, invokes the task, and commits or rolls back depending on the outcome.
func (m *txManager) Run(ctx context.Context, task any, args ...any) error {
	tx, err := m.Begin(ctx)
	if err != nil {
		return err
	}
	taskErr := runTxTask(ctx, tx, task, args)
	if taskErr != nil {
		tx.ReportError(&taskErr)
	}
	endErr := tx.End(ctx)
	if taskErr != nil {
		if endErr != nil {
			return errors.Join(taskErr, endErr)
		}
		return taskErr
	}
	return endErr
}

func (t *managedTx) End(ctx context.Context) error {
	if t.real == nil {
		return nil
	}
	if len(t.errs) == 0 {
		t.manager.logger.DebugContext(ctx, "Committing transaction")
		return t.real.Commit(ctx)
	}
	t.manager.logger.DebugContext(
		ctx,
		"Rolling back transaction",
		slog.Any("errors", t.errs),
	)
	return t.real.Rollback(ctx)
}

// managedTx is an implementation of the transaction interface that will start a real transaction only when one of the
// methods of the interface that require it is called. This is intended to avoid the cost of real transactions for code
// that doesn't interact with the database.
type managedTx struct {
	manager *txManager
	real    pgx.Tx
	errs    []error
}

func (t *managedTx) Query(ctx context.Context, query string, args ...any) (result pgx.Rows, err error) {
	err = t.ensureReal(ctx)
	if err != nil {
		return
	}
	result, err = t.real.Query(ctx, query, args...)
	return
}

func (t *managedTx) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	err := t.ensureReal(ctx)
	if err != nil {
		return &managedRow{
			err: err,
		}
	}
	return t.real.QueryRow(ctx, query, args...)
}

func (t *managedTx) Exec(ctx context.Context, query string, args ...any) (tag pgconn.CommandTag, err error) {
	err = t.ensureReal(ctx)
	if err != nil {
		return
	}
	tag, err = t.real.Exec(ctx, query, args...)
	return
}

func (t *managedTx) ReportError(err *error) {
	if err != nil && *err != nil {
		t.errs = append(t.errs, *err)
	}
}

func (t *managedTx) Run(ctx context.Context, task any, args ...any) error {
	taskErr := runTxTask(ctx, t, task, args)
	if taskErr != nil {
		t.ReportError(&taskErr)
	}
	return taskErr
}

// ensureReal makes sure that the real transaction exists, creating it if needed.
func (t *managedTx) ensureReal(ctx context.Context) error {
	if t.real != nil {
		return nil
	}
	t.manager.logger.DebugContext(ctx, "Starting transaction")
	var err error
	t.real, err = t.manager.pool.Begin(ctx)
	return err
}

// managedRow is an implementation of the row interface that always returns the contained error. This is necessary
// because we start transactions lazyly when the QueryRow method is called, and there is no way to return errors
// directly from that. Instead we need to save the error and return it later, when the Scan method is called.
type managedRow struct {
	err error
}

func (r *managedRow) Scan(dest ...any) error {
	return r.err
}

// runTask validates the task function signature, builds the argument list, calls the function via reflection, and
// extracts the trailing error return value if present.
func runTxTask(ctx context.Context, tx Tx, task any, args []any) (taskErr error) {
	defer func() {
		if r := recover(); r != nil {
			switch v := r.(type) {
			case error:
				taskErr = fmt.Errorf("task panicked: %w", v)
			default:
				taskErr = fmt.Errorf("task panicked: %v", v)
			}
		}
	}()

	// Check that the task function is acceptable:
	taskFunc := reflect.ValueOf(task)
	taskType := taskFunc.Type()
	if taskType.Kind() != reflect.Func {
		taskErr = fmt.Errorf("task must be a function, got '%T'", task)
		return
	}
	if taskType.NumIn() == 0 {
		taskErr = errors.New("task function must have at least one parameter, context or transaction")
		return
	}

	// Check that the first parameter of the task is either a context or a transaction:
	firstParam := taskType.In(0)
	var firstArg reflect.Value
	switch {
	case firstParam.Implements(contextType):
		firstArg = reflect.ValueOf(TxIntoContext(ctx, tx))
	case firstParam == txType:
		firstArg = reflect.ValueOf(tx)
	default:
		taskErr = fmt.Errorf(
			"first parameter of task function must be context.Context or database.Tx, got %s", firstParam,
		)
		return
	}

	// Check that we got exactly the number of expected arguments:
	expectedArgs := taskType.NumIn() - 1
	if len(args) != expectedArgs {
		taskErr = fmt.Errorf("task function expects %d additional argument(s), got %d", expectedArgs, len(args))
		return
	}

	// Validate and build the argument list:
	callArgs := make([]reflect.Value, 0, taskType.NumIn())
	callArgs = append(callArgs, firstArg)
	for i, arg := range args {
		paramType := taskType.In(i + 1)
		if arg == nil {
			switch paramType.Kind() {
			case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan, reflect.Interface:
				callArgs = append(callArgs, reflect.Zero(paramType))
			default:
				taskErr = fmt.Errorf(
					"argument %d is nil but parameter type %s is not nilable",
					i+1, paramType,
				)
				return
			}
		} else {
			argType := reflect.TypeOf(arg)
			if !argType.AssignableTo(paramType) {
				taskErr = fmt.Errorf(
					"argument %d has type %s which is not assignable to parameter type %s",
					i+1, argType, paramType,
				)
				return
			}
			callArgs = append(callArgs, reflect.ValueOf(arg))
		}
	}

	// Call the task function:
	results := taskFunc.Call(callArgs)

	// Extract the trailing error return value if present:
	numOut := taskType.NumOut()
	if numOut > 0 && taskType.Out(numOut-1).Implements(errorType) {
		lastResult := results[numOut-1]
		if !lastResult.IsNil() {
			taskErr = lastResult.Interface().(error)
		}
	}
	return
}

// Well-known reflection types:
var (
	contextType = reflect.TypeFor[context.Context]()
	txType      = reflect.TypeFor[Tx]()
	errorType   = reflect.TypeFor[error]()
)
