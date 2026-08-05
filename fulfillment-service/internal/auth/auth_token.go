/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"time"
)

// Token is a struct that represents a token that can be used to authenticate requests.
type Token struct {
	// Access is the access token.
	Access string

	// Refresh is the refresh token.
	Refresh string

	// Expiry is the expiry time of the token.
	Expiry time.Time
}

// Valid returns true if the token is valid, false otherwise.
func (t *Token) Valid() bool {
	return !t.Expired()
}

// Expired returns true if the token is expired, false otherwise.
func (t *Token) Expired() bool {
	return !t.Expiry.IsZero() && time.Now().After(t.Expiry)
}
