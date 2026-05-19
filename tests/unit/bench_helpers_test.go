package unit

import (
	"os"
	"testing"

	"github.com/SAY-5/station-diag-dashboard/internal/rules"
)

// loadBenchEngine loads the production rule set for benchmarks and the bench
// smoke test. It accepts testing.TB so the same helper serves *testing.T and
// *testing.B.
func loadBenchEngine(tb testing.TB) *rules.Engine {
	tb.Helper()
	data, err := os.ReadFile("../../rules/actuator_signatures.yaml")
	if err != nil {
		tb.Fatalf("read rules: %v", err)
	}
	e, err := rules.Load(data)
	if err != nil {
		tb.Fatalf("load engine: %v", err)
	}
	return e
}
