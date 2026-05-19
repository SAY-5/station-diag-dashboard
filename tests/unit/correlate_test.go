package unit

import (
	"testing"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/correlate"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"
)

// failureAt builds a Failure for a given subsystem at a millisecond offset
// from a fixed base time, all on the same run.
func failureAt(offsetMS int, ruleID, subsystem, severity string) rules.Failure {
	base := time.Date(2026, 5, 18, 14, 0, 0, 0, time.UTC)
	return rules.Failure{
		RuleID:    ruleID,
		StationID: "s1",
		RunID:     "run-cascade",
		Subsystem: subsystem,
		Actuator:  "act-1",
		Severity:  severity,
		Detail:    ruleID + " detail",
		At:        base.Add(time.Duration(offsetMS) * time.Millisecond),
	}
}

// TestCorrelatorCascadeIsOneIncident is the v3 spec test: a known
// multi-subsystem failure cascade must collapse into exactly one incident
// with the earliest-in-window subsystem ordered first as the root cause.
func TestCorrelatorCascadeIsOneIncident(t *testing.T) {
	c := correlate.New(500 * time.Millisecond)

	// A real cascade: an actuator overcurrent at t=0, the shared power rail
	// sags 200ms later, and a downstream encoder drops out at 350ms. Each is
	// inside the 500ms window of the previous one.
	cascade := []rules.Failure{
		failureAt(0, "actuator_overcurrent", "actuator", "error"),
		failureAt(200, "power_supply_sag", "power", "error"),
		failureAt(350, "encoder_dropout", "actuator", "error"),
	}

	var ids []string
	var last correlate.Incident
	newCount := 0
	for _, f := range cascade {
		inc, isNew := c.Observe(f)
		if isNew {
			newCount++
		}
		ids = append(ids, inc.ID)
		last = inc
	}

	if newCount != 1 {
		t.Fatalf("expected exactly one incident created, got %d", newCount)
	}
	for i, id := range ids {
		if id != ids[0] {
			t.Fatalf("failure %d landed in incident %q, want %q", i, id, ids[0])
		}
	}
	if len(last.Members) != 3 {
		t.Fatalf("expected 3 members in the incident, got %d", len(last.Members))
	}

	// Root cause is the earliest subsystem in the window.
	if last.RootCause != "actuator" {
		t.Fatalf("root cause = %q, want actuator", last.RootCause)
	}
	wantOrder := []string{
		"actuator_overcurrent", "power_supply_sag", "encoder_dropout",
	}
	for i, want := range wantOrder {
		if last.Members[i].RuleID != want {
			t.Fatalf("member %d = %q, want %q (members must be earliest first)",
				i, last.Members[i].RuleID, want)
		}
	}
	subs := last.Subsystems()
	if len(subs) != 2 || subs[0] != "actuator" || subs[1] != "power" {
		t.Fatalf("subsystem ordering = %v, want [actuator power]", subs)
	}
}

// TestCorrelatorSeparatesDistantFailures checks that failures further apart
// than the window become separate incidents, not one merged group.
func TestCorrelatorSeparatesDistantFailures(t *testing.T) {
	c := correlate.New(500 * time.Millisecond)

	first, isNew := c.Observe(failureAt(0, "actuator_overcurrent", "actuator", "error"))
	if !isNew {
		t.Fatal("first failure should create an incident")
	}
	// 900ms later: outside the 500ms window, so a new incident.
	second, isNew := c.Observe(failureAt(900, "power_supply_sag", "power", "error"))
	if !isNew {
		t.Fatal("failure outside the window should create a new incident")
	}
	if first.ID == second.ID {
		t.Fatalf("distant failures merged into one incident %q", first.ID)
	}
}

// TestCorrelatorOutOfOrderArrival checks that a late-arriving earlier
// failure still joins the incident and is sorted to the front.
func TestCorrelatorOutOfOrderArrival(t *testing.T) {
	c := correlate.New(500 * time.Millisecond)

	c.Observe(failureAt(200, "power_supply_sag", "power", "error"))
	// This failure has an earlier timestamp but arrives second. It is within
	// the window of the open incident, so it joins and sorts to the front.
	inc, isNew := c.Observe(failureAt(0, "actuator_overcurrent", "actuator", "error"))
	if isNew {
		t.Fatal("in-window failure should extend the incident, not start one")
	}
	if inc.Members[0].RuleID != "actuator_overcurrent" {
		t.Fatalf("earliest member = %q, want actuator_overcurrent",
			inc.Members[0].RuleID)
	}
	if inc.RootCause != "actuator" {
		t.Fatalf("root cause = %q, want actuator", inc.RootCause)
	}
}

// TestCorrelatorWindowDefault checks the zero-window fallback.
func TestCorrelatorWindowDefault(t *testing.T) {
	if got := correlate.New(0).Window(); got != correlate.DefaultWindow {
		t.Fatalf("zero window = %v, want default %v", got, correlate.DefaultWindow)
	}
	if got := correlate.New(2 * time.Second).Window(); got != 2*time.Second {
		t.Fatalf("explicit window not honored, got %v", got)
	}
}

// TestCorrelatorFlush surfaces still-open incidents.
func TestCorrelatorFlush(t *testing.T) {
	c := correlate.New(500 * time.Millisecond)
	c.Observe(failureAt(0, "actuator_overcurrent", "actuator", "error"))
	open := c.Flush()
	if len(open) != 1 {
		t.Fatalf("flush returned %d incidents, want 1", len(open))
	}
	if got := c.Flush(); len(got) != 0 {
		t.Fatalf("flush did not clear state, %d incidents remain", len(got))
	}
}

// TestFailureSubsystemFallback checks the subsystem fallback chain.
func TestFailureSubsystemFallback(t *testing.T) {
	if got := (rules.Failure{Subsystem: "power"}).SubsystemOrActuator(); got != "power" {
		t.Fatalf("subsystem present = %q, want power", got)
	}
	if got := (rules.Failure{Actuator: "act-9"}).SubsystemOrActuator(); got != "act-9" {
		t.Fatalf("actuator fallback = %q, want act-9", got)
	}
	if got := (rules.Failure{}).SubsystemOrActuator(); got != "unknown" {
		t.Fatalf("empty failure = %q, want unknown", got)
	}
}
