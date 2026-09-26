/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dao

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

// SignalRequest represents a request to signal an object by its identifier.
type SignalRequest[O Object] struct {
	request[O]
	id string
}

// SetId sets the identifier of the object to signal.
func (r *SignalRequest[O]) SetId(value string) *SignalRequest[O] {
	r.id = value
	return r
}

// Do executes the signal operation and returns the response.
func (r *SignalRequest[O]) Do(ctx context.Context) (response *SignalResponse, err error) {
	r.tx, err = database.TxFromContext(ctx)
	if err != nil {
		return
	}
	defer r.tx.ReportError(&err)
	response, err = r.do(ctx)
	return
}

func (r *SignalRequest[O]) do(ctx context.Context) (response *SignalResponse, err error) {
	// Check parameters:
	if r.id == "" {
		err = errors.New("object identifier is mandatory")
		return
	}

	// Check visibility:
	ok, err := r.addVisibilityFilter(ctx)
	if err != nil {
		return
	}
	if !ok {
		err = &ErrNotFound{
			IDs: []string{r.id},
		}
		return
	}

	// Add the identifier filter:
	r.sql.params = append(r.sql.params, r.id)
	if r.sql.filter.Len() > 0 {
		r.sql.filter.WriteString(` and`)
	}
	fmt.Fprintf(&r.sql.filter, ` id = $%d`, len(r.sql.params))

	// Mark the update as a signal for the change trigger. SET LOCAL limits the flag to the current transaction, and we
	// clear it immediately after the update so that later operations in the same transaction remain regular updates.
	_, err = r.exec(ctx, signalOpType, "set local osac.signal = 'on'")
	if err != nil {
		return
	}

	// Assign the version to itself to cause the change trigger to enqueue the signal event without changing the object.
	var buffer strings.Builder
	fmt.Fprintf(
		&buffer,
		`
		update %s
		set version = version
		where %s
		`,
		r.dao.table,
		r.sql.filter.String(),
	)
	tag, updateErr := r.exec(ctx, signalOpType, buffer.String(), r.sql.params...)
	_, clearErr := r.exec(ctx, signalOpType, "set local osac.signal = 'off'")
	if updateErr == nil && tag.RowsAffected() == 0 {
		err = &ErrNotFound{
			IDs: []string{r.id},
		}
		return
	}
	err = updateErr
	if err == nil {
		err = clearErr
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.DeadlockDetected {
			err = &ErrDeadlock{}
		}
		return
	}

	response = &SignalResponse{}
	return
}

// SignalResponse represents the result of a signal operation.
type SignalResponse struct {
}

// Signal creates and returns a new signal request.
func (d *GenericDAO[O]) Signal() *SignalRequest[O] {
	return &SignalRequest[O]{
		request: request[O]{
			dao: d,
		},
	}
}
