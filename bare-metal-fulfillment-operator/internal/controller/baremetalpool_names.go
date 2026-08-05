/*
Copyright 2026.

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

package controller

import (
	"fmt"
	"time"

	"github.com/osac-project/osac-operator/pkg/provisioning"

	"github.com/osac-project/bare-metal-fulfillment-operator/internal/shared"
)

const (
	// DefaultHostReadyPollIntervalDuration is the default interval for polling hosts to be ready
	DefaultHostReadyPollIntervalDuration = 10 * time.Second

	// DefaultHostDeletionPollInterval is the default interval for polling host deletion status
	DefaultHostDeletionPollIntervalDuration = 5 * time.Second

	// DefaultProvisionJobPollInterval is the default interval for polling provision job status
	DefaultAAPStatusPollIntervalDuration = provisioning.DefaultStatusPollInterval

	// DefaultMaxJobHistory is the default maximum number of job history entries to retain
	DefaultMaxJobHistory = provisioning.DefaultMaxJobHistory
)

var (
	// bareMetalPoolFinalizer is the finalizer added to BareMetalPool resources
	BareMetalPoolFinalizer string = fmt.Sprintf("%s/bare-metal-pool", shared.OsacPrefix)

	// bareMetalPoolLabelKey is the label key used to identify BareMetalPool ownership
	BareMetalPoolLabelKey string = fmt.Sprintf("%s/bare-metal-pool-id", shared.OsacPrefix)

	// hostTypeLabelKey is the label key used to identify the host type
	HostTypeLabelKey string = fmt.Sprintf("%s/host-type", shared.OsacPrefix)

	// BareMetalPoolTemplateIDAnnotationKey is the annotation key used to store the BareMetalPool template ID
	BareMetalPoolTemplateIDAnnotationKey string = fmt.Sprintf("%s/templateID", shared.OsacPrefix)
)
