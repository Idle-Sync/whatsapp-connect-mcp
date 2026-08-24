"use strict";
// All WhatsApp-originated strings enter the DOM via textContent ONLY.
// This file must never use an HTML-parsing DOM sink; a Go test fails the
// build if one appears.

async function api(path, opts) {
  const r = await fetch(path, Object.assign({ headers: { "X-Requested-With": "dashboard" } }, opts));
  if (r.status === 401) { statusLine("logged out — run: whatsapp-connect-mcp dashboard"); throw new Error("401"); }
  if (!r.ok) throw new Error("api " + path + " " + r.status);
  return r.headers.get("Content-Type")?.includes("json") ? r.json() : r;
}

function el(tag, text, cls) {
  const e = document.createElement(tag);
  if (text !== undefined) e.textContent = text;
  if (cls) e.className = cls;
  return e;
}

function row(table, cells, cls) {
  const tr = document.createElement("tr");
  if (cls) tr.className = cls;
  for (const c of cells) tr.appendChild(el("td", c));
  table.appendChild(tr);
  return tr;
}

function statusLine(text) { document.getElementById("health-detail").textContent = text; }

async function refreshStatus() {
  try {
    const s = await api("/api/status");
    const badge = document.getElementById("state");
    badge.textContent = s.state;
    badge.className = "badge " + s.state;
    statusLine(s.messages + " messages · " + s.chats + " chats · v" + s.version);
    const t = document.getElementById("status-table");
    t.replaceChildren();
    row(t, ["state", s.state + " (since " + s.since + ")"]);
    if (s.last_event) row(t, ["last event", s.last_event]);
    row(t, ["reconnects", String(s.reconnects)]);
    row(t, ["ingest errors", String(s.ingest_errors)]);
    if (s.last_disconnect) row(t, ["last disconnect", s.last_disconnect]);
    return s;
  } catch (e) { return null; }
}

async function refreshDoctor() {
  try {
    const findings = await api("/api/doctor");
    const t = document.getElementById("doctor-table");
    t.replaceChildren();
    for (const f of findings) row(t, [f.status, f.check, f.detail, f.fix], f.status);
  } catch (e) { /* header already shows logged-out state */ }
}

const tabs = document.querySelectorAll("nav button");
for (const b of tabs) b.addEventListener("click", () => {
  for (const t of tabs) t.classList.toggle("active", t === b);
  for (const s of document.querySelectorAll("main > section"))
    s.hidden = s.id !== "tab-" + b.dataset.tab;
});

document.getElementById("pair-start").addEventListener("click", async () => {
  await api("/api/pair/start", { method: "POST" });
  pollPair();
});

async function pollPair() {
  const info = await api("/api/pair");
  const img = document.getElementById("pair-qr");
  const msg = document.getElementById("pair-msg");
  if (info.error) { msg.textContent = "pairing failed: " + info.error; img.hidden = true; return; }
  if (!info.pairing) {
    const s = await refreshStatus();
    msg.textContent = s && !s.needs_pairing ? "Paired." : "Not pairing.";
    img.hidden = true;
    return;
  }
  msg.textContent = "Scan with WhatsApp > Linked devices > Link a device";
  if (info.has_code) { img.src = "/api/pair/qr.png?t=" + Date.now(); img.hidden = false; }
  setTimeout(pollPair, 1000);
}

refreshStatus();
refreshDoctor();
setInterval(refreshStatus, 5000);
setInterval(refreshDoctor, 30000);
