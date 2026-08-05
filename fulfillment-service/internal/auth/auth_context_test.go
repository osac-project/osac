/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// This file contains tests for the functions that extract authentication information from
// contexts.

package auth

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Subject inside context", func() {
	It("Returns the same subject that was added", func() {
		subject := &Subject{}
		ctx := ContextWithSubject(context.Background(), subject)
		extracted := SubjectFromContext(ctx)
		Expect(extracted).To(BeIdenticalTo(subject))
	})

	It("Panics if there is no subject", func() {
		ctx := context.Background()
		Expect(func() {
			SubjectFromContext(ctx)
		}).To(PanicWith("failed to get subject from context"))
	})
})
