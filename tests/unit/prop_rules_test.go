package unit

import (
	"math/rand"
	"testing"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
)

// Property test for the rule engine: a random sequence of healthy actuator
// traffic with a known number of injected failure signatures must surface
// every injected signature exactly once and must not flag a healthy run.
//
// The generator interleaves complete, self-contained signature blocks so the
// expected fire count is deterministic regardless of the random ordering of
// the blocks.

type sigKind int

const (
	sigHealthy sigKind = iota
	sigTimeout
	sigStuck
	sigOvercurrent
)

// signatureBlock is one self-contained run fragment on its own actuator and
// run id so blocks never cross-contaminate each other's detector state.
type signatureBlock struct {
	kind     sigKind
	runID    string
	actuator string
}

// emit renders a block into LogEvents starting at base. Each block spans a
// generous 10s slot so timing windows are unambiguous.
func (b signatureBlock) emit(base time.Time) []ingest.LogEvent {
	mk := func(off float64, level, msg string, fields map[string]float64) ingest.LogEvent {
		return ingest.LogEvent{
			TS:         base.Add(time.Duration(off * float64(time.Second))),
			StationID:  "s1",
			RunID:      b.runID,
			Level:      level,
			Subsystem:  "actuator",
			Message:    msg,
			ActuatorID: b.actuator,
			Fields:     fields,
		}
	}
	switch b.kind {
	case sigTimeout:
		// move_command with a resolve well outside the 2s window.
		return []ingest.LogEvent{
			mk(0, "info", "move_command issued", nil),
			mk(5, "info", "position_reached confirmed", nil),
		}
	case sigStuck:
		// three move_command with no resolve: also trips actuator_timeout
		// once for the dangling first command.
		return []ingest.LogEvent{
			mk(0, "warn", "move_command issued, retrying", nil),
			mk(1, "warn", "move_command issued, retrying", nil),
			mk(2, "warn", "move_command issued, retrying", nil),
		}
	case sigOvercurrent:
		return []ingest.LogEvent{
			mk(0, "info", "move_command issued", nil),
			mk(0.5, "warn", "drive current sampled", map[string]float64{"current_a": 4.7}),
			mk(1, "info", "position_reached confirmed", nil),
		}
	default: // sigHealthy
		return []ingest.LogEvent{
			mk(0, "info", "move_command issued", nil),
			mk(0.5, "info", "position_reached confirmed", nil),
		}
	}
}

// wantFires returns the rule fire counts this block must produce.
func (b signatureBlock) wantFires() map[string]int {
	switch b.kind {
	case sigTimeout:
		return map[string]int{"actuator_timeout": 1}
	case sigStuck:
		// The three dangling move_commands trip actuator_stuck once and the
		// first dangling command also leaves actuator_timeout open once.
		return map[string]int{"actuator_stuck": 1, "actuator_timeout": 1}
	case sigOvercurrent:
		return map[string]int{"actuator_overcurrent": 1}
	default:
		return map[string]int{}
	}
}

func TestPropertyRuleEngineFiresEverySignatureExactlyOnce(t *testing.T) {
	engine := loadTestEngine(t)
	base := time.Date(2026, 5, 18, 8, 0, 0, 0, time.UTC)

	const iterations = 300
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(iter) + 1))
		nBlocks := rng.Intn(8) + 1

		blocks := make([]signatureBlock, nBlocks)
		want := map[string]int{}
		for i := 0; i < nBlocks; i++ {
			blocks[i] = signatureBlock{
				kind:     sigKind(rng.Intn(4)),
				runID:    runIDFor(iter, i),
				actuator: actuatorFor(i),
			}
			for id, n := range blocks[i].wantFires() {
				want[id] += n
			}
		}

		// Each block occupies a distinct, non-overlapping 100s time slot.
		var events []ingest.LogEvent
		for i, blk := range blocks {
			slot := base.Add(time.Duration(i) * 100 * time.Second)
			events = append(events, blk.emit(slot)...)
		}

		got := failureIDs(engine.Evaluate(events))
		for id, n := range want {
			if got[id] != n {
				t.Fatalf("iter %d: rule %s want %d fires, got %d (blocks=%v)",
					iter, id, n, got[id], kinds(blocks))
			}
		}
		for id, n := range got {
			if want[id] != n {
				t.Fatalf("iter %d: false positive: rule %s fired %d times, want %d (blocks=%v)",
					iter, id, n, want[id], kinds(blocks))
			}
		}
	}
}

func runIDFor(iter, block int) string {
	return "iter" + itoa(iter) + "-blk" + itoa(block)
}

func actuatorFor(i int) string { return "act-" + itoa(i) }

func kinds(bs []signatureBlock) []sigKind {
	out := make([]sigKind, len(bs))
	for i, b := range bs {
		out[i] = b.kind
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
