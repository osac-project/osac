/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package get

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/packages"
	"github.com/osac-project/fulfillment-service/internal/reflection"
	"github.com/osac-project/fulfillment-service/internal/terminal"
	"github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("Watch e2e", func() {
	var (
		ctx          context.Context
		cancel       context.CancelFunc
		server       *testing.Server
		conn         *grpc.ClientConn
		eventsServer *testing.EventsServerFuncs
		helper       reflection.ObjectHelper
		console      *terminal.Console
	)

	BeforeEach(func() {
		var err error

		// Create cancellable context
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)

		// Create console
		console, err = terminal.NewConsole().
			SetLogger(logger).
			SetStdout(GinkgoWriter).
			SetStderr(GinkgoWriter).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create server
		server = testing.NewServer()
		DeferCleanup(server.Stop)

		// Create test scenario structure
		scenario := &testing.EventScenario{
			Name:        "test-scenario",
			Description: "Test scenario for watch e2e",
			Events: []*testing.ScenarioEvent{
				{
					ID:           "event-1",
					Type:         publicv1.EventType_EVENT_TYPE_OBJECT_CREATED,
					DelaySeconds: 0,
					Cluster: &testing.ClusterEventData{
						ID:    "test-cluster-1",
						Name:  "my-test-cluster",
						State: publicv1.ClusterState_CLUSTER_STATE_PROGRESSING,
						Conditions: []*testing.ConditionData{
							{
								Type:    publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
								Status:  publicv1.ConditionStatus_CONDITION_STATUS_FALSE,
								Message: "Cluster is being created",
							},
						},
					},
				},
			},
		}

		// Create events server using the builder with test scenario
		eventsServer = testing.NewMockEventsServerBuilder().
			WithScenario(scenario).
			Build()

		// Register the events server
		publicv1.RegisterEventsServer(server.Registrar(), eventsServer)

		// Start the server
		server.Start()

		// Create client connection
		conn, err = grpc.NewClient(
			server.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(conn.Close)

		// Create reflection helper
		reflectionHelper, err := reflection.NewHelper().
			SetLogger(logger).
			SetConnection(conn).
			AddPackage(packages.PublicV1, 0).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Get cluster helper
		helper = reflectionHelper.Lookup("cluster")
		Expect(helper).ToNot(BeNil())
	})

	It("should receive and display events from the stream", func() {
		// Create global helper for the runner
		globalHelper, err := reflection.NewHelper().
			SetLogger(logger).
			SetConnection(conn).
			AddPackage(packages.PublicV1, 0).
			Build()
		Expect(err).ToNot(HaveOccurred())

		runner := &runnerContext{
			logger:       logger,
			conn:         conn,
			globalHelper: globalHelper,
			objectHelper: helper,
			console:      console,
			args: struct {
				format string
				filter string
				watch  bool
			}{
				format: outputFormatTable,
				watch:  true,
			},
		}

		// Start watching in a goroutine
		done := make(chan error, 1)
		go func() {
			// Watch with no specific keys (all clusters)
			done <- runner.watch(ctx, []string{})
		}()

		// Give it a moment to start watching and receive events
		time.Sleep(100 * time.Millisecond)

		// Cancel the context to stop watching
		cancel()

		// Wait for watch to finish
		err = <-done

		// Should get context cancelled error (wrapped)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("context canceled"))
	})

	It("should build correct filter for specific cluster", func() {
		runner := &runnerContext{
			objectHelper: helper,
		}

		filter, err := runner.buildEventFilter([]string{"test-cluster-1"})
		Expect(err).ToNot(HaveOccurred())
		Expect(filter).To(ContainSubstring("has(event.cluster)"))
		Expect(filter).To(ContainSubstring("test-cluster-1"))
	})

	It("should build correct filter for all clusters", func() {
		runner := &runnerContext{
			objectHelper: helper,
		}

		filter, err := runner.buildEventFilter([]string{})
		Expect(err).ToNot(HaveOccurred())
		Expect(filter).To(Equal("has(event.cluster)"))
	})
})
