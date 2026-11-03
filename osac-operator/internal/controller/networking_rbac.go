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

// RBAC of the networking controllers (the resource controllers on the network
// task engine and the feedback controllers).
//
// +kubebuilder:rbac:groups=osac.openshift.io,resources=virtualnetworks;subnets;securitygroups;externalippools;externalips;externalipattachments;natgateways;fabricdomains;networkattachments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=virtualnetworks/status;subnets/status;securitygroups/status;externalippools/status;externalips/status;externalipattachments/status;natgateways/status;fabricdomains/status;networkattachments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=virtualnetworks/finalizers;subnets/finalizers;securitygroups/finalizers;externalippools/finalizers;externalips/finalizers;externalipattachments/finalizers;natgateways/finalizers;fabricdomains/finalizers;networkattachments/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=networkclasses,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=networkclasses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=networkclasses/finalizers,verbs=update
//
// The networking resource controllers read the target workloads of
// NetworkAttachments and ExternalIPAttachments, and the namespace of an
// accepted vm_attachment result:
//
// +kubebuilder:rbac:groups=osac.openshift.io,resources=computeinstances;clusterorders;baremetalinstances,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get
