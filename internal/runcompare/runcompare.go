// Package runcompare diffs two test-station runs.
//
// Re-running a bench after a fix is the core debugging loop: did the fix
// clear the failure, did it leave anything behind, did it break something
// new. This package answers that by classifying the failures of run B
// against run A:
//
//   - new       failures present in B but not in A (a regression)
//   - resolved  failures present in A but not in B (a fix)
//   - persisting failures present in both (still broken)
//
// It also reports, per subsystem, how the failure count and the run timing
// changed, so a slow-down that did not cross a rule threshold is still
// visible.
package runcompare

import (
	"sort"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/rules"
	"github.com/SAY-5/station-diag-dashboard/internal/store"
)

// FailureKey identifies a failure for diffing. Two failures are "the same"
// when they come from the same rule against the same actuator; the detail
// text and timestamp are expected to differ between runs.
type FailureKey struct {
	RuleID   string `json:"rule_id"`
	Actuator string `json:"actuator_id"`
}

// FailureChange is one failure classified by the diff.
type FailureChange struct {
	RuleID    string `json:"rule_id"`
	Actuator  string `json:"actuator_id"`
	Subsystem string `json:"subsystem"`
	Severity  string `json:"severity"`
	// Detail is taken from run B for new and persisting failures, and from
	// run A for resolved failures, so it always describes a real occurrence.
	Detail string `json:"detail"`
}

// SubsystemDelta reports how one subsystem changed between the runs.
type SubsystemDelta struct {
	Subsystem    string `json:"subsystem"`
	FailuresA    int    `json:"failures_a"`
	FailuresB    int    `json:"failures_b"`
	FailureDelta int    `json:"failure_delta"`
	SpanDeltaMS  int64  `json:"span_delta_ms"`
	SpanAMS      int64  `json:"span_a_ms"`
	SpanBMS      int64  `json:"span_b_ms"`
}

// ActuatorChange reports an actuator whose failure status flipped.
type ActuatorChange struct {
	Actuator string `json:"actuator_id"`
	// WasFailing and NowFailing describe the actuator in run A and run B.
	WasFailing bool `json:"was_failing"`
	NowFailing bool `json:"now_failing"`
}

// Diff is the full comparison of run B against run A.
type Diff struct {
	RunA string `json:"run_a"`
	RunB string `json:"run_b"`

	NewFailures        []FailureChange `json:"new_failures"`
	ResolvedFailures   []FailureChange `json:"resolved_failures"`
	PersistingFailures []FailureChange `json:"persisting_failures"`

	ActuatorChanges []ActuatorChange `json:"actuator_changes"`
	SubsystemDeltas []SubsystemDelta `json:"subsystem_deltas"`
}

// RunInput is everything the diff needs about one run.
type RunInput struct {
	Run      store.Run
	Failures []rules.Failure
	Events   []store.RunEvent
}

// Compare diffs run B against run A. The result classifies every failure as
// new, resolved or persisting, lists actuators whose status flipped, and
// reports per-subsystem failure-count and timing deltas.
func Compare(a, b RunInput) Diff {
	d := Diff{RunA: a.Run.RunID, RunB: b.Run.RunID}

	indexA := indexFailures(a.Failures)
	indexB := indexFailures(b.Failures)

	for k, fb := range indexB {
		if _, ok := indexA[k]; ok {
			d.PersistingFailures = append(d.PersistingFailures, changeOf(fb))
		} else {
			d.NewFailures = append(d.NewFailures, changeOf(fb))
		}
	}
	for k, fa := range indexA {
		if _, ok := indexB[k]; !ok {
			d.ResolvedFailures = append(d.ResolvedFailures, changeOf(fa))
		}
	}
	sortChanges(d.NewFailures)
	sortChanges(d.ResolvedFailures)
	sortChanges(d.PersistingFailures)

	d.ActuatorChanges = actuatorChanges(indexA, indexB)
	d.SubsystemDeltas = subsystemDeltas(a, b)
	return d
}

func indexFailures(fs []rules.Failure) map[FailureKey]rules.Failure {
	m := make(map[FailureKey]rules.Failure, len(fs))
	for _, f := range fs {
		// Keep the first occurrence of a (rule, actuator) pair: a run can
		// flag the same signature more than once, but for diffing the run
		// either has the failure or it does not.
		k := FailureKey{RuleID: f.RuleID, Actuator: f.Actuator}
		if _, ok := m[k]; !ok {
			m[k] = f
		}
	}
	return m
}

func changeOf(f rules.Failure) FailureChange {
	return FailureChange{
		RuleID:    f.RuleID,
		Actuator:  f.Actuator,
		Subsystem: f.SubsystemOrActuator(),
		Severity:  f.Severity,
		Detail:    f.Detail,
	}
}

func sortChanges(cs []FailureChange) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].RuleID != cs[j].RuleID {
			return cs[i].RuleID < cs[j].RuleID
		}
		return cs[i].Actuator < cs[j].Actuator
	})
}

// actuatorChanges lists actuators whose failing/not-failing status differs
// between the runs.
func actuatorChanges(a, b map[FailureKey]rules.Failure) []ActuatorChange {
	failingA := actuatorSet(a)
	failingB := actuatorSet(b)

	seen := map[string]struct{}{}
	for act := range failingA {
		seen[act] = struct{}{}
	}
	for act := range failingB {
		seen[act] = struct{}{}
	}

	var out []ActuatorChange
	for act := range seen {
		if act == "" {
			continue
		}
		_, wasFailing := failingA[act]
		_, nowFailing := failingB[act]
		if wasFailing != nowFailing {
			out = append(out, ActuatorChange{
				Actuator:   act,
				WasFailing: wasFailing,
				NowFailing: nowFailing,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Actuator < out[j].Actuator
	})
	return out
}

func actuatorSet(m map[FailureKey]rules.Failure) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range m {
		if k.Actuator != "" {
			out[k.Actuator] = struct{}{}
		}
	}
	return out
}

// subsystemDeltas reports, per subsystem, the change in failure count and in
// run span. The span of a subsystem is the time between its first and last
// event in the run; a positive span delta means run B took longer.
func subsystemDeltas(a, b RunInput) []SubsystemDelta {
	failA := failuresBySubsystem(a.Failures)
	failB := failuresBySubsystem(b.Failures)
	spanA := spanBySubsystem(a.Events)
	spanB := spanBySubsystem(b.Events)

	seen := map[string]struct{}{}
	for s := range failA {
		seen[s] = struct{}{}
	}
	for s := range failB {
		seen[s] = struct{}{}
	}
	for s := range spanA {
		seen[s] = struct{}{}
	}
	for s := range spanB {
		seen[s] = struct{}{}
	}

	var out []SubsystemDelta
	for s := range seen {
		sa := int64(spanA[s] / time.Millisecond)
		sb := int64(spanB[s] / time.Millisecond)
		out = append(out, SubsystemDelta{
			Subsystem:    s,
			FailuresA:    failA[s],
			FailuresB:    failB[s],
			FailureDelta: failB[s] - failA[s],
			SpanAMS:      sa,
			SpanBMS:      sb,
			SpanDeltaMS:  sb - sa,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Subsystem < out[j].Subsystem
	})
	return out
}

func failuresBySubsystem(fs []rules.Failure) map[string]int {
	out := map[string]int{}
	for _, f := range fs {
		out[f.SubsystemOrActuator()]++
	}
	return out
}

// spanBySubsystem returns, per subsystem, the duration between the earliest
// and latest event in that subsystem.
func spanBySubsystem(events []store.RunEvent) map[string]time.Duration {
	type bounds struct {
		first, last time.Time
	}
	b := map[string]*bounds{}
	for _, e := range events {
		sub := e.Subsystem
		if sub == "" {
			sub = "unknown"
		}
		cur, ok := b[sub]
		if !ok {
			b[sub] = &bounds{first: e.TS, last: e.TS}
			continue
		}
		if e.TS.Before(cur.first) {
			cur.first = e.TS
		}
		if e.TS.After(cur.last) {
			cur.last = e.TS
		}
	}
	out := make(map[string]time.Duration, len(b))
	for sub, bb := range b {
		out[sub] = bb.last.Sub(bb.first)
	}
	return out
}
