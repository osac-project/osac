package events

import "testing"

func TestNetworkingTransitionTablesAreExhaustive(t *testing.T) {
	tests := []struct {
		name   string
		table  TransitionTable
		states []string
		start  string
		delete string
	}{
		{
			name:   "ExternalIP",
			table:  externalIPTransitions,
			states: []string{StateEmpty, ExternalIPStatePending, ExternalIPStateAllocated, ExternalIPStateFailed, ExternalIPStateDeleting, ExternalIPStateUnspecified},
			start:  ExternalIPStateAllocated,
			delete: ExternalIPStateDeleting,
		},
		{
			name:   "NATGateway",
			table:  natGatewayTransitions,
			states: []string{StateEmpty, NATGatewayStatePending, NATGatewayStateReady, NATGatewayStateFailed, NATGatewayStateDeleting, NATGatewayStateUnspecified},
			start:  NATGatewayStateReady,
			delete: NATGatewayStateDeleting,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, from := range test.states {
				for _, to := range test.states[1:] {
					if _, err := resolveTransition(test.table, from, to); err != ErrSkipTransition && err != nil {
						t.Fatalf("transition %s -> %s: %v", from, to, err)
					}
				}
			}
			if got, err := resolveTransition(test.table, StateEmpty, test.start); err != nil || got != eventBillableStart {
				t.Fatalf("initial billable transition: got %q, err %v", got, err)
			}
			if got, err := resolveTransition(test.table, test.start, test.delete); err != nil || got != EventSuspended {
				t.Fatalf("deletion transition: got %q, err %v", got, err)
			}
		})
	}
}

func TestNetworkingBillableFailureTransitionsCloseUsage(t *testing.T) {
	tests := []struct {
		name  string
		table TransitionTable
		from  string
		to    string
	}{
		{"ExternalIP allocated to failed", externalIPTransitions, ExternalIPStateAllocated, ExternalIPStateFailed},
		{"NATGateway ready to pending", natGatewayTransitions, NATGatewayStateReady, NATGatewayStatePending},
		{"NATGateway ready to failed", natGatewayTransitions, NATGatewayStateReady, NATGatewayStateFailed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveTransition(test.table, test.from, test.to)
			if err != nil {
				t.Fatalf("transition: %v", err)
			}
			if got != EventSuspended {
				t.Fatalf("expected %q, got %q", EventSuspended, got)
			}
		})
	}
}
