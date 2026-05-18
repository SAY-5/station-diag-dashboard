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

el("note-form").addEventListener("submit", submitNote);
el("resolve-btn").addEventListener("click", toggleResolve);
el("refresh-runs").addEventListener("click", loadRuns);

connect();
loadRuns();
setInterval(loadRuns, 8000);
