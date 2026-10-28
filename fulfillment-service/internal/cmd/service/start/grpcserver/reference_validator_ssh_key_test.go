/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"context"
	"io"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

var _ = Describe("ComputeInstance SSH reference interception", func() {
	DescribeTable("leaves ComputeInstance SSH references for the tenant-scoped handler", func(layer, method string) {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		tenancy, err := auth.NewGuestTenancyLogic().SetLogger(logger).Build()
		Expect(err).NotTo(HaveOccurred())
		validator, err := newReferenceValidator(logger, tenancy, prometheus.NewRegistry())
		Expect(err).NotTo(HaveOccurred())

		prefix := "osac." + layer + ".v1."
		requestType, err := protoregistry.GlobalTypes.FindMessageByName(
			protoreflect.FullName(prefix + "ComputeInstances" + method + "Request"))
		Expect(err).NotTo(HaveOccurred())
		request := requestType.New().Interface()
		body := `{"object":{"metadata":{"tenant":"tenant-a"},"spec":{"sshKey":{"name":"login-key","project":"apps"}}}}`
		if method == "Update" {
			body = `{"object":{"id":"instance-id","spec":{"sshKey":{"name":"login-key","project":"apps"}}},"updateMask":"spec.sshKey"}`
		}
		Expect(protojson.Unmarshal([]byte(body), request)).To(Succeed())
		original := proto.Clone(request)
		called := false
		_, err = validator.UnaryServer(context.Background(), request,
			&grpc.UnaryServerInfo{FullMethod: "/" + prefix + "ComputeInstances/" + method},
			func(_ context.Context, actual any) (any, error) {
				called = true
				Expect(proto.Equal(actual.(proto.Message), original)).To(BeTrue())
				return "ok", nil
			})
		Expect(err).NotTo(HaveOccurred())
		Expect(called).To(BeTrue())
	},
		Entry("public create", "public", "Create"),
		Entry("private create", "private", "Create"),
		Entry("public update", "public", "Update"),
		Entry("private update", "private", "Update"),
	)
})
