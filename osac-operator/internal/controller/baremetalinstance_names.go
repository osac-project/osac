/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"fmt"
)

const (
	DefaultBareMetalInstanceNamespace string = "osac-baremetalinstance"

	// bareMetalInstanceObjectNamePrefix is the prefix the fulfillment service
	// applies to every BareMetalInstance hub CR name ("bmi-<id>"). It mirrors
	// objectPrefix in
	// fulfillment-service/internal/controllers/baremetalinstance and lets the
	// feedback controller recover the fulfillment BMI identifier from a
	// reconcile request after the CR has been physically deleted, when the
	// identifier label is no longer readable.
	bareMetalInstanceObjectNamePrefix string = "bmi-"
)

var (
	osacBareMetalInstanceIDLabel           string = fmt.Sprintf("%s/baremetalinstance-uuid", osacPrefix)
	osacBareMetalInstanceFeedbackFinalizer string = fmt.Sprintf("%s/baremetalinstance-feedback", osacPrefix)
)
