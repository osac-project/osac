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
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

// Cmd creates the command to describe a bare metal instance type.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "baremetalinstancetype [FLAG...] ID|NAME",
		Aliases:               []string{"baremetalinstancetypes"},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(1),
		RunE:                  runner.run,
	}
	return result
}

type runnerContext struct {
	console *terminal.Console
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ref := args[0]

	// Get the context:
	ctx := cmd.Context()

	// Get the console:
	c.console = terminal.ConsoleFromContext(ctx)

	// Get the configuration:
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Create the gRPC connection from the configuration:
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	// Create the client:
	client := publicv1.NewBareMetalInstanceTypesClient(conn)

	// Find the bare metal instance type:
	matched, err := lookup.Find(ref, "bare metal instance type", func(filter string, limit int32) ([]*publicv1.BareMetalInstanceType, error) {
		resp, err := client.List(ctx, publicv1.BareMetalInstanceTypesListRequest_builder{
			Filter: proto.String(filter),
			Limit:  proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, fmt.Errorf("failed to describe bare metal instance type: %w", err)
		}
		return resp.GetItems(), nil
	})
	if err != nil {
		return err
	}

	// Display the result:
	renderBareMetalInstanceType(c.console, matched)

	return nil
}

func renderBareMetalInstanceType(w io.Writer, bmiType *publicv1.BareMetalInstanceType) {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Base fields (always shown):
	name := bmiType.GetMetadata().GetName()
	if name == "" {
		name = "-"
	}
	fmt.Fprintf(writer, "Name:\t%s\n", name)
	fmt.Fprintf(writer, "ID:\t%s\n", bmiType.GetId())

	spec := bmiType.GetSpec()
	if spec != nil {
		// Description (only when non-empty):
		if desc := spec.GetDescription(); desc != "" {
			fmt.Fprintf(writer, "Description:\t%s\n", desc)
		}

		// Hardware specification section:
		if hardware := spec.GetHardware(); hardware != nil {
			fmt.Fprintf(writer, "\nHardware:\n")

			// CPU information:
			if cpu := hardware.GetCpu(); cpu != nil {
				cpuInfo := formatCPU(cpu)
				fmt.Fprintf(writer, "  CPU:\t%s\n", cpuInfo)
			}

			// Memory information:
			if memory := hardware.GetMemory(); memory != nil {
				memInfo := formatMemory(memory)
				fmt.Fprintf(writer, "  Memory:\t%s\n", memInfo)
			}

			// Accelerators (GPUs, etc.):
			if accelerators := hardware.GetAccelerators(); len(accelerators) > 0 {
				accInfo := formatAccelerators(accelerators)
				fmt.Fprintf(writer, "  Accelerators:\t%s\n", accInfo)
			}

			// Disk/Storage information:
			if disks := hardware.GetDisks(); len(disks) > 0 {
				diskInfo := formatDisks(disks)
				fmt.Fprintf(writer, "  Disks:\t%s\n", diskInfo)
			}

			// Network ports:
			if ports := hardware.GetNetworkPorts(); len(ports) > 0 {
				portInfo := formatNetworkPorts(ports)
				fmt.Fprintf(writer, "  Network Ports:\t%s\n", portInfo)
			}

			// Capabilities (freeform metadata):
			if capabilities := hardware.GetCapabilities(); len(capabilities) > 0 {
				capInfo := formatCapabilities(capabilities)
				fmt.Fprintf(writer, "  Capabilities:\t%s\n", capInfo)
			}
		}
	}

	writer.Flush()
}

// formatCPU formats CPU specification into human-readable string
func formatCPU(cpu *publicv1.BareMetalCPUSpec) string {
	var parts []string

	cores := cpu.GetCores()
	if cores > 0 {
		parts = append(parts, fmt.Sprintf("%d cores", cores))
	}

	arch := cpu.GetArchitecture()
	if arch != "" {
		parts = append(parts, arch)
	}

	model := cpu.GetModel()
	if model != "" {
		parts = append(parts, model)
	}

	threadsPerCore := cpu.GetThreadsPerCore()
	if threadsPerCore > 0 {
		parts = append(parts, fmt.Sprintf("%d threads/core", threadsPerCore))
	}

	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// formatMemory formats memory specification into human-readable string
func formatMemory(memory *publicv1.BareMetalMemorySpec) string {
	var parts []string

	total := memory.GetTotalGb()
	if total > 0 {
		parts = append(parts, fmt.Sprintf("%d GB", total))
	}

	memType := memory.GetType()
	if memType != "" {
		parts = append(parts, memType)
	}

	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// formatAccelerators formats accelerator specs into human-readable string
func formatAccelerators(accelerators []*publicv1.BareMetalAcceleratorSpec) string {
	if len(accelerators) == 0 {
		return "-"
	}

	var accStrings []string
	for _, acc := range accelerators {
		var parts []string

		accType := acc.GetType()
		model := acc.GetModel()

		if accType != "" {
			parts = append(parts, accType)
		}

		if model != "" {
			parts = append(parts, model)
		}

		vendor := acc.GetVendor()
		if vendor != "" {
			parts = append(parts, vendor)
		}

		memory := acc.GetMemoryGb()
		if memory > 0 {
			parts = append(parts, fmt.Sprintf("%d GB VRAM", memory))
		}

		if len(parts) > 0 {
			accStrings = append(accStrings, strings.Join(parts, " "))
		}
	}

	if len(accStrings) == 0 {
		return "-"
	}
	return strings.Join(accStrings, "; ")
}

// formatDisks formats disk specs into human-readable string
func formatDisks(disks []*publicv1.BareMetalDiskSpec) string {
	if len(disks) == 0 {
		return "-"
	}

	var diskStrings []string
	for _, disk := range disks {
		var parts []string

		capacity := disk.GetCapacityGb()
		diskType := disk.GetType()

		if capacity > 0 && diskType != "" {
			parts = append(parts, fmt.Sprintf("%d GB %s", capacity, diskType))
		} else if capacity > 0 {
			parts = append(parts, fmt.Sprintf("%d GB", capacity))
		} else if diskType != "" {
			parts = append(parts, diskType)
		}

		iface := disk.GetInterface()
		if iface != "" {
			parts = append(parts, iface)
		}

		if len(parts) > 0 {
			diskStrings = append(diskStrings, strings.Join(parts, " "))
		}
	}

	if len(diskStrings) == 0 {
		return "-"
	}
	return strings.Join(diskStrings, "; ")
}

// formatNetworkPorts formats network port specs into human-readable string
func formatNetworkPorts(ports []*publicv1.BareMetalNetworkPortSpec) string {
	if len(ports) == 0 {
		return "-"
	}

	var portStrings []string
	for _, port := range ports {
		var parts []string

		name := port.GetName()
		role := port.GetRole()
		speed := port.GetSpeed()
		portType := port.GetType()

		if name != "" {
			parts = append(parts, name)
		}

		if role != "" {
			parts = append(parts, fmt.Sprintf("(%s)", role))
		}

		if speed != "" && portType != "" {
			parts = append(parts, fmt.Sprintf("%s %s", speed, portType))
		} else if speed != "" {
			parts = append(parts, speed)
		} else if portType != "" {
			parts = append(parts, portType)
		}

		if len(parts) > 0 {
			portStrings = append(portStrings, strings.Join(parts, " "))
		}
	}

	if len(portStrings) == 0 {
		return "-"
	}
	return strings.Join(portStrings, "; ")
}

// formatCapabilities formats capabilities map into human-readable string
func formatCapabilities(capabilities map[string]string) string {
	if len(capabilities) == 0 {
		return "-"
	}

	// Sort keys for consistent output
	var keys []string
	for key := range capabilities {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var capStrings []string
	for _, key := range keys {
		value := capabilities[key]
		if value != "" {
			capStrings = append(capStrings, fmt.Sprintf("%s=%s", key, value))
		} else {
			capStrings = append(capStrings, key)
		}
	}

	return strings.Join(capStrings, ", ")
}

const shortHelp = `Describe a bare metal instance type`

const longHelp = `
Describe a bare metal instance type.

Displays detailed hardware specifications for a bare metal instance type, including CPU, memory,
accelerators, storage, network ports, and additional capabilities. This provides complete hardware
metadata in human-readable format.

To describe a bare metal instance type by name:

{{ bt 3 }}shell
{{ binary }} describe baremetalinstancetype gpu-large
{{ bt 3 }}

To describe by ID:

{{ bt 3 }}shell
{{ binary }} describe baremetalinstancetype type-abc123
{{ bt 3 }}
`
