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
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

// request is a common base for all DAO request types, containing shared fields.
type request[O Object] struct {
	dao        *GenericDAO[O]
	tx         database.Tx
	visibility *auth.Visibility
	sql        struct {
		filter strings.Builder
		params []any
	}
}

type archiveArgs struct {
	id              string
	creationTs      time.Time
	deletionTs      time.Time
	creator         string
	tenant          string
	project         string
	name            string
	labelsData      []byte
	annotationsData []byte
	version         int32
	data            []byte
}

// archive moves a deleted object to the archived table and removes it from the main table.
func (r *request[O]) archive(ctx context.Context, args archiveArgs) error {
	sql := fmt.Sprintf(
		`
		insert into archived_%s (
			id,
			name,
			creation_timestamp,
			deletion_timestamp,
			creator,
			tenant,
			project,
			labels,
			annotations,
			version,
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
			$9,
			$10,
			$11
		)
		`,
		r.dao.table,
	)
	_, err := r.exec(
		ctx,
		archiveOpType,
		sql,
		args.id,
		args.name,
		args.creationTs,
		args.deletionTs,
		args.creator,
		args.tenant,
		args.project,
		args.labelsData,
		args.annotationsData,
		args.version,
		args.data,
	)
	if err != nil {
		return r.translateArchiveError(ctx, args.tenant, err)
	}
	sql = fmt.Sprintf(`delete from %s where id = $1`, r.dao.table)
	_, err = r.exec(ctx, deleteOpType, sql, args.id)
	if err != nil {
		return r.translateArchiveError(ctx, args.tenant, err)
	}
	return nil
}

// translateArchiveError translates a raw PostgreSQL error from either statement of archive() into
// a domain-specific error type. Any foreign key violation means the object is still referenced by
// other objects and cannot yet be removed — that is always an "in use" condition, regardless of
// which specific constraint was violated, so unrecognized constraint names still produce a
// FailedPrecondition instead of an opaque Internal error. The constraint name itself is logged but
// not included in the returned error, since it exposes internal schema detail to API callers.
func (r *request[O]) translateArchiveError(ctx context.Context, tenant string, err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case errInUseCode:
		return &ErrInUse{
			Reason: pgErr.Message,
		}
	case pgerrcode.ForeignKeyViolation:
		switch pgErr.ConstraintName {
		case "projects_tenant_fk":
			return &ErrInUse{
				Reason: fmt.Sprintf("tenant '%s' cannot be deleted because it still has projects", tenant),
			}
		default:
			r.dao.logger.WarnContext(
				ctx,
				"Object cannot be deleted because it is still referenced by other objects",
				slog.String("table", pgErr.TableName),
				slog.String("constraint", pgErr.ConstraintName),
			)
			return &ErrInUse{
				Reason: "object cannot be deleted because it is still referenced by other objects",
			}
		}
	case pgerrcode.DeadlockDetected:
		return &ErrDeadlock{}
	default:
		r.dao.logger.WarnContext(
			ctx,
			"Unknown error while archiving object",
			slog.String("table", pgErr.TableName),
			slog.String("constraint", pgErr.ConstraintName),
			slog.Any("error", err),
		)
		return err
	}
}

// addVisibilityFilter adds a where clause to restrict results to only those objects that belong to tenants and projects
// that the current user has permission to see.
//
// Returns a boolean flag indicating if the user has permissions to see any objects. When this is false it means that
// the user doesn't have permission to see any objects, so the caller can skip building and executing the query and
// return an error or an empty result instead.
func (r *request[O]) addVisibilityFilter(ctx context.Context) (result bool, err error) {
	// This method should always be called before any other filter or parameter is added:
	if r.sql.filter.Len() > 0 {
		err = fmt.Errorf(
			"visibility filter must be the first filter added, but it already contains '%s'",
			r.sql.filter.String(),
		)
		return
	}
	if len(r.sql.params) > 0 {
		err = fmt.Errorf(
			"visibility filter must be the first filter added, but it already contains %d parameters",
			len(r.sql.params),
		)
		return
	}

	// Determine the set of visible projects for each visible tenant on first use:
	if r.visibility == nil {
		r.visibility, err = r.dao.tenancyLogic.DetermineVisibility(ctx)
		if err != nil {
			return
		}
	}

	// If the visibility is unrestricted, it means that the user has permission to see all tenants and projects,
	// so we don't need to apply any filtering:
	if r.visibility.IsTotal() {
		result = true
		return
	}

	// Add a filter that matches each visible tenant. The default project (empty string) is always visible for a
	// tenant the user can see, and is matched with equality. Named projects use the ltree '@>' operator so that
	// granting a project also grants its descendants: the bound array is on the left and must contain an ancestor of
	// the row project. The default project is never passed to '@>': the empty ltree is the root of the tree, so
	// `array['']::ltree[] @> project` would match every project in the tenant.
	//
	// For example, if the user can see projects 'a1' and 'a2' in tenant 'a', and only the default project in tenant
	// 'b', the filter will be like this:
	//
	//	tenant = 'a' and (project = '' or array['a1', 'a2']::ltree[] @> project) or tenant = 'b' and project = ''
	//
	// Tenants that are not visible are omitted. If the user can see tenant 'b' but not tenant 'a', the filter will
	// not mention tenant 'a'.
	tenants := r.visibility.VisibleTenants()
	filters := 0
	fmt.Fprintf(&r.sql.filter, "(")
	for _, tenant := range tenants {
		projects := r.visibility.VisibleProjects(tenant)
		named := make([]string, 0, len(projects))
		for _, project := range projects {
			if project != "" {
				named = append(named, project)
			}
		}
		if filters > 0 {
			fmt.Fprintf(&r.sql.filter, " or ")
		}
		index := len(r.sql.params) + 1
		r.sql.params = append(r.sql.params, tenant)
		if len(named) == 0 {
			fmt.Fprintf(&r.sql.filter, "tenant = $%d and project = ''", index)
		} else {
			r.sql.params = append(r.sql.params, named)
			fmt.Fprintf(
				&r.sql.filter,
				"tenant = $%d and (project = '' or $%d::ltree[] @> project)",
				index, index+1,
			)
		}
		filters++
	}
	fmt.Fprintf(&r.sql.filter, ")")

	// Due to the way we construct the filters above, if no filters were added it means that the user doesn't have
	// permission to see any objects. We also clean the filter buffer, which at that point will contain '()'.
	result = filters > 0
	if !result {
		r.sql.filter.Reset()
	}
	return
}

type makeMetadataArgs struct {
	creationTs  time.Time
	deletionTs  time.Time
	finalizers  []string
	creator     string
	tenant      string
	project     string
	name        string
	labels      map[string]string
	annotations map[string]string
	version     int32
}

func (r *request[O]) makeMetadata(args makeMetadataArgs) metadataIface {
	result := r.dao.metadataTemplate.New().Interface().(metadataIface)
	result.SetName(args.name)
	if args.creationTs.Unix() != 0 {
		result.SetCreationTimestamp(timestamppb.New(args.creationTs))
	}
	if args.deletionTs.Unix() != 0 {
		result.SetDeletionTimestamp(timestamppb.New(args.deletionTs))
	}
	result.SetFinalizers(args.finalizers)
	result.SetCreator(args.creator)
	result.SetTenant(args.tenant)
	result.SetProject(args.project)
	result.SetLabels(args.labels)
	result.SetAnnotations(args.annotations)
	result.SetVersion(args.version)
	return result
}

func (r *request[O]) getMetadata(object O) metadataIface {
	objectReflect := object.ProtoReflect()
	if !objectReflect.Has(r.dao.metadataField) {
		return nil
	}
	return objectReflect.Get(r.dao.metadataField).Message().Interface().(metadataIface)
}

func (r *request[O]) setMetadata(object O, metadata metadataIface) {
	objectReflect := object.ProtoReflect()
	if metadata != nil {
		metadataReflect := metadata.ProtoReflect()
		objectReflect.Set(r.dao.metadataField, protoreflect.ValueOfMessage(metadataReflect))
	} else {
		objectReflect.Clear(r.dao.metadataField)
	}
}

func (r *request[O]) newObject() O {
	return r.dao.objectTemplate.New().Interface().(O)
}

func (r *request[O]) cloneObject(object O) O {
	return proto.Clone(object).(O)
}

func (r *request[O]) marshalData(object O) (result []byte, err error) {
	result, err = r.dao.jsonEncoder.Marshal(object)
	return
}

func (r *request[O]) unmarshalData(data []byte, object O) error {
	return r.dao.unmarshalOptions.Unmarshal(data, object)
}

func (r *request[O]) fireEvent(ctx context.Context, event Event) error {
	event.Table = r.dao.table
	for _, eventCallback := range r.dao.eventCallbacks {
		err := eventCallback(ctx, event)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *request[O]) getFinalizers(metadata metadataIface) []string {
	if metadata == nil {
		return []string{}
	}
	list := metadata.GetFinalizers()
	set := make(map[string]struct{}, len(list))
	for _, item := range list {
		set[item] = struct{}{}
	}
	list = make([]string, len(set))
	i := 0
	for item := range set {
		list[i] = item
		i++
	}
	sort.Strings(list)
	return list
}

func (r *request[O]) marshalMap(value map[string]string) (result []byte, err error) {
	if value == nil {
		result = []byte("{}")
		return
	}
	result, err = json.Marshal(value)
	return
}

func (r *request[O]) unmarshalMap(data []byte) (result map[string]string, err error) {
	if len(data) == 0 {
		return
	}
	var value map[string]string
	err = json.Unmarshal(data, &value)
	if err != nil {
		return
	}
	result = value
	return
}

// queryRow executes a SQL query expected to return a single row. It logs the SQL statement before delegating to the
// underlying transaction.
func (r *request[O]) queryRow(ctx context.Context, op opType, sql string, args ...any) pgx.Row {
	if r.dao.logger.Enabled(ctx, slog.LevelDebug) {
		r.dao.logger.DebugContext(
			ctx,
			"Running SQL operation",
			slog.String("type", string(op)),
			slog.String("sql", r.cleanSQL(sql)),
			slog.Int("parameter_count", len(args)),
		)
	}
	return r.tx.QueryRow(ctx, sql, args...)
}

// query executes a SQL query expected to return multiple rows. It logs the SQL statement before delegating to the
// underlying transaction.
func (r *request[O]) query(ctx context.Context, op opType, sql string, args ...any) (rows pgx.Rows, err error) {
	if r.dao.logger.Enabled(ctx, slog.LevelDebug) {
		r.dao.logger.DebugContext(
			ctx,
			"Running SQL operation",
			slog.String("type", string(op)),
			slog.String("sql", r.cleanSQL(sql)),
			slog.Int("parameter_count", len(args)),
		)
	}
	rows, err = r.tx.Query(ctx, sql, args...)
	return
}

// exec executes a SQL statement that doesn't return rows. It logs the SQL statement before delegating to the
// underlying transaction.
func (r *request[O]) exec(ctx context.Context, op opType, sql string, args ...any) (pgconn.CommandTag, error) {
	if r.dao.logger.Enabled(ctx, slog.LevelDebug) {
		r.dao.logger.DebugContext(
			ctx,
			"Running SQL operation",
			slog.String("type", string(op)),
			slog.String("sql", r.cleanSQL(sql)),
			slog.Int("parameter_count", len(args)),
		)
	}
	start := time.Now()
	tag, err := r.tx.Exec(ctx, sql, args...)
	r.recordOpDuration(op, start, err)
	return tag, err
}

// recordOpDuration records the elapsed time since start as a Prometheus histogram observation, if metrics are
// configured. The err parameter is the error returned by the SQL operation. When it is nil the `error` label will be
// empty, otherwise it will contain the PostgreSQL error code.
func (r *request[O]) recordOpDuration(op opType, start time.Time, err error) {
	if r.dao.opDurationMetric != nil {
		code := ""
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				code = pgErr.Code
			}
		}
		r.dao.opDurationMetric.With(prometheus.Labels{
			errorMetricLabel: code,
			tableMetricLabel: r.dao.table,
			typeMetricLabel:  string(op),
		}).Observe(time.Since(start).Seconds())
	}
}

// cleanSQL collapses all sequences of whitespace in the given SQL string into a single space, producing a
// compact single-line representation suitable for logging.
func (r *request[O]) cleanSQL(sql string) string {
	var buf strings.Builder
	buf.Grow(len(sql))
	space := true
	for _, c := range sql {
		if unicode.IsSpace(c) {
			if !space {
				buf.WriteRune(' ')
				space = true
			}
		} else {
			buf.WriteRune(c)
			space = false
		}
	}
	result := buf.String()
	if space && len(result) > 0 {
		result = result[:len(result)-1]
	}
	return result
}
