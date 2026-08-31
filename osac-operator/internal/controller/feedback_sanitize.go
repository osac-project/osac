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

import "strings"

// sanitizeFeedbackText makes s safe to place into a gRPC-marshaled protobuf
// string field. Feedback controllers copy free-form text from sources this
// operator doesn't control (KubeVirt/libvirt/qemu VM status messages, AAP job
// output, vendor network backend IDs) straight into a CR Condition.Message
// and from there into the gRPC object sent to fulfillment-service.
// protobuf's wire format rejects invalid UTF-8 in a string field at
// unmarshal time, so a single bad byte sequence doesn't just fail the
// update that produced it -- it breaks every subsequent List/Get call that
// returns that record, for every caller, until the bad record is
// overwritten. Replace instead of reject so the rest of the message stays
// readable.
func sanitizeFeedbackText(s string) string {
	return strings.ToValidUTF8(s, "�")
}
