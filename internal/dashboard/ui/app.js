"use strict";
// All WhatsApp-originated strings enter the DOM via textContent ONLY.
// This file must never use an HTML-parsing DOM sink; a Go test fails the
// build if one appears.

async function api(path, opts) {
  const r = await fetch(path, Object.assign({ headers: { "X-Requested-With": "dashboard" } }, opts));
  if (r.status === 401) { statusLine("logged out — run: whatsapp-connect-mcp dashboard"); throw new Error("401"); }
  if (!r.ok) {
    const err = new Error("api " + path + " " + r.status);
    if (r.headers.get("Content-Type")?.includes("json")) {
      try { err.body = await r.json(); } catch (e) { /* body not parseable JSON */ }
    }
    throw err;
  }
  return r.headers.get("Content-Type")?.includes("json") ? r.json() : r;
}

// errorMessage extracts the server's category-only error text from a
// failed api() call, falling back to a fixed message when the response
// carried no such body (e.g. a plain-text 401/403/405).
function errorMessage(err) {
  return err && err.body && typeof err.body.error === "string" ? err.body.error : "request failed";
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
  const msg = document.getElementById("pair-msg");
  try {
    await api("/api/pair/start", { method: "POST" });
    msg.textContent = "";
    pollPair();
  } catch (e) {
    msg.textContent = errorMessage(e);
  }
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

function messageText(m) {
  if (m.has_media) {
    const label = "[" + m.kind + "]";
    return m.text ? label + " " + m.text : label;
  }
  return m.text;
}

function renderMessages(title, msgs) {
  const h2 = document.getElementById("messages-title");
  h2.hidden = false;
  h2.textContent = title;
  const t = document.getElementById("messages-table");
  t.replaceChildren();
  for (const m of msgs) row(t, [m.ts, m.sender, messageText(m)], m.from_me ? "from-me" : "");
}

async function loadChats() {
  const chats = await api("/api/chats?limit=50");
  const list = document.getElementById("chats-list");
  list.replaceChildren();
  for (const c of chats) {
    const b = el("button", (c.name || c.jid) + (c.is_group ? " (group)" : ""));
    b.addEventListener("click", async () => {
      const msgs = await api("/api/messages?chat=" + encodeURIComponent(c.jid) + "&limit=50");
      renderMessages(c.name || c.jid, msgs.reverse());
    });
    list.appendChild(b);
  }
}

document.getElementById("search-go").addEventListener("click", async () => {
  const q = document.getElementById("search-box").value.trim();
  if (!q) return;
  const msgs = await api("/api/search?q=" + encodeURIComponent(q) + "&limit=50");
  renderMessages("Search: " + q, msgs);
});

document.querySelector('nav button[data-tab="chats"]').addEventListener("click", loadChats);

async function loadTrust() {
  const jids = await api("/api/trust");
  const ul = document.getElementById("trust-list");
  ul.replaceChildren();
  for (const j of jids) {
    const li = el("li", j + " ");
    const del = el("button", "remove");
    del.addEventListener("click", async () => {
      const msg = document.getElementById("trust-msg");
      try {
        await api("/api/trust/" + encodeURIComponent(j), { method: "DELETE" });
        msg.textContent = "";
        loadTrust();
      } catch (e) {
        msg.textContent = errorMessage(e);
      }
    });
    li.appendChild(del);
    ul.appendChild(li);
  }
}
document.getElementById("trust-add").addEventListener("click", async () => {
  const jid = document.getElementById("trust-jid").value.trim();
  if (!jid) return;
  const msg = document.getElementById("trust-msg");
  try {
    await api("/api/trust", { method: "POST", body: JSON.stringify({ jid }) });
    document.getElementById("trust-jid").value = "";
    msg.textContent = "";
    loadTrust();
  } catch (e) {
    msg.textContent = errorMessage(e);
  }
});

async function loadSchedules() {
  const rows = await api("/api/schedules");
  const t = document.getElementById("schedules-table");
  t.replaceChildren();
  for (const s of rows) {
    const tr = row(t, [s.fire_at, s.preview]);
    const cancel = el("button", "cancel");
    cancel.addEventListener("click", async () => {
      const msg = document.getElementById("schedules-msg");
      try {
        await api("/api/schedules/" + encodeURIComponent(s.id), { method: "DELETE" });
        msg.textContent = "";
        loadSchedules();
      } catch (e) {
        msg.textContent = errorMessage(e);
      }
    });
    const td = document.createElement("td");
    td.appendChild(cancel);
    tr.appendChild(td);
  }
}

async function loadDrafts() {
  const rows = await api("/api/drafts");
  const t = document.getElementById("drafts-table");
  t.replaceChildren();
  for (const d of rows) {
    const tr = row(t, [d.preview, "expires " + d.expires]);
    const td = document.createElement("td");
    const ok = el("button", "approve");
    ok.addEventListener("click", async () => {
      const msg = document.getElementById("drafts-msg");
      try {
        await api("/api/drafts/" + encodeURIComponent(d.token) + "/approve", { method: "POST" });
        msg.textContent = "";
        loadDrafts();
      } catch (e) {
        msg.textContent = errorMessage(e);
      }
    });
    const no = el("button", "discard");
    no.addEventListener("click", async () => {
      const msg = document.getElementById("drafts-msg");
      try {
        await api("/api/drafts/" + encodeURIComponent(d.token), { method: "DELETE" });
        msg.textContent = "";
        loadDrafts();
      } catch (e) {
        msg.textContent = errorMessage(e);
      }
    });
    td.appendChild(ok);
    td.appendChild(no);
    tr.appendChild(td);
  }
}

document.getElementById("backup-go").addEventListener("click", async () => {
  const result = document.getElementById("backup-result");
  try {
    const r = await api("/api/backup", { method: "POST" });
    result.textContent = "written: " + r.path + " (" + r.size + " bytes)";
  } catch (e) {
    result.textContent = errorMessage(e);
  }
});

document.querySelector('nav button[data-tab="trust"]').addEventListener("click", loadTrust);
document.querySelector('nav button[data-tab="schedules"]').addEventListener("click", loadSchedules);
document.querySelector('nav button[data-tab="drafts"]').addEventListener("click", loadDrafts);

refreshStatus();
refreshDoctor();
setInterval(refreshStatus, 5000);
setInterval(refreshDoctor, 30000);
