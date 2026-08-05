/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package edit

import (
	"bytes"
	"context"
	"log/slog"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/reflection"
	"github.com/osac-project/fulfillment-service/internal/terminal"
	"github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("Edit command", func() {
	var (
		ctx     context.Context
		logger  *slog.Logger
		server  *testing.Server
		conn    *grpc.ClientConn
		console *terminal.Console
		output  *bytes.Buffer
		helper  reflection.ObjectHelper
	)

	BeforeEach(func() {
		var err error

		ctx = context.Background()

		logger = slog.New(slog.NewTextHandler(GinkgoWriter, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))

		output = &bytes.Buffer{}

		console, err = terminal.NewConsole().
			SetLogger(logger).
			SetStdout(output).
			SetStderr(GinkgoWriter).
			Build()
		Expect(err).ToNot(HaveOccurred())

		err = console.AddTemplates(templatesFS, "templates")
		Expect(err).ToNot(HaveOccurred())

		server = testing.NewServer()
		DeferCleanup(server.Stop)
		server.Start()

		conn, err = grpc.NewClient(
			server.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(conn.Close)

		reflectionHelper, err := reflection.NewHelper().
			SetLogger(logger).
			SetConnection(conn).
			AddPackage("osac.public.v1", 0).
			Build()
		Expect(err).ToNot(HaveOccurred())

		helper = reflectionHelper.Lookup("cluster")
		Expect(helper).ToNot(BeNil())
	})

	DescribeTable("isWatchable",
		func(objectType string, packages map[string]int, expected bool) {
			builder := reflection.NewHelper().
				SetLogger(logger).
				SetConnection(conn)
			for pkg, order := range packages {
				builder = builder.AddPackage(pkg, order)
			}
			reflectionHelper, err := builder.Build()
			Expect(err).ToNot(HaveOccurred())

			h := reflectionHelper.Lookup(objectType)
			Expect(h).ToNot(BeNil(), "failed to look up object type %q", objectType)

			runner := &runnerContext{helper: h}
			Expect(runner.isWatchable()).To(Equal(expected))
		},
		Entry("public cluster is watchable", "cluster", map[string]int{"osac.public.v1": 0}, true),
		Entry("public compute instance is watchable", "computeinstance", map[string]int{"osac.public.v1": 0}, true),
		Entry("public identity provider is not watchable", "identityprovider", map[string]int{"osac.public.v1": 0}, false),
		Entry("private hub is watchable", "hub", map[string]int{"osac.private.v1": 0}, true),
	)

	Describe("showWatchSuggestion", func() {
		It("should render watch suggestion with object type and ID", func() {
			runner := &runnerContext{
				console: console,
				helper:  helper,
			}

			cluster := &publicv1.Cluster{
				Id: "test-cluster-123",
				Metadata: &publicv1.Metadata{
					Name: "my-test-cluster",
				},
			}

			runner.showWatchSuggestion(ctx, cluster)

			outputStr := output.String()
			Expect(outputStr).To(ContainSubstring("get cluster test-cluster-123 --watch"))
			Expect(outputStr).To(ContainSubstring("watch for changes"))
		})
	})
})
