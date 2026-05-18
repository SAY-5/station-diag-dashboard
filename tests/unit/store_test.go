package unit

import (
	"testing"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"
	"github.com/SAY-5/station-diag-dashboard/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func storeEvent(runID, msg string) ingest.LogEvent {
	return ingest.LogEvent{
		TS: time.Now().UTC(), StationID: "s1", RunID: runID,
		Level: "info", Subsystem: "actuator", Message: msg, ActuatorID: "act-1",
	}
}

func TestStoreRecordEventCreatesRun(t *testing.T) {
	st := openTestStore(t)
	if err := st.RecordEvent(storeEvent("r1", "move_command")); err != nil {
		t.Fatalf("record event: %v", err)
	}
	if err := st.RecordEvent(storeEvent("r1", "position_reached")); err != nil {
		t.Fatalf("record event: %v", err)
	}
	run, err := st.GetRun("r1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.EventCount != 2 {
		t.Fatalf("expected 2 events, got %d", run.EventCount)
	}
	if run.StationID != "s1" {
		t.Fatalf("bad station: %q", run.StationID)
	}
}

func TestStoreRejectsEventWithoutRun(t *testing.T) {
	st := openTestStore(t)
	ev := storeEvent("", "x")
	if err := st.RecordEvent(ev); err == nil {
		t.Fatal("expected error for event without run_id")
	}
}

func TestStoreRunNotFound(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.GetRun("missing"); err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreFailuresAndTimeline(t *testing.T) {
	st := openTestStore(t)
	_ = st.RecordEvent(storeEvent("r1", "move_command"))
	_ = st.RecordEvent(storeEvent("r1", "watchdog reset"))

	f := rules.Failure{
		RuleID: "actuator_timeout", StationID: "s1", RunID: "r1",
		Actuator: "act-1", Detail: "no position_reached", Severity: "error",
		At: time.Now().UTC(),
	}
	if err := st.RecordFailure(f); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	run, _ := st.GetRun("r1")
	if run.Failures != 1 {
		t.Fatalf("expected 1 failure on run, got %d", run.Failures)
	}
	failures, err := st.RunFailures("r1")
	if err != nil || len(failures) != 1 {
		t.Fatalf("run failures: %v len=%d", err, len(failures))
	}
	if failures[0].RuleID != "actuator_timeout" {
		t.Fatalf("bad failure rule: %q", failures[0].RuleID)
	}
	timeline, err := st.RunEvents("r1")
	if err != nil || len(timeline) != 2 {
		t.Fatalf("timeline: %v len=%d", err, len(timeline))
	}
}

func TestStoreNotes(t *testing.T) {
	st := openTestStore(t)
	_ = st.RecordEvent(storeEvent("r1", "move_command"))

	note, err := st.AddNote("s1", "r1", "alice", "checked the wiring harness")
	if err != nil {
		t.Fatalf("add note: %v", err)
	}
	if note.ID == 0 {
		t.Fatal("note ID not assigned")
	}
	notes, err := st.RunNotes("r1")
	if err != nil || len(notes) != 1 {
		t.Fatalf("run notes: %v len=%d", err, len(notes))
	}
	if notes[0].Author != "alice" || notes[0].Body != "checked the wiring harness" {
		t.Fatalf("bad note: %+v", notes[0])
	}
}

func TestStoreRejectsEmptyNote(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.AddNote("s1", "r1", "alice", ""); err == nil {
		t.Fatal("expected error for empty note body")
	}
}

func TestStoreResolveRun(t *testing.T) {
	st := openTestStore(t)
	_ = st.RecordEvent(storeEvent("r1", "move_command"))

	if err := st.SetResolved("r1", true); err != nil {
		t.Fatalf("set resolved: %v", err)
	}
	run, _ := st.GetRun("r1")
	if !run.Resolved {
		t.Fatal("run should be resolved")
	}
	if err := st.SetResolved("missing", true); err == nil {
		t.Fatal("expected error resolving missing run")
	}
}

func TestStoreListRunsOrdersByRecent(t *testing.T) {
	st := openTestStore(t)
	older := storeEvent("old", "x")
	older.TS = time.Now().UTC().Add(-time.Hour)
	_ = st.RecordEvent(older)
	_ = st.RecordEvent(storeEvent("new", "x"))

	runs, err := st.ListRuns(10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("list runs: %v len=%d", err, len(runs))
	}
	if runs[0].RunID != "new" {
		t.Fatalf("expected newest run first, got %q", runs[0].RunID)
	}
}
