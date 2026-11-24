/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Cmd creates the Cloud Provider Admin model-management command group.
func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:                   "model",
		Aliases:               []string{"models"},
		Short:                 "Manage MaaS model deployments",
		Long:                  "Manage local MaaS model deployments on the registered AI Grid Hub.",
		DisableFlagsInUseLine: true,
	}
	result.AddCommand(listCmd())
	result.AddCommand(getCmd())
	result.AddCommand(createCmd())
	result.AddCommand(updateCmd())
	result.AddCommand(deleteCmd())
	result.AddCommand(placementClustersCmd())
	return result
}

func listCmd() *cobra.Command {
	var offset, limit int32
	result := &cobra.Command{
		Use:                   "list",
		Aliases:               []string{"ls"},
		Short:                 "List MaaS model deployments",
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, ctx, done, err := connect(cmd)
			if err != nil {
				return err
			}
			defer done()
			request := publicv1.ModelsListRequest_builder{Offset: proto.Int32(offset)}
			if cmd.Flags().Changed("limit") {
				request.Limit = proto.Int32(limit)
			}
			response, err := client.List(ctx, request.Build())
			if err != nil {
				return fmt.Errorf("failed to list models: %w", err)
			}
			return renderList(cmd.OutOrStdout(), response.GetItems())
		},
	}
	result.Flags().Int32Var(&offset, "offset", 0, "_NUMBER_ - Number of models to skip.")
	result.Flags().Int32Var(&limit, "limit", 0, "_NUMBER_ - Maximum number of models to list.")
	return result
}

func getCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "get ID",
		Short:                 "Get a MaaS model deployment",
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, done, err := connect(cmd)
			if err != nil {
				return err
			}
			defer done()
			response, err := client.Get(ctx, publicv1.ModelsGetRequest_builder{Id: args[0]}.Build())
			if err != nil {
				return fmt.Errorf("failed to get model %q: %w", args[0], err)
			}
			return writeJSON(cmd.OutOrStdout(), response.GetObject())
		},
	}
}

func createCmd() *cobra.Command {
	var filename string
	result := &cobra.Command{
		Use:                   "create -f FILE",
		Short:                 "Create a MaaS model deployment from a JSON object",
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if filename == "" {
				return errors.New("--filename is required")
			}
			model := &publicv1.Model{}
			if err := readModel(filename, model); err != nil {
				return err
			}
			client, ctx, done, err := connect(cmd)
			if err != nil {
				return err
			}
			defer done()
			response, err := client.Create(ctx, publicv1.ModelsCreateRequest_builder{Object: model}.Build())
			if err != nil {
				return fmt.Errorf("failed to create model: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), response.GetObject())
		},
	}
	result.Flags().StringVarP(&filename, "filename", "f", "", "_FILE_ - JSON model object to create.")
	return result
}

func updateCmd() *cobra.Command {
	var filename, mask string
	var lock bool
	result := &cobra.Command{
		Use:                   "update -f FILE --update-mask PATH[,PATH...]",
		Short:                 "Update supported settings on a MaaS model deployment",
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if filename == "" {
				return errors.New("--filename is required")
			}
			if strings.TrimSpace(mask) == "" {
				return errors.New("--update-mask is required")
			}
			model := &publicv1.Model{}
			if err := readModel(filename, model); err != nil {
				return err
			}
			paths := strings.Split(mask, ",")
			for i := range paths {
				paths[i] = strings.TrimSpace(paths[i])
			}
			fieldMask, err := fieldmaskpb.New(model, paths...)
			if err != nil {
				return fmt.Errorf("invalid update mask: %w", err)
			}
			client, ctx, done, err := connect(cmd)
			if err != nil {
				return err
			}
			defer done()
			response, err := client.Update(ctx, publicv1.ModelsUpdateRequest_builder{Object: model, UpdateMask: fieldMask, Lock: lock}.Build())
			if err != nil {
				return fmt.Errorf("failed to update model %q: %w", model.GetId(), err)
			}
			return writeJSON(cmd.OutOrStdout(), response.GetObject())
		},
	}
	result.Flags().StringVarP(&filename, "filename", "f", "", "_FILE_ - JSON model object to update.")
	result.Flags().StringVar(&mask, "update-mask", "", "_PATHS_ - Comma-separated list of supported update paths.")
	result.Flags().BoolVar(&lock, "lock", true, "Require the model version in the file to match the current version.")
	return result
}

func deleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "delete ID",
		Short:                 "Delete a MaaS model deployment",
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, done, err := connect(cmd)
			if err != nil {
				return err
			}
			defer done()
			if _, err := client.Delete(ctx, publicv1.ModelsDeleteRequest_builder{Id: args[0]}.Build()); err != nil {
				return fmt.Errorf("failed to delete model %q: %w", args[0], err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted model %q.\n", args[0])
			return err
		},
	}
}

func placementClustersCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "placement-clusters",
		Short:                 "List eligible AI Grid inference clusters",
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, ctx, done, err := connect(cmd)
			if err != nil {
				return err
			}
			defer done()
			response, err := client.ListPlacementClusters(ctx, &publicv1.ModelsListPlacementClustersRequest{})
			if err != nil {
				return fmt.Errorf("failed to list placement clusters: %w", err)
			}
			for _, cluster := range response.GetClusters() {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), cluster.GetName()); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func connect(cmd *cobra.Command) (publicv1.ModelsClient, context.Context, func(), error) {
	ctx := cmd.Context()
	settings := config.SettingsFromContext(ctx)
	if !settings.Armed() {
		return nil, nil, nil, errors.New("there is no configuration, run the 'login' command")
	}
	connection, err := settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	return publicv1.NewModelsClient(connection), ctx, func() { _ = connection.Close() }, nil
}

func readModel(filename string, model *publicv1.Model) error {
	var data []byte
	var err error
	if filename == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(filename) // #nosec G304 -- the CLI caller explicitly selects this local file
	}
	if err != nil {
		return fmt.Errorf("failed to read model file %q: %w", filename, err)
	}
	if err := (protojson.UnmarshalOptions{}).Unmarshal(data, model); err != nil {
		return fmt.Errorf("failed to decode model JSON: %w", err)
	}
	return nil
}

func writeJSON(w io.Writer, message proto.Message) error {
	data, err := (protojson.MarshalOptions{UseProtoNames: true, Indent: "  "}).Marshal(message)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func renderList(w io.Writer, models []*publicv1.Model) error {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "ID\tNAME\tSTATUS\tINFERENCE CLUSTERS\tSOURCE"); err != nil {
		return err
	}
	for _, model := range models {
		clusters := strings.Join(model.GetSpec().GetPlacement().GetClusters(), ",")
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", model.GetId(), model.GetMetadata().GetName(), model.GetStatus().GetPhase().String(), clusters, model.GetSpec().GetSourceUri()); err != nil {
			return err
		}
	}
	return writer.Flush()
}
