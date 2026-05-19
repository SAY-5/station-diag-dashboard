package unit

import (
	"testing"

	"github.com/SAY-5/station-diag-dashboard/internal/benchcore"
)

// BenchmarkIngestThroughput measures sustained ingest-to-fan-out throughput
// at K = 1, 10 and 50 concurrent subscribers. Run with:
//
//	go test -run=^$ -bench=BenchmarkIngestThroughput ./tests/unit/...
func BenchmarkIngestThroughput(b *testing.B) {
	engine := loadBenchEngine(b)
	for _, k := range []int{1, 10, 50} {
		k := k
		b.Run("K="+itoa(k), func(b *testing.B) {
			corpus := benchcore.GenerateLines(2000)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				res := benchcore.Run(engine, corpus, k)
				b.ReportMetric(res.EventsPerSec, "events/sec")
				b.ReportMetric(res.RuleEvalP99US, "rule-p99-us")
				b.ReportMetric(res.FanoutP99US, "fanout-p99-us")
			}
		})
	}
}

// TestBenchSmoke runs a tiny sweep so CI proves the bench harness builds and
// executes without measuring anything for regression. The bench-regress gate
// lives in scripts/bench-regress.sh and is exercised by `make bench-regress`.
func TestBenchSmoke(t *testing.T) {
	engine := loadBenchEngine(t)
	corpus := benchcore.GenerateLines(500)
	for _, k := range []int{1, 10, 50} {
		res := benchcore.Run(engine, corpus, k)
		if res.Lines != 500 {
			t.Fatalf("K=%d: expected 500 lines, got %d", k, res.Lines)
		}
		if res.EventsPerSec <= 0 {
			t.Fatalf("K=%d: non-positive throughput %.2f", k, res.EventsPerSec)
		}
		if res.Subscribers != k {
			t.Fatalf("K=%d: result reports %d subscribers", k, res.Subscribers)
		}
	}
}

// TestRunRepeated checks the median-aggregating sweep used by cmd/bench and
// the regression gate: it must record the repetition count, keep the line
// count, and return a positive throughput for every K.
func TestRunRepeated(t *testing.T) {
	engine := loadBenchEngine(t)
	corpus := benchcore.GenerateLines(400)
	for _, k := range []int{1, 10} {
		res := benchcore.RunRepeated(engine, corpus, k, 3)
		if res.Reps != 3 {
			t.Fatalf("K=%d: expected 3 reps, got %d", k, res.Reps)
		}
		if res.Lines != 400 {
			t.Fatalf("K=%d: expected 400 lines, got %d", k, res.Lines)
		}
		if res.EventsPerSec <= 0 {
			t.Fatalf("K=%d: non-positive throughput %.2f", k, res.EventsPerSec)
		}
		if res.RuleEvalP99US < res.RuleEvalP50US {
			t.Fatalf("K=%d: P99 %.1f below P50 %.1f", k,
				res.RuleEvalP99US, res.RuleEvalP50US)
		}
	}
	// reps below 1 is clamped to a single run.
	if got := benchcore.RunRepeated(engine, corpus, 1, 0); got.Reps != 1 {
		t.Fatalf("reps=0 should clamp to 1, got %d", got.Reps)
	}
}
