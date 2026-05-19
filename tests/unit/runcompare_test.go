package unit

import (
	"testing"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/rules"
	"github.com/SAY-5/station-diag-dashboard/internal/runcompare"
	"github.com/SAY-5/station-diag-dashboard/internal/store"
)

// cmpFailure builds a failure for the run-diff tests.
func cmpFailure(runID, ruleID, subsystem, actuator string) rules.Failure {
	return rules.Failure{
		RuleID:    ruleID,
		StationID: "s1",
		RunID:     runID,
		Subsystem: subsystem,
		Actuator:  actuator,
		Severity:  "error",
		Detail:    ruleID + " on " + actuator,
		At:        time.Date(2026, 5, 19, 9, 0, 0, 0, time.UTC),
	}
}

func cmpRun(runID string, failures []rules.Failure, events []store.RunEvent) runcompare.RunInput {
	return runcompare.RunInput{
		Run:      store.Run{RunID: runID, StationID: "s1"},
		Failures: failures,
		Events:   events,
	}
}

// hasChange reports whether a change list contains a (rule, actuator) pair.
func hasChange(cs []runcompare.FailureChange, rule, actuator string) bool {
	for _, c := range cs {
		if c.RuleID == rule && c.Actuator == actuator {
			return true
		}
	}
	return false
}

// TestCompareNewAndResolved is the v4 spec test: run B has one failure that
// run A did not (a regression) and is missing one failure that run A had (a
// fix). The diff must classify both correctly.
func TestCompareNewAndResolved(t *testing.T) {
	// Run A: actuator_timeout on act-1, actuator_overcurrent on act-2.
	runA := cmpRun("run-a", []rules.Failure{
		cmpFailure("run-a", "actuator_timeout", "actuator", "act-1"),
		cmpFailure("run-a", "actuator_overcurrent", "actuator", "act-2"),
	}, nil)

	// Run B: actuator_timeout on act-1 still fails (persisting),
	// actuator_overcurrent on act-2 is gone (resolved), and a brand new
	// power_supply_sag on act-3 appears (new regression).
	runB := cmpRun("run-b", []rules.Failure{
		cmpFailure("run-b", "actuator_timeout", "actuator", "act-1"),
		cmpFailure("run-b", "power_supply_sag", "power", "act-3"),
	}, nil)

	d := runcompare.Compare(runA, runB)

	if len(d.NewFailures) != 1 {
		t.Fatalf("expected 1 new failure, got %d: %+v", len(d.NewFailures), d.NewFailures)
	}
	if !hasChange(d.NewFailures, "power_supply_sag", "act-3") {
		t.Fatalf("new failure not classified: %+v", d.NewFailures)
	}

	if len(d.ResolvedFailures) != 1 {
		t.Fatalf("expected 1 resolved failure, got %d: %+v",
			len(d.ResolvedFailures), d.ResolvedFailures)
	}
	if !hasChange(d.ResolvedFailures, "actuator_overcurrent", "act-2") {
		t.Fatalf("resolved failure not classified: %+v", d.ResolvedFailures)
	}

	if len(d.PersistingFailures) != 1 {
		t.Fatalf("expected 1 persisting failure, got %d", len(d.PersistingFailures))
	}
	if !hasChange(d.PersistingFailures, "actuator_timeout", "act-1") {
		t.Fatalf("persisting failure not classified: %+v", d.PersistingFailures)
	}

	// act-2 went clean, act-3 started failing; act-1 stayed failing.
	flips := map[string]runcompare.ActuatorChange{}
	for _, c := range d.ActuatorChanges {
		flips[c.Actuator] = c
	}
	if c, ok := flips["act-2"]; !ok || !c.WasFailing || c.NowFailing {
		t.Fatalf("act-2 should flip failing to clean, got %+v", c)
	}
	if c, ok := flips["act-3"]; !ok || c.WasFailing || !c.NowFailing {
		t.Fatalf("act-3 should flip clean to failing, got %+v", c)
	}
	if _, ok := flips["act-1"]; ok {
		t.Fatal("act-1 did not change status and should not be listed")
	}
}

// TestCompareIdenticalRuns checks that diffing a run against itself produces
// only persisting failures and no new, resolved or actuator changes.
func TestCompareIdenticalRuns(t *testing.T) {
	failures := []rules.Failure{
		cmpFailure("run-a", "actuator_timeout", "actuator", "act-1"),
	}
	runA := cmpRun("run-a", failures, nil)
	runB := cmpRun("run-b", failures, nil)

	d := runcompare.Compare(runA, runB)
	if len(d.NewFailures) != 0 || len(d.ResolvedFailures) != 0 {
		t.Fatalf("identical runs produced new=%d resolved=%d",
			len(d.NewFailures), len(d.ResolvedFailures))
	}
	if len(d.PersistingFailures) != 1 {
		t.Fatalf("expected 1 persisting failure, got %d", len(d.PersistingFailures))
	}
	if len(d.ActuatorChanges) != 0 {
		t.Fatalf("identical runs should report no actuator changes")
	}
}

// TestCompareSubsystemDeltas checks the per-subsystem failure-count and
// timing deltas.
func TestCompareSubsystemDeltas(t *testing.T) {
	base := time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC)
	eventsA := []store.RunEvent{
		{TS: base, Subsystem: "actuator"},
		{TS: base.Add(1 * time.Second), Subsystem: "actuator"},
	}
	eventsB := []store.RunEvent{
		{TS: base, Subsystem: "actuator"},
		{TS: base.Add(3 * time.Second), Subsystem: "actuator"},
	}
	runA := cmpRun("run-a", []rules.Failure{
		cmpFailure("run-a", "actuator_timeout", "actuator", "act-1"),
	}, eventsA)
	runB := cmpRun("run-b", []rules.Failure{
		cmpFailure("run-b", "actuator_timeout", "actuator", "act-1"),
		cmpFailure("run-b", "actuator_stuck", "actuator", "act-1"),
	}, eventsB)

	d := runcompare.Compare(runA, runB)
	if len(d.SubsystemDeltas) != 1 {
		t.Fatalf("expected 1 subsystem delta, got %d", len(d.SubsystemDeltas))
	}
	got := d.SubsystemDeltas[0]
	if got.Subsystem != "actuator" {
		t.Fatalf("subsystem = %q, want actuator", got.Subsystem)
	}
	if got.FailureDelta != 1 {
		t.Fatalf("failure delta = %d, want 1", got.FailureDelta)
	}
	// Run A span 1s, run B span 3s, so the delta is +2000ms.
	if got.SpanDeltaMS != 2000 {
		t.Fatalf("span delta = %d ms, want 2000", got.SpanDeltaMS)
	}
}
