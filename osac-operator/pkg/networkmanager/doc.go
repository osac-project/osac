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

// Package networkmanager provides discovery and validation of network manager
// registrations. Managers are registered as labeled ConfigMaps in the operator
// namespace. Fabric managers (osac.openshift.io/network/fabric-manager) handle
// physical networking, while k8s managers (osac.openshift.io/network/k8s-manager)
// bridge the Kubernetes OVN overlay to the physical fabric.
package networkmanager
