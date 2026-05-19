# Run comparison

Re-running a bench after a change is the core debugging loop: did the change
clear the failure, did it leave something behind, did it break something
new. The `runcompare` package answers that by diffing two stored runs, and
`GET /api/runs/{a}/compare/{b}` exposes the diff.

Run A is the **baseline**; run B is the **compared** run. The diff is read
as "what changed going from A to B".

## Failure classification

Two failures are considered the same when they share a `FailureKey`: the
same `rule_id` against the same `actuator_id`. The detail text and the
timestamp are expected to differ between runs, so they are not part of the
key. If a run flags the same signature more than once, only the first
occurrence is kept; for the diff a run either has a failure or it does not.

Every failure is then classified:

- **new** is in run B but not run A. This is a regression: run B introduced
  a failure that the baseline did not have.
- **resolved** is in run A but not run B. This is a fix: a failure the
  baseline had is gone in run B.
- **persisting** is in both runs. Still broken; the change did not touch it.

The detail string on a new or persisting change is taken from run B; on a
resolved change it is taken from run A, so it always describes a real
occurrence of that failure.

## Actuator status changes

An actuator is "failing" in a run when at least one failure is keyed to it.
`actuator_changes` lists every actuator whose failing status flipped between
the runs: a `was_failing` to `now_failing` transition. An actuator that was
failing in both runs, or clean in both, is not listed because nothing
changed. Failures with no actuator id are ignored here.

## Per-subsystem deltas

`subsystem_deltas` reports, for every subsystem seen in either run:

- `failures_a` / `failures_b`: the failure count per run, and
  `failure_delta` (B minus A).
- `span_a_ms` / `span_b_ms`: the subsystem's span, the time between its
  first and last event in the run, and `span_delta_ms` (B minus A).

The span delta surfaces a slow-down that did not cross any rule threshold: a
subsystem taking noticeably longer in run B is visible even when no failure
was flagged. It is wall-clock span over the run's events, not a profiler
measurement, so treat it as a directional signal rather than a precise
timing.

## Output formats

- `GET /api/runs/{a}/compare/{b}` returns the diff as JSON.
- `GET /api/runs/{a}/compare/{b}?format=md` returns the same diff as a
  Markdown report attachment: a summary line, a table per failure class, the
  actuator status table, and the per-subsystem delta table.

The dashboard's run comparison panel picks two runs from dropdowns, renders
the diff inline with new failures highlighted, and links the Markdown
export.

## What it does not do

- It compares stored runs only. A run must have been ingested and persisted
  before it can be diffed.
- It does not pair up individual failure occurrences or correlate timing
  within an incident. That is the job of the `correlate` package; this diff
  works at the run-vs-run level.
- A missing failure is reported as resolved even if it simply did not
  reproduce. An intermittent fault that did not fire in run B will show as
  resolved; the operator still decides whether the fix is real.
