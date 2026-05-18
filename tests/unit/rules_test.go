package unit

import (
	"os"
	"testing"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"
)

// ev builds an actuator-subsystem event at offset seconds from a base time.
func ev(offsetSec float64, level, msg string, fields map[string]float64) ingest.LogEvent {
	base := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	return ingest.LogEvent{
		TS:         base.Add(time.Duration(offsetSec * float64(time.Second))),
		StationID:  "s1",
		RunID:      "r1",
		Level:      level,
		Subsystem:  "actuator",
		Message:    msg,
		ActuatorID: "act-1",
		Fields:     fields,
	}
}

func loadTestEngine(t *testing.T) *rules.Engine {
	t.Helper()
	data, err := os.ReadFile("../../rules/actuator_signatures.yaml")
	if err != nil {
		t.Fatalf("read rules: %v", err)
	}
	e, err := rules.Load(data)
	if err != nil {
		t.Fatalf("load engine: %v", err)
	}
	return e
}

func failureIDs(fs []rules.Failure) map[string]int {
	m := map[string]int{}
	for _, f := range fs {
		m[f.RuleID]++
	}
	return m
}

func TestRuleEngineTableDriven(t *testing.T) {
	engine := loadTestEngine(t)

	cases := []struct {
		name   string
		events []ingest.LogEvent
		want   map[string]int
	}{
		{
			name: "healthy run fires nothing",
			events: []ingest.LogEvent{
				ev(0, "info", "move_command issued", nil),
				ev(0.5, "info", "position_reached confirmed", nil),
			},
			want: map[string]int{},
		},
		{
			name: "timeout: resolve arrives too late",
			events: []ingest.LogEvent{
				ev(0, "info", "move_command issued", nil),
				ev(3, "info", "position_reached confirmed", nil),
			},
			want: map[string]int{"actuator_timeout": 1},
		},
		{
			name: "timeout: no resolve at all",
			events: []ingest.LogEvent{
				ev(0, "info", "move_command issued", nil),
				ev(1, "warn", "still settling", nil),
			},
			want: map[string]int{"actuator_timeout": 1},
		},
		{
			name: "timeout: resolve in window does not fire",
			events: []ingest.LogEvent{
				ev(0, "info", "move_command issued", nil),
				ev(1.5, "info", "position_reached confirmed", nil),
			},
			want: map[string]int{},
		},
		{
			name: "overcurrent: above threshold fires",
			events: []ingest.LogEvent{
				ev(0, "warn", "drive current sampled", map[string]float64{"current_a": 4.1}),
			},
			want: map[string]int{"actuator_overcurrent": 1},
		},
		{
			name: "overcurrent: below threshold does not fire",
			events: []ingest.LogEvent{
				ev(0, "warn", "drive current sampled", map[string]float64{"current_a": 2.0}),
			},
			want: map[string]int{},
		},
		{
			name: "overcurrent: wrong level does not fire",
			events: []ingest.LogEvent{
				ev(0, "info", "drive current sampled", map[string]float64{"current_a": 9.0}),
			},
			want: map[string]int{},
		},
		{
			name: "stuck: three move_command with no resolve fires",
			events: []ingest.LogEvent{
				ev(0, "warn", "move_command issued, retrying", nil),
				ev(1, "warn", "move_command issued, retrying", nil),
				ev(2, "warn", "move_command issued, retrying", nil),
			},
			want: map[string]int{"actuator_stuck": 1, "actuator_timeout": 1},
		},
		{
			name: "stuck: resolve between commands clears streak",
			events: []ingest.LogEvent{
				ev(0, "warn", "move_command issued", nil),
				ev(0.2, "info", "position_reached confirmed", nil),
				ev(1, "warn", "move_command issued", nil),
				ev(1.2, "info", "position_reached confirmed", nil),
				ev(2, "warn", "move_command issued", nil),
				ev(2.2, "info", "position_reached confirmed", nil),
			},
			want: map[string]int{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := failureIDs(engine.Evaluate(tc.events))
			for id, n := range tc.want {
				if got[id] != n {
					t.Errorf("rule %s: want %d fires, got %d", id, n, got[id])
				}
			}
			for id, n := range got {
				if tc.want[id] != n {
					t.Errorf("unexpected rule %s fired %d times", id, n)
				}
			}
		})
	}
}

func TestRuleLoadValidation(t *testing.T) {
	bad := []struct {
		name string
		yaml string
	}{
		{"empty", `rules: []`},
		{"missing id", "rules:\n  - kind: timeout\n    trigger_pattern: a\n    resolve_pattern: b\n    within: 2s"},
		{"unknown kind", "rules:\n  - id: x\n    kind: telepathy"},
		{"timeout without within", "rules:\n  - id: x\n    kind: timeout\n    trigger_pattern: a\n    resolve_pattern: b"},
		{"threshold bad operator", "rules:\n  - id: x\n    kind: threshold\n    field: c\n    operator: near"},
		{"consecutive count too low", "rules:\n  - id: x\n    kind: consecutive\n    trigger_pattern: a\n    resolve_pattern: b\n    count: 1"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := rules.Load([]byte(tc.yaml)); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestRuleLoadValidYAML(t *testing.T) {
	e := loadTestEngine(t)
	if len(e.Rules()) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(e.Rules()))
	}
}
