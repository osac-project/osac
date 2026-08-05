/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package files

// ServiceAccountNamespace is the absolute path of the file containing the name of the namespace where the pod is
// running.
const ServiceAccountNamespace = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// ServiceAccountToken is the absolute path of the file containing the bearer token for authenticating to the Kubernetes
// API server.
const ServiceAccountToken = "/var/run/secrets/kubernetes.io/serviceaccount/token"
