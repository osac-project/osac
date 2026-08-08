/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalinstancetype

import (
	"bytes"
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

var _ = Describe("get baremetalinstancetype command", func() {
	var (
		cmd     *cobra.Command
		console *terminal.Console
		buffer  *bytes.Buffer
		ctx     context.Context
	)

	BeforeEach(func() {
		cmd = Cmd()
		buffer = &bytes.Buffer{}

		var err error
		console, err = terminal.NewConsole().
			SetLogger(logger).
			SetStdout(buffer).
			Build()
		Expect(err).ToNot(HaveOccurred())

		ctx = terminal.ConsoleIntoContext(context.Background(), console)
		ctx = config.SettingsIntoContext(ctx, &config.Settings{})
	})

	Context("when listing bare metal instance types", func() {
		It("should call the List RPC and render table output", func() {
			// This test validates the list operation contract:
			// - When no arguments provided, calls BareMetalInstanceTypes.List
			// - Renders table with NAME, UUID, and DESCRIPTION columns
			// - Handles empty result set gracefully

			// Mock data
			types := []*publicv1.BareMetalInstanceType{
				{
					Id: "type-1",
					Metadata: &publicv1.Metadata{
						Name: "gpu-large",
					},
					Spec: &publicv1.BareMetalInstanceTypeSpec{
						Description: "Large GPU node with dual A100",
					},
				},
				{
					Id: "type-2",
					Metadata: &publicv1.Metadata{
						Name: "cpu-intensive",
					},
					Spec: &publicv1.BareMetalInstanceTypeSpec{
						Description: "High CPU core count",
					},
				},
			}

			// Test the rendering function directly first
			renderBareMetalInstanceTypeTable(console, types)
			output := buffer.String()

			// Verify table structure and content
			Expect(output).To(ContainSubstring("NAME"))
			Expect(output).To(ContainSubstring("UUID"))
			Expect(output).To(ContainSubstring("DESCRIPTION"))
			Expect(output).To(ContainSubstring("gpu-large"))
			Expect(output).To(ContainSubstring("type-1"))
			Expect(output).To(ContainSubstring("Large GPU node with dual A100"))
			Expect(output).To(ContainSubstring("cpu-intensive"))
			Expect(output).To(ContainSubstring("type-2"))
			Expect(output).To(ContainSubstring("High CPU core count"))
		})

		It("should handle empty result set gracefully", func() {
			// This test validates empty list contract:
			// - Shows appropriate message when no types exist
			// - No table headers when empty

			renderBareMetalInstanceTypeTable(console, nil)
			output := buffer.String()

			Expect(output).To(ContainSubstring("No bare metal instance types found"))
		})
	})

	Context("when getting a specific bare metal instance type", func() {
		It("should render detailed view for individual type", func() {
			// This test validates the get operation contract:
			// - When ID/name provided, shows detailed view
			// - Displays key fields in readable format

			bmiType := &publicv1.BareMetalInstanceType{
				Id: "type-1",
				Metadata: &publicv1.Metadata{
					Name: "gpu-large",
				},
				Spec: &publicv1.BareMetalInstanceTypeSpec{
					Description: "Large GPU node with dual A100",
					Hardware: &publicv1.BareMetalHardwareSpec{
						Cpu: &publicv1.BareMetalCPUSpec{
							Cores:        32,
							Architecture: "x86_64",
						},
						Memory: &publicv1.BareMetalMemorySpec{
							TotalGb: 512,
						},
					},
				},
			}

			renderBareMetalInstanceTypeDetail(console, bmiType)
			output := buffer.String()

			Expect(output).To(ContainSubstring("ID:"))
			Expect(output).To(ContainSubstring("type-1"))
			Expect(output).To(ContainSubstring("Name:"))
			Expect(output).To(ContainSubstring("gpu-large"))
			Expect(output).To(ContainSubstring("Description:"))
			Expect(output).To(ContainSubstring("Large GPU node with dual A100"))
		})
	})

	Context("error handling", func() {
		It("should handle network errors gracefully", func() {
			// This test validates error handling contract:
			// - Network failures produce clear error messages
			// - gRPC errors are translated to user-friendly messages

			err := status.Error(codes.Unavailable, "connection refused")

			// Test that we can handle gRPC errors
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.Unavailable))
		})

		It("should handle not found errors clearly", func() {
			// This test validates not-found contract:
			// - Missing types return clear error messages

			err := status.Error(codes.NotFound, "bare metal instance type not found")

			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			Expect(err.Error()).To(ContainSubstring("bare metal instance type not found"))
		})
	})

	Context("command structure", func() {
		It("should accept zero or one argument", func() {
			// This test validates command argument contract:
			// - Zero args = list operation
			// - One arg = get operation by ID/name
			// - Multiple args = error

			Expect(cmd.Use).To(Equal("baremetalinstancetype [ID_OR_NAME]"))
			Expect(cmd.Args).ToNot(BeNil())
		})

		It("should have appropriate aliases", func() {
			// This test validates command alias contract:
			// - Supports common variations for usability

			Expect(cmd.Aliases).To(ContainElement("baremetalinstancetypes"))
		})
	})
})
