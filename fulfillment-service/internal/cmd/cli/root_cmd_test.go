/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cli

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
)

var _ = Describe("CLI root color flag", func() {
	It("enables styled help on a redirected writer", func() {
		GinkgoT().Setenv("NO_COLOR", "")
		cmd, err := Root()
		Expect(err).NotTo(HaveOccurred())
		Expect(cmd.PersistentFlags().Lookup("color")).NotTo(BeNil())
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs([]string{"--color", "describe", "computeinstancecatalogitem", "--help"})
		Expect(cmd.Execute()).To(Succeed())
		Expect(output.String()).To(ContainSubstring("\x1b["))
	})
})

var _ = Describe("CLI authentication hints", func() {
	DescribeTable("adds endpoint guidance to API auth failures",
		func(code codes.Code, message, expectedHint string) {
			root := &cobra.Command{Use: "osac", SilenceErrors: true, SilenceUsage: true}
			root.AddCommand(&cobra.Command{
				Use: "request",
				RunE: func(*cobra.Command, []string) error {
					return fmt.Errorf("request failed: %w", status.Error(code, message))
				},
			})
			root.SetArgs([]string{"request"})

			err := Execute(root)
			Expect(err.Error()).To(ContainSubstring(message))
			Expect(err.Error()).To(ContainSubstring(expectedHint))
			Expect(err.Error()).To(ContainSubstring("fulfillment-api"))
			Expect(err.Error()).To(ContainSubstring("fulfillment-internal-api"))
		},
		Entry("unauthenticated", codes.Unauthenticated, "token expired", "Authentication failed"),
		Entry("permission denied", codes.PermissionDenied, "permission denied", "not authorized"),
	)

	It("leaves non-authentication errors unchanged", func() {
		root := &cobra.Command{Use: "osac", SilenceErrors: true, SilenceUsage: true}
		originalErr := status.Error(codes.NotFound, "missing resource")
		root.AddCommand(&cobra.Command{
			Use: "request",
			RunE: func(*cobra.Command, []string) error {
				return originalErr
			},
		})
		root.SetArgs([]string{"request"})

		err := Execute(root)
		Expect(err).To(BeIdenticalTo(originalErr))
	})

	It("uses the saved private mode to guide tenant users", func() {
		settings, err := config.NewSettings().
			SetLogger(slog.New(slog.DiscardHandler)).
			SetDir("/tmp").
			Build()
		Expect(err).NotTo(HaveOccurred())
		settings.SetPrivate(true)

		root := &cobra.Command{Use: "osac", SilenceErrors: true, SilenceUsage: true}
		request := &cobra.Command{
			Use: "request",
			RunE: func(*cobra.Command, []string) error {
				return status.Error(codes.PermissionDenied, "permission denied")
			},
		}
		request.SetContext(config.SettingsIntoContext(context.Background(), settings))
		root.AddCommand(request)
		root.SetArgs([]string{"request"})

		err = Execute(root)
		Expect(err.Error()).To(ContainSubstring("private API mode enabled"))
		Expect(err.Error()).To(ContainSubstring("without --private"))
	})
})

var _ = Describe("CLI root --project flag", func() {
	It("registers the --project persistent flag", func() {
		cmd, err := Root()
		Expect(err).NotTo(HaveOccurred())
		flag := cmd.PersistentFlags().Lookup("project")
		Expect(flag).NotTo(BeNil())
		Expect(flag.DefValue).To(Equal(""))
	})

	It("includes the project subcommand", func() {
		cmd, err := Root()
		Expect(err).NotTo(HaveOccurred())
		var names []string
		for _, sub := range cmd.Commands() {
			names = append(names, sub.Name())
		}
		Expect(names).To(ContainElement("project"))
	})
})
