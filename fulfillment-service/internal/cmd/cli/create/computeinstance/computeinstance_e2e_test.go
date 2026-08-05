/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package computeinstance

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("Compute Instance E2E", func() {
	var (
		ctx            context.Context
		cancel         context.CancelFunc
		server         *testing.Server
		conn           *grpc.ClientConn
		instanceClient publicv1.ComputeInstancesClient
		templateClient publicv1.ComputeInstanceTemplatesClient
	)

	BeforeEach(func() {
		var err error

		// Create cancellable context
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)

		// Create test server
		server = testing.NewServer()
		DeferCleanup(server.Stop)

		// Create and load compute instance scenario
		scenario := &testing.ComputeInstanceScenario{
			Name:        "e2e-test-scenario",
			Description: "E2E test scenario for compute instances",
			Templates: []*testing.TemplateData{
				{
					ID:          "tpl-test-001",
					Name:        "test-template",
					Title:       "Test Template",
					Description: "A test compute instance template",
				},
			},
			Instances: []*testing.InstanceData{
				{
					ID:                "ci-existing-001",
					Name:              "existing-instance",
					Template:          "tpl-test-001",
					State:             publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					InternalIPAddress: "192.168.1.100",
				},
			},
		}

		// Create and register mock compute instance server
		ciServer := testing.NewMockComputeInstancesServer(scenario)
		publicv1.RegisterComputeInstancesServer(server.Registrar(), ciServer)

		// Create and register mock compute instance templates server
		citServer := testing.NewMockComputeInstanceTemplatesServer(scenario)
		publicv1.RegisterComputeInstanceTemplatesServer(server.Registrar(), citServer)

		// Start the server
		server.Start()

		// Create client connection
		conn, err = grpc.NewClient(
			server.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(conn.Close)

		// Create clients
		instanceClient = publicv1.NewComputeInstancesClient(conn)
		templateClient = publicv1.NewComputeInstanceTemplatesClient(conn)
	})

	It("should create, list, and delete a compute instance", func() {
		// Step 1: List templates to get a valid template
		listTemplatesResp, err := templateClient.List(ctx, &publicv1.ComputeInstanceTemplatesListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(listTemplatesResp.Items).ToNot(BeEmpty())
		template := listTemplatesResp.Items[0]

		// Step 2: List existing instances (should have 1)
		listResp, err := instanceClient.List(ctx, &publicv1.ComputeInstancesListRequest{})
		Expect(err).ToNot(HaveOccurred())
		initialCount := len(listResp.Items)
		Expect(initialCount).To(Equal(1))

		// Step 3: Create a new compute instance
		createResp, err := instanceClient.Create(ctx, &publicv1.ComputeInstancesCreateRequest{
			Object: &publicv1.ComputeInstance{
				Metadata: &publicv1.Metadata{
					Name: "new-test-instance",
				},
				Spec: &publicv1.ComputeInstanceSpec{
					Template: template.Id,
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(createResp.Object).ToNot(BeNil())
		Expect(createResp.Object.Id).ToNot(BeEmpty())
		createdID := createResp.Object.Id

		// Step 4: List instances again (should have 2 now)
		listResp, err = instanceClient.List(ctx, &publicv1.ComputeInstancesListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(listResp.Items).To(HaveLen(initialCount + 1))

		// Step 5: Get the created instance by ID
		getResp, err := instanceClient.Get(ctx, &publicv1.ComputeInstancesGetRequest{
			Id: createdID,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.Object.Id).To(Equal(createdID))
		Expect(getResp.Object.Spec.Template).To(Equal(template.Id))

		// Step 6: Delete the created instance
		_, err = instanceClient.Delete(ctx, &publicv1.ComputeInstancesDeleteRequest{
			Id: createdID,
		})
		Expect(err).ToNot(HaveOccurred())

		// Step 7: List instances again (should be back to initial count)
		listResp, err = instanceClient.List(ctx, &publicv1.ComputeInstancesListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(listResp.Items).To(HaveLen(initialCount))

		// Step 8: Verify the instance was deleted (Get should fail)
		_, err = instanceClient.Get(ctx, &publicv1.ComputeInstancesGetRequest{
			Id: createdID,
		})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	})
})
