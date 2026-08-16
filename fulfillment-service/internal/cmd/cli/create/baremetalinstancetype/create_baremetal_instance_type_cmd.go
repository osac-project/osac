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
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

// Cmd creates the command to create a bare metal instance type.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "baremetalinstancetype",
		Aliases:               []string{string(proto.MessageName((*privatev1.BareMetalInstanceType)(nil)))},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVarP(
		&runner.name,
		"name",
		"n",
		"",
		nameFlagHelp,
	)
	flags.StringVar(
		&runner.description,
		"description",
		"",
		descriptionFlagHelp,
	)
	// CPU flags
	flags.Int32Var(
		&runner.cpuCores,
		"cpu-cores",
		0,
		cpuCoresFlagHelp,
	)
	flags.StringVar(
		&runner.cpuArchitecture,
		"cpu-architecture",
		"",
		cpuArchitectureFlagHelp,
	)
	flags.StringVar(
		&runner.cpuModel,
		"cpu-model",
		"",
		cpuModelFlagHelp,
	)
	flags.Int32Var(
		&runner.cpuThreadsPerCore,
		"cpu-threads-per-core",
		0,
		cpuThreadsPerCoreFlagHelp,
	)
	// Memory flags
	flags.Int64Var(
		&runner.memoryTotalGb,
		"memory-total-gb",
		0,
		memoryTotalGbFlagHelp,
	)
	flags.StringVar(
		&runner.memoryType,
		"memory-type",
		"",
		memoryTypeFlagHelp,
	)
	// Disk flags (repeatable)
	flags.StringSliceVar(
		&runner.diskSpecs,
		"disk",
		[]string{},
		diskFlagHelp,
	)
	// Accelerator flags (repeatable)
	flags.StringSliceVar(
		&runner.acceleratorSpecs,
		"accelerator",
		[]string{},
		acceleratorFlagHelp,
	)
	// Network port flags (repeatable)
	flags.StringSliceVar(
		&runner.networkPortSpecs,
		"network-port",
		[]string{},
		networkPortFlagHelp,
	)
	// Capability flags (repeatable)
	flags.StringSliceVar(
		&runner.capabilitySpecs,
		"capability",
		[]string{},
		capabilityFlagHelp,
	)
	// Host label flags (repeatable, required)
	flags.StringSliceVar(
		&runner.hostLabelSpecs,
		"host-label",
		[]string{},
		hostLabelFlagHelp,
	)
	return result
}

type runnerContext struct {
	console           *terminal.Console
	name              string
	description       string
	cpuCores          int32
	cpuArchitecture   string
	cpuModel          string
	cpuThreadsPerCore int32
	memoryTotalGb     int64
	memoryType        string
	diskSpecs         []string
	acceleratorSpecs  []string
	networkPortSpecs  []string
	capabilitySpecs   []string
	hostLabelSpecs    []string
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	// Get the context:
	ctx := cmd.Context()

	// Get the console:
	c.console = terminal.ConsoleFromContext(ctx)

	// Get the configuration:
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Check the required parameters:
	if c.name == "" {
		return fmt.Errorf("name is required")
	}
	if c.cpuCores <= 0 {
		return fmt.Errorf("cpu-cores must be greater than zero")
	}
	if c.cpuThreadsPerCore < 0 {
		return fmt.Errorf("cpu-threads-per-core must not be negative")
	}
	if c.cpuArchitecture == "" {
		return fmt.Errorf("cpu-architecture is required")
	}
	if c.memoryTotalGb <= 0 {
		return fmt.Errorf("memory-total-gb must be greater than zero")
	}
	if len(c.hostLabelSpecs) == 0 {
		return fmt.Errorf("at least one host label is required")
	}

	// Create the gRPC connection from the configuration:
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	// Create the client:
	client := privatev1.NewBareMetalInstanceTypesClient(conn)

	// Build CPU specification:
	cpuSpec := privatev1.BareMetalCPUSpec_builder{
		Cores:        c.cpuCores,
		Architecture: c.cpuArchitecture,
	}
	if c.cpuModel != "" {
		cpuSpec.Model = c.cpuModel
	}
	if c.cpuThreadsPerCore > 0 {
		cpuSpec.ThreadsPerCore = c.cpuThreadsPerCore
	}

	// Build memory specification:
	memorySpec := privatev1.BareMetalMemorySpec_builder{
		TotalGb: c.memoryTotalGb,
	}
	if c.memoryType != "" {
		memorySpec.Type = c.memoryType
	}

	// Parse disk specifications:
	var diskSpecs []*privatev1.BareMetalDiskSpec
	for _, diskSpec := range c.diskSpecs {
		disk, err := parseDiskFlag(diskSpec)
		if err != nil {
			return fmt.Errorf("invalid disk specification '%s': %w", diskSpec, err)
		}
		diskSpecs = append(diskSpecs, disk)
	}

	// Parse accelerator specifications:
	var acceleratorSpecs []*privatev1.BareMetalAcceleratorSpec
	for _, acceleratorSpec := range c.acceleratorSpecs {
		accelerator, err := parseAcceleratorFlag(acceleratorSpec)
		if err != nil {
			return fmt.Errorf("invalid accelerator specification '%s': %w", acceleratorSpec, err)
		}
		acceleratorSpecs = append(acceleratorSpecs, accelerator)
	}

	// Parse network port specifications:
	var networkPortSpecs []*privatev1.BareMetalNetworkPortSpec
	for _, networkPortSpec := range c.networkPortSpecs {
		networkPort, err := parseNetworkPortFlag(networkPortSpec)
		if err != nil {
			return fmt.Errorf("invalid network port specification '%s': %w", networkPortSpec, err)
		}
		networkPortSpecs = append(networkPortSpecs, networkPort)
	}

	// Parse capability specifications:
	capabilities := make(map[string]string)
	for _, capabilitySpec := range c.capabilitySpecs {
		key, value, err := parseCapabilityFlag(capabilitySpec)
		if err != nil {
			return fmt.Errorf("invalid capability specification '%s': %w", capabilitySpec, err)
		}
		capabilities[key] = value
	}

	// Parse host label specifications:
	hostLabels := make(map[string]string)
	for _, hostLabelSpec := range c.hostLabelSpecs {
		key, value, err := parseHostLabelFlag(hostLabelSpec)
		if err != nil {
			return fmt.Errorf("invalid host label specification '%s': %w", hostLabelSpec, err)
		}
		hostLabels[key] = value
	}

	// Build hardware specification:
	hardwareSpec := privatev1.BareMetalHardwareSpec_builder{
		Cpu:          cpuSpec.Build(),
		Memory:       memorySpec.Build(),
		Disks:        diskSpecs,
		Accelerators: acceleratorSpecs,
		NetworkPorts: networkPortSpecs,
		Capabilities: capabilities,
	}.Build()

	// Build host label selector:
	hostLabelSelector := privatev1.BareMetalLabelSelector_builder{
		MatchLabels: hostLabels,
	}.Build()

	// Prepare the bare metal instance type:
	bareMetalInstanceType := privatev1.BareMetalInstanceType_builder{
		Id: c.name,
		Metadata: privatev1.Metadata_builder{
			Name: c.name,
		}.Build(),
		Spec: privatev1.BareMetalInstanceTypeSpec_builder{
			Hardware:          hardwareSpec,
			HostLabelSelector: hostLabelSelector,
			Description:       c.description,
		}.Build(),
	}.Build()

	// Create the bare metal instance type:
	response, err := client.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
		Object: bareMetalInstanceType,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create bare metal instance type: %w", err)
	}

	// Display the result:
	c.console.Infof(ctx, "Created bare metal instance type '%s'.\n", response.GetObject().GetId())

	return nil
}

const shortHelp = `Create a bare metal instance type`

const longHelp = `
Create a bare metal instance type.

A bare metal instance type defines a pre-configured hardware bundle (CPU, memory, and other
specifications) that can be referenced by name when provisioning bare metal instances.
Bare metal instance types are managed by Cloud Provider Admins.

To create a bare metal instance type:

{{ bt 3 }}shell
{{ binary }} create baremetalinstancetype \
  --name gpu-large \
  --description 'Large GPU node for ML workloads' \
  --cpu-cores 32 \
  --cpu-architecture x86_64 \
  --cpu-model 'Intel Xeon Gold 6338' \
  --cpu-threads-per-core 2 \
  --memory-total-gb 512 \
  --memory-type DDR4 \
  --disk SSD:1000:NVMe \
  --disk HDD:4000:SATA \
  --accelerator GPU:A100:NVIDIA:80 \
  --network-port data-0:fabric:Ethernet:100Gbps \
  --network-port mgmt-0:management:Ethernet:1Gbps \
  --capability secure-boot=enabled \
  --capability tpm-version=2.0 \
  --host-label rack=r1 \
  --host-label zone=us-west-1a
{{ bt 3 }}

Required fields: name, cpu-cores, cpu-architecture, memory-total-gb, host-label
`

const nameFlagHelp = `
_NAME_ - Name of the bare metal instance type. Must be a unique, human-readable identifier
(e.g., {{ bt }}gpu-large{{ bt }}).
`

const descriptionFlagHelp = `
_DESCRIPTION_ - Human friendly description of the bare metal instance type.
`

const cpuCoresFlagHelp = `
_CORES_ - Number of CPU cores. Must be greater than zero.
`

const cpuArchitectureFlagHelp = `
_ARCHITECTURE_ - CPU architecture (e.g., {{ bt }}x86_64{{ bt }}, {{ bt }}aarch64{{ bt }}). Required.
`

const cpuModelFlagHelp = `
_MODEL_ - CPU model name (e.g., {{ bt }}Intel Xeon Gold 6338{{ bt }}). Optional.
`

const cpuThreadsPerCoreFlagHelp = `
_THREADS_ - Number of threads per CPU core. Optional.
`

const memoryTotalGbFlagHelp = `
_MEMORY_ - Total memory in gigabytes. Must be greater than zero.
`

const memoryTypeFlagHelp = `
_TYPE_ - Memory type (e.g., {{ bt }}DDR4{{ bt }}, {{ bt }}DDR5{{ bt }}). Optional.
`

const diskFlagHelp = `
_DISK_ - Disk specification in format {{ bt }}type:capacity_gb:interface{{ bt }}. Can be specified multiple times.
Example: {{ bt }}--disk SSD:1000:NVMe --disk HDD:4000:SATA{{ bt }}
`

const acceleratorFlagHelp = `
_ACCELERATOR_ - Accelerator specification in format {{ bt }}type:model[:vendor[:memory_gb]]{{ bt }}. Can be specified multiple times.
Example: {{ bt }}--accelerator GPU:A100:NVIDIA:80 --accelerator FPGA:F1:Intel{{ bt }}
`

const networkPortFlagHelp = `
_NETWORK_ - Network port specification in format {{ bt }}name:role:type:speed{{ bt }}. Can be specified multiple times.
Example: {{ bt }}--network-port data-0:fabric:Ethernet:100Gbps --network-port mgmt-0:management:Ethernet:1Gbps{{ bt }}
`

const capabilityFlagHelp = `
_CAPABILITY_ - Capability key-value pair in format {{ bt }}key=value{{ bt }}. Can be specified multiple times.
Example: {{ bt }}--capability secure-boot=enabled --capability tpm-version=2.0{{ bt }}
`

const hostLabelFlagHelp = `
_LABEL_ - Host label selector key-value pair in format {{ bt }}key=value{{ bt }}. Can be specified multiple times. At least one is required.
Example: {{ bt }}--host-label rack=r1 --host-label zone=us-west-1a{{ bt }}
`

// parseDiskFlag parses a disk specification in format "type:capacity_gb:interface"
func parseDiskFlag(value string) (*privatev1.BareMetalDiskSpec, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("disk specification must have format 'type:capacity_gb:interface'")
	}

	diskType := strings.TrimSpace(parts[0])
	if diskType == "" {
		return nil, fmt.Errorf("disk type cannot be empty")
	}

	capacityStr := strings.TrimSpace(parts[1])
	capacity, err := strconv.ParseInt(capacityStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid disk capacity '%s': %w", capacityStr, err)
	}
	if capacity <= 0 {
		return nil, fmt.Errorf("disk capacity must be greater than zero")
	}

	diskInterface := strings.TrimSpace(parts[2])
	if diskInterface == "" {
		return nil, fmt.Errorf("disk interface cannot be empty")
	}

	return privatev1.BareMetalDiskSpec_builder{
		Type:       diskType,
		CapacityGb: capacity,
		Interface:  diskInterface,
	}.Build(), nil
}

// parseAcceleratorFlag parses an accelerator specification in format "type:model[:vendor[:memory_gb]]"
func parseAcceleratorFlag(value string) (*privatev1.BareMetalAcceleratorSpec, error) {
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 4 {
		return nil, fmt.Errorf("accelerator specification must have format 'type:model[:vendor[:memory_gb]]'")
	}

	acceleratorType := strings.TrimSpace(parts[0])
	if acceleratorType == "" {
		return nil, fmt.Errorf("accelerator type cannot be empty")
	}

	model := strings.TrimSpace(parts[1])
	if model == "" {
		return nil, fmt.Errorf("accelerator model cannot be empty")
	}

	builder := privatev1.BareMetalAcceleratorSpec_builder{
		Type:  acceleratorType,
		Model: model,
	}

	// Parse optional vendor
	if len(parts) > 2 && strings.TrimSpace(parts[2]) != "" {
		vendor := strings.TrimSpace(parts[2])
		builder.Vendor = &vendor
	}

	// Parse optional memory
	if len(parts) > 3 && strings.TrimSpace(parts[3]) != "" {
		memoryStr := strings.TrimSpace(parts[3])
		memory, err := strconv.ParseInt(memoryStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid accelerator memory '%s': %w", memoryStr, err)
		}
		if memory <= 0 {
			return nil, fmt.Errorf("accelerator memory must be greater than zero")
		}
		memoryGb := int32(memory)
		builder.MemoryGb = &memoryGb
	}

	return builder.Build(), nil
}

// parseNetworkPortFlag parses a network port specification in format "name:role:type:speed"
func parseNetworkPortFlag(value string) (*privatev1.BareMetalNetworkPortSpec, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 4 {
		return nil, fmt.Errorf("network port specification must have format 'name:role:type:speed'")
	}

	name := strings.TrimSpace(parts[0])
	if name == "" {
		return nil, fmt.Errorf("network port name cannot be empty")
	}

	role := strings.TrimSpace(parts[1])
	if role == "" {
		return nil, fmt.Errorf("network port role cannot be empty")
	}

	portType := strings.TrimSpace(parts[2])
	if portType == "" {
		return nil, fmt.Errorf("network port type cannot be empty")
	}

	speed := strings.TrimSpace(parts[3])
	if speed == "" {
		return nil, fmt.Errorf("network port speed cannot be empty")
	}

	return privatev1.BareMetalNetworkPortSpec_builder{
		Name:  name,
		Role:  role,
		Type:  portType,
		Speed: speed,
	}.Build(), nil
}

// parseCapabilityFlag parses a capability specification in format "key=value"
func parseCapabilityFlag(value string) (string, string, error) {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("capability specification must have format 'key=value'")
	}

	key := strings.TrimSpace(parts[0])
	if key == "" {
		return "", "", fmt.Errorf("capability key cannot be empty")
	}

	// Value can be empty
	val := strings.TrimSpace(parts[1])

	return key, val, nil
}

// parseHostLabelFlag parses a host label specification in format "key=value"
func parseHostLabelFlag(value string) (string, string, error) {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("host label specification must have format 'key=value'")
	}

	key := strings.TrimSpace(parts[0])
	if key == "" {
		return "", "", fmt.Errorf("host label key cannot be empty")
	}

	val := strings.TrimSpace(parts[1])
	if val == "" {
		return "", "", fmt.Errorf("host label value cannot be empty")
	}

	return key, val, nil
}
