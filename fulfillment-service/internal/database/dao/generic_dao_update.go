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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/osac-project/fulfillment-service/internal/database"
)

// UpdateRequest represents a request to update an existing object.
type UpdateRequest[O Object] struct {
	request[O]
	object O
}

// SetObject sets the object to update.
func (r *UpdateRequest[O]) SetObject(value O) *UpdateRequest[O] {
	r.object = value
	return r
}

// Do executes the update operation and returns the response.
func (r *UpdateRequest[O]) Do(ctx context.Context) (response *UpdateResponse[O], err error) {
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

func (r *UpdateRequest[O]) do(ctx context.Context) (response *UpdateResponse[O], err error) {
	// Add the where clause to filter by tenant:
	err = r.addTenancyFilter(ctx)
	if err != nil {
		return
	}

	// Add the where clause to filter by identifier:
	id := r.object.GetId()
	if id == "" {
		err = errors.New("object identifier is mandatory")
		return
	}
	r.sql.params = append(r.sql.params, id)
	if r.sql.filter.Len() > 0 {
		r.sql.filter.WriteString(` and`)
	}
	fmt.Fprintf(&r.sql.filter, ` id = $%d`, len(r.sql.params))

	// Get the requested finalizers, name, labels, annotations and tenant:
	metadata := r.getMetadata(r.object)
	finalizers := r.getFinalizers(metadata)
	var (
		name        string
		labels      map[string]string
		annotations map[string]string
		tenant      string
		project     string
	)
	if metadata != nil {
		name = metadata.GetName()
		labels = metadata.GetLabels()
		annotations = metadata.GetAnnotations()
		tenant = metadata.GetTenant()
		project = metadata.GetProject()
	}

	// Validate that tenant is not empty:
	if tenant == "" {
		err = errors.New("cannot update object with empty tenant")
		return
	}

	// Marshal the data, labels and annotations:
	data, err := r.marshalData(r.object)
	if err != nil {
		return
	}
	labelsData, err := r.marshalMap(labels)
	if err != nil {
		return
	}
	annotationsData, err := r.marshalMap(annotations)
	if err != nil {
		return
	}

	// Build the SQL statement. When optimistic locking is enabled add a version condition to the where clause
	// so that the update only succeeds if the version matches.
	var buffer strings.Builder
	fmt.Fprintf(&buffer, `update %s set`, r.dao.table)
	addColumn := func(name string, value any) {
		r.sql.params = append(r.sql.params, value)
		fmt.Fprintf(&buffer, ` %s = $%d,`, name, len(r.sql.params))
	}
	addColumn("name", name)
	addColumn("finalizers", finalizers)
	addColumn("labels", labelsData)
	addColumn("annotations", annotationsData)
	addColumn("tenant", tenant)
	addColumn("project", project)
	addColumn("data", data)
	fmt.Fprintf(&buffer, ` version = version + 1`)
	fmt.Fprintf(&buffer, ` where %s`, r.sql.filter.String())
	fmt.Fprintf(&buffer, ` returning creation_timestamp, deletion_timestamp, creator, project, version`)

	// Run the SQL statement:
	sql := buffer.String()
	var (
		creationTs time.Time
		deletionTs time.Time
		creator    string
		version    int32
	)
	err = func() (err error) {
		row := r.queryRow(ctx, updateOpType, sql, r.sql.params...)
		start := time.Now()
		defer func() {
			r.recordOpDuration(updateOpType, start, err)
		}()
		err = row.Scan(
			&creationTs,
			&deletionTs,
			&creator,
			&project,
			&version,
		)
		return
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		err = &ErrNotFound{
			IDs: []string{id},
		}
		return
	}
	if err != nil {
		err = r.translateError(ctx, id, name, tenant, err)
		return
	}

	// Prepare the result:
	object := r.cloneObject(r.object)
	metadata = r.makeMetadata(makeMetadataArgs{
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
	object.SetId(id)
	r.setMetadata(object, metadata)

	// Fire the event:
	err = r.fireEvent(ctx, Event{
		Type:   EventTypeUpdated,
		Object: object,
	})
	if err != nil {
		return
	}

	// If the object has been deleted and there are no finalizers we can now archive the object and fire the
	// delete event:
	if deletionTs.Unix() != 0 && len(finalizers) == 0 {
		err = r.archive(ctx, archiveArgs{
			id:              id,
			creationTs:      creationTs,
			deletionTs:      deletionTs,
			creator:         creator,
			tenant:          tenant,
			project:         project,
			name:            name,
			labelsData:      labelsData,
			annotationsData: annotationsData,
			version:         version,
			data:            data,
		})
		if err != nil {
			return
		}
		err = r.fireEvent(ctx, Event{
			Type:   EventTypeDeleted,
			Object: object,
		})
		if err != nil {
			return
		}
	}

	// Create and return the response:
	response = &UpdateResponse[O]{
		object: object,
	}
	return
}

// translateError translates raw PostgreSQL errors into domain-specific error types.
func (r *UpdateRequest[O]) translateError(ctx context.Context, id, name, tenant string, err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case pgerrcode.UniqueViolation:
		if strings.Contains(pgErr.ConstraintName, "_unique_name_") {
			return &ErrAlreadyExists{
				ID:   id,
				Name: name,
			}
		}
		return &ErrAlreadyExists{
			ID: id,
		}
	case pgerrcode.ForeignKeyViolation:
		switch {
		case strings.HasSuffix(pgErr.ConstraintName, "_tenant_fk"):
			return &ErrReference{
				Reason: fmt.Sprintf("tenant '%s' doesn't exist", tenant),
			}
		default:
			r.dao.logger.WarnContext(
				ctx,
				"Unknown foreign key violation",
				slog.String("constraint", pgErr.ConstraintName),
				slog.Any("error", err),
			)
			return &ErrReference{}
		}
	case errReferenceCode:
		return &ErrReference{
			Reason: pgErr.Message,
		}
	case errImmutableCode:
		if pgErr.Detail == "" {
			r.dao.logger.WarnContext(
				ctx,
				"Empty detail in immutable column trigger error",
				slog.Any("error", err),
			)
			return &ErrImmutable{}
		}
		var columns []string
		jsonErr := json.Unmarshal([]byte(pgErr.Detail), &columns)
		if jsonErr != nil {
			r.dao.logger.WarnContext(
				ctx,
				"Failed to parse immutable column names from trigger detail",
				slog.Any("error", err),
			)
			return &ErrImmutable{}
		}
		if len(columns) == 0 {
			r.dao.logger.WarnContext(
				ctx,
				"Empty columns in immutable column trigger error",
				slog.Any("error", err),
			)
			return &ErrImmutable{}
		}
		var fields []string
		for _, column := range columns {
			switch column {
			case "creator", "name", "project", "tenant":
				fields = append(fields, fmt.Sprintf("metadata.%s", column))
			default:
				r.dao.logger.WarnContext(
					ctx,
					"Unknown immutable column in trigger detail",
					slog.String("column", column),
					slog.Any("error", err),
				)
			}
		}
		return &ErrImmutable{
			Fields: fields,
		}
	case errNotUniqueCode:
		// When the trigger provides a custom message, use it as Reason.
		// If no message is provided, fall back to ID.
		if pgErr.Message != "" {
			return &ErrAlreadyExists{
				ID:     id,
				Reason: pgErr.Message,
			}
		}
		return &ErrAlreadyExists{
			ID: id,
		}
	case pgerrcode.DeadlockDetected:
		return &ErrDeadlock{}
	default:
		return err
	}
}

// UpdateResponse represents the result of an update operation.
type UpdateResponse[O Object] struct {
	object O
}

// GetObject returns the updated object.
func (r *UpdateResponse[O]) GetObject() O {
	return r.object
}

// Update creates and returns a new update request.
func (d *GenericDAO[O]) Update() *UpdateRequest[O] {
	return &UpdateRequest[O]{
		request: request[O]{
			dao: d,
		},
	}
}
