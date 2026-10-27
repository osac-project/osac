/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package validation

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Storage backend ownership validation", func() {
	DescribeTable("validates backend configuration according to ownership",
		func(specJSON, expectedError string) {
			spec := &privatev1.StorageBackendSpec{}
			Expect(protojson.Unmarshal([]byte(specJSON), spec)).To(Succeed())
			interceptor, err := NewProtovalidateInterceptor().SetLogger(logger).Build()
			Expect(err).NotTo(HaveOccurred())
			called := false
			response, err := interceptor.UnaryServer(context.Background(),
				privatev1.StorageBackendsCreateRequest_builder{
					Object: privatev1.StorageBackend_builder{
						Metadata: privatev1.Metadata_builder{Name: "ownership-test"}.Build(),
						Spec:     spec,
					}.Build(),
				}.Build(),
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.StorageBackendsService/Create"},
				func(context.Context, any) (any, error) {
					called = true
					return "accepted", nil
				},
			)
			if expectedError != "" {
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
				Expect(status.Convert(err).Message()).To(ContainSubstring(expectedError))
				Expect(called).To(BeFalse())
				Expect(response).To(BeNil())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(called).To(BeTrue())
			Expect(response).To(Equal("accepted"))
		},
		Entry("CSP-managed NetApp without endpoint or credentials",
			`{"provider":"netapp","cspManaged":true}`, ""),
		Entry("OSAC-managed NetApp with valid connection details",
			`{"provider":"netapp","cspManaged":false,"endpoint":"https://storage.example.com","credentials":{"username":"admin","password":"fixture-password"}}`, "OSAC-managed NetApp storage is not supported; set cspManaged to true"),
		Entry("NetApp with omitted ownership and valid connection details",
			`{"provider":"netapp","endpoint":"https://storage.example.com","credentials":{"username":"admin","password":"fixture-password"}}`, "OSAC-managed NetApp storage is not supported; set cspManaged to true"),
		Entry("CSP-managed VAST without endpoint or credentials",
			`{"provider":"vast","cspManaged":true}`, ""),
		Entry("accepts the protobuf field spelling",
			`{"provider":"netapp","csp_managed":true}`, ""),
		Entry("omitted ownership preserves OSAC-managed configuration",
			`{"provider":"vast","endpoint":"https://storage.example.com","credentials":{"username":"admin","password":"fixture-password"}}`, ""),
		Entry("OSAC-managed Pure remains supported",
			`{"provider":"pure","cspManaged":false,"endpoint":"https://storage.example.com","credentials":{"username":"admin","password":"fixture-password"}}`, ""),
		Entry("OSAC-managed VAST with a Secret reference remains supported",
			`{"provider":"vast","cspManaged":false,"endpoint":"https://storage.example.com","credentials":{"username":"admin","password_secret":{"id":"fixture-secret"}}}`, ""),
		Entry("OSAC-managed endpoint remains required",
			`{"provider":"vast","credentials":{"username":"admin","password":"fixture-password"}}`, "endpoint"),
		Entry("OSAC-managed credentials remain required",
			`{"provider":"vast","cspManaged":false,"endpoint":"https://storage.example.com"}`, "credentials"),
		Entry("CSP-managed provider remains required",
			`{"cspManaged":true}`, "provider"),
		Entry("CSP-managed supplied inline credentials are accepted",
			`{"provider":"netapp","cspManaged":true,"credentials":{"username":"admin","password":"fixture-password"}}`, ""),
		Entry("CSP-managed supplied Secret reference is accepted",
			`{"provider":"netapp","cspManaged":true,"credentials":{"username":"admin","password_secret":{"id":"fixture-secret"}}}`, ""),
		Entry("CSP-managed supplied credentials still require username",
			`{"provider":"netapp","cspManaged":true,"credentials":{"password":"fixture-password"}}`, "username"),
		Entry("CSP-managed supplied credentials still require a password source",
			`{"provider":"netapp","cspManaged":true,"credentials":{"username":"admin"}}`, "exactly one"),
		Entry("CSP-managed supplied credentials reject two password sources",
			`{"provider":"netapp","cspManaged":true,"credentials":{"username":"admin","password":"fixture-password","password_secret":{"id":"fixture-secret"}}}`, "exactly one"),
	)

	It("uses cspManaged as its JSON field name", func() {
		spec := &privatev1.StorageBackendSpec{}
		Expect(protojson.Unmarshal([]byte(`{"provider":"netapp","cspManaged":true}`), spec)).To(Succeed())
		data, err := protojson.Marshal(spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(`"cspManaged":true`))
	})
})
