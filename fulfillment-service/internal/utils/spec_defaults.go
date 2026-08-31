/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package utils

import (
	"fmt"
	"sort"
	"strings"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
)

// ApplySpecDefaults applies default values from a template's spec_defaults to a compute instance spec.
//
// User-provided values have precedence over defaults, and should never be overridden by defaults.
func ApplySpecDefaults(spec *privatev1.ComputeInstanceSpec, defaults *privatev1.ComputeInstanceTemplateSpecDefaults) {
	if spec == nil || defaults == nil {
		return
	}

	// Apply instance_type default.
	if spec.GetInstanceType() == nil && defaults.HasInstanceType() && defaults.GetInstanceType() != nil {
		spec.SetInstanceType(defaults.GetInstanceType())
	}

	if !spec.HasRunStrategy() && defaults.HasRunStrategy() {
		spec.SetRunStrategy(defaults.GetRunStrategy())
	}
	if !spec.HasDiskImage() && defaults.HasDiskImage() {
		spec.SetDiskImage(defaults.GetDiskImage())
	}
	mergeBootDiskDefaults(spec, defaults)
}

func mergeBootDiskDefaults(spec *privatev1.ComputeInstanceSpec, defaults *privatev1.ComputeInstanceTemplateSpecDefaults) {
	if !defaults.HasBootDisk() {
		return
	}
	if !spec.HasBootDisk() {
		spec.SetBootDisk(proto.Clone(defaults.GetBootDisk()).(*privatev1.ComputeInstanceDisk))
		return
	}
	disk := spec.GetBootDisk()
	defDisk := defaults.GetBootDisk()
	if !disk.HasSizeGib() && defDisk.HasSizeGib() {
		disk.SetSizeGib(defDisk.GetSizeGib())
	}
	if !disk.HasStorageTier() && defDisk.HasStorageTier() {
		disk.SetStorageTier(defDisk.GetStorageTier())
	}
}

// ValidateRequiredSpecFields checks that all fields required by the Kubernetes ComputeInstance CRD
// are present in the spec. instance_type is always required (TMPL-03, COMP-06).
func ValidateRequiredSpecFields(spec *privatev1.ComputeInstanceSpec) error {
	if spec == nil {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"compute instance spec is required",
		)
	}
	var missing []string
	// instance_type is always required.
	if spec.GetInstanceType() == nil {
		missing = append(missing, "instance_type")
	}
	if !spec.HasDiskImage() {
		missing = append(missing, "disk_image")
	}
	if !spec.HasBootDisk() {
		missing = append(missing, "boot_disk")
	}
	if !spec.HasRunStrategy() {
		missing = append(missing, "run_strategy")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"the following required spec fields are missing: %s",
			strings.Join(missing, ", "),
		)
	}

	if err := validateRunStrategy(spec.GetRunStrategy()); err != nil {
		return err
	}
	if err := validateDisk(spec.GetBootDisk()); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "boot_disk.%s", err)
	}
	for i, disk := range spec.GetAdditionalDisks() {
		if err := validateDisk(disk); err != nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "additional_disks[%d].%s", i, err)
		}
	}

	return nil
}

func validateRunStrategy(value privatev1.ComputeInstanceRunStrategy) error {
	switch value {
	case privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS,
		privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED:
		return nil
	default:
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"invalid run_strategy %q: must be one of COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS, COMPUTE_INSTANCE_RUN_STRATEGY_HALTED",
			value,
		)
	}
}

func validateDisk(disk *privatev1.ComputeInstanceDisk) error {
	if disk == nil {
		return nil
	}
	if !disk.HasSizeGib() {
		return fmt.Errorf("size_gib is required")
	}
	if disk.GetSizeGib() <= 0 {
		return fmt.Errorf("size_gib must be greater than 0")
	}
	return nil
}
