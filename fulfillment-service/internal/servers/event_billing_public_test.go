/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
	"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The public event stream is a tenant-facing interface. The billing id is private, so the public event message must not
// have the field the private envelope carries.
var _ = Describe("Public event", func() {
	It("Omits the external billing id", func() {
		publicFields := (&publicv1.Event{}).ProtoReflect().Descriptor().Fields()
		Expect(publicFields.ByName("external_billing_id")).To(BeNil())

		privateField := (&privatev1.Event{}).ProtoReflect().Descriptor().Fields().ByName("external_billing_id")
		Expect(privateField).NotTo(BeNil())
		Expect(int(privateField.Number())).To(Equal(40))
	})
})
