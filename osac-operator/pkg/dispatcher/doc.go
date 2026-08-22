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

// Package dispatcher resolves NetworkClass names to fabric/k8s managers via a
// gRPC NetworkClassesClient, validates them against registered ConfigMaps, and
// provides a dispatch table that maps resource types to the manager(s)
// responsible for their provisioning operations.
package dispatcher
