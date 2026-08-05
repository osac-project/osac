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
	"slices"
	"sort"
	"strings"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

// validRunStrategies contains the run strategy values accepted by the Kubernetes ComputeInstance CRD.
// Note: these values are case-sensitive as currently no normalization is performed.
var validRunStrategies = []string{"Always", "Halted"}

// ApplySpecDefaults applies default values from a template's spec_defaults to a compute instance spec.
//
// User-provided values have precedence over defaults, and should never be overridden by defaults.
func ApplySpecDefaults(spec *privatev1.ComputeInstanceSpec, defaults *privatev1.ComputeInstanceTemplateSpecDefaults) {
	if spec == nil || defaults == nil {
		return
	}

	// Apply instance_type default.
	if spec.GetInstanceType() == "" && defaults.HasInstanceType() && defaults.GetInstanceType() != "" {
		spec.SetInstanceType(defaults.GetInstanceType())
	}

	if !spec.HasRunStrategy() && defaults.HasRunStrategy() {
		spec.SetRunStrategy(defaults.GetRunStrategy())
	}
	if !spec.HasIsWindows() && defaults.HasIsWindows() {
		spec.SetIsWindows(defaults.GetIsWindows())
	}
	mergeImageDefaults(spec, defaults)
	mergeBootDiskDefaults(spec, defaults)
}

func mergeImageDefaults(spec *privatev1.ComputeInstanceSpec, defaults *privatev1.ComputeInstanceTemplateSpecDefaults) {
	if !defaults.HasImage() {
		return
	}
	if !spec.HasImage() {
		spec.SetImage(proto.Clone(defaults.GetImage()).(*privatev1.ComputeInstanceImage))
		return
	}
	img := spec.GetImage()
	defImg := defaults.GetImage()
	if img.GetSourceType() == "" && defImg.GetSourceType() != "" {
		img.SetSourceType(defImg.GetSourceType())
	}
	if img.GetSourceRef() == "" && defImg.GetSourceRef() != "" {
		img.SetSourceRef(defImg.GetSourceRef())
	}
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
	if disk.GetSizeGib() <= 0 && defDisk.GetSizeGib() > 0 {
		disk.SetSizeGib(defDisk.GetSizeGib())
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
	if spec.GetInstanceType() == "" {
		missing = append(missing, "instance_type")
	}
	if !spec.HasImage() {
		missing = append(missing, "image")
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
	if err := validateImage(spec.GetImage()); err != nil {
		return err
	}
	if err := validateBootDisk(spec.GetBootDisk()); err != nil {
		return err
	}

	return nil
}

func validateRunStrategy(value string) error {
	if slices.Contains(validRunStrategies, value) {
		return nil
	}
	return grpcstatus.Errorf(
		grpccodes.InvalidArgument,
		"invalid run_strategy %q: must be one of %s",
		value, strings.Join(validRunStrategies, ", "),
	)
}

func validateImage(image *privatev1.ComputeInstanceImage) error {
	if image == nil {
		return nil
	}
	var missing []string
	if image.GetSourceType() == "" {
		missing = append(missing, "image.source_type")
	}
	if image.GetSourceRef() == "" {
		missing = append(missing, "image.source_ref")
	}
	if len(missing) > 0 {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"the following required image fields are missing: %s",
			strings.Join(missing, ", "),
		)
	}
	return nil
}

func validateBootDisk(disk *privatev1.ComputeInstanceDisk) error {
	if disk == nil {
		return nil
	}
	if disk.GetSizeGib() <= 0 {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"boot_disk.size_gib must be greater than 0",
		)
	}
	return nil
}
