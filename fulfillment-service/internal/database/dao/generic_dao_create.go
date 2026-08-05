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
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/osac-project/fulfillment-service/internal/database"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

// CreateRequest represents a request to create a new object.
type CreateRequest[O Object] struct {
	request[O]
	object O
}

// SetObject sets the object to create.
func (r *CreateRequest[O]) SetObject(value O) *CreateRequest[O] {
	r.object = value
	return r
}

// Do executes the create operation and returns the response.
func (r *CreateRequest[O]) Do(ctx context.Context) (response *CreateResponse[O], err error) {
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

func (r *CreateRequest[O]) do(ctx context.Context) (response *CreateResponse[O], err error) {
	// If the object is nil, create an empty one:
	if reflect.ValueOf(r.object).IsNil() {
		r.object = r.newObject()
	}

	// Generate an identifier if needed:
	id := r.object.GetId()
	if id == "" {
		id = uuid.New()
	}

	// Get the metadata:
	metadata := r.getMetadata(r.object)
	finalizers := r.getFinalizers(metadata)
	var (
		name        string
		labels      map[string]string
		annotations map[string]string
		tenant      string
		project     string
		creator     string
	)
	if metadata != nil {
		name = metadata.GetName()
		labels = metadata.GetLabels()
		annotations = metadata.GetAnnotations()
		tenant = metadata.GetTenant()
		project = metadata.GetProject()
		creator = metadata.GetCreator()
	}

	// Validate that tenant is not empty:
	if tenant == "" {
		err = errors.New("cannot create object with empty tenant")
		return
	}

	// Save the object:
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
	sql := fmt.Sprintf(
		`
		insert into %s (
			id,
			name,
			finalizers,
			creator,
			tenant,
			project,
			labels,
			annotations,
			data
		) values (
		 	$1,
		 	$2,
			$3,
			$4,
			$5,
			$6,
			$7,
			$8,
			$9
		)
		returning
			creation_timestamp,
			deletion_timestamp,
			version
		`,
		r.dao.table,
	)
	var (
		creationTs time.Time
		deletionTs time.Time
		version    int32
	)
	err = func() (err error) {
		start := time.Now()
		row := r.queryRow(
			ctx,
			createOpType,
			sql,
			id,
			name,
			finalizers,
			creator,
			tenant,
			project,
			labelsData,
			annotationsData,
			data,
		)
		defer func() {
			r.recordOpDuration(createOpType, start, err)
		}()
		return row.Scan(
			&creationTs,
			&deletionTs,
			&version,
		)
	}()
	if err != nil {
		err = r.translateError(ctx, id, tenant, project, name, err)
		return
	}
	created := r.cloneObject(r.object)
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
	created.SetId(id)
	r.setMetadata(created, metadata)

	// Fire the event:
	err = r.fireEvent(ctx, Event{
		Type:   EventTypeCreated,
		Object: created,
	})
	if err != nil {
		return
	}

	// Create the response:
	response = &CreateResponse[O]{
		object: created,
	}
	return
}

// translateError translates raw PostgreSQL errors into domain-specific error types.
func (r *CreateRequest[O]) translateError(ctx context.Context, id, tenant, project, name string, err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case pgerrcode.UniqueViolation:
		return &ErrAlreadyExists{
			Table: r.dao.table,
			ID:    id,
			Name:  name,
		}
	case errReferenceCode:
		return &ErrReference{
			Reason: pgErr.Message,
		}
	case pgerrcode.ForeignKeyViolation:
		switch {
		case strings.HasSuffix(pgErr.ConstraintName, "_tenant_fk"):
			return &ErrReference{
				Reason: fmt.Sprintf("tenant '%s' doesn't exist", tenant),
			}
		case strings.HasSuffix(pgErr.ConstraintName, "_parent_project_fk"):
			return &ErrReference{
				Reason: fmt.Sprintf("parent project '%s' doesn't exist", project),
			}
		case strings.HasSuffix(pgErr.ConstraintName, "_project_fk"):
			return &ErrReference{
				Reason: fmt.Sprintf("project '%s' doesn't exist", project),
			}
		default:
			r.dao.logger.WarnContext(
				ctx,
				"Unknown foreign key violation",
				slog.String("constraint", pgErr.ConstraintName),
			)
			return &ErrReference{}
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
	}
	return err
}

// CreateResponse represents the result of a create operation.
type CreateResponse[O Object] struct {
	object O
}

// GetObject returns the created object.
func (r *CreateResponse[O]) GetObject() O {
	return r.object
}

// Create creates and returns a new create request.
func (d *GenericDAO[O]) Create() *CreateRequest[O] {
	return &CreateRequest[O]{
		request: request[O]{
			dao: d,
		},
	}
}
