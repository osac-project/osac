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

// noopMessageFromOutputs returns the human-readable no-op message
// from provisioning job outputs when the backend signals that no real
// effect was applied (osac_noop: true). Returns "" when the outputs
// carry no no-op signal.
func noopMessageFromOutputs(outputs map[string]any) string {
	if outputs == nil {
		return ""
	}
	noop, _ := outputs["osac_noop"].(bool)
	if !noop {
		return ""
	}
	msg, _ := outputs["osac_noop_message"].(string)
	if msg == "" {
		msg = "Resource accepted as a no-op: " +
			"no backend policy was applied."
	}
	return msg
}
