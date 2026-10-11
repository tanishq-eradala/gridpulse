package telemetry

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sample(sequence int, temp float64) Reading {
	return Reading{SampleID: fmt.Sprintf("reading-%04d", sequence), EquipmentID: "lab-unit-01", RecordedAt: time.Now().UTC().Add(time.Duration(sequence) * time.Second).Format(time.RFC3339), Voltage: 480, Current: 120, Temperature: temp, Efficiency: 96}
}

func TestPersistentAlertAndResolution(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for i := 0; i < 2; i++ {
		inserted, changed, err := s.Ingest(ctx, sample(i, 91))
		if err != nil || !inserted || len(changed) != 0 {
			t.Fatalf("early reading %d: %v %v %v", i, inserted, changed, err)
		}
	}
	inserted, changed, err := s.Ingest(ctx, sample(2, 91))
	if err != nil || !inserted || len(changed) != 1 || changed[0].Kind != "over_temperature" || !changed[0].Active {
		t.Fatalf("third reading: %v %+v %v", inserted, changed, err)
	}
	inserted, changed, err = s.Ingest(ctx, sample(2, 91))
	if err != nil || inserted || len(changed) != 0 {
		t.Fatalf("duplicate reading: %v %+v %v", inserted, changed, err)
	}
	conflicting := sample(2, 70)
	if _, _, err := s.Ingest(ctx, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting sample ID error=%v", err)
	}
	_, changed, err = s.Ingest(ctx, sample(3, 92))
	if err != nil || len(changed) != 0 {
		t.Fatalf("persistent alert duplicated: %+v %v", changed, err)
	}
	m, err := s.Summary(ctx)
	if err != nil || m.TotalReadings != 4 || m.ActiveAlerts != 1 {
		t.Fatalf("summary before resolution: %+v %v", m, err)
	}
	_, changed, err = s.Ingest(ctx, sample(4, 65))
	if err != nil || len(changed) != 1 || changed[0].Active {
		t.Fatalf("resolution: %+v %v", changed, err)
	}
	alerts, err := s.Alerts(ctx, 10)
	if err != nil || len(alerts) != 1 || alerts[0].Active {
		t.Fatalf("persisted resolution: %+v %v", alerts, err)
	}
}

func TestInvalidReadingDoesNotPersist(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	r := sample(0, 300)
	if _, _, err := s.Ingest(ctx, r); err == nil {
		t.Fatal("expected out-of-range validation error")
	}
	m, err := s.Summary(ctx)
	if err != nil || m.TotalReadings != 0 {
		t.Fatalf("invalid reading persisted: %+v %v", m, err)
	}
}
