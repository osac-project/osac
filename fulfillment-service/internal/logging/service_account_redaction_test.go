/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Service account credential logging", func() {
	DescribeTable("redacts transient secrets without modifying the delivered response", func(response proto.Message) {
		var buffer bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
		interceptor, err := NewInterceptor().SetLogger(logger).SetBodies(true).Build()
		Expect(err).ToNot(HaveOccurred())
		original := proto.Clone(response)
		result, err := interceptor.UnaryServer(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/osac.public.v1.ServiceAccounts/Create"},
			func(context.Context, any) (any, error) { return response, nil })
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(result.(proto.Message), original)).To(BeTrue())
		Expect(strings.Contains(buffer.String(), "test-only-credential-marker")).To(BeFalse(), "response logs must omit the credential")
		Expect(buffer.String()).To(ContainSubstring("clientSecret"))
		Expect(buffer.String()).To(ContainSubstring(redactMark))
	}, Entry("public Create", publicv1.ServiceAccountsCreateResponse_builder{ClientSecret: "test-only-credential-marker"}.Build()),
		Entry("private Create", privatev1.ServiceAccountsCreateResponse_builder{ClientSecret: "test-only-credential-marker"}.Build()),
		Entry("public RegenerateSecret", publicv1.ServiceAccountsRegenerateSecretResponse_builder{ClientSecret: "test-only-credential-marker"}.Build()),
		Entry("private RegenerateSecret", privatev1.ServiceAccountsRegenerateSecretResponse_builder{ClientSecret: "test-only-credential-marker"}.Build()))
})
