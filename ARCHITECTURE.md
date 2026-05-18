# Architecture

The service is a single Go process. Log lines enter through an ingestion
path, pass through a pipeline that persists, fans out, and evaluates them,
and reach browser dashboards over WebSocket. A REST API serves stored runs
and accepts operator notes.

## Ingestion and normalization

Two ingestion paths exist and both produce the same `ingest.LogEvent`:

- **TCP listener** (`internal/ingest/tcp.go`). Accepts station connections
  on a TCP port and scans newline-delimited JSON. Each connection is
  handled in its own goroutine; one slow or broken station does not affect
  others.
- **File tail** (`internal/ingest/filetail.go`). Reads an existing log file
  to its end, then polls for appended lines. Polling is used instead of
  inotify so the path behaves identically on Windows and Linux.

Both paths call `ingest.ParseLine`, which is the single normalization
point. It validates required fields (`station_id`, `level`), lower-cases
`level` and `subsystem`, parses `ts` to UTC (stamping receipt time if
absent), and rejects unknown log levels. A bad line is dropped with a
warning; the stream continues. This means downstream code, the rule engine
and the store, only ever sees well-formed events.

## Pipeline

`cmd/dashboard/pipeline.go` implements the `ingest.Sink` interface and is
the one place every event flows through. For each event it:

1. Persists the event and upserts its run row in the store.
2. Publishes the event to the hub for live fan-out.
3. Appends the event to a 256-entry sliding window and re-evaluates the
   whole window with the rule engine.
4. De-duplicates detected failures by the key
   `rule|run|actuator|detail`, so a failure that stays matched as the
   window slides is only recorded and broadcast once.
5. Persists and broadcasts each newly detected failure.

The pipeline holds a mutex around the window and the seen-set, so multiple
ingestion goroutines can publish concurrently.

## Rule engine model

The rule engine (`internal/rules`) is pure: `Evaluate(events)` returns
`[]Failure` with no side effects, which makes it straightforward to unit
test and to call repeatedly from the pipeline.

Rules are loaded from YAML and validated at startup. Three detector kinds
are implemented:

- **timeout**: a trigger event with no resolve event within a duration, or
  a trigger still unresolved at the end of the window.
- **threshold**: a numeric field in `fields` crossing a `gt`/`lt` bound.
- **consecutive**: N trigger events in a row for one run and actuator with
  no resolve between them.

Timeout and consecutive detectors key their per-actuator state on
`(run_id, actuator_id)`, so interleaved runs do not cross-contaminate.
Pattern matching is case-insensitive substring matching against the log
message. See [docs/rule-engine.md](docs/rule-engine.md).

## WebSocket hub and reconnect cursor

The hub (`internal/hub`) is the fan-out point. Every published message
(log event, failure highlight, operator note) is assigned a monotonic
`seq`, appended to a bounded backlog, and pushed to every subscriber.

Each subscriber has a buffered channel. If a subscriber cannot keep up and
its buffer fills, the hub unsubscribes it rather than blocking ingestion.
The dropped client is expected to reconnect.

On connect, a subscriber passes a `last_seq` cursor. The hub replays every
retained message with `seq > last_seq` before attaching the subscriber to
the live stream. A reconnecting client that recorded its highest `seq`
resumes without gaps, provided the gap is within the backlog size. A fresh
client passes `0` and receives the whole retained backlog. See
[docs/websocket-protocol.md](docs/websocket-protocol.md).

The `internal/api/ws.go` layer bridges one WebSocket connection to one hub
subscription, with separate read and write pumps, ping/pong keepalive, and
a clean close frame on shutdown.

## Persistence

`internal/store` uses SQLite through `modernc.org/sqlite`, a pure-Go
driver. Four tables hold runs, events, failures, and notes. The connection
pool is capped at one open connection because SQLite is single-writer; for
a single-process bench tool this is correct and simple. Run summaries are
computed with correlated subqueries over the events and failures tables.

## Cross-platform notes

The project builds and tests on Windows and Linux. Three choices make that
hold:

- **Pure-Go SQLite.** `modernc.org/sqlite` needs no cgo and no C
  toolchain, so the Windows CI job builds with the stock Go toolchain.
- **Polling file tail.** The file ingestion path polls instead of using
  inotify or ReadDirectoryChangesW, so one implementation serves both
  platforms.
- **Forward-slash embed paths.** `internal/web` embeds the frontend with
  `//go:embed`, whose paths are always forward-slash regardless of OS.

CI runs the `test` job on both `ubuntu-latest` and `windows-latest`, and
`GOOS=windows go build ./...` is part of local verification.

## Frontend

`internal/web` embeds a small static dashboard (HTML, JS, CSS) with
`//go:embed`. The API server serves it at `/`. The frontend opens the
`/ws` WebSocket, renders the live event stream and failure highlights, and
calls the REST API for run detail, notes, and export.
