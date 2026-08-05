/*
Copyright (c) 2025 Red Hat, Inc.

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
	"log/slog"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/logging"
)

func TestDatabase(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Database package")
}

var (
	logger *slog.Logger
	server *Container
)

var _ = BeforeSuite(func() {
	var err error

	ctx := context.Background()

	// Create the logger:
	logger, err = logging.NewLogger().
		SetLevel(slog.LevelDebug.String()).
		SetOut(GinkgoWriter).
		Build()
	Expect(err).ToNot(HaveOccurred())

	// Create and start the database server:
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	DeferCleanup(cancel)
	server, err = NewContainer().
		SetLogger(logger).
		Build()
	Expect(err).ToNot(HaveOccurred())
	err = server.Start(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err = server.Stop(ctx)
		Expect(err).ToNot(HaveOccurred())
	})
})
