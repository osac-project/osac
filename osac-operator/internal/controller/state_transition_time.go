/*
Copyright (c) 2026 Red Hat Inc.

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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// setState records a state transition without allowing its timestamp to move
// backward or remain equal to the stored boundary. PostgreSQL stores these
// timestamps at microsecond precision and requires a later boundary on change.
func setState[S comparable](state *S, stateTransitionTime **metav1.Time, next S, observedAt metav1.Time) bool {
	if *state == next {
		return false
	}
	if previous := *stateTransitionTime; previous != nil && !observedAt.After(previous.Time) {
		observedAt = metav1.NewTime(previous.Time.Add(time.Microsecond))
	}
	*state = next
	*stateTransitionTime = observedAt.DeepCopy()
	return true
}
