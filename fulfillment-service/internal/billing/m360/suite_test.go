/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package m360

import (
	"fmt"
	"log/slog"
	"testing"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/logging"
)

// TestM360 runs the M360 billing adapter suites.
func TestM360(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "M360 billing adapter")
}

var logger *slog.Logger

var _ = BeforeSuite(func() {
	var err error
	logger, err = logging.NewLogger().
		SetLevel(slog.LevelDebug.String()).
		SetWriter(GinkgoWriter).
		Build()
	Expect(err).ToNot(HaveOccurred())
})

// listBody is an M360 page in the ApiResponse envelope; content is the raw JSON array of items.
func listBody(number int, last bool, content string) string {
	return fmt.Sprintf(`{"success":true,"status":200,"errors":[],"data":{"content":%s,"number":%d,"size":20,"last":%t}}`,
		content, number, last)
}

// dataBody is a single record in the ApiResponse envelope.
func dataBody(data string) string {
	return `{"success":true,"status":200,"errors":[],"data":` + data + `}`
}
