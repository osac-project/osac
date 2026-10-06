package projection

import (
	"reflect"
	"testing"
	"time"

	"github.com/osac-project/osac-metering/schema"
)

type fakeRow struct {
	values []any
}

func (r fakeRow) Scan(dest ...any) error {
	for i, value := range r.values {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(value))
	}
	return nil
}

func TestScanResourceStateIncludesMeterState(t *testing.T) {
	transitionTime := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	allocationActive := transitionTime.Add(-time.Hour)
	allocationFirst := transitionTime.Add(-2 * time.Hour)
	consumptionActive := transitionTime.Add(-30 * time.Minute)
	consumptionFirst := transitionTime.Add(-90 * time.Minute)
	parentBillableSince := transitionTime.Add(-3 * time.Hour)

	state, err := scanResourceState(fakeRow{values: []any{
		"bmi-1",
		schema.ResourceTypeBareMetalInstance,
		"tenant-1",
		(*string)(nil),
		"BARE_METAL_INSTANCE_STATE_RUNNING",
		(*string)(nil),
		false,
		true,
		&parentBillableSince,
		(*time.Time)(nil),
		transitionTime,
		int32(4),
		[]byte(`{"bm_instance_type":"large"}`),
		[]byte(`{}`),
		(*time.Time)(nil),
		&allocationActive,
		&allocationFirst,
		&consumptionActive,
		&consumptionFirst,
	}})
	if err != nil {
		t.Fatalf("scanResourceState() error = %v", err)
	}

	if !reflect.DeepEqual(state.BMaaSMeterState, BMaaSMeterState{
		Allocation:  MeterState{ActiveSince: &allocationActive, FirstStartedAt: &allocationFirst},
		Consumption: MeterState{ActiveSince: &consumptionActive, FirstStartedAt: &consumptionFirst},
	}) {
		t.Fatalf("meter state = %#v", state.BMaaSMeterState)
	}
	if !reflect.DeepEqual(state.BillableSince, &allocationActive) {
		t.Fatalf("billable since = %v, want %v", state.BillableSince, allocationActive)
	}
	if !state.IsBillable {
		t.Fatal("BMaaS allocation activity should determine IsBillable")
	}
}
