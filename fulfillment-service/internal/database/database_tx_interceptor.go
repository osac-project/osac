/*
Copyright (c) 2025 Red Hat, Inc.

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
	"log/slog"

	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// TxInterceptorBuilder contains the data and logic needed to build an interceptor that begins and ends transactions
// automatically. Don't create instances of this type directly, use the NewTxInterceptor function instead.
type TxInterceptorBuilder struct {
	logger  *slog.Logger
	manager TxManager
}

// TxInterceptor contains the data needed by the interceptor.
type TxInterceptor struct {
	logger  *slog.Logger
	manager TxManager
}

// NewTxInterceptor creates a builder that can then be used to configure and create a transactions interceptor.
func NewTxInterceptor() *TxInterceptorBuilder {
	return &TxInterceptorBuilder{}
}

// SetLogger sets the logger that will be used to write to the log. This is mandatory.
func (b *TxInterceptorBuilder) SetLogger(value *slog.Logger) *TxInterceptorBuilder {
	b.logger = value
	return b
}

// SetManager sets the transaction manager that will be used to begin and end transactions. This is mandatory.
func (b *TxInterceptorBuilder) SetManager(value TxManager) *TxInterceptorBuilder {
	b.manager = value
	return b
}

// Build uses the data stored in the builder to create and configure a new interceptor.
func (b *TxInterceptorBuilder) Build() (result *TxInterceptor, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.manager == nil {
		err = errors.New("transaction manager is mandatory")
		return
	}

	// Create and populate the object:
	result = &TxInterceptor{
		logger:  b.logger,
		manager: b.manager,
	}
	return
}

// UnaryServer is the unary server interceptor function.
func (i *TxInterceptor) UnaryServer(ctx context.Context, request any, info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler) (response any, err error) {
	// Begin the transaction:
	tx, err := i.manager.Begin(ctx)
	if err != nil {
		i.logger.ErrorContext(
			ctx,
			"Failed to begin transaction",
			slog.String("method", info.FullMethod),
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to begin transaction")
		return
	}

	// After calling the method we will try to end the transaction. If the method returns an error then we will
	// return that, no matter what is the outcome of the transaction. If the method succeeded but ending the
	// transaction fails, then we will return an internal error.
	defer func() {
		// Try to end the transaction:
		txErr := tx.End(ctx)

		// Write to the log both errors, the one returned by the method and the one resulting from trying to
		// end the transaction.
		if txErr != nil {
			logFields := []any{
				slog.String("method", info.FullMethod),
			}
			if err != nil {
				logFields = append(logFields, slog.Any("method_error", err))
			}
			logFields = append(logFields, slog.Any("tx_error", txErr))
			i.logger.ErrorContext(ctx, "Failed to end transaction", logFields...)
		}

		// If the method succeeded, but ending the transaction failed, then replace the method error with the
		// transaction error.
		if err == nil && txErr != nil {
			err = grpcstatus.Errorf(grpccodes.Internal, "failed to end transaction")
		}
	}()

	// Add the manager and the transaction to the context:
	handlerCtx := TxManagerIntoContext(ctx, i.manager)
	handlerCtx = TxIntoContext(handlerCtx, tx)

	// Call the method:
	response, err = handler(handlerCtx, request)
	return
}
