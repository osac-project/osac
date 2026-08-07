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

package v1alpha1

const (
	// VolumeNamespace is the default namespace where Volume CRs are created.
	VolumeNamespace = "osac-volume"

	// VolumeLabelName is the label key for the volume name.
	VolumeLabelName = "osac.openshift.io/volume"

	// VolumeLabelUUID is the label key for the fulfillment-service volume ID,
	// used by the feedback controller to map Volume CRs back to inventory records.
	VolumeLabelUUID = "osac.openshift.io/volume-uuid"

	// VolumeFinalizer is the finalizer managed by the Volume resource controller.
	VolumeFinalizer = "osac.openshift.io/volume-finalizer"

	// VolumeFeedbackFinalizer is the finalizer managed by the Volume feedback controller.
	VolumeFeedbackFinalizer = "osac.openshift.io/volume-feedback"

	// VolumeCleanupFinalizer is the finalizer added to ClusterOrder when volumes
	// are created, blocking cluster deletion until volumes are processed.
	VolumeCleanupFinalizer = "osac.openshift.io/volume-cleanup"
)
