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

import (
	"errors"
	"fmt"
	"testing"
)

func TestDeleteFailureCondition(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantReason  string
		wantMessage string
	}{
		{"bare trash-disabled", ErrVendorNFSTrashDisabled, "VendorNFSTrashDisabled", vastNFSTrashDisabledMessage},
		{"wrapped trash-disabled", fmt.Errorf("delete: %w", ErrVendorNFSTrashDisabled), "VendorNFSTrashDisabled", vastNFSTrashDisabledMessage},
		{"other error", errors.New("array unavailable: detail"), "VendorDeleteFailed", "Vendor volume deletion failed; see operator logs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, message := deleteFailureCondition(tc.err)
			if reason != tc.wantReason || message != tc.wantMessage {
				t.Errorf("deleteFailureCondition() = (%q, %q), want (%q, %q)", reason, message, tc.wantReason, tc.wantMessage)
			}
		})
	}
}
