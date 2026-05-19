# Failure correlation

The rule engine flags individual actuator failures. On a real bench a single
fault rarely shows up alone: it cascades. An actuator draws too much current,
the power rail it shares sags a few hundred milliseconds later, and an
encoder downstream loses lock. The rule engine sees three failures. An
operator looking at three separate failure highlights has to reconstruct, by
hand, that they are one event with one cause.

The `correlate` package does that reconstruction. It groups failures into
**incidents** and orders each incident so the probable root cause is first.

## Windowing

The Correlator holds one open incident per run. When a failure arrives:

- If its timestamp is within the **correlation window** of the open
  incident's most recent member, it joins that incident.
- Otherwise the open incident is closed and the failure starts a new one.

The window is configured by `correlation_window` in the service config and
defaults to **500ms**. 500ms comfortably spans an
overcurrent-to-rail-sag-to-encoder cascade on a typical bench while still
keeping genuinely independent faults, minutes apart, in separate incidents.

The comparison uses the absolute time gap, so a failure that arrives out of
order (an earlier timestamp delivered after a later one) still joins the
incident as long as it falls inside the window. Members are always sorted
earliest-timestamp-first inside the incident, with the rule id breaking
ties, so an incident is deterministic regardless of arrival order.

The window is measured member-to-member, not from the incident start. A
slow cascade where each step lands within the window of the previous step
stays a single incident even if the whole chain is longer than one window.
This is intentional: a fault that keeps propagating is one incident.

## Root-cause heuristic

Within an incident the **earliest-in-window subsystem is the probable root
cause**. The reasoning is causal ordering: in a cascade the subsystem that
failed first is the most likely origin, and the later failures are
downstream effects.

- `Incident.Members` is ordered earliest first, so `Members[0]` names the
  root-cause failure.
- `Incident.RootCause` repeats that subsystem for callers that do not want
  to index into the member list.
- `Incident.Subsystems()` returns the distinct subsystems in first-seen
  order: the full root-cause-to-effect chain.

This is a heuristic, not a proof of causation. It assumes the cascade
propagates forward in time and that the rule engine timestamps failures
from the originating event. It does not model the physical topology of the
bench: two unrelated subsystems that happen to fail close together will be
grouped, and the operator still has to confirm the causal link. The value
is collapsing three highlights into one band with a sensible default
ordering, not replacing the operator's judgement.

## Emitted incident

Each time a failure creates or extends an incident the pipeline:

1. Persists the incident with `store.SaveIncident` (an upsert keyed by
   incident id, so a growing incident replaces its previous row).
2. Fans an `incident` WebSocket message out to every dashboard client.

Because an incident grows, the same incident id is sent repeatedly; a client
keeps the latest message per id. A known multi-subsystem cascade therefore
surfaces as exactly **one** incident id, carrying every correlated failure,
with the root-cause subsystem first.

## API and dashboard

- `GET /api/incidents` returns correlated incidents across all runs, most
  recent first.
- `GET /api/runs/{id}` includes an `incidents` array for that run.
- The dashboard's failure timeline panel renders each incident as a
  horizontal band of subsystem segments, leftmost segment marked as the
  probable root cause.
