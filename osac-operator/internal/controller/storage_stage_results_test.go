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
	"testing"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

// TestCombineStageResults covers the rule for merging the VMaaS and CaaS stage
// results: VMaaS error first, then CaaS error, then the smaller positive requeue.
func TestCombineStageResults(t *testing.T) {
	errVMaaS := errors.New("vmaas failed")
	errCaaS := errors.New("caas failed")
	rq := func(d time.Duration) ctrl.Result { return ctrl.Result{RequeueAfter: d} }

	tests := []struct {
		name       string
		vmaas      ctrl.Result
		vmaasErr   error
		caas       ctrl.Result
		caasErr    error
		wantResult ctrl.Result
		wantErr    error
	}{
		{name: "both zero"},
		{name: "only VMaaS requeue", vmaas: rq(10 * time.Second), wantResult: rq(10 * time.Second)},
		{name: "only CaaS requeue", caas: rq(20 * time.Second), wantResult: rq(20 * time.Second)},
		{name: "both requeue, VMaaS shorter", vmaas: rq(5 * time.Second), caas: rq(30 * time.Second), wantResult: rq(5 * time.Second)},
		{name: "both requeue, CaaS shorter", vmaas: rq(30 * time.Second), caas: rq(5 * time.Second), wantResult: rq(5 * time.Second)},
		{name: "VMaaS error with CaaS requeue", vmaasErr: errVMaaS, caas: rq(5 * time.Second), wantErr: errVMaaS},
		{name: "CaaS error with VMaaS requeue", vmaas: rq(5 * time.Second), caasErr: errCaaS, wantErr: errCaaS},
		{name: "both errors, VMaaS wins", vmaas: rq(7 * time.Second), vmaasErr: errVMaaS, caas: rq(3 * time.Second), caasErr: errCaaS, wantResult: rq(7 * time.Second), wantErr: errVMaaS},
		{name: "negative VMaaS requeue ignored", vmaas: rq(-time.Second), caas: rq(10 * time.Second), wantResult: rq(10 * time.Second)},
		{name: "negative CaaS requeue ignored", vmaas: rq(10 * time.Second), caas: rq(-time.Second), wantResult: rq(10 * time.Second)},
		{name: "only a negative requeue gives zero", vmaas: rq(-time.Second)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := combineStageResults(tt.vmaas, tt.vmaasErr, tt.caas, tt.caasErr)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.wantResult {
				t.Fatalf("result = %+v, want %+v", got, tt.wantResult)
			}
		})
	}
}
