/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package database

import (
	"context"
	"embed"
	"errors"
	"fmt"
	neturl "net/url"

	"github.com/go-logr/logr"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Tool struct {
	logger logr.Logger
	url    string
}

func NewTool(logger logr.Logger, url string) *Tool {
	return &Tool{logger: logger, url: url}
}

func (t *Tool) Migrate(ctx context.Context) error {
	parsed, err := neturl.Parse(t.url)
	if err != nil {
		return fmt.Errorf("parsing database URL: %w", err)
	}
	parsed.Scheme = "pgx5"
	migrateURL := parsed.String()

	driver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("loading migration files: %w", err)
	}

	migrations, err := migrate.NewWithSourceInstance("iofs", driver, migrateURL)
	if err != nil {
		return fmt.Errorf("creating migration instance: %w", err)
	}
	defer func() {
		sourceErr, databaseErr := migrations.Close()
		if sourceErr != nil {
			t.logger.Error(sourceErr, "closing migration source")
		}
		if databaseErr != nil {
			t.logger.Error(databaseErr, "closing migration database")
		}
	}()

	version, dirty, err := migrations.Version()
	switch {
	case err == nil:
		t.logger.Info("schema version before migration", "version", version, "dirty", dirty)
		if dirty {
			return fmt.Errorf("schema version %d is dirty: manual intervention required", version)
		}
	case errors.Is(err, migrate.ErrNilVersion):
		t.logger.Info("schema not created yet, will create now")
	default:
		return fmt.Errorf("querying schema version: %w", err)
	}

	err = migrations.Up()
	switch {
	case err == nil:
		t.logger.Info("migrations executed successfully")
	case errors.Is(err, migrate.ErrNoChange):
		t.logger.Info("migrations already up to date")
	default:
		return fmt.Errorf("running migrations: %w", err)
	}

	version, dirty, err = migrations.Version()
	if err != nil {
		return fmt.Errorf("querying schema version after migration: %w", err)
	}
	t.logger.Info("schema version after migration", "version", version, "dirty", dirty)

	return nil
}
