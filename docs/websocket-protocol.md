# WebSocket protocol

The dashboard streams live events to browser clients over a single
WebSocket endpoint. The server is the only sender of application messages;
the client is receive-only.

## Endpoint

```
GET /ws
GET /ws?last_seq=<n>
```

`last_seq` is a reconnect cursor. On connect, the hub backfills every
retained message with a sequence number greater than `last_seq`, then
streams live messages. A fresh client passes `0` (or omits the parameter)
and receives the full retained backlog.

## Sequence numbers

Every message carries a monotonic `seq` assigned by the hub. The hub keeps a
bounded backlog (default 500 messages, set by `backlog_size`). A client that
records the highest `seq` it has seen can reconnect with that value as
`last_seq` and resume without gaps, as long as the gap is smaller than the
backlog.

If a client is too slow to drain its channel, the hub drops it rather than
blocking ingestion. The client is expected to reconnect with its last
`seq`.

## Message envelope

```json
{
  "seq": 128,
  "kind": "failure_highlight",
  "event":   { ... },
  "failure": { ... },
  "note":    { ... }
}
```

Exactly one of `event`, `failure`, `note` is populated, selected by `kind`.

### `kind: "log_event"`

`event` carries a normalized log event.

```json
{"seq":42,"kind":"log_event","event":{"seq":42,"ts":"2026-05-18T22:14:03.512Z","station_id":"station-1","run_id":"station-1-run-007","level":"warn","subsystem":"actuator","message":"drive current sampled","actuator_id":"act-2","fields":{"current_a":4.1}}}
```

### `kind: "failure_highlight"`

`failure` carries a detected actuator failure from the rule engine.

```json
{"seq":43,"kind":"failure_highlight","failure":{"rule_id":"actuator_overcurrent","station_id":"station-1","run_id":"station-1-run-007","actuator_id":"act-2","detail":"current_a=4.1 crossed gt bound 3.5","severity":"error","at":"2026-05-18T22:14:03.512Z"}}
```

### `kind: "note_added"`

`note` carries an operator note posted through the REST API.

```json
{"seq":44,"kind":"note_added","note":{"id":7,"station_id":"station-1","run_id":"station-1-run-007","author":"tech-2","body":"swapped the drive cable","created_at":"2026-05-18T22:20:00Z"}}
```

## Keepalive

The server sends a WebSocket ping every 50 seconds and expects a pong
within 60 seconds. A client that does not pong is closed. Browser
WebSocket clients answer pings automatically.

## Shutdown

On service shutdown the server sends a close frame
(`CloseGoingAway`, "server shutdown"). Clients should reconnect with their
last `seq`.
