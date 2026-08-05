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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Tx is a database transaction automatically started and automatically committed or rolled back.
//
//go:generate mockgen -destination=database_tx_mock.go -package=database . Tx
type Tx interface {
	// Query executes a SQL query against the database using the provided context, query string, and optional
	// arguments. It returns the resulting rows or an error if the query fails.
	Query(ctx context.Context, query string, args ...any) (result pgx.Rows, err error)

	// QueryRow executes a SQL query that is expected to return a single row. It uses the provided context, query
	// string, and optional arguments. The returned row can be used to scan the result into variables.
	QueryRow(ctx context.Context, query string, args ...any) pgx.Row

	// Exec executes a SQL statement against the database using the provided context, query string, and optional
	// arguments. It returns the result of the execution or an error if the execution fails.
	Exec(ctx context.Context, query string, args ...any) (tag pgconn.CommandTag, err error)

	// ReportError adds a error that the transaction manager will use to determine if the transaction should be
	// committed or rolled back. The default behaviour is that if there are no errors reported, or if they are all
	// nil then the transaction will be committed. Otherwise it will be rolled back.
	//
	// The recommended way to use this is with the defer mechanism:
	//
	//	func myWork(ctx context.Context) (err error) {
	//		// Get the transaction and remember to report the errors:
	//		tx, err := database.TxFromContext(ctx)
	//		if err != nil {
	//			return
	//		}
	//		defer tx.ReportError(&err)
	//
	//		// Do the actual work, which will update the returned 'err'.
	//
	//		return
	//	}
	//
	// Note that value passed is a pointer to a error. This is needed to make possible the recommended usage above,
	// because if the error was passed by value then it most cases it will be nil, because it will be evaluated at
	// the time of execution of the 'defer' statement, not at the time of execution of deferred ReportError
	// function.
	//
	// It this method is called multiple times for the same transaction the reported errors will be accumulated.
	ReportError(err *error)

	// Run executes the given task function within this transaction. If the task returns an error or panics, the
	// error is reported to the transaction (marking it for rollback) and returned to the caller. The transaction is
	// not ended by this method; the caller is still responsible for calling End.
	//
	// The task must be a function whose first parameter is either context.Context or Tx. Any additional parameters
	// are passed via args. If the last return value implements the error interface, it will be used to determine
	// the outcome.
	Run(ctx context.Context, task any, args ...any) error

	// End finishes a transaction. It will be committed if no errors have been reported, or rolled back otherwise.
	// See the ReportError method for details on how errors are tracked.
	End(ctx context.Context) error
}
