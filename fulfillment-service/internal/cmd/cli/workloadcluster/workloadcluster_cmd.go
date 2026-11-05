/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package workloadcluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/create/metautil"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Cmd creates the WorkloadCluster command group.
func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:   "workload-cluster",
		Short: "Manage Hub-registered workload clusters",
		Long:  "Register and inspect Hub-managed clusters used as provider MaaS capacity.",
	}
	result.AddCommand(createCmd())
	result.AddCommand(listCmd())
	result.AddCommand(describeCmd())
	result.AddCommand(updateCmd())
	return result
}

type createArgs struct {
	name              string
	sourceHub         string
	managedCluster    string
	supportedWorkload []string
}

func createCmd() *cobra.Command {
	args := &createArgs{}
	result := &cobra.Command{
		Use:                   "create [FLAG...]",
		Short:                 "Register a Hub-managed workload cluster",
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCreate(cmd, args)
		},
	}
	flags := result.Flags()
	flags.StringVar(&args.name, "name", "", "_NAME_ - Unique OSAC resource name.")
	flags.StringVar(&args.sourceHub, "source-hub", "", "_ID|NAME_ - OSAC Hub hosting the ManagedCluster.")
	flags.StringVar(&args.managedCluster, "managed-cluster-name", "", "_NAME_ - ManagedCluster name on the Hub.")
	flags.StringSliceVar(&args.supportedWorkload, "supported-workloads", nil, "_VALUES_ - Supported workloads, currently only `maas`.")
	return result
}

func runCreate(cmd *cobra.Command, args *createArgs) error {
	if args.name == "" {
		return errors.New("name is required")
	}
	if args.sourceHub == "" {
		return errors.New("source-hub is required")
	}
	if args.managedCluster == "" {
		return errors.New("managed-cluster-name is required")
	}
	workloads, err := parseSupportedWorkloads(args.supportedWorkload)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return errors.New("there is no configuration, run the 'login' command")
	}
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()
	hub, err := findHub(ctx, privatev1.NewHubsClient(conn), args.sourceHub)
	if err != nil {
		return err
	}

	object := privatev1.WorkloadCluster_builder{
		Metadata: privatev1.Metadata_builder{
			Name:    args.name,
			Tenant:  config.TenantFromContext(ctx),
			Project: config.ProjectFromContext(ctx),
		}.Build(),
		Spec: privatev1.WorkloadClusterSpec_builder{
			SourceHubId:        hub.GetId(),
			ManagedClusterName: args.managedCluster,
			SupportedWorkloads: workloads,
		}.Build(),
	}.Build()
	response, err := privatev1.NewWorkloadClustersClient(conn).Create(ctx, privatev1.WorkloadClustersCreateRequest_builder{
		Object: object,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create workload cluster: %w", err)
	}

	terminal.ConsoleFromContext(ctx).Infof(ctx, "Created workload cluster '%s'.\n", response.GetObject().GetId())
	return nil
}

func findHub(ctx context.Context, client privatev1.HubsClient, ref string) (*privatev1.Hub, error) {
	return lookup.Find(ref, "hub", func(filter string, limit int32) ([]*privatev1.Hub, error) {
		response, err := client.List(ctx, privatev1.HubsListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to find hub: %w", err)
		}
		return response.GetItems(), nil
	})
}

func listCmd() *cobra.Command {
	var filter string
	result := &cobra.Command{
		Use:                   "list",
		Aliases:               []string{"ls"},
		Short:                 "List registered workload clusters",
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg := config.SettingsFromContext(ctx)
			if !cfg.Armed() {
				return errors.New("there is no configuration, run the 'login' command")
			}
			conn, err := cfg.Connect(ctx, cmd.Flags())
			if err != nil {
				return fmt.Errorf("failed to create gRPC connection: %w", err)
			}
			defer conn.Close()
			response, err := privatev1.NewWorkloadClustersClient(conn).List(ctx, privatev1.WorkloadClustersListRequest_builder{
				Filter: proto.String(filter),
			}.Build())
			if err != nil {
				return fmt.Errorf("failed to list workload clusters: %w", err)
			}
			hubsResponse, err := privatev1.NewHubsClient(conn).List(ctx, privatev1.HubsListRequest_builder{}.Build())
			if err != nil {
				return fmt.Errorf("failed to list hubs: %w", err)
			}
			hubNames := make(map[string]string, len(hubsResponse.GetItems()))
			for _, hub := range hubsResponse.GetItems() {
				hubNames[hub.GetId()] = hub.GetMetadata().GetName()
			}
			renderWorkloadClusterList(terminal.ConsoleFromContext(ctx), response.GetItems(), hubNames)
			return nil
		},
	}
	result.Flags().StringVar(&filter, "filter", "", "_EXPRESSION_ - CEL filter for selecting workload clusters.")
	return result
}

func describeCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "describe ID|NAME",
		Short:                 "Describe a registered workload cluster",
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg := config.SettingsFromContext(ctx)
			if !cfg.Armed() {
				return errors.New("there is no configuration, run the 'login' command")
			}
			conn, err := cfg.Connect(ctx, cmd.Flags())
			if err != nil {
				return fmt.Errorf("failed to create gRPC connection: %w", err)
			}
			defer conn.Close()
			client := privatev1.NewWorkloadClustersClient(conn)
			object, err := findWorkloadCluster(ctx, client, args[0])
			if err != nil {
				return err
			}
			return renderWorkloadCluster(terminal.ConsoleFromContext(ctx), object)
		},
	}
}

type updateArgs struct {
	supportedWorkload []string
	labels            []string
	clearLabels       bool
}

func updateCmd() *cobra.Command {
	args := &updateArgs{}
	result := &cobra.Command{
		Use:                   "update [FLAG...] ID|NAME",
		Short:                 "Update a registered workload cluster",
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			return runUpdate(cmd, positional[0], args)
		},
	}
	flags := result.Flags()
	flags.StringSliceVar(&args.supportedWorkload, "supported-workloads", nil, "_VALUES_ - Replace supported workloads.")
	flags.StringSliceVar(&args.labels, "label", nil, "_KEY=VALUE_ - Replace user labels; repeat for multiple labels.")
	flags.BoolVar(&args.clearLabels, "clear-labels", false, "_[BOOLEAN]_ - Clear all user labels.")
	return result
}

func runUpdate(cmd *cobra.Command, ref string, args *updateArgs) error {
	paths := []string{}
	if cmd.Flags().Changed("supported-workloads") {
		paths = append(paths, "spec.supported_workloads")
	}
	if cmd.Flags().Changed("label") || args.clearLabels {
		paths = append(paths, "metadata.labels")
	}
	if len(paths) == 0 {
		return errors.New("at least one update flag must be specified")
	}
	workloads := args.supportedWorkload
	if cmd.Flags().Changed("supported-workloads") {
		var err error
		workloads, err = parseSupportedWorkloads(workloads)
		if err != nil {
			return err
		}
	}
	labels, err := metautil.ParseLabels(args.labels)
	if err != nil {
		return err
	}
	if args.clearLabels && len(args.labels) > 0 {
		return errors.New("--clear-labels cannot be combined with --label")
	}

	ctx := cmd.Context()
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return errors.New("there is no configuration, run the 'login' command")
	}
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()
	client := privatev1.NewWorkloadClustersClient(conn)
	object, err := findWorkloadCluster(ctx, client, ref)
	if err != nil {
		return err
	}
	updated := proto.Clone(object).(*privatev1.WorkloadCluster)
	if !updated.HasMetadata() {
		updated.SetMetadata(&privatev1.Metadata{})
	}
	if !updated.HasSpec() {
		updated.SetSpec(&privatev1.WorkloadClusterSpec{})
	}
	if cmd.Flags().Changed("supported-workloads") {
		updated.GetSpec().SetSupportedWorkloads(workloads)
	}
	if cmd.Flags().Changed("label") || args.clearLabels {
		updated.GetMetadata().SetLabels(labels)
	}
	_, err = client.Update(ctx, privatev1.WorkloadClustersUpdateRequest_builder{
		Object:     updated,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to update workload cluster: %w", err)
	}
	terminal.ConsoleFromContext(ctx).Infof(ctx, "Updated workload cluster '%s'.\n", object.GetId())
	return nil
}

func findWorkloadCluster(ctx context.Context, client privatev1.WorkloadClustersClient, ref string) (*privatev1.WorkloadCluster, error) {
	return lookup.Find(ref, "workload cluster", func(filter string, limit int32) ([]*privatev1.WorkloadCluster, error) {
		response, err := client.List(ctx, privatev1.WorkloadClustersListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to find workload cluster: %w", err)
		}
		return response.GetItems(), nil
	})
}

func parseSupportedWorkloads(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("at least one supported workload is required")
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.ToLower(strings.TrimSpace(item))
			if item == "" {
				continue
			}
			if item != "maas" {
				return nil, fmt.Errorf("unsupported workload %q; only 'maas' is supported", item)
			}
			if !seen[item] {
				seen[item] = true
				result = append(result, item)
			}
		}
	}
	if len(result) == 0 {
		return nil, errors.New("at least one supported workload is required")
	}
	return result, nil
}

func renderWorkloadClusterList(w io.Writer, objects []*privatev1.WorkloadCluster, hubNames map[string]string) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tNAME\tHUB\tMANAGED CLUSTER\tAVAILABLE\tHUB REACHABLE")
	for _, object := range objects {
		hubID := object.GetSpec().GetSourceHubId()
		hubName := hubNames[hubID]
		if hubName == "" {
			hubName = hubID
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
			object.GetId(), object.GetMetadata().GetName(), hubName,
			object.GetSpec().GetManagedClusterName(), conditionValue(object, "Available"), conditionValue(object, "HubReachable"))
	}
	_ = writer.Flush()
}

func renderWorkloadCluster(w io.Writer, object *privatev1.WorkloadCluster) error {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(writer, "ID:\t%s\n", object.GetId())
	fmt.Fprintf(writer, "Name:\t%s\n", object.GetMetadata().GetName())
	fmt.Fprintf(writer, "Source Hub ID:\t%s\n", object.GetSpec().GetSourceHubId())
	fmt.Fprintf(writer, "ManagedCluster:\t%s\n", object.GetSpec().GetManagedClusterName())
	fmt.Fprintf(writer, "Supported workloads:\t%s\n", strings.Join(object.GetSpec().GetSupportedWorkloads(), ", "))
	fmt.Fprintf(writer, "ManagedCluster UID:\t%s\n", object.GetStatus().GetManagedClusterUid())
	fmt.Fprintln(writer, "Conditions:")
	conditions := append([]*privatev1.WorkloadClusterCondition(nil), object.GetStatus().GetConditions()...)
	sort.Slice(conditions, func(i, j int) bool { return conditions[i].GetType() < conditions[j].GetType() })
	for _, condition := range conditions {
		fmt.Fprintf(writer, "  %s:\t%s (%s) %s\n", condition.GetType(), condition.GetStatus(), condition.GetReason(), condition.GetMessage())
	}
	return writer.Flush()
}

func conditionValue(object *privatev1.WorkloadCluster, kind string) string {
	for _, condition := range object.GetStatus().GetConditions() {
		if condition.GetType() == kind {
			if condition.GetReason() == "" {
				return condition.GetStatus()
			}
			return condition.GetStatus() + " (" + condition.GetReason() + ")"
		}
	}
	return "Unknown"
}
