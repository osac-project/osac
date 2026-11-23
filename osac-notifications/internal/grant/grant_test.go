/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grant_test

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-notifications/internal/grant"
)

func TestDefaultMapAllowsFulfillmentEvent(t *testing.T) {
	g := NewWithT(t)
	g.Expect(grant.DefaultMap.Check("osac-fulfillment", "event")).To(Succeed())
	g.Expect(grant.DefaultMap.Check("osac-fulfillment", "announcement")).To(HaveOccurred())
	g.Expect(grant.DefaultMap.Check("osac-metering", "event")).To(HaveOccurred())
}
