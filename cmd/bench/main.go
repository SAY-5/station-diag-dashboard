// Command bench runs the ingest-to-fan-out throughput sweep and writes a
// timestamped JSON report under bench/results. It measures the same pipeline
// the dashboard runs in production: rule evaluation over a sliding window and
// hub fan-out to K concurrent subscribers.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/benchcore"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"
)

func main() {
	lines := flag.Int("lines", 20000, "log lines to feed through the pipeline")
	reps := flag.Int("reps", 5, "repetitions per K; reported metrics are the median")
	rulesFile := flag.String("rules", "rules/actuator_signatures.yaml", "rule YAML file")
	outDir := flag.String("out", "bench/results", "directory for the JSON report")
	stdout := flag.Bool("stdout", false, "also print the report to stdout")
	flag.Parse()

	subs := []int{1, 10, 50}

	ruleData, err := os.ReadFile(*rulesFile)
	if err != nil {
		fail("read rules: " + err.Error())
	}
	engine, err := rules.Load(ruleData)
	if err != nil {
		fail("load rules: " + err.Error())
	}

	corpus := benchcore.GenerateLines(*lines)

	report := benchcore.Report{
		Timestamp:  time.Now().UTC().Format("20060102T150405Z"),
		GoOS:       runtime.GOOS,
		WindowSize: 256,
	}
	for _, k := range subs {
		res := benchcore.RunRepeated(engine, corpus, k, *reps)
		report.Results = append(report.Results, res)
		fmt.Printf("K=%-3d %10.0f events/sec  rule p50/p95/p99 %.1f/%.1f/%.1f us  fanout p50/p95/p99 %.1f/%.1f/%.1f us\n",
			res.Subscribers, res.EventsPerSec,
			res.RuleEvalP50US, res.RuleEvalP95US, res.RuleEvalP99US,
			res.FanoutP50US, res.FanoutP95US, res.FanoutP99US)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fail("mkdir results: " + err.Error())
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fail("marshal report: " + err.Error())
	}
	path := filepath.Join(*outDir, report.Timestamp+".json")
	if err := os.WriteFile(path, append(payload, '\n'), 0o644); err != nil {
		fail("write report: " + err.Error())
	}
	fmt.Println("report written:", path)

	if *stdout {
		fmt.Println(string(payload))
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "bench:", msg)
	os.Exit(1)
}
