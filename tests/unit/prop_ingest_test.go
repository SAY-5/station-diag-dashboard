package unit

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
)

// Property test for LogEvent normalization.
//
// ParseLine is fed random byte sequences, random near-JSON, and random
// well-formed JSON. The invariant is that ParseLine is total: for any input
// it either returns a valid LogEvent or a non-nil structured error, and it
// never panics. When it does return an event the required fields must be
// populated and the level must be one of the four canonical values.

var levels = []string{"debug", "info", "warn", "error"}

func randString(rng *rand.Rand, maxLen int) string {
	n := rng.Intn(maxLen + 1)
	var b strings.Builder
	for i := 0; i < n; i++ {
		// Printable ASCII plus the occasional control byte and quote.
		switch rng.Intn(10) {
		case 0:
			b.WriteByte(byte(rng.Intn(32)))
		case 1:
			b.WriteByte('"')
		case 2:
			b.WriteByte('\\')
		default:
			b.WriteByte(byte(rng.Intn(95) + 32))
		}
	}
	return b.String()
}

func TestPropertyParseLineNeverPanics(t *testing.T) {
	const iterations = 4000
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(iter) + 1))

		var line string
		switch rng.Intn(4) {
		case 0:
			// Pure random bytes.
			line = randString(rng, 120)
		case 1:
			// Near-JSON: starts like an object, truncated or corrupted.
			line = "{" + randString(rng, 80)
		case 2:
			// JSON-shaped with random keys and values.
			line = "{\"" + randString(rng, 12) + "\":\"" + randString(rng, 20) + "\"}"
		default:
			// Random whitespace, sometimes empty.
			line = strings.Repeat(" ", rng.Intn(6))
		}

		ev, err := safeParse(t, iter, line)
		if err != nil {
			// A structured error is a valid outcome; nothing more to check.
			if err.Error() == "" {
				t.Fatalf("iter %d: empty error string for %q", iter, line)
			}
			continue
		}
		// A successful parse must yield a well-formed event.
		if ev.StationID == "" {
			t.Fatalf("iter %d: parsed event has empty station_id: %q", iter, line)
		}
		if !contains(levels, ev.Level) {
			t.Fatalf("iter %d: parsed event has non-canonical level %q: %q",
				iter, ev.Level, line)
		}
	}
}

// TestPropertyParseLineRoundTripsWellFormed feeds randomly generated but
// always-valid JSON and asserts it always parses into a faithful event.
func TestPropertyParseLineRoundTripsWellFormed(t *testing.T) {
	const iterations = 2000
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(iter) + 9001))

		station := "s" + itoa(rng.Intn(1000)+1)
		level := levels[rng.Intn(len(levels))]
		// Upper-case the level half the time to exercise normalization.
		wireLevel := level
		if rng.Intn(2) == 0 {
			wireLevel = strings.ToUpper(level)
		}
		msg := "evt" + itoa(rng.Intn(100000))

		line := `{"station_id":"` + station + `","level":"` + wireLevel +
			`","subsystem":"actuator","message":"` + msg + `"}`

		ev, err := safeParse(t, iter, line)
		if err != nil {
			t.Fatalf("iter %d: well-formed line rejected: %q err=%v", iter, line, err)
		}
		if ev.StationID != station {
			t.Fatalf("iter %d: station mismatch: got %q want %q", iter, ev.StationID, station)
		}
		if ev.Level != level {
			t.Fatalf("iter %d: level not normalized: got %q want %q", iter, ev.Level, level)
		}
		if ev.Message != msg {
			t.Fatalf("iter %d: message mismatch: got %q want %q", iter, ev.Message, msg)
		}
	}
}

// safeParse runs ParseLine and converts any panic into a test failure.
func safeParse(t *testing.T, iter int, line string) (ev ingest.LogEvent, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("iter %d: ParseLine panicked on %q: %v", iter, line, r)
		}
	}()
	return ingest.ParseLine([]byte(line))
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}
