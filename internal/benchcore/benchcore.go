// Package benchcore drives a measured ingest-to-fan-out workload through the
// real pipeline: a JSON log line is parsed, persisted is skipped here to keep
// the measurement on the hot path (ingest -> rule engine -> hub fan-out), and
// the resulting event plus any failures are fanned out to K subscribers.
//
// It is shared by the `go test -bench` functions and the standalone bench
// command so both measure exactly the same code path.
package benchcore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/hub"
	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"
)

// Result is one measured run of the pipeline at a fixed subscriber count.
// When produced by RunRepeated the metrics are the median across Reps runs.
type Result struct {
	Subscribers   int     `json:"subscribers"`
	Lines         int     `json:"lines"`
	Reps          int     `json:"reps,omitempty"`
	DurationMS    float64 `json:"duration_ms"`
	EventsPerSec  float64 `json:"events_per_sec"`
	RuleEvalP50US float64 `json:"rule_eval_p50_us"`
	RuleEvalP95US float64 `json:"rule_eval_p95_us"`
	RuleEvalP99US float64 `json:"rule_eval_p99_us"`
	FanoutP50US   float64 `json:"fanout_p50_us"`
	FanoutP95US   float64 `json:"fanout_p95_us"`
	FanoutP99US   float64 `json:"fanout_p99_us"`
}

// Report bundles the results of a full bench sweep.
type Report struct {
	Timestamp  string   `json:"timestamp"`
	GoOS       string   `json:"goos"`
	WindowSize int      `json:"window_size"`
	Results    []Result `json:"results"`
}

// windowSize bounds the sliding rule-engine window. It mirrors the value the
// production pipeline uses in cmd/dashboard.
const windowSize = 256

// GenerateLines builds a deterministic corpus of pre-parsed log events that
// mixes healthy traffic with each failure signature. Parsing is excluded from
// the measured loop on purpose; ParseLine is covered by its own benchmark.
func GenerateLines(n int) []ingest.LogEvent {
	base := time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)
	out := make([]ingest.LogEvent, 0, n)
	for i := 0; i < n; i++ {
		runID := "bench-run-" + itoa(i/8)
		actuator := "act-" + itoa(i%4+1)
		ts := base.Add(time.Duration(i) * 10 * time.Millisecond)
		var ev ingest.LogEvent
		switch i % 8 {
		case 3:
			ev = ingest.LogEvent{TS: ts, StationID: "bench", RunID: runID,
				Level: "warn", Subsystem: "actuator", ActuatorID: actuator,
				Message: "drive current sampled",
				Fields:  map[string]float64{"current_a": 4.6}}
		case 6:
			ev = ingest.LogEvent{TS: ts, StationID: "bench", RunID: runID,
				Level: "warn", Subsystem: "power", ActuatorID: actuator,
				Message: "rail voltage sampled",
				Fields:  map[string]float64{"rail_v": 19.0}}
		default:
			msg := "move_command issued"
			if i%2 == 1 {
				msg = "position_reached confirmed"
			}
			ev = ingest.LogEvent{TS: ts, StationID: "bench", RunID: runID,
				Level: "info", Subsystem: "actuator", ActuatorID: actuator,
				Message: msg}
		}
		out = append(out, ev)
	}
	return out
}

// Run feeds the corpus through the rule engine and hub with subs subscribers
// attached, measuring sustained throughput and per-stage latency.
func Run(engine *rules.Engine, corpus []ingest.LogEvent, subs int) Result {
	h := hub.New(windowSize * 2)
	defer h.Shutdown(context.Background())

	// Attach subscribers and drain them concurrently so a slow drain never
	// stalls the producer; the hub drops a slow subscriber by design.
	var wg sync.WaitGroup
	subscribers := make([]*hub.Subscriber, subs)
	for i := 0; i < subs; i++ {
		s := h.Subscribe(0)
		subscribers[i] = s
		wg.Add(1)
		go func(sub *hub.Subscriber) {
			defer wg.Done()
			for range sub.C() {
			}
		}(s)
	}

	window := make([]ingest.LogEvent, 0, windowSize)
	ruleSamples := make([]time.Duration, 0, len(corpus))
	fanoutSamples := make([]time.Duration, 0, len(corpus))

	start := time.Now()
	for _, ev := range corpus {
		fanStart := time.Now()
		h.Publish(ev)
		fanoutSamples = append(fanoutSamples, time.Since(fanStart))

		window = append(window, ev)
		if len(window) > windowSize {
			window = window[len(window)-windowSize:]
		}
		ruleStart := time.Now()
		failures := engine.Evaluate(window)
		ruleSamples = append(ruleSamples, time.Since(ruleStart))
		for i := range failures {
			h.PublishFailure(failures[i])
		}
	}
	elapsed := time.Since(start)

	h.Shutdown(context.Background())
	wg.Wait()

	eps := 0.0
	if elapsed > 0 {
		eps = float64(len(corpus)) / elapsed.Seconds()
	}
	return Result{
		Subscribers:   subs,
		Lines:         len(corpus),
		DurationMS:    float64(elapsed.Microseconds()) / 1000.0,
		EventsPerSec:  eps,
		RuleEvalP50US: percentileUS(ruleSamples, 50),
		RuleEvalP95US: percentileUS(ruleSamples, 95),
		RuleEvalP99US: percentileUS(ruleSamples, 99),
		FanoutP50US:   percentileUS(fanoutSamples, 50),
		FanoutP95US:   percentileUS(fanoutSamples, 95),
		FanoutP99US:   percentileUS(fanoutSamples, 99),
	}
}

// RunRepeated runs the pipeline reps times at the given subscriber count and
// returns the per-metric median. A single short run jitters heavily on shared
// CI hardware; the median across repetitions is the stable figure the
// regression gate compares. reps is clamped to at least 1.
func RunRepeated(engine *rules.Engine, corpus []ingest.LogEvent, subs, reps int) Result {
	if reps < 1 {
		reps = 1
	}
	runs := make([]Result, reps)
	for i := 0; i < reps; i++ {
		runs[i] = Run(engine, corpus, subs)
	}
	pick := func(get func(Result) float64) float64 {
		vals := make([]float64, len(runs))
		for i, r := range runs {
			vals[i] = get(r)
		}
		sort.Float64s(vals)
		return vals[len(vals)/2]
	}
	return Result{
		Subscribers:   subs,
		Lines:         len(corpus),
		Reps:          reps,
		DurationMS:    pick(func(r Result) float64 { return r.DurationMS }),
		EventsPerSec:  pick(func(r Result) float64 { return r.EventsPerSec }),
		RuleEvalP50US: pick(func(r Result) float64 { return r.RuleEvalP50US }),
		RuleEvalP95US: pick(func(r Result) float64 { return r.RuleEvalP95US }),
		RuleEvalP99US: pick(func(r Result) float64 { return r.RuleEvalP99US }),
		FanoutP50US:   pick(func(r Result) float64 { return r.FanoutP50US }),
		FanoutP95US:   pick(func(r Result) float64 { return r.FanoutP95US }),
		FanoutP99US:   pick(func(r Result) float64 { return r.FanoutP99US }),
	}
}

// percentileUS returns the p-th percentile of d in microseconds.
func percentileUS(d []time.Duration, p int) float64 {
	if len(d) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(d))
	copy(cp, d)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := (p * (len(cp) - 1)) / 100
	return float64(cp[idx].Nanoseconds()) / 1000.0
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
