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
	"fmt"
)

// WithNewTx starts a new independent transaction using the transaction manager from the context, and runs the given
// function inside it. This does not reuse any transaction already present in the context — it always begins a fresh
// one, giving the function its own short-lived transaction scope. The transaction is committed if the function
// succeeds, or rolled back otherwise. If the function panics, the transaction is rolled back and the panic is
// re-raised.
func WithNewTx[T any](ctx context.Context, fn func(context.Context) (T, error)) (result T, err error) {
	manager, err := TxManagerFromContext(ctx)
	if err != nil {
		err = fmt.Errorf("get transaction manager from context: %w", err)
		return
	}
	tx, err := manager.Begin(ctx)
	if err != nil {
		err = fmt.Errorf("begin transaction: %w", err)
		return
	}
	defer func() {
		// If fn panicked, ensure the transaction is rolled back before re-panicking.
		if p := recover(); p != nil {
			panicErr := fmt.Errorf("panic: %v", p)
			tx.ReportError(&panicErr)
			_ = tx.End(ctx)
			panic(p)
		}
		endErr := tx.End(ctx)
		if err == nil && endErr != nil {
			err = fmt.Errorf("end transaction: %w", endErr)
		}
	}()
	result, err = fn(TxIntoContext(ctx, tx))
	tx.ReportError(&err)
	return
}
