/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package password

import (
	"crypto/rand"
	"math/big"
)

// Charset contains only URL/form-safe characters. The characters %, #, and $
// are intentionally excluded because they are reserved in
// application/x-www-form-urlencoded encoding and can be mangled by the
// Keycloak login form.
const Charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@"

// Length is the number of characters in a generated password.
const Length = 24

// Generate creates a cryptographically random password of Length characters
// drawn from Charset.
func Generate() (string, error) {
	b := make([]byte, Length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(Charset))))
		if err != nil {
			return "", err
		}
		b[i] = Charset[n.Int64()]
	}
	return string(b), nil
}
