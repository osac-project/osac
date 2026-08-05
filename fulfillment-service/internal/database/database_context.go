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
)

// contextKey is a custom type used to define keys for storing and retrieving values in a context.
type contextKey int

const (
	contextTxManagerKey contextKey = iota
	contextTxKey
)

// TxManagerFromContext retrieves the transaction manager from the provided context.
func TxManagerFromContext(ctx context.Context) (tm TxManager, err error) {
	tm, ok := ctx.Value(contextTxManagerKey).(TxManager)
	if ok {
		return
	}
	err = errors.New("failed to get transaction manager from context")
	return
}

// TxManagerIntoContext adds the given transaction manager into the provided context and returns the new context
// containing it.
func TxManagerIntoContext(ctx context.Context, tx TxManager) context.Context {
	return context.WithValue(ctx, contextTxManagerKey, tx)
}

// TxFromContext retrieves the transaction from the provided context.
func TxFromContext(ctx context.Context) (tx Tx, err error) {
	tx, ok := ctx.Value(contextTxKey).(Tx)
	if ok {
		return
	}
	err = errors.New("failed to get transaction from context")
	return
}

// TxIntoContext adds the given transaction into the provided context and returns the new context containing it.
func TxIntoContext(ctx context.Context, tx Tx) context.Context {
	return context.WithValue(ctx, contextTxKey, tx)
}
