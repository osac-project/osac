/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package connect

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrConnectionLost is a sentinel indicating the session was established
// but the connection dropped. This resets the retry counter.
var ErrConnectionLost = errors.New("connection lost")

// ErrLocalIOFailed indicates the local Reader/Writer side of a Proxy
// failed (e.g., a TCP viewer connection broke), as opposed to the gRPC
// stream side. Callers can use errors.Is to decide whether the local
// endpoint needs to be replaced before retrying.
var ErrLocalIOFailed = errors.New("local I/O failed")

// IsPermanentError returns true for errors that should not be retried.
func IsPermanentError(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.PermissionDenied, codes.NotFound, codes.Unauthenticated,
		codes.FailedPrecondition, codes.InvalidArgument, codes.Unimplemented:
		return true
	}
	return false
}

// UserFacingError extracts a clean message from gRPC status errors.
func UserFacingError(err error) string {
	if st, ok := status.FromError(err); ok {
		return st.Message()
	}
	return err.Error()
}
