/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import "context"

// Severity indicates whether a failed check blocks `osac install validate`
// or only warns.
type Severity int

const (
	// Required means a Failed means the Hub cluster isn't ready to install
	// OSAC: `osac install validate` exits non-zero.
	Required Severity = iota
	// Warning means a Failed flags a likely problem without blocking
	// `osac install validate`.
	Warning
)

// String implements fmt.Stringer.
func (s Severity) String() string {
	switch s {
	case Required:
		return "required"
	case Warning:
		return "warning"
	default:
		return "unknown"
	}
}

// Status is the outcome of running a single Check.
type Status int

const (
	// Pass means the check's condition holds.
	Pass Status = iota
	// Failed means the check's condition does not hold.
	Failed
)

// String implements fmt.Stringer.
func (s Status) String() string {
	switch s {
	case Pass:
		return "pass"
	case Failed:
		return "fail"
	default:
		return "unknown"
	}
}

// Category groups a Check for display purposes (e.g. `osac install
// status`'s Resources/Operators sections). It has no effect on which
// checks run or how they gate `osac install validate`.
type Category int

const (
	// ResourceCategory is a CRD, StorageClass, cluster version, or other
	// non-Operator resource.
	ResourceCategory Category = iota
	// OperatorCategory is an OLM-managed Operator, checked via its
	// ClusterServiceVersion.
	OperatorCategory
)

// String implements fmt.Stringer.
func (c Category) String() string {
	switch c {
	case ResourceCategory:
		return "resource"
	case OperatorCategory:
		return "operator"
	default:
		return "unknown"
	}
}

// Check is a single prerequisite probe against the target Hub cluster. Run
// must be read-only: it must never create, update, or delete cluster state.
type Check struct {
	// Name is a short, stable, machine-friendly identifier, e.g.
	// "cert-manager-crds". Used in --output json and for referencing a
	// specific check.
	Name string
	// Description is a one-line human-readable summary shown in
	// discover/validate output.
	Description string
	// Severity determines whether a Failed blocks `osac install validate`.
	Severity Severity
	// Category groups this check for display purposes; see Category.
	Category Category
	// Run executes the check against the given clients and returns its
	// status and a human-readable message explaining that status.
	Run func(ctx context.Context, clients *Clients) (Status, string)
}

// Result is the outcome of running one Check.
type Result struct {
	Check   Check
	Status  Status
	Message string
}

// RunAll runs every check in checks against clients, in order, and returns
// one Result per check. A check that panics is not recovered: a bug in a
// check's Run function should fail loudly, not be silently swallowed into a
// misleading "fail" result.
func RunAll(ctx context.Context, clients *Clients, checks []Check) []Result {
	results := make([]Result, 0, len(checks))
	for _, check := range checks {
		status, message := check.Run(ctx, clients)
		results = append(results, Result{
			Check:   check,
			Status:  status,
			Message: message,
		})
	}
	return results
}

// AnyRequiredFailed reports whether any Required-severity check in results
// failed. `osac install validate` uses this to decide its exit code;
// `osac install discover` ignores it and reports every result regardless of
// severity.
func AnyRequiredFailed(results []Result) bool {
	for _, result := range results {
		if result.Status == Failed && result.Check.Severity == Required {
			return true
		}
	}
	return false
}
