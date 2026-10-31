/*
Copyright (c) 2025 Red Hat Inc.

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
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

// GetRequest represents a request to get a single object by its identifier.
type GetRequest[O Object] struct {
	request[O]
	id        string
	lock      bool
	shareLock bool
}

// SetId sets the identifier of the object to retrieve.
func (r *GetRequest[O]) SetId(value string) *GetRequest[O] {
	r.id = value
	return r
}

// SetLock sets whether to lock the object for update.
func (r *GetRequest[O]) SetLock(value bool) *GetRequest[O] {
	r.lock = value
	return r
}

// SetShareLock sets whether to take a shared row lock on the object. Shared locks don't block each other but block
// an exclusive lock taken with SetLock. SetLock takes precedence when both are set.
func (r *GetRequest[O]) SetShareLock(value bool) *GetRequest[O] {
	r.shareLock = value
	return r
}

// Do executes the get operation and returns the response.
func (r *GetRequest[O]) Do(ctx context.Context) (response *GetResponse[O], err error) {
	r.tx, err = database.TxFromContext(ctx)
	if err != nil {
		return
	}
	defer r.tx.ReportError(&err)
	response, err = r.do(ctx)
	return
}

func (r *GetRequest[O]) do(ctx context.Context) (response *GetResponse[O], err error) {
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
			IDs: []string{
				r.id,
			},
		}
		return
	}

	// Add the where clause to filter by identifier:
	r.sql.params = append(r.sql.params, r.id)
	if r.sql.filter.Len() > 0 {
		r.sql.filter.WriteString(` and`)
	}
	fmt.Fprintf(&r.sql.filter, ` id = $%d`, len(r.sql.params))

	// Create the SQL statement:
	var buffer strings.Builder
	fmt.Fprintf(
		&buffer,
		`
		select
			name,
			creation_timestamp,
			deletion_timestamp,
			finalizers,
			creator,
			tenant,
			project,
			labels,
			annotations,
			version,
			data
		from
			%s
		where
			%s
		`,
		r.dao.table,
		r.sql.filter.String(),
	)
	if r.lock {
		buffer.WriteString(" for update")
	} else if r.shareLock {
		buffer.WriteString(" for share")
	}

	// Execute the SQL statement:
	sql := buffer.String()
	stored := storedObject{id: r.id}
	err = func() (err error) {
		start := time.Now()
		row := r.queryRow(ctx, getOpType, sql, r.sql.params...)
		defer func() {
			r.recordOpDuration(getOpType, start, err)
		}()
		return row.Scan(
			&stored.name,
			&stored.creationTs,
			&stored.deletionTs,
			&stored.finalizers,
			&stored.creator,
			&stored.tenant,
			&stored.project,
			&stored.labelsData,
			&stored.annotationsData,
			&stored.version,
			&stored.data,
		)
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		err = &ErrNotFound{
			IDs: []string{r.id},
		}
		return
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.DeadlockDetected {
			err = &ErrDeadlock{}
		}
		return
	}

	object, err := r.objectFromRow(stored)
	if err != nil {
		return
	}

	// Create the response:
	response = &GetResponse[O]{
		object: object,
	}
	return
}

// GetResponse represents the result of a get operation.
type GetResponse[O Object] struct {
	object O
}

// GetObject returns the retrieved object. Returns nil if the object was not found.
func (r *GetResponse[O]) GetObject() O {
	return r.object
}

// Get creates and returns a new get request.
func (d *GenericDAO[O]) Get() *GetRequest[O] {
	return &GetRequest[O]{
		request: request[O]{
			dao: d,
		},
	}
}
