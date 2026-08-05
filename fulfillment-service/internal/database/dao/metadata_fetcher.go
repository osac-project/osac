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
	"log/slog"

	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database"
)

// MetadataFetcherBuilder builds a metadata fetcher that queries object metadata for authorization.
type MetadataFetcherBuilder struct {
	logger *slog.Logger
	table  string
}

// NewMetadataFetcher creates a new metadata fetcher builder.
func NewMetadataFetcher() *MetadataFetcherBuilder {
	return &MetadataFetcherBuilder{}
}

// SetLogger sets the logger.
func (b *MetadataFetcherBuilder) SetLogger(value *slog.Logger) *MetadataFetcherBuilder {
	b.logger = value
	return b
}

// SetTable sets the database table name to query.
func (b *MetadataFetcherBuilder) SetTable(value string) *MetadataFetcherBuilder {
	b.table = value
	return b
}

// Build creates the metadata fetcher function.
// This bypasses authorization checks since it's used FOR authorization.
func (b *MetadataFetcherBuilder) Build() (auth.MetadataFetcher, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.table == "" {
		return nil, errors.New("table is mandatory")
	}

	logger := b.logger
	query := fmt.Sprintf("select tenant, name::text, project::text from %s where id = $1", b.table)

	return func(ctx context.Context, id string) *auth.ObjectMetadata {
		tx, err := database.TxFromContext(ctx)
		if err != nil {
			logger.WarnContext(ctx, "Failed to get transaction from context for metadata fetch",
				slog.String("id", id),
				slog.Any("error", err),
			)
			return nil
		}

		var meta auth.ObjectMetadata
		row := tx.QueryRow(ctx, query, id)
		err = row.Scan(&meta.Tenant, &meta.Name, &meta.Project)
		if err != nil {
			logger.WarnContext(ctx, "Failed to fetch object metadata for authorization",
				slog.String("id", id),
				slog.Any("error", err),
			)
			return nil
		}

		return &meta
	}, nil
}
