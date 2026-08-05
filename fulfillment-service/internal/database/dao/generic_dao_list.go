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
	"fmt"
	"strings"
	"time"

	"github.com/osac-project/fulfillment-service/internal/database"
)

// ListRequest represents a request to list objects with pagination and filtering.
type ListRequest[O Object] struct {
	request[O]
	filter string
	limit  int32
	offset int32
}

// SetFilter sets the CEL expression that defines which objects should be returned.
func (r *ListRequest[O]) SetFilter(value string) *ListRequest[O] {
	r.filter = value
	return r
}

// SetLimit sets the maximum number of items to return.
func (r *ListRequest[O]) SetLimit(value int32) *ListRequest[O] {
	r.limit = value
	return r
}

// SetOffset sets the starting point for pagination.
func (r *ListRequest[O]) SetOffset(value int32) *ListRequest[O] {
	r.offset = value
	return r
}

// Do executes the list operation and returns the response.
func (r *ListRequest[O]) Do(ctx context.Context) (response *ListResponse[O], err error) {
	err = r.init(ctx)
	if err != nil {
		return
	}
	r.tx, err = database.TxFromContext(ctx)
	if err != nil {
		return
	}
	defer r.tx.ReportError(&err)
	response, err = r.do(ctx)
	return
}

func (r *ListRequest[O]) do(ctx context.Context) (response *ListResponse[O], err error) {
	// Add tenant visibility filter:
	err = r.addTenancyFilter(ctx)
	if err != nil {
		return
	}

	// Calculate the requested filter:
	if r.filter != "" {
		var filter string
		filter, err = r.dao.filterTranslator.Translate(ctx, r.filter)
		if err != nil {
			return
		}
		if r.sql.filter.Len() > 0 {
			r.sql.filter.WriteString(` and `)
		}
		r.sql.filter.WriteString(filter)
	}

	// Calculate the order clause:
	const order = "id"

	// Count the total number of results, disregarding the offset and the limit:
	var buffer strings.Builder
	fmt.Fprintf(&buffer, `select count(*) from %s`, r.dao.table)
	if r.sql.filter.Len() > 0 {
		buffer.WriteString(` where `)
		buffer.WriteString(r.sql.filter.String())
	}
	sql := buffer.String()
	var total int
	err = func() (err error) {
		start := time.Now()
		row := r.queryRow(ctx, countOpType, sql, r.sql.params...)
		defer func() {
			r.recordOpDuration(countOpType, start, err)
		}()
		return row.Scan(&total)
	}()
	if err != nil {
		return
	}

	// Fetch the results:
	buffer.Reset()
	fmt.Fprintf(
		&buffer,
		`
		select
			id,
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
		`,
		r.dao.table,
	)
	if r.sql.filter.Len() > 0 {
		buffer.WriteString(` where `)
		buffer.WriteString(r.sql.filter.String())
	}
	if order != "" {
		buffer.WriteString(` order by `)
		buffer.WriteString(order)
	}

	// Add the offset:
	offset := max(r.offset, 0)
	r.sql.params = append(r.sql.params, offset)
	fmt.Fprintf(&buffer, ` offset $%d`, len(r.sql.params))

	// Add the limit:
	limit := r.limit
	if limit < 0 {
		limit = 0
	} else if limit == 0 {
		limit = r.dao.defaultLimit
	} else if limit > r.dao.maxLimit {
		limit = r.dao.maxLimit
	}
	r.sql.params = append(r.sql.params, limit)
	fmt.Fprintf(&buffer, ` limit $%d`, len(r.sql.params))

	// Execute the SQL query:
	sql = buffer.String()
	var items []O
	err = func() (err error) {
		start := time.Now()
		rows, err := r.query(ctx, listOpType, sql, r.sql.params...)
		defer func() {
			r.recordOpDuration(listOpType, start, err)
		}()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				id              string
				name            string
				creationTs      time.Time
				deletionTs      time.Time
				finalizers      []string
				creator         string
				tenant          string
				project         string
				labelsData      []byte
				annotationsData []byte
				version         int32
				data            []byte
			)
			err = rows.Scan(
				&id,
				&name,
				&creationTs,
				&deletionTs,
				&finalizers,
				&creator,
				&tenant,
				&project,
				&labelsData,
				&annotationsData,
				&version,
				&data,
			)
			if err != nil {
				return err
			}
			item := r.cloneObject(r.newObject())
			err = r.unmarshalData(data, item)
			if err != nil {
				return err
			}
			var labels map[string]string
			labels, err = r.unmarshalMap(labelsData)
			if err != nil {
				return err
			}
			var annotations map[string]string
			annotations, err = r.unmarshalMap(annotationsData)
			if err != nil {
				return err
			}
			md := r.makeMetadata(makeMetadataArgs{
				creationTs:  creationTs,
				deletionTs:  deletionTs,
				finalizers:  finalizers,
				creator:     creator,
				tenant:      tenant,
				project:     project,
				name:        name,
				labels:      labels,
				annotations: annotations,
				version:     version,
			})
			item.SetId(id)
			r.setMetadata(item, md)
			items = append(items, item)
		}
		return rows.Err()
	}()
	if err != nil {
		return
	}

	// Create and return the response:
	response = &ListResponse[O]{
		size:  int32(len(items)), // #nosec G115 -- bounded by MaxLimit
		total: int32(total),      // #nosec G115 -- bounded by MaxLimit
		items: items,
	}
	return
}

// ListResponse represents the result of a list operation.
type ListResponse[O Object] struct {
	size  int32
	total int32
	items []O
}

// GetSize returns the actual number of items returned.
func (r *ListResponse[O]) GetSize() int32 {
	return r.size
}

// GetTotal returns the total number of items available.
func (r *ListResponse[O]) GetTotal() int32 {
	return r.total
}

// GetItems returns the list of items.
func (r *ListResponse[O]) GetItems() []O {
	return r.items
}

// List creates and returns a new list request.
func (d *GenericDAO[O]) List() *ListRequest[O] {
	return &ListRequest[O]{
		request: request[O]{
			dao: d,
		},
	}
}
