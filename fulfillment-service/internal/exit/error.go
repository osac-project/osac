/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package exit

import "fmt"

// Error is an error type that contains a process exit code. This is itended for situations where you want to call
// os.Exit only in one place, but also want some deeply nested functions to decide what should be the exit code.
type Error int

// Error is the implementation of the error interface.
func (e Error) Error() string {
	return fmt.Sprintf("%d", e)
}

// Code returns the exit code.
func (e Error) Code() int {
	return int(e)
}
