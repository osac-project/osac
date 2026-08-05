/*
Copyright (c) 2026 Red Hat Inc.

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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/decorators"
	. "github.com/onsi/gomega"
)

var _ = Describe("Schema", Ordered, func() {
	var (
		ctx context.Context
		db  *Instance
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create the database:
		db, err = server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
	})

	It("Is consistent", func() {
		url, err := db.Url(ctx)
		Expect(err).ToNot(HaveOccurred())
		tool, err := NewTool().
			SetLogger(logger).
			SetURL(url).
			Build()
		Expect(err).ToNot(HaveOccurred())
		err = tool.CheckSchema(ctx)
		Expect(err).ToNot(HaveOccurred())
	})
})
