/*
Copyright (c) 2025 Red Hat Inc.

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
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	neturl "net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/pflag"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Tool tries to simplify and centralize database operations that are needed frequently during the startup of a process
// that uses the database, like creating the database connection string from the command line flags, waiting till the
// database is up and running, applying the migrations and creationg the database connection pool.
type Tool interface {
	// Wait waits till the database is available.
	Wait(ctx context.Context) error

	// Migrate runs the database migrations up to and including the given version. Pass math.MaxUint to apply all
	// available migrations.
	Migrate(ctx context.Context, version uint) error

	// CheckSchema checks that the database schema is consistent. Writes errors to the logger and returns an error if any
	// consistency check fails.
	CheckSchema(ctx context.Context) error

	// Pool returns the pool of database connections.
	Pool(ctx context.Context) (result *pgxpool.Pool, err error)

	// URL returns the database connection URL.
	URL() string
}

type ToolBuilder struct {
	logger  *slog.Logger
	url     string
	urlFile string
}

type tool struct {
	logger *slog.Logger
	url    string
}

func NewTool() *ToolBuilder {
	return &ToolBuilder{}
}

func (b *ToolBuilder) SetLogger(value *slog.Logger) *ToolBuilder {
	b.logger = value
	return b
}

// SetURL sets the database connection URL directly.
func (b *ToolBuilder) SetURL(value string) *ToolBuilder {
	b.url = value
	return b
}

// SetURLFile sets the path to a file or directory containing database connection settings.
//
// When pointing to a file the URL is read from the file contents.
//
// When pointing to a directory, the tool scans for files named after PostgreSQL connection parameters and builds the
// URL from them. A file named 'url' provides the base URL. Files named after URL components ('host', 'port', 'user',
// 'password', 'dbname') modify the corresponding parts of the URL. Files named after file-path parameters ('sslcert',
// 'sslkey', 'sslrootcert', 'sslcrl' and 'sslcrldir', for example) are referenced by their path in the URL rather
// than their contents. All other files are treated as query parameters whose values are read from the file contents.
// For example, given a directory '/etc/db' with the following files:
//
//	/etc/db/url         - Contains 'postgres://service@db.example.com:5432/mydb'.
//	/etc/db/sslmode     - Contains 'verify-full'.
//	/etc/db/sslcert     - Contains the client certificate in PEM format.
//	/etc/db/sslkey      - Contains the client private key in PEM format.
//	/etc/db/sslrootcert - Contains the CA certificate in PEM format.
//
// The resulting URL will be:
//
//	postgres://service@db.example.com:5432/mydb?sslcert=/etc/db/sslcert&sslkey=/etc/db/sslkey&sslmode=verify-full&sslrootcert=/etc/db/sslrootcert
//
// Note that 'sslmode' is set to the file contents ('verify-full') while 'sslcert', 'sslkey', and 'sslrootcert' are
// set to the file paths, because PostgreSQL expects those parameters to point to certificate files on disk.
//
// Note that this supports the parameters supported by the `pgx` library, not all PostgreSQL connection parameters.
// For example, 'sslcrl' or 'passfile` aren't supported. Check the documentation of the `pgx` library for more details.
//
// When set, this is incompatible with the URL set with SetURL.
func (b *ToolBuilder) SetURLFile(value string) *ToolBuilder {
	b.urlFile = value
	return b
}

// SetFlags sets the command line flags that should be used to configure the tool. This is optional. Note that no
// files are read at this point; file reading is deferred to the Build method.
func (b *ToolBuilder) SetFlags(flags *pflag.FlagSet) *ToolBuilder {
	if flags == nil {
		return b
	}

	var (
		flag  string
		value string
		err   error
	)
	failure := func() {
		b.logger.Error(
			"Failed to get flag value",
			slog.String("flag", flag),
			slog.Any("error", err),
		)
	}

	// URL:
	flag = urlFlagName
	value, err = flags.GetString(flag)
	if err != nil {
		failure()
	} else if value != "" {
		b.SetURL(value)
	}

	// URL file:
	flag = urlFileFlagName
	value, err = flags.GetString(flag)
	if err != nil {
		failure()
	} else if value != "" {
		b.SetURLFile(value)
	}

	return b
}

func (b *ToolBuilder) Build() (result Tool, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Check that the URL and URL file are not both specified:
	if b.url != "" && b.urlFile != "" {
		err = errors.New(
			"database connection URL and URL file are incompatible, use one or the other but not both",
		)
		return
	}

	// Resolve the URL and collect parameters. If the value points to a directory, the tool scans the directory
	// for files that represent connection settings. Each file name is treated as a parameter name. For file-path
	// parameters ('sslcert', 'sslkey', 'sslrootcert', 'sslcrl' and 'sslcrldir', for example) the value is the path
	// to the file itself. For all other parameters the value is read from the file contents. If the directory
	// contains a file named 'url', it is used as the base URL.
	url := b.url
	parameters := map[string]string{}
	if b.urlFile != "" {
		var info os.FileInfo
		info, err = os.Stat(b.urlFile)
		if err != nil {
			err = fmt.Errorf("failed to stat database URL path '%s': %w", b.urlFile, err)
			return
		}
		if info.IsDir() {
			url, parameters, err = b.readURLDirectory(b.urlFile)
			if err != nil {
				return
			}
		} else {
			var data []byte
			data, err = os.ReadFile(b.urlFile)
			if err != nil {
				err = fmt.Errorf("failed to read database URL file '%s': %w", b.urlFile, err)
				return
			}
			url = strings.TrimSpace(string(data))
		}
	}

	if url == "" {
		err = errors.New("connection URL is mandatory")
		return
	}

	// Apply URL parameters. Some parameter names are special and modify the URL structure instead of
	// being added as query parameters: 'host', 'port', 'user', 'password', and 'dbname'.
	if len(parameters) > 0 {
		var parsed *neturl.URL
		parsed, err = neturl.Parse(url)
		if err != nil {
			err = fmt.Errorf("failed to parse database URL: %w", err)
			return
		}
		query := parsed.Query()
		for parameter, value := range parameters {
			switch parameter {
			case "host":
				port := parsed.Port()
				if port != "" {
					parsed.Host = fmt.Sprintf("%s:%s", value, port)
				} else {
					parsed.Host = value
				}
			case "port":
				parsed.Host = fmt.Sprintf("%s:%s", parsed.Hostname(), value)
			case "user":
				if parsed.User != nil {
					password, hasPassword := parsed.User.Password()
					if hasPassword {
						parsed.User = neturl.UserPassword(value, password)
					} else {
						parsed.User = neturl.User(value)
					}
				} else {
					parsed.User = neturl.User(value)
				}
			case "password":
				username := ""
				if parsed.User != nil {
					username = parsed.User.Username()
				}
				parsed.User = neturl.UserPassword(username, value)
			case "dbname":
				parsed.Path = "/" + value
			default:
				query.Set(parameter, value)
			}
		}
		parsed.RawQuery = query.Encode()
		url = parsed.String()
	}

	// Create and populate the object:
	result = &tool{
		logger: b.logger,
		url:    url,
	}
	return
}

// readURLDirectory reads a directory containing database connection settings. Each file in the directory is treated
// as a connection parameter. The file named 'url' (if present) provides the base URL. File-path parameters like
// 'sslcert' use the file path itself as their value. All other parameters use the file contents as their value.
func (b *ToolBuilder) readURLDirectory(dir string) (url string, parameters map[string]string, err error) {
	parameters = map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		err = fmt.Errorf("failed to read database URL directory '%s': %w", dir, err)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		if name == "url" {
			data, readErr := os.ReadFile(filepath.Clean(path))
			if readErr != nil {
				err = fmt.Errorf("failed to read 'url' from file '%s': %w", path, readErr)
				return
			}
			url = strings.TrimSpace(string(data))
		} else if slices.Contains(toolFilePathParameters, name) {
			parameters[name] = path
		} else {
			data, readErr := os.ReadFile(filepath.Clean(path))
			if readErr != nil {
				err = fmt.Errorf(
					"failed to read parameter '%s' from file '%s': %w", name, path, readErr,
				)
				return
			}
			parameters[name] = strings.TrimSpace(string(data))
		}
	}

	// If no url file was found, build a default base URL so that the parameter application logic
	// (which handles 'host', 'port', 'user', 'password', and 'dbname') can construct the full URL.
	if url == "" {
		url = "postgres://localhost:5432"
	}
	return
}

// Wait waits for the database to be available.
func (t *tool) Wait(ctx context.Context) error {
	// If the database IP address or host name have not been created yet then the connection will take a long time
	// to fail, approximately five minutes. To avoid that we need to explicitly set a shorter timeout.
	parsed, err := neturl.Parse(t.url)
	if err != nil {
		return err
	}
	query := parsed.Query()
	query.Set("connect_timeout", "1")
	parsed.RawQuery = query.Encode()
	waitURL := parsed.String()

	// Try to connect to the database until we succeed, without limit of attempts:
	for {
		conn, err := pgx.Connect(ctx, waitURL)
		if err != nil {
			t.logger.InfoContext(
				ctx,
				"Database isn't responding yet",
				slog.Any("error", err),
			)
			time.Sleep(1 * time.Second)
			continue
		}
		err = conn.Close(ctx)
		if err != nil {
			t.logger.ErrorContext(
				ctx,
				"Failed to close database connection",
				slog.Any("error", err),
			)
		}
		return nil
	}
}

// Migrate runs the database migrations up to and including the given desired version. Pass math.MaxUint to apply all
// available migrations.
func (t *tool) Migrate(ctx context.Context, desiredVersion uint) error {
	// The database connection URL given by the user will probably start with 'postgres', and that works fine for
	// regular connections, but for the migration library it needs to be 'pgx5'.
	parsed, err := neturl.Parse(t.url)
	if err != nil {
		return err
	}
	parsed.Scheme = "pgx5"
	migrateURL := parsed.String()

	// Load the migration files:
	driver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	migrations, err := migrate.NewWithSourceInstance("iofs", driver, migrateURL)
	if err != nil {
		return err
	}
	migrations.Log = &migrationsLogger{
		ctx:    ctx,
		logger: t.logger.WithGroup("migrations"),
	}
	defer func() {
		sourceErr, databaseErr := migrations.Close()
		if sourceErr != nil || databaseErr != nil {
			t.logger.ErrorContext(
				ctx,
				"Failed to close migrations",
				slog.Any("source", sourceErr),
				slog.Any("database", databaseErr),
			)
		}
	}()

	// Show the schema version before running the migrations:
	version, dirty, err := migrations.Version()
	switch {
	case err == nil:
		t.logger.InfoContext(
			ctx,
			"Version before running migrations",
			slog.Uint64("version", uint64(version)),
			slog.Bool("dirty", dirty),
		)
	case errors.Is(err, migrate.ErrNilVersion):
		t.logger.InfoContext(
			ctx,
			"Schema hasn't been created yet, will create it now",
		)
	default:
		return err
	}

	// Run the migrations:
	if desiredVersion == math.MaxUint {
		err = migrations.Up()
	} else {
		err = migrations.Migrate(desiredVersion)
	}
	switch {
	case err == nil:
		t.logger.InfoContext(
			ctx,
			"Migrations executed successfully",
		)
	case errors.Is(err, migrate.ErrNoChange):
		t.logger.InfoContext(
			ctx,
			"Migrations don't need to be executed",
		)
	default:
		return err
	}

	// Show the schema version after running the migrations:
	version, dirty, err = migrations.Version()
	if err != nil {
		return err
	}
	t.logger.InfoContext(
		ctx,
		"Schema version after running migrations",
		slog.Uint64("version", uint64(version)),
		slog.Bool("dirty", dirty),
	)

	return nil
}

// URL returns the database connection URL.
func (t *tool) URL() string {
	return t.url
}

// Pool returns the pool of database connections.
func (t *tool) Pool(ctx context.Context) (result *pgxpool.Pool, err error) {
	result, err = pgxpool.New(ctx, t.url)
	return
}

// CheckSchema checks that the database schema is consistent. Writes errors to the logger and returns an error if any
// consistency check fails.
func (t *tool) CheckSchema(ctx context.Context) error {
	// Create a connection pool:
	pool, err := t.Pool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Get the list of object and archive tables:
	objectTables, err := t.listObjectTables(ctx, pool)
	if err != nil {
		return err
	}
	archiveTables, err := t.listArchiveTables(ctx, pool)
	if err != nil {
		return err
	}

	// Perform the per-table checks:
	issues := 0
	for _, table := range objectTables {
		issues += t.checkObjectTable(ctx, pool, table)
	}
	for _, table := range archiveTables {
		issues += t.checkArchiveTable(ctx, pool, table)
	}
	if issues > 0 {
		return fmt.Errorf("found %d issues in the database schema", issues)
	}

	return nil
}

// listObjectTables returns the sorted list of object table names from the public schema, excluding archive tables
// and internal tables like notifications and schema_migrations.
func (t *tool) listObjectTables(ctx context.Context, pool *pgxpool.Pool) (result []string, err error) {
	rows, err := pool.Query(
		ctx,
		`
		select
			c.relname
		from
			pg_catalog.pg_class c
		join
			pg_catalog.pg_namespace n on n.oid = c.relnamespace
		where
			n.nspname = 'public' and
			c.relkind = 'r' and
			c.relname not like 'archived_%' and
			c.relname not in (
				'notifications',
				'project_membership_subjects',
				'schema_migrations',
				'storage_tier_backends',
				'tenant_domains'
			)
		order by
			c.relname
		`,
	)
	if err != nil {
		err = fmt.Errorf("failed to get list of object tables: %w", err)
		return
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			err = fmt.Errorf("failed to scan table name: %w", err)
			return
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	if err != nil {
		err = fmt.Errorf("failed to get list of object tables: %w", err)
		return
	}
	result = tables
	return
}

// listArchiveTables returns the sorted list of archive table names from the public schema.
func (t *tool) listArchiveTables(ctx context.Context, pool *pgxpool.Pool) (result []string, err error) {
	rows, err := pool.Query(
		ctx,
		`
		select
			c.relname
		from
			pg_catalog.pg_class c
		join
			pg_catalog.pg_namespace n on n.oid = c.relnamespace
		where
			n.nspname = 'public' and
			c.relkind = 'r' and
			c.relname like 'archived_%'
		order by
			c.relname
		`,
	)
	if err != nil {
		err = fmt.Errorf("failed to get list of archive tables: %w", err)
		return
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			err = fmt.Errorf("failed to scan archive table name: %w", err)
			return
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	if err != nil {
		err = fmt.Errorf("failed to get list of archive tables: %w", err)
		return
	}
	result = tables
	return
}

// checkObjectTable performs all consistency checks for a single object table: verifies that it has the expected columns
// with the correct types, a primary key on 'id', a tenant foreign key, and a corresponding archive table. Returns the
// number of issues found.
func (t *tool) checkObjectTable(ctx context.Context, pool *pgxpool.Pool, table string) int {
	// Make a copy of the expected columns so that we can adjust it for special cases without interfering with the
	// original map:
	objectColumns := maps.Clone(toolObjectColumns)

	// The 'projects' table is special because the 'name' column is of type 'ltree' instead of 'text' like in all
	// the other tables:
	if table == "projects" {
		objectColumns["name"] = "ltree"
	}

	// Perform the consistency checks:
	issues := 0

	// Check the columns:
	issues += t.checkColumns(ctx, pool, table, objectColumns)

	// Check the primary key. The 'tenants' table uses the 'name' column as its primary key, and the projects table
	// uses the 'tenant' and 'name' columns. All the other tables, for now, use just the 'id'.
	switch table {
	case "tenants":
		issues += t.checkPrimaryKey(ctx, pool, table, "name")
	case "projects":
		issues += t.checkPrimaryKey(ctx, pool, table, "tenant", "name")
	default:
		issues += t.checkPrimaryKey(ctx, pool, table, "id")
	}

	issues += t.checkTenantForeignKey(ctx, pool, table)
	issues += t.checkTableExists(ctx, pool, "archived_"+table)
	return issues
}

// checkArchiveTable performs all consistency checks for a single archive table: verifies that it has the expected
// columns with the correct types and a corresponding object table. Returns the number of issues found.
func (t *tool) checkArchiveTable(ctx context.Context, pool *pgxpool.Pool, table string) int {
	issues := 0
	issues += t.checkColumns(ctx, pool, table, toolArchivedColumns)
	issues += t.checkTableExists(ctx, pool, strings.TrimPrefix(table, "archived_"))
	return issues
}

// checkColumns verifies that the given table contains all the columns specified in the expected map with the correct
// data types. Returns the number of issues found.
func (t *tool) checkColumns(ctx context.Context, pool *pgxpool.Pool, table string, expected map[string]string) int {
	columns, err := t.fetchColumns(ctx, pool, table)
	if err != nil {
		t.logger.ErrorContext(
			ctx,
			"Failed to fetch columns",
			slog.String("table", table),
			slog.Any("error", err),
		)
		return 1
	}
	issues := 0
	for column, expectedType := range expected {
		actualType, ok := columns[column]
		if !ok {
			t.logger.ErrorContext(
				ctx,
				"Table doesn't have the expected column",
				slog.String("table", table),
				slog.String("column", column),
			)
			issues++
		} else if !strings.EqualFold(actualType, expectedType) {
			t.logger.ErrorContext(
				ctx,
				"Table column has unexpected type",
				slog.String("table", table),
				slog.String("column", column),
				slog.String("expected", expectedType),
				slog.String("actual", actualType),
			)
			issues++
		}
	}
	return issues
}

// checkPrimaryKey verifies that the given table has a primary key constraint on exactly the specified columns. Returns
// the number of issues found.
func (t *tool) checkPrimaryKey(ctx context.Context, pool *pgxpool.Pool, table string, columns ...string) int {
	var actualColumns []string
	rows, err := pool.Query(
		ctx,
		`
		select
			a.attname
		from
			pg_catalog.pg_constraint con
		join
			pg_catalog.pg_class c on c.oid = con.conrelid
		join
			pg_catalog.pg_namespace n on n.oid = c.relnamespace
		join
			pg_catalog.pg_attribute a on a.attrelid = c.oid and a.attnum = any(con.conkey)
		where
			n.nspname = 'public' and
			c.relname = $1 and
			con.contype = 'p'
		order by
			array_position(con.conkey, a.attnum)`,
		table,
	)
	if err != nil {
		t.logger.ErrorContext(
			ctx,
			"Failed to check primary key for table",
			slog.String("table", table),
			slog.Any("error", err),
		)
		return 1
	}
	defer rows.Close()
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.logger.ErrorContext(
				ctx,
				"Failed to scan primary key column",
				slog.String("table", table),
				slog.Any("error", err),
			)
			return 1
		}
		actualColumns = append(actualColumns, column)
	}
	if err := rows.Err(); err != nil {
		t.logger.ErrorContext(
			ctx,
			"Failed to check primary key for table",
			slog.String("table", table),
			slog.Any("error", err),
		)
		return 1
	}
	if !slices.Equal(actualColumns, columns) {
		t.logger.ErrorContext(
			ctx,
			"Object table has unexpected primary key columns",
			slog.String("table", table),
			slog.Any("expected", columns),
			slog.Any("actual", actualColumns),
		)
		return 1
	}
	return 0
}

// checkTableExists verifies that the given table exists in the public schema. Returns the number of issues found.
func (t *tool) checkTableExists(ctx context.Context, pool *pgxpool.Pool, table string) int {
	var count int
	row := pool.QueryRow(
		ctx,
		`
		select
			count(*)
		from
			pg_catalog.pg_class c
		join
			pg_catalog.pg_namespace n on n.oid = c.relnamespace
		where
			n.nspname = 'public' and
			c.relkind = 'r' and
			c.relname = $1`,
		table,
	)
	err := row.Scan(&count)
	if err != nil {
		t.logger.ErrorContext(
			ctx,
			"Failed to check table existence",
			slog.String("table", table),
			slog.Any("error", err),
		)
		return 1
	}
	if count != 1 {
		t.logger.ErrorContext(
			ctx,
			"Expected table doesn't exist",
			slog.String("table", table),
		)
		return 1
	}
	return 0
}

// checkTenantForeignKey verifies that the given table has a foreign key constraint on the 'tenant' column referencing
// the 'id' column of the 'tenants' table. Returns the number of issues found.
func (t *tool) checkTenantForeignKey(ctx context.Context, pool *pgxpool.Pool, table string) int {
	constraint := table + "_tenant_fk"
	var count int
	row := pool.QueryRow(
		ctx,
		`
		select
			count(*)
		from
			pg_catalog.pg_constraint con
		join
			pg_catalog.pg_class c on c.oid = con.conrelid
		join
			pg_catalog.pg_namespace n on n.oid = c.relnamespace
		join
			pg_catalog.pg_attribute a on a.attrelid = c.oid and a.attnum = any(con.conkey)
		join
			pg_catalog.pg_class fc on fc.oid = con.confrelid
		join
			pg_catalog.pg_namespace fn on fn.oid = fc.relnamespace
		join
			pg_catalog.pg_attribute fa on fa.attrelid = fc.oid and fa.attnum = any(con.confkey)
		where
			n.nspname = 'public' and
			c.relname = $1 and
			con.conname = $2 and
			con.contype = 'f' and
			a.attname = 'tenant' and
			fn.nspname = 'public' and
			fc.relname = 'tenants' and
			fa.attname = 'name'`,
		table,
		constraint,
	)
	err := row.Scan(&count)
	if err != nil {
		t.logger.ErrorContext(
			ctx,
			"Failed to check tenant foreign key for table",
			slog.String("table", table),
			slog.Any("error", err),
		)
		return 1
	}
	if count != 1 {
		t.logger.ErrorContext(
			ctx,
			"Object table is missing the tenant foreign key constraint",
			slog.String("table", table),
			slog.String("constraint", constraint),
		)
		return 1
	}
	return 0
}

// fetchColumns returns a map from column name to its full normalized type for the given table.
func (t *tool) fetchColumns(ctx context.Context, pool *pgxpool.Pool, table string) (result map[string]string,
	err error) {
	rows, err := pool.Query(
		ctx,
		`
		select
			a.attname,
			format_type(a.atttypid, a.atttypmod)
		from
			pg_catalog.pg_attribute a
		join
			pg_catalog.pg_class c on c.oid = a.attrelid
		join
			pg_catalog.pg_namespace n on n.oid = c.relnamespace
		where
			n.nspname = 'public' and
			c.relname = $1 and
			a.attnum > 0 and
			not a.attisdropped
		order by
			a.attname`,
		table,
	)
	if err != nil {
		err = fmt.Errorf("failed to get columns for table '%s': %w", table, err)
		return
	}
	defer rows.Close()
	columns := map[string]string{}
	for rows.Next() {
		var name, kind string
		err = rows.Scan(&name, &kind)
		if err != nil {
			err = fmt.Errorf("failed to scan column for table '%s': %w", table, err)
			return
		}
		columns[name] = kind
	}
	err = rows.Err()
	if err != nil {
		err = fmt.Errorf("failed to get columns for table '%s': %w", table, err)
		return
	}
	result = columns
	return
}

// migrationsLogger is an adapter to implement the logging interface of the underlying migrations library using our
// logging library.
type migrationsLogger struct {
	ctx    context.Context
	logger *slog.Logger
}

// Verbose is part of the implementation of the migrate.Logger interface.
func (l *migrationsLogger) Verbose() bool {
	return true
}

// Printf is part of the implementation of the migrate.Logger interface.
func (l *migrationsLogger) Printf(format string, v ...any) {
	message := strings.TrimSpace(fmt.Sprintf(format, v...))
	l.logger.InfoContext(l.ctx, message)
}

// toolFilePathParameters is the set of PostgreSQL connection parameters whose values are file paths. When these are
// found in a connection settings directory, the value used in the URL is the path to the file itself, not the
// file contents.
var toolFilePathParameters = []string{
	"sslcert",
	"sslkey",
	"sslrootcert",
	"sslcrl",
	"sslcrldir",
}

// toolObjectColumns maps each column name that the DAO expects in every object table to its expected PostgreSQL
// full type, as reported by 'pg_catalog.format_type'.
var toolObjectColumns = map[string]string{
	"annotations":        "jsonb",
	"creation_timestamp": "timestamp with time zone",
	"creator":            "text",
	"data":               "jsonb",
	"deletion_timestamp": "timestamp with time zone",
	"finalizers":         "text[]",
	"id":                 "text",
	"labels":             "jsonb",
	"name":               "text",
	"project":            "ltree",
	"tenant":             "text",
	"version":            "integer",
}

// toolArchivedColumns maps each column name that the DAO expects in every archived object table to its expected
// PostgreSQL full type, as reported by 'pg_catalog.format_type'.
var toolArchivedColumns = map[string]string{
	"annotations":        "jsonb",
	"archival_timestamp": "timestamp with time zone",
	"creation_timestamp": "timestamp with time zone",
	"creator":            "text",
	"data":               "jsonb",
	"deletion_timestamp": "timestamp with time zone",
	"id":                 "text",
	"labels":             "jsonb",
	"name":               "text",
	"project":            "ltree",
	"tenant":             "text",
	"version":            "integer",
}
