"use strict";

// Dashboard frontend. Connects to /ws, renders a live event feed, lists
// runs from the REST API, highlights flagged failures, and lets an operator
// attach notes, mark a run resolved, and export a report.

const state = {
  lastSeq: 0,
  eventCount: 0,
  failedRuns: new Set(),
  selectedRun: null,
  ws: null,
  reconnectDelay: 1000,
  // incidents is keyed by incident id; the correlator re-sends a growing
  // incident under the same id, so the latest message replaces the prior.
  incidents: new Map(),
};

const el = (id) => document.getElementById(id);

function setConn(status) {
  const dot = el("conn-dot");
  const text = el("conn-text");
  dot.className = "dot " + (status === "live" ? "dot-on"
    : status === "connecting" ? "dot-wait" : "dot-off");
  text.textContent = status;
}

function connect() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const url = `${proto}//${location.host}/ws?last_seq=${state.lastSeq}`;
  setConn("connecting");
  const ws = new WebSocket(url);
  state.ws = ws;

  ws.onopen = () => {
    setConn("live");
    state.reconnectDelay = 1000;
  };
  ws.onmessage = (evt) => {
    let msg;
    try { msg = JSON.parse(evt.data); } catch (_) { return; }
    if (typeof msg.seq === "number" && msg.seq > state.lastSeq) {
      state.lastSeq = msg.seq;
    }
    handleMessage(msg);
  };
  ws.onclose = () => {
    setConn("offline");
    setTimeout(connect, state.reconnectDelay);
    state.reconnectDelay = Math.min(state.reconnectDelay * 2, 15000);
  };
  ws.onerror = () => ws.close();
}

function handleMessage(msg) {
  if (msg.kind === "log_event" && msg.event) {
    appendEvent(msg.event, false);
  } else if (msg.kind === "failure_highlight" && msg.failure) {
    markFailure(msg.failure);
  } else if (msg.kind === "note_added" && msg.note) {
    if (state.selectedRun === msg.note.run_id) {
      loadRunDetail(msg.note.run_id);
    }
  } else if (msg.kind === "incident" && msg.incident) {
    state.incidents.set(msg.incident.id, msg.incident);
    renderIncidents();
  }
}

function appendEvent(ev, isFailure) {
  const feed = el("feed");
  const li = document.createElement("li");
  if (isFailure) li.className = "failure";

  const ts = document.createElement("span");
  ts.className = "ts";
  ts.textContent = (ev.ts || "").replace("T", " ").slice(11, 23);

  const lvl = document.createElement("span");
  lvl.className = "lvl lvl-" + (ev.level || "info");
  lvl.textContent = (ev.level || "info").toUpperCase();

  const station = document.createElement("span");
  station.className = "station";
  station.textContent = ev.station_id || "?";

  const sub = document.createElement("span");
  sub.textContent = (ev.subsystem || "") + (ev.actuator_id ? "/" + ev.actuator_id : "");

  const msg = document.createElement("span");
  msg.textContent = ev.message || "";

  li.append(ts, lvl, station, sub, msg);
  feed.prepend(li);
  while (feed.childElementCount > 300) feed.removeChild(feed.lastChild);

  state.eventCount += 1;
  el("event-count").textContent = state.eventCount + " events";
}

function markFailure(failure) {
  const li = document.createElement("li");
  li.className = "failure";
  const ts = document.createElement("span");
  ts.className = "ts";
  ts.textContent = (failure.at || "").replace("T", " ").slice(11, 19);
  const lvl = document.createElement("span");
  lvl.className = "lvl lvl-error";
  lvl.textContent = "FAILURE";
  const body = document.createElement("span");
  body.textContent = `${failure.rule_id} run=${failure.run_id} ${failure.detail}`;
  li.append(ts, lvl, body);
  el("feed").prepend(li);

  state.failedRuns.add(failure.run_id);
  loadRuns();
}

async function loadRuns() {
  let runs;
  try {
    const res = await fetch("/api/runs?limit=100");
    runs = await res.json();
  } catch (_) { return; }
  refreshCompareOptions(Array.isArray(runs) ? runs : []);
  const list = el("runs");
  list.textContent = "";
  if (!runs || runs.length === 0) {
    const li = document.createElement("li");
    li.className = "empty";
    li.textContent = "no runs yet";
    list.appendChild(li);
    return;
  }
  for (const run of runs) {
    const li = document.createElement("li");
    if (run.failures > 0) li.classList.add("has-failure");
    if (run.run_id === state.selectedRun) li.classList.add("selected");

    const id = document.createElement("span");
    id.className = "run-id";
    id.textContent = run.run_id;

    const tags = document.createElement("span");
    tags.className = "run-tags";
    if (run.failures > 0) {
      const t = document.createElement("span");
      t.className = "tag tag-fail";
      t.textContent = run.failures + " failed";
      tags.appendChild(t);
    }
    if (run.resolved) {
      const t = document.createElement("span");
      t.className = "tag tag-ok";
      t.textContent = "resolved";
      tags.appendChild(t);
    }
    const evt = document.createElement("span");
    evt.className = "tag";
    evt.textContent = run.event_count + " ev";
    tags.appendChild(evt);

    li.append(id, tags);
    li.addEventListener("click", () => loadRunDetail(run.run_id));
    list.appendChild(li);
  }
}

async function loadRunDetail(runID) {
  state.selectedRun = runID;
  let data;
  try {
    const res = await fetch("/api/runs/" + encodeURIComponent(runID));
    if (!res.ok) return;
    data = await res.json();
  } catch (_) { return; }

  el("run-detail").classList.remove("hidden");
  el("run-detail-title").textContent = "Run " + data.run.run_id;
  el("run-detail-meta").textContent =
    `station ${data.run.station_id} | ${data.run.event_count} events | ` +
    `${data.run.resolved ? "resolved" : "open"}`;

  const fl = el("run-failures");
  fl.textContent = "";
  if (!data.failures || data.failures.length === 0) {
    const li = document.createElement("li");
    li.className = "empty";
    li.textContent = "no failures flagged";
    fl.appendChild(li);
  } else {
    for (const f of data.failures) {
      const li = document.createElement("li");
      const r = document.createElement("span");
      r.className = "rule";
      r.textContent = f.rule_id + ": ";
      li.append(r, document.createTextNode(f.detail));
      fl.appendChild(li);
    }
  }

  const nl = el("run-notes");
  nl.textContent = "";
  if (!data.notes || data.notes.length === 0) {
    const li = document.createElement("li");
    li.className = "empty";
    li.textContent = "no notes yet";
    nl.appendChild(li);
  } else {
    for (const n of data.notes) {
      const li = document.createElement("li");
      const a = document.createElement("span");
      a.className = "author";
      a.textContent = (n.author || "anonymous") + ": ";
      li.append(a, document.createTextNode(n.body));
      nl.appendChild(li);
    }
  }

  el("export-link").href = "/api/runs/" + encodeURIComponent(runID) + "/export";
  el("resolve-btn").textContent = data.run.resolved ? "Mark open" : "Mark resolved";
  el("resolve-btn").dataset.resolved = data.run.resolved ? "1" : "0";
  loadRuns();
}

async function submitNote(evt) {
  evt.preventDefault();
  if (!state.selectedRun) return;
  const body = el("note-body").value.trim();
  if (!body) return;
  const author = el("note-author").value.trim();
  try {
    await fetch("/api/runs/" + encodeURIComponent(state.selectedRun) + "/notes", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ author, body }),
    });
  } catch (_) { return; }
  el("note-body").value = "";
  loadRunDetail(state.selectedRun);
}

async function toggleResolve() {
  if (!state.selectedRun) return;
  const next = el("resolve-btn").dataset.resolved !== "1";
  try {
    await fetch("/api/runs/" + encodeURIComponent(state.selectedRun) + "/resolve", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ resolved: next }),
    });
  } catch (_) { return; }
  loadRunDetail(state.selectedRun);
}

// loadIncidents backfills the timeline from the REST API on page load and
// on the periodic refresh, so a freshly opened dashboard is not empty.
async function loadIncidents() {
  let incidents;
  try {
    const res = await fetch("/api/incidents?limit=100");
    incidents = await res.json();
  } catch (_) { return; }
  if (!Array.isArray(incidents)) return;
  for (const inc of incidents) state.incidents.set(inc.id, inc);
  renderIncidents();
}

// renderIncidents draws each correlated incident as a horizontal band of
// subsystem segments, earliest subsystem first. The first segment carries a
// "root cause" marker.
function renderIncidents() {
  const list = el("incidents");
  list.textContent = "";

  const incidents = [...state.incidents.values()].sort((a, b) =>
    (b.started_at || "").localeCompare(a.started_at || ""));

  el("incident-count").textContent =
    incidents.length + (incidents.length === 1 ? " incident" : " incidents");

  if (incidents.length === 0) {
    const li = document.createElement("li");
    li.className = "empty";
    li.textContent = "no correlated incidents yet";
    list.appendChild(li);
    return;
  }

  for (const inc of incidents) {
    const li = document.createElement("li");
    li.className = "incident";

    const head = document.createElement("div");
    head.className = "incident-head";
    const id = document.createElement("span");
    id.className = "incident-id";
    id.textContent = inc.run_id;
    const span = document.createElement("span");
    span.className = "incident-span";
    span.textContent =
      windowMs(inc.started_at, inc.ended_at) + " ms span, " +
      (inc.members || []).length + " failures";
    head.append(id, span);
    li.appendChild(head);

    const band = document.createElement("div");
    band.className = "incident-band";
    const members = inc.members || [];
    members.forEach((m, idx) => {
      const seg = document.createElement("div");
      seg.className = "band-seg sev-" + (m.severity || "error");
      if (idx === 0) seg.classList.add("root-cause");
      const sub = document.createElement("span");
      sub.className = "seg-sub";
      sub.textContent = m.subsystem || "?";
      const rule = document.createElement("span");
      rule.className = "seg-rule";
      rule.textContent = m.rule_id || "";
      seg.append(sub, rule);
      seg.title = (m.rule_id || "") + ": " + (m.detail || "");
      band.appendChild(seg);
    });
    li.appendChild(band);

    const cause = document.createElement("div");
    cause.className = "incident-cause";
    cause.textContent = "probable root cause: " + (inc.root_cause || "unknown");
    li.appendChild(cause);

    list.appendChild(li);
  }
}

// windowMs returns the millisecond span between two RFC3339 timestamps.
function windowMs(start, end) {
  const a = Date.parse(start), b = Date.parse(end);
  if (isNaN(a) || isNaN(b)) return "?";
  return Math.max(0, Math.round(b - a));
}

// refreshCompareOptions keeps the two run pickers in sync with the run list.
function refreshCompareOptions(runs) {
  const a = el("compare-a"), b = el("compare-b");
  const prevA = a.value, prevB = b.value;
  a.textContent = "";
  b.textContent = "";
  for (const run of runs) {
    a.appendChild(new Option(run.run_id, run.run_id));
    b.appendChild(new Option(run.run_id, run.run_id));
  }
  if (prevA) a.value = prevA;
  if (prevB) b.value = prevB;
}

// runComparison fetches the diff for the two selected runs and renders it.
async function runComparison(evt) {
  evt.preventDefault();
  const runA = el("compare-a").value, runB = el("compare-b").value;
  if (!runA || !runB) return;
  const path = "/api/runs/" + encodeURIComponent(runA) +
    "/compare/" + encodeURIComponent(runB);
  let diff;
  try {
    const res = await fetch(path);
    if (!res.ok) {
      el("compare-result").textContent = "comparison failed";
      return;
    }
    diff = await res.json();
  } catch (_) {
    el("compare-result").textContent = "comparison failed";
    return;
  }
  renderComparison(diff);
  const link = el("compare-export");
  link.href = path + "?format=md";
  link.classList.remove("hidden");
}

function renderComparison(diff) {
  const root = el("compare-result");
  root.textContent = "";

  const summary = document.createElement("div");
  summary.className = "compare-summary";
  summary.append(
    countTag("new", diff.new_failures, "tag-fail"),
    countTag("resolved", diff.resolved_failures, "tag-ok"),
    countTag("persisting", diff.persisting_failures, "tag"));
  root.appendChild(summary);

  root.appendChild(failureGroup("New failures in B", diff.new_failures,
    "fail", "no regressions"));
  root.appendChild(failureGroup("Resolved since A", diff.resolved_failures,
    "ok", "nothing resolved"));
  root.appendChild(failureGroup("Still failing in both",
    diff.persisting_failures, "", "nothing persisted"));

  if (diff.subsystem_deltas && diff.subsystem_deltas.length) {
    const h = document.createElement("h4");
    h.textContent = "Per-subsystem deltas";
    root.appendChild(h);
    const ul = document.createElement("ul");
    ul.className = "delta-list";
    for (const s of diff.subsystem_deltas) {
      const li = document.createElement("li");
      const sign = s.failure_delta > 0 ? "+" : "";
      li.textContent = `${s.subsystem}: ${s.failures_a} to ${s.failures_b} ` +
        `failures (${sign}${s.failure_delta}), span ` +
        `${s.span_delta_ms >= 0 ? "+" : ""}${s.span_delta_ms} ms`;
      ul.appendChild(li);
    }
    root.appendChild(ul);
  }
}

function countTag(label, list, cls) {
  const n = (list || []).length;
  const t = document.createElement("span");
  t.className = "tag " + cls;
  t.textContent = n + " " + label;
  return t;
}

function failureGroup(title, changes, cls, emptyText) {
  const wrap = document.createElement("div");
  const h = document.createElement("h4");
  h.textContent = title;
  wrap.appendChild(h);
  if (!changes || changes.length === 0) {
    const p = document.createElement("p");
    p.className = "empty";
    p.textContent = emptyText;
    wrap.appendChild(p);
    return wrap;
  }
  const ul = document.createElement("ul");
  ul.className = "compare-failures";
  for (const c of changes) {
    const li = document.createElement("li");
    if (cls) li.classList.add("cf-" + cls);
    const r = document.createElement("span");
    r.className = "rule";
    r.textContent = c.rule_id + " ";
    const meta = document.createElement("span");
    meta.className = "cf-meta";
    meta.textContent = `[${c.subsystem}${c.actuator_id ? "/" + c.actuator_id : ""}] `;
    li.append(r, meta, document.createTextNode(c.detail || ""));
    ul.appendChild(li);
  }
  wrap.appendChild(ul);
  return wrap;
}

el("note-form").addEventListener("submit", submitNote);
el("resolve-btn").addEventListener("click", toggleResolve);
el("refresh-runs").addEventListener("click", loadRuns);
el("compare-form").addEventListener("submit", runComparison);

connect();
loadRuns();
loadIncidents();
setInterval(loadRuns, 8000);
setInterval(loadIncidents, 8000);
