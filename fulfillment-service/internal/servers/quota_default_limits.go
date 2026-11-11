/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// quotaDefaultLimitsTable is the table that stores the default limits. A default belongs to no
// tenant, so the rows carry no tenant and the generic DAO, which requires one, can't manage them;
// the store below speaks SQL directly. The rows still follow the object table shape, so the
// enqueue_change trigger audits every change in the changes table.
const quotaDefaultLimitsTable = "quota_default_limits"

// errQuotaDefaultLimitNotFound tells the caller that no default limit has the given identifier.
var errQuotaDefaultLimitNotFound = errors.New("quota default limit not found")

// quotaDefaultLimitsStore reads and writes the default limits.
type quotaDefaultLimitsStore struct {
	logger           *slog.Logger
	filterTranslator *dao.FilterTranslator
	marshalOptions   protojson.MarshalOptions
	unmarshalOptions protojson.UnmarshalOptions
}

// newQuotaDefaultLimitsStore creates the store. It fails when the filter translator can't be
// built.
func newQuotaDefaultLimitsStore(logger *slog.Logger) (result *quotaDefaultLimitsStore, err error) {
	filterTranslator, err := dao.NewFilterTranslator().
		SetLogger(logger).
		SetDescriptor((*privatev1.QuotaDefaultLimit)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return nil, err
	}
	return &quotaDefaultLimitsStore{
		logger:           logger,
		filterTranslator: filterTranslator,
		marshalOptions:   protojson.MarshalOptions{UseProtoNames: true},
		unmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: true},
	}, nil
}

// list returns the default limits ordered by identifier, and the total count of the collection.
// The filter is a CEL expression, like the filter of any List request; the limit of zero with
// limitSet returns only the count.
func (s *quotaDefaultLimitsStore) list(
	ctx context.Context, filter string, offset, limit int32, limitSet bool,
) (items []*privatev1.QuotaDefaultLimit, total int32, err error) {
	where := "true"
	args := []any{}
	if filter != "" {
		translated, translateErr := s.filterTranslator.Translate(ctx, filter)
		if translateErr != nil {
			return nil, 0, &dao.ErrInvalidFilter{Reason: translateErr.Error()}
		}
		where = translated
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	err = tx.QueryRow(ctx, fmt.Sprintf("select count(*) from %s where %s", quotaDefaultLimitsTable, where), args...).
		Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	if limitSet && limit == 0 {
		return nil, total, nil
	}
	query := fmt.Sprintf(
		"select %s from %s where %s order by id offset $%d",
		quotaDefaultLimitColumns, quotaDefaultLimitsTable, where, len(args)+1,
	)
	args = append(args, offset)
	if limit > 0 {
		query += fmt.Sprintf(" limit $%d", len(args)+1)
		args = append(args, limit)
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanQuotaDefaultLimit(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// get returns the default limit with the given identifier.
func (s *quotaDefaultLimitsStore) get(ctx context.Context, id string) (result *privatev1.QuotaDefaultLimit, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	row := tx.QueryRow(
		ctx,
		fmt.Sprintf("select %s from %s where id = $1", quotaDefaultLimitColumns, quotaDefaultLimitsTable),
		id,
	)
	result, err = scanQuotaDefaultLimit(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errQuotaDefaultLimitNotFound
	}
	return result, err
}

// set writes the default limit of the object's dimension, replacing the value when the dimension
// already has a default, and returns the stored object. The whole object is rewritten, so the
// version increments on every set.
func (s *quotaDefaultLimitsStore) set(
	ctx context.Context, object *privatev1.QuotaDefaultLimit,
) (result *privatev1.QuotaDefaultLimit, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	data, err := s.marshalOptions.Marshal(object)
	if err != nil {
		return nil, err
	}
	// Lock the row of the dimension, if any, so two sets of the same dimension can't race. Two
	// first-time sets can still both miss the lock and race on the insert; the loser of that race
	// updates the row the winner committed instead of surfacing the unique-index error. The
	// update leaves name alone: the column is immutable, and the row already carries the name
	// the insert gave it.
	probe := fmt.Sprintf(
		"select id from %s where data -> 'spec' ->> 'dimension' = $1 for update", quotaDefaultLimitsTable)
	write := fmt.Sprintf(
		"update %s set creator = $1, data = $2, version = version + 1 where id = $3",
		quotaDefaultLimitsTable,
	)
	var id string
	err = tx.QueryRow(ctx, probe, object.GetSpec().GetDimension()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		id = object.GetId()
		if id == "" {
			id = uuid.New().String()
		}
		_, err = tx.Exec(
			ctx,
			fmt.Sprintf(
				"insert into %s (id, name, creator, data) values ($1, $2, $3, $4)",
				quotaDefaultLimitsTable,
			),
			id, object.GetMetadata().GetName(), object.GetMetadata().GetCreator(), data,
		)
		if isUniqueViolation(err) {
			err = tx.QueryRow(ctx, probe, object.GetSpec().GetDimension()).Scan(&id)
			if err == nil {
				_, err = tx.Exec(ctx, write, object.GetMetadata().GetCreator(), data, id)
			}
		}
	} else if err == nil {
		_, err = tx.Exec(ctx, write, object.GetMetadata().GetCreator(), data, id)
	}
	if err != nil {
		return nil, err
	}
	return s.get(ctx, id)
}

// isUniqueViolation tells whether the error is PostgreSQL's unique-violation error (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == pgerrcode.UniqueViolation
	}
	return false
}

// clear deletes the default limit with the given identifier. The changes table keeps its history.
func (s *quotaDefaultLimitsStore) clear(ctx context.Context, id string) error {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(
		ctx, fmt.Sprintf("delete from %s where id = $1", quotaDefaultLimitsTable), id,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errQuotaDefaultLimitNotFound
	}
	return nil
}

// values returns the default limit of every dimension, for limit resolution.
func (s *quotaDefaultLimitsStore) values(ctx context.Context) (result map[string]int64, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(
		ctx,
		fmt.Sprintf(
			"select data -> 'spec' ->> 'dimension', (data -> 'spec' ->> 'value')::bigint from %s",
			quotaDefaultLimitsTable,
		),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = map[string]int64{}
	for rows.Next() {
		var dimension string
		var value int64
		if err = rows.Scan(&dimension, &value); err != nil {
			return nil, err
		}
		result[dimension] = value
	}
	return result, rows.Err()
}

// quotaDefaultLimitColumns are the columns the store reads, in scan order.
const quotaDefaultLimitColumns = "id, name, creation_timestamp, creator, labels, annotations, version, data"

// scanQuotaDefaultLimit reads one row of the default limits table into a message.
func scanQuotaDefaultLimit(row pgx.Row) (*privatev1.QuotaDefaultLimit, error) {
	var (
		id        string
		name      string
		created   time.Time
		creator   string
		labels    []byte
		annotated []byte
		version   int32
		data      []byte
	)
	err := row.Scan(&id, &name, &created, &creator, &labels, &annotated, &version, &data)
	if err != nil {
		return nil, err
	}
	object := &privatev1.QuotaDefaultLimit{}
	if err = protojson.Unmarshal(data, object); err != nil {
		return nil, err
	}
	object.SetId(id)
	metadata := privatev1.Metadata_builder{
		CreationTimestamp: timestamppb.New(created),
		Creator:           creator,
		Name:              name,
		Version:           version,
	}
	if labelsMap := jsonToMap(labels); labelsMap != nil {
		metadata.Labels = labelsMap
	}
	if annotationsMap := jsonToMap(annotated); annotationsMap != nil {
		metadata.Annotations = annotationsMap
	}
	object.SetMetadata(metadata.Build())
	return object, nil
}
