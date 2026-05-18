package unit

import (
	"testing"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		wantErr bool
		check   func(*testing.T, ingest.LogEvent)
	}{
		{
			name: "well-formed line",
			line: `{"ts":"2026-05-18T10:00:00Z","station_id":"s1","run_id":"r1","level":"info","subsystem":"actuator","message":"move_command issued","actuator_id":"act-1"}`,
			check: func(t *testing.T, e ingest.LogEvent) {
				if e.StationID != "s1" || e.RunID != "r1" {
					t.Fatalf("bad ids: %+v", e)
				}
				if e.Level != "info" || e.Subsystem != "actuator" {
					t.Fatalf("bad level/subsystem: %+v", e)
				}
				if e.ActuatorID != "act-1" {
					t.Fatalf("bad actuator: %q", e.ActuatorID)
				}
				if !e.TS.Equal(time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)) {
					t.Fatalf("bad ts: %v", e.TS)
				}
			},
		},
		{
			name: "fields parsed",
			line: `{"station_id":"s1","level":"warn","subsystem":"actuator","message":"current","fields":{"current_a":4.2}}`,
			check: func(t *testing.T, e ingest.LogEvent) {
				v, ok := e.Field("current_a")
				if !ok || v != 4.2 {
					t.Fatalf("field not parsed: %v %v", v, ok)
				}
			},
		},
		{
			name: "level normalized to lowercase",
			line: `{"station_id":"s1","level":"ERROR","subsystem":"ACTUATOR","message":"x"}`,
			check: func(t *testing.T, e ingest.LogEvent) {
				if e.Level != "error" || e.Subsystem != "actuator" {
					t.Fatalf("not normalized: %+v", e)
				}
			},
		},
		{name: "blank line", line: "   ", wantErr: true},
		{name: "malformed json", line: `{"station_id":`, wantErr: true},
		{name: "missing station_id", line: `{"level":"info","message":"x"}`, wantErr: true},
		{name: "missing level", line: `{"station_id":"s1","message":"x"}`, wantErr: true},
		{name: "unknown level", line: `{"station_id":"s1","level":"chatty","message":"x"}`, wantErr: true},
		{name: "bad timestamp", line: `{"ts":"not-a-time","station_id":"s1","level":"info","message":"x"}`, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := ingest.ParseLine([]byte(tc.line))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got event %+v", ev)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, ev)
			}
		})
	}
}

func TestParseLineDefaultsTimestamp(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	ev, err := ingest.ParseLine([]byte(`{"station_id":"s1","level":"info","message":"x"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.TS.Before(before) {
		t.Fatalf("default timestamp not set: %v", ev.TS)
	}
}

func TestEmptyLineSentinel(t *testing.T) {
	_, err := ingest.ParseLine([]byte(""))
	if err != ingest.ErrEmptyLine {
		t.Fatalf("expected ErrEmptyLine, got %v", err)
	}
}
