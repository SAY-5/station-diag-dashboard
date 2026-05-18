# Rule engine

The rule engine detects actuator failure signatures. It is defined in
`internal/rules` and configured by a YAML document (default
`rules/actuator_signatures.yaml`). The engine is pure: `Evaluate` takes an
ordered slice of events and returns the failures found, with no side
effects.

## Rule kinds

The `kind` field selects the detector.

### `timeout`

A trigger event with no matching resolve event inside a time window.

| Field             | Purpose |
|-------------------|---------|
| `trigger_pattern` | Substring matched against `message` to open a pending state. |
| `resolve_pattern` | Substring that closes the pending state. |
| `within`          | Go duration. If the resolve arrives later than this, a failure fires. A pending state still open at the end of the window also fires. |

### `threshold`

A numeric field in `fields` crosses a bound.

| Field       | Purpose |
|-------------|---------|
| `field`     | Key in the event `fields` object. |
| `operator`  | `gt` or `lt`. |
| `threshold` | The bound. |

### `consecutive`

`count` trigger events in a row for the same run and actuator with no
resolve event between them.

| Field             | Purpose |
|-------------------|---------|
| `trigger_pattern` | Substring that increments the streak. |
| `resolve_pattern` | Substring that clears the streak. |
| `count`           | Streak length that fires a failure (must be >= 2). |

## Common fields

| Field         | Purpose |
|---------------|---------|
| `id`          | Unique rule identifier. Required. |
| `description` | Human-readable explanation. |
| `subsystem`   | Optional scope: the rule only sees events from this subsystem. |
| `level`       | Optional scope: the rule only sees events at this log level. |
| `severity`    | Severity stamped on the failure. Defaults to `error`. |

## Grouping

Timeout and consecutive detectors key their state on the pair
`(run_id, actuator_id)`, so interleaved events from different runs or
actuators do not interfere.

## Pattern matching

`trigger_pattern` and `resolve_pattern` are case-insensitive substring
matches against `message`, not regular expressions. This keeps rule
authoring simple and predictable for bench operators.

## Sliding window

The dashboard pipeline (`cmd/dashboard/pipeline.go`) keeps the last 256
events in a sliding window and re-evaluates the whole window on each new
event. Every failure key (`rule|run|actuator|detail`) is recorded once, so
the same failure is not flagged twice as the window slides.

## Validation

`rules.Load` validates every rule at startup. A missing `id`, an unknown
`kind`, a `timeout` without a positive `within`, a `threshold` with an
operator other than `gt`/`lt`, or a `consecutive` with `count < 2` is a
load-time error and the service refuses to start.
