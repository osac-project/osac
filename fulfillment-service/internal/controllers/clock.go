/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package controllers

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// NowTruncated returns the current time truncated to whole seconds, for stamping a
// metering-tracked transition time. No source clock in the pipeline is finer than a
// second -- Kubernetes CR timestamps carry no fractional part -- and the storage
// trigger normalizes supplied times to seconds anyway: stamping at the source keeps
// the in-memory object, the API response, the stored row, and the event log on the
// same time instead of a response that disagrees with what was stored.
func NowTruncated() *timestamppb.Timestamp {
	return timestamppb.New(time.Now().Truncate(time.Second))
}
