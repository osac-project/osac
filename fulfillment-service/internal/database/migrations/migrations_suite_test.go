/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	gotesting "testing"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/decorators"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/database"
	"github.com/osac-project/fulfillment-service/internal/logging"
)

func TestMigrations(t *gotesting.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Database migrations")
}

// Logger and database objects used by the tests:
var (
	logger *slog.Logger
	server *database.Container
	db     *database.Instance
	tool   database.Tool
	conn   *pgx.Conn
)

var _ = BeforeSuite(func(ctx context.Context) {
	var err error

	// Create the logger:
	logger, err = logging.NewLogger().
		SetLevel(slog.LevelDebug.String()).
		SetOut(GinkgoWriter).
		Build()
	Expect(err).ToNot(HaveOccurred())

	// Create the database server:
	server, err = database.NewContainer().
		SetLogger(logger).
		Build()
	Expect(err).ToNot(HaveOccurred())
	err = server.Start(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		err = server.Stop(ctx)
		Expect(err).ToNot(HaveOccurred())
	})
})

// DescribeMigration is a testing utility to test database migrations. It creates a database and applies all the
// migrations before the given one, and then it runs all the tests in the given body.
//
// The migration name is derived from the calling test file name by stripping the '_test.go' suffix, so it is very
// important that the test file is named correctly.
func DescribeMigration(description string, body func()) bool {
	// Calculate the name of the migration from the name of the calling test file. This must happen here, during the
	// Ginkgo discovery phase, because inside a nested block (BeforeEach, It, etc.) the caller on the stack would be
	// the Ginkgo framework rather than the migration test file.
	_, callerFile, _, _ := runtime.Caller(1)
	migrationName := strings.TrimSuffix(filepath.Base(callerFile), "_test.go")
	migrationFile := fmt.Sprintf("%s.up.sql", migrationName)

	// Create a describe block that prepares the database running all the migrations up to the tested one before
	// running the tests.
	return Describe(description, Ordered, func() {
		// File and number of the previous migration. These are used to pass information between the 'BeforeAll'
		// and the 'BeforeEach' blocks.
		var (
			previousFile   string
			previousNumber uint
		)

		BeforeAll(func() {
			// Check that the migration file exists.
			_, err := os.Stat(migrationFile)
			Expect(err).ToNot(
				HaveOccurred(),
				"Migration file '%s' doesn't exist",
				migrationFile,
			)

			// Sort the migration files by their numeric prefix:
			migrationFiles, err := filepath.Glob("*.up.sql")
			Expect(err).ToNot(HaveOccurred())
			migrationNumber := func(file string) uint {
				parts := strings.SplitN(file, "_", 2)
				Expect(len(parts)).To(
					BeNumerically(">=", 2),
					"Migration file '%s' doesn't follow the naming convention",
				)
				number, err := strconv.ParseUint(parts[0], 10, 64)
				Expect(err).ToNot(HaveOccurred())
				return uint(number)
			}
			sort.Slice(migrationFiles, func(i, j int) bool {
				iNumber := migrationNumber(migrationFiles[i])
				jNumber := migrationNumber(migrationFiles[j])
				return iNumber < jNumber
			})

			// Find the migration inmediatly before the current one:
			for _, currentFile := range migrationFiles {
				if currentFile == migrationFile {
					break
				}
				previousFile = currentFile
			}
			Expect(previousFile).ToNot(
				BeEmpty(),
				"No previous migration file found for '%s'",
				migrationFile,
			)
			previousNumber = migrationNumber(previousFile)
		})

		BeforeEach(func(ctx context.Context) {
			var err error

			// Create the database migrated up to the previous migration:
			db, err = server.NewInstance().
				SetVersion(previousNumber).
				Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(db.Close)

			// Create the database tool:
			url, err := db.Url(ctx)
			Expect(err).ToNot(HaveOccurred())
			tool, err = database.NewTool().
				SetLogger(logger).
				SetURL(url).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Get the database pool:
			conn, err = db.Connection(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(conn.Close)
		})

		// Describe the tests.
		body()
	})
}
