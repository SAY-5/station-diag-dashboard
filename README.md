# station-diag-dashboard

A diagnostics dashboard for a hardware test bench. Test stations emit
newline-delimited JSON log lines; the service ingests them, persists each
run, fans events out to browser dashboards over WebSocket, and runs a
YAML-driven rule engine that flags actuator failure signatures as they
happen. Operators can attach notes to a run and export a Markdown report.

## What this studies

This is a portfolio project built to work through three things end to end:

- **WebSocket fan-out.** A hub that assigns monotonic sequence numbers,
  keeps a bounded backlog, backfills a reconnecting client from a
  `last_seq` cursor, and drops a slow subscriber rather than stalling
  ingestion.
- **A YAML actuator-failure rule engine.** Three detector kinds (timeout,
  threshold, consecutive) over a sliding event window, with the rule set
  defined in data rather than code.
- **Cross-platform Go with pure-Go SQLite.** Persistence uses
  `modernc.org/sqlite`, which needs no cgo, so the same code builds and the
  test suite runs on both Linux and Windows. CI proves this with a
  `windows-latest` job.

## Modules

| Path                     | Responsibility |
|--------------------------|----------------|
| `cmd/dashboard`          | Service entry point: wires ingest, rules, hub, store, API. |
| `cmd/station-emitter`    | Test fixture: emits simulated bench-run log lines to a socket or stdout. |
| `internal/config`        | YAML service configuration with defaults. |
| `internal/ingest`        | Canonical `LogEvent` type; TCP and file-tail ingestion paths. |
| `internal/rules`         | YAML-driven actuator failure rule engine. |
| `internal/hub`           | WebSocket fan-out hub with sequencing and backlog. |
| `internal/store`         | SQLite persistence for runs, events, failures, notes. |
| `internal/api`           | REST handlers, WebSocket endpoint, Markdown export. |
| `internal/web`           | Embedded static dashboard frontend. |

## Quickstart

```sh
make build          # build both binaries into ./bin
make run            # start the dashboard on :8080 (ingest on :7000)
make emit           # in another shell: feed it simulated bench runs
```

Then open <http://localhost:8080>. With Docker:

```sh
make up             # dashboard + station-emitter via docker-compose
```

## Architecture

```
  test station                          browser dashboard
       |                                       ^
       | newline-delimited JSON                | WebSocket (seq, backlog,
       v  (TCP :7000 or tailed file)           |  last_seq reconnect)
  +----------+      +----------+      +-------------------+
  |  ingest  | ---> | pipeline | ---> |        hub        |
  | TCP/file |      |  sliding |      |  fan-out + seq    |
  +----------+      |  window  |      +-------------------+
                    +----+-----+
                         |  every event + detected failure
                         v
                  +-------------+        +-----------------+
                  | rule engine |        |  SQLite store   |
                  | YAML signatures      |  runs / events  |
                  +-------------+        |  failures/notes |
                                         +-----------------+
                                                 ^
                                                 | REST: list runs,
                                                 | run detail, notes,
                                                 | resolve, export .md
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for the detail.

## From 10 troubleshooting steps to 5

Without the dashboard, chasing one intermittent actuator fault on the bench
typically meant:

1. Notice a run looked wrong.
2. SSH into the station and find the right log file.
3. `grep` the log for the run window by timestamp.
4. Read the surrounding lines to guess which actuator was involved.
5. Recall which failure modes look like what (timeout vs overcurrent vs
   stuck) and check each by eye.
6. Cross-check the drive current numbers against the safe bound from memory
   or a spec sheet.
7. Re-run the bench to see if it reproduces.
8. Repeat the grep for the new run.
9. Write the finding into a separate notes doc or ticket.
10. Copy log excerpts into that doc by hand for the report.

With the dashboard the same investigation is:

1. See the run flagged live on the dashboard, with the actuator named.
2. Read the `failure_highlight` detail: it states the rule, the actuator,
   and the measured value or missing event (steps 3 to 6 above collapse
   into reading one line).
3. Re-run the bench; the next run is flagged automatically with no manual
   grep (steps 7 and 8 collapse into one).
4. Type the finding into the run's operator note in the same view (step 9).
5. Click export to get a Markdown report with the timeline, the failure
   table, and the notes already assembled (step 10).

The reduction is real because the rule engine does the log-reading and
pattern-recall (old steps 3 to 6, 8) and the export does the report
assembly (old step 10). It does not remove the need to re-run the bench or
to decide what the fix is.

## What this is not

- Not a replacement for a real test executive or instrument control
  software. It consumes logs; it does not drive hardware.
- Not tied to any specific instrument protocol. The actuator signatures in
  `rules/actuator_signatures.yaml` are illustrative bench failure modes.
- Not a metrics or time-series system. It stores discrete runs and events,
  not continuous telemetry, and has no alerting beyond the live highlight.
- Not multi-tenant or authenticated. It is a single-process internal tool;
  the WebSocket endpoint accepts any origin by design.
- Not horizontally scalable. The SQLite store is single-writer and the hub
  state is in-process.

## License

MIT. See [LICENSE](LICENSE).
