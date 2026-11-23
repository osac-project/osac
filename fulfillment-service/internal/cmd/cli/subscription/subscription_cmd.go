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

package subscription

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
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Cmd creates the Cloud Provider Admin subscription-management command group.
func Cmd() *cobra.Command {
	result := &cobra.Command{Use: "subscription", Aliases: []string{"subscriptions"}, Short: "Manage MaaS subscriptions", Long: "Manage MaaS subscriptions that grant model access to Tenant users and groups.", DisableFlagsInUseLine: true}
	result.AddCommand(listCmd(), getCmd(), createCmd(), updateCmd(), deleteCmd())
	return result
}

func listCmd() *cobra.Command {
	var offset, limit int32
	var filter, order string
	result := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List MaaS subscriptions", DisableFlagsInUseLine: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, ctx, done, err := connect(cmd)
			if err != nil {
				return err
			}
			defer done()
			request := &publicv1.SubscriptionsListRequest{Offset: proto.Int32(offset)}
			if cmd.Flags().Changed("limit") {
				request.Limit = proto.Int32(limit)
			}
			if filter != "" {
				request.Filter = proto.String(filter)
			}
			if order != "" {
				request.Order = proto.String(order)
			}
			response, err := client.List(ctx, request)
			if err != nil {
				return fmt.Errorf("failed to list subscriptions: %w", err)
			}
			return renderList(cmd.OutOrStdout(), response.GetItems())
		},
	}
	result.Flags().Int32Var(&offset, "offset", 0, "_NUMBER_ - Number of subscriptions to skip.")
	result.Flags().Int32Var(&limit, "limit", 0, "_NUMBER_ - Maximum number of subscriptions to list.")
	result.Flags().StringVar(&filter, "filter", "", "_CEL_ - CEL expression used to filter subscriptions.")
	result.Flags().StringVar(&order, "order", "", "_FIELD_ - Sort by id, -id, priority, -priority, metadata.name, or -metadata.name.")
	return result
}

func getCmd() *cobra.Command {
	return &cobra.Command{Use: "get ID", Short: "Get a MaaS subscription", DisableFlagsInUseLine: true, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, done, err := connect(cmd)
			if err != nil {
				return err
			}
			defer done()
			response, err := client.Get(ctx, &publicv1.SubscriptionsGetRequest{Id: args[0]})
			if err != nil {
				return fmt.Errorf("failed to get subscription %q: %w", args[0], err)
			}
			return writeJSON(cmd.OutOrStdout(), response)
		},
	}
}

func createCmd() *cobra.Command {
	var filename string
	result := &cobra.Command{Use: "create -f FILE", Short: "Create a MaaS subscription from a JSON object", DisableFlagsInUseLine: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if filename == "" {
				return errors.New("--filename is required")
			}
			object := &publicv1.Subscription{}
			if err := readSubscription(filename, object); err != nil {
				return err
			}
			client, ctx, done, err := connect(cmd)
			if err != nil {
				return err
			}
			defer done()
			response, err := client.Create(ctx, &publicv1.SubscriptionsCreateRequest{Object: object})
			if err != nil {
				return fmt.Errorf("failed to create subscription: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), response)
		},
	}
	result.Flags().StringVarP(&filename, "filename", "f", "", "_FILE_ - JSON subscription object to create.")
	return result
}

func updateCmd() *cobra.Command {
	var filename, mask string
	var lock bool
	result := &cobra.Command{Use: "update -f FILE --update-mask PATH[,PATH...]", Short: "Update a MaaS subscription", DisableFlagsInUseLine: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if filename == "" {
				return errors.New("--filename is required")
			}
			if strings.TrimSpace(mask) == "" {
				return errors.New("--update-mask is required")
			}
			object := &publicv1.Subscription{}
			if err := readSubscription(filename, object); err != nil {
				return err
			}
			paths := strings.Split(mask, ",")
			for i := range paths {
				paths[i] = strings.TrimSpace(paths[i])
			}
			fieldMask, err := fieldmaskpb.New(object, paths...)
			if err != nil {
				return fmt.Errorf("invalid update mask: %w", err)
			}
			client, ctx, done, err := connect(cmd)
			if err != nil {
				return err
			}
			defer done()
			response, err := client.Update(ctx, &publicv1.SubscriptionsUpdateRequest{Object: object, UpdateMask: fieldMask, Lock: lock})
			if err != nil {
				return fmt.Errorf("failed to update subscription %q: %w", object.GetId(), err)
			}
			return writeJSON(cmd.OutOrStdout(), response.GetObject())
		},
	}
	result.Flags().StringVarP(&filename, "filename", "f", "", "_FILE_ - JSON subscription object to update.")
	result.Flags().StringVar(&mask, "update-mask", "", "_PATHS_ - Comma-separated list of supported update paths.")
	result.Flags().BoolVar(&lock, "lock", true, "Require metadata.version in the file to match the current version.")
	return result
}

func deleteCmd() *cobra.Command {
	var version int32
	result := &cobra.Command{Use: "delete ID", Short: "Delete a MaaS subscription", DisableFlagsInUseLine: true, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, done, err := connect(cmd)
			if err != nil {
				return err
			}
			defer done()
			request := &publicv1.SubscriptionsDeleteRequest{Id: args[0]}
			if cmd.Flags().Changed("resource-version") {
				request.ResourceVersion = version
			}
			if _, err := client.Delete(ctx, request); err != nil {
				return fmt.Errorf("failed to delete subscription %q: %w", args[0], err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted subscription %q; MaaS access revocation is asynchronous (target: 5 minutes).\n", args[0])
			return err
		},
	}
	result.Flags().Int32Var(&version, "resource-version", 0, "_VERSION_ - Expected metadata.version; reject a stale delete.")
	return result
}

func connect(cmd *cobra.Command) (publicv1.SubscriptionsClient, context.Context, func(), error) {
	ctx := cmd.Context()
	settings := config.SettingsFromContext(ctx)
	if !settings.Armed() {
		return nil, nil, nil, errors.New("there is no configuration, run the 'login' command")
	}
	connection, err := settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	return publicv1.NewSubscriptionsClient(connection), ctx, func() { _ = connection.Close() }, nil
}

func readSubscription(filename string, object *publicv1.Subscription) error {
	var data []byte
	var err error
	if filename == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(filename)
	} // #nosec G304 -- CLI caller explicitly selects this local file
	if err != nil {
		return fmt.Errorf("failed to read subscription file %q: %w", filename, err)
	}
	if err := (protojson.UnmarshalOptions{}).Unmarshal(data, object); err != nil {
		return fmt.Errorf("failed to decode subscription JSON: %w", err)
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

func renderList(w io.Writer, items []*publicv1.Subscription) error {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "ID\tTENANT\tPRIORITY\tSTATUS\tMODELS"); err != nil {
		return err
	}
	for _, item := range items {
		var names []string
		for _, ref := range item.GetSpec().GetModelRefs() {
			names = append(names, ref.GetName())
		}
		phase := item.GetStatus().GetPhase()
		if phase == "" {
			phase = "-"
		}
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%d\t%s\t%s\n", item.GetId(), item.GetSpec().GetOrganization().GetId(), item.GetSpec().GetPriority(), phase, strings.Join(names, ",")); err != nil {
			return err
		}
	}
	return writer.Flush()
}
