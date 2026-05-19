package unit

import (
	"testing"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/correlate"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"
)

// buildIncident drives a Correlator with a cascade and returns the final
// incident for the run.
func buildIncident(runID string) correlate.Incident {
	c := correlate.New(500 * time.Millisecond)
	base := time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC)
	mk := func(off int, rule, sub string) rules.Failure {
		return rules.Failure{
			RuleID: rule, StationID: "s1", RunID: runID, Subsystem: sub,
			Actuator: "act-1", Severity: "error", Detail: rule + " detail",
			At: base.Add(time.Duration(off) * time.Millisecond),
		}
	}
	c.Observe(mk(0, "actuator_overcurrent", "actuator"))
	last, _ := c.Observe(mk(150, "power_supply_sag", "power"))
	return last
}

func TestStoreSaveAndListIncident(t *testing.T) {
	st := openTestStore(t)
	inc := buildIncident("run-a")

	if err := st.SaveIncident(inc); err != nil {
		t.Fatalf("save incident: %v", err)
	}
	got, err := st.ListIncidents(10)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(got))
	}
	if got[0].ID != inc.ID {
		t.Fatalf("incident id = %q, want %q", got[0].ID, inc.ID)
	}
	if len(got[0].Members) != 2 {
		t.Fatalf("expected 2 members round-tripped, got %d", len(got[0].Members))
	}
	if got[0].RootCause != "actuator" {
		t.Fatalf("root cause = %q, want actuator", got[0].RootCause)
	}
	if got[0].Members[0].RuleID != "actuator_overcurrent" {
		t.Fatalf("first member = %q, want actuator_overcurrent",
			got[0].Members[0].RuleID)
	}
}

// TestStoreSaveIncidentUpsert checks that re-saving a grown incident under
// the same id replaces the member set rather than duplicating the row.
func TestStoreSaveIncidentUpsert(t *testing.T) {
	st := openTestStore(t)
	inc := buildIncident("run-b")

	if err := st.SaveIncident(inc); err != nil {
		t.Fatalf("first save: %v", err)
	}
	// Simulate the correlator extending the incident with a third member.
	inc.Members = append(inc.Members, correlate.Member{
		RuleID: "encoder_dropout", Subsystem: "actuator", Severity: "error",
		At: inc.EndedAt.Add(100 * time.Millisecond),
	})
	inc.EndedAt = inc.EndedAt.Add(100 * time.Millisecond)
	if err := st.SaveIncident(inc); err != nil {
		t.Fatalf("upsert save: %v", err)
	}
	got, err := st.ListIncidents(10)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("upsert produced %d rows, want 1", len(got))
	}
	if len(got[0].Members) != 3 {
		t.Fatalf("upsert kept %d members, want 3", len(got[0].Members))
	}
}

func TestStoreRunIncidents(t *testing.T) {
	st := openTestStore(t)
	if err := st.SaveIncident(buildIncident("run-x")); err != nil {
		t.Fatalf("save run-x: %v", err)
	}
	if err := st.SaveIncident(buildIncident("run-y")); err != nil {
		t.Fatalf("save run-y: %v", err)
	}
	got, err := st.RunIncidents("run-x")
	if err != nil {
		t.Fatalf("run incidents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("run-x has %d incidents, want 1", len(got))
	}
	if got[0].RunID != "run-x" {
		t.Fatalf("run incident leaked run %q", got[0].RunID)
	}
}

func TestStoreSaveIncidentRejectsEmptyID(t *testing.T) {
	st := openTestStore(t)
	if err := st.SaveIncident(correlate.Incident{}); err == nil {
		t.Fatal("expected an error saving an incident with no id")
	}
}
