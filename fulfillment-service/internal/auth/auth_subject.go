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
	"encoding/json"
	"fmt"

	"github.com/osac-project/fulfillment-service/internal/collections"
)

// Subject represents an entity, such as person or a service account.
type Subject struct {
	// User is the name of the user.
	User string

	// Tenants is the set of tenants that the subject belongs to. It may be the universal set if the subject has
	// access to all tenants.
	Tenants collections.Set[string]
}

// subjectJson is the JSON representation of a subject used internally for serialization and deserialization.
type subjectJson struct {
	User    string   `json:"user"`
	Tenants []string `json:"tenants"`
}

// UnmarshalJSON implements custom JSON unmarshalling for Subject. It interprets the special value '*' in the tenants
// array as meaning all tenants. This special value must not be combined with specific tenant names, that will be
// considered as an error.
func (s *Subject) UnmarshalJSON(data []byte) error {
	var subject subjectJson
	if err := json.Unmarshal(data, &subject); err != nil {
		return err
	}
	s.User = subject.User
	s.Tenants = collections.NewSet[string]()
	universal := false
	for _, tenant := range subject.Tenants {
		if tenant == universalMarker {
			universal = true
			break
		}
	}
	if universal {
		if len(subject.Tenants) > 1 {
			return fmt.Errorf(
				"the universal marker '%s' cannot be combined with specific tenant names",
				universalMarker,
			)
		}
		s.Tenants = AllTenants
	} else {
		s.Tenants = collections.NewSet(subject.Tenants...)
	}
	return nil
}

// MarshalJSON implements custom JSON marshalling for Subject.
func (s *Subject) MarshalJSON() (data []byte, err error) {
	var tenants []string
	if s.Tenants.Universal() {
		tenants = []string{
			universalMarker,
		}
	} else if s.Tenants.Finite() {
		tenants = s.Tenants.Inclusions()
	} else {
		err = fmt.Errorf("the tenant set is infinite")
		return
	}
	data, err = json.Marshal(subjectJson{
		User:    s.User,
		Tenants: tenants,
	})
	return
}

// universalMarker is the special value used in the subject's tenant list to indicate that the subject has access to all
// tenants.
const universalMarker = "*"
