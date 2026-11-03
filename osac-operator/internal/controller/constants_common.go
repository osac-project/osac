/*
Copyright 2025.

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

const (
	// ManagementStateManual indicates that the resource should not be managed by the controller
	ManagementStateManual = "manual"

	// ManagementStateUnmanaged indicates that the resource should be ignored by the controller
	ManagementStateUnmanaged = "unmanaged"

	// osacAppName is the name of the osac application
	osacAppName string = "osac-operator"

	// osacPrefix is the prefix used to identify osac resources
	osacPrefix string = "osac.openshift.io"

	labelValueTrue = "true"

	// fieldName and fieldAddress are keys of unstructured objects and job inputs.
	fieldName    = "name"
	fieldAddress = "address"
)
