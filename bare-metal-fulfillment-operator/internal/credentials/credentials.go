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

// Package credentials defines the BMC credentials shared by inventory
// backends.
package credentials

import "context"

// BMC contains the username and password used to connect to a BMC.
type BMC struct {
	Username []byte
	Password []byte
}

// Reader retrieves BMC credentials for a host from its configured source.
type Reader interface {
	GetCredentials(ctx context.Context, hostID string) (*BMC, error)
	Close() error
}
