# Log format

Test stations emit newline-delimited JSON. Each line is one log event. The
dashboard ingests these over a TCP socket or by tailing a file; both paths
parse identically through `ingest.ParseLine`.

## Wire fields

| Field         | Type              | Required | Notes |
|---------------|-------------------|----------|-------|
| `ts`          | string            | no       | RFC3339 / RFC3339Nano. If absent, ingest stamps receipt time. |
| `station_id`  | string            | yes      | Identifies the emitting station. |
| `run_id`      | string            | no       | Groups events into a bench run. Events without one are streamed but not persisted as a run. |
| `level`       | string            | yes      | One of `debug`, `info`, `warn`, `error`. Case-insensitive. |
| `subsystem`   | string            | no       | E.g. `actuator`, `controller`. Lower-cased on ingest. |
| `message`     | string            | no       | Human-readable text. The rule engine matches patterns against this. |
| `actuator_id` | string            | no       | Set when the event concerns a specific actuator. |
| `fields`      | object of numbers | no       | Structured numeric data, e.g. `{"current_a": 4.2}`. |

## Example line

```json
{"ts":"2026-05-18T22:14:03.512Z","station_id":"station-1","run_id":"station-1-run-007","level":"warn","subsystem":"actuator","message":"drive current sampled","actuator_id":"act-2","fields":{"current_a":4.1}}
```

## Normalization rules

- A blank line is skipped, not an error.
- Malformed JSON, an unknown `level`, an unparseable `ts`, or a missing
  `station_id` / `level` causes the line to be dropped with a warning. One
  bad line never stops the stream.
- `level` and `subsystem` are lower-cased so rule matching is stable.
- `ts` is converted to UTC.

## Persistence

`store.RecordEvent` persists each event with a `run_id` and upserts the
parent run row (`first_seen` on insert, `last_seen` on every event). Events
without a `run_id` are still fanned out over WebSocket but are not stored,
because the store is run-indexed.
