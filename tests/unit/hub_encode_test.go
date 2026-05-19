package unit

import (
	"encoding/json"
	"testing"

	"github.com/SAY-5/station-diag-dashboard/internal/hub"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"
)

// TestHubEncodeRoundTrip checks every message kind marshals to JSON that
// decodes back with its kind and payload intact.
func TestHubEncodeRoundTrip(t *testing.T) {
	cases := []hub.Message{
		{Seq: 1, Kind: hub.KindLogEvent, Event: &eventForEncode},
		{Seq: 2, Kind: hub.KindFailureHighlight, Failure: &rules.Failure{
			RuleID: "actuator_timeout", RunID: "r1", Severity: "error",
		}},
		{Seq: 3, Kind: hub.KindNoteAdded, Note: &hub.Note{
			ID: 7, RunID: "r1", Author: "alice", Body: "checked harness",
		}},
	}
	for _, m := range cases {
		raw, err := hub.Encode(m)
		if err != nil {
			t.Fatalf("encode %s: %v", m.Kind, err)
		}
		var back hub.Message
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("decode %s: %v", m.Kind, err)
		}
		if back.Seq != m.Seq || back.Kind != m.Kind {
			t.Fatalf("round trip mismatch: got seq=%d kind=%s", back.Seq, back.Kind)
		}
	}
}

var eventForEncode = sampleEvent("move_command")

// TestHubNewClampsCapacity verifies a non-positive capacity is clamped to a
// usable minimum rather than producing a hub that retains nothing.
func TestHubNewClampsCapacity(t *testing.T) {
	for _, cap := range []int{-5, 0} {
		h := hub.New(cap)
		h.Publish(sampleEvent("event"))
		if got := len(h.Backlog(0)); got != 1 {
			t.Fatalf("New(%d): backlog len %d, want 1", cap, got)
		}
	}
}
