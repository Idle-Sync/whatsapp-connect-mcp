"use strict";
// Every WhatsApp-originated string enters the DOM as plain text via
// textContent — this file must never use an HTML-parsing sink; a Go test
// fails the build if one appears. All dynamic URL parts pass through
// encodeURIComponent. The one style influenced by WhatsApp data is a
// sender's label hue, derived from a numeric hash — never the string.

/* ---------- plumbing ---------- */

// signedOut flips the header to an honest signed-out state so stale data
// left on screen can't masquerade as a live connection.
function signedOut() {
  statusLine("signed out — run: whatsapp-connect-mcp dashboard for a new login link");
  document.getElementById("state-name").textContent = "signed out";
  document.getElementById("state").className = "badge offline";
  document.getElementById("pulse").className = "pulse offline";
}

async function api(path, opts) {
  const r = await fetch(path, Object.assign({ headers: { "X-Requested-With": "dashboard" } }, opts));
  if (r.status === 401) { signedOut(); throw new Error("401"); }
  if (!r.ok) {
    const err = new Error("api " + path + " " + r.status);
    if (r.headers.get("Content-Type")?.includes("json")) {
      try { err.body = await r.json(); } catch (_) { /* fall through to fixed copy */ }
    }
    throw err;
  }
  return r.headers.get("Content-Type")?.includes("json") ? r.json() : r;
}

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
  tr.className = cls ? cls + " enter" : "enter";
  for (const c of cells) tr.appendChild(el("td", c));
  table.appendChild(tr);
  return tr;
}

function say(id, text, kind) {
  const p = document.getElementById(id);
  if (!text) { p.hidden = true; return; }
  p.textContent = text;
  p.className = "chip" + (kind ? " " + kind : "");
  p.hidden = false;
  p.style.animation = "none";
  void p.offsetHeight;
  p.style.animation = "";
}

function statusLine(text) { document.getElementById("health-detail").textContent = text; }

async function withBusy(btn, fn) {
  if (btn.disabled) return;
  btn.disabled = true;
  btn.classList.add("busy");
  try { await fn(); } finally { btn.disabled = false; btn.classList.remove("busy"); }
}

function skeleton(container, count, height) {
  container.replaceChildren();
  for (let i = 0; i < count; i++) {
    const s = el("div", undefined, "skel");
    s.style.height = height;
    s.style.flex = "none";
    container.appendChild(s);
  }
}

function skeletonRows(table, count) {
  table.replaceChildren();
  for (let i = 0; i < count; i++) {
    const tr = document.createElement("tr");
    const td = document.createElement("td");
    td.colSpan = 4;
    const s = el("div", undefined, "skel");
    s.style.height = "1.1rem";
    s.style.marginBottom = "0";
    td.appendChild(s);
    tr.appendChild(td);
    table.appendChild(tr);
  }
}

function empty(container, text) {
  container.replaceChildren(el("div", text, "empty"));
}

function emptyRow(table, text) {
  table.replaceChildren();
  const tr = document.createElement("tr");
  const td = document.createElement("td");
  td.colSpan = 4;
  td.appendChild(el("div", text, "empty"));
  tr.appendChild(td);
  table.appendChild(tr);
}

function stagger(container) {
  let i = 0;
  for (const child of container.children) {
    child.style.animationDelay = Math.min(i++ * 25, 250) + "ms";
  }
}

/* ---------- time ---------- */

function span(seconds) {
  const s = Math.max(0, seconds);
  if (s < 60) return Math.floor(s) + "s";
  if (s < 3600) return Math.floor(s / 60) + "m";
  if (s < 86400) return Math.floor(s / 3600) + "h";
  return Math.floor(s / 86400) + "d";
}

function ago(iso) { return iso ? span((Date.now() - Date.parse(iso)) / 1000) : ""; }
function until(iso) { return iso ? "in " + span((Date.parse(iso) - Date.now()) / 1000) : ""; }

function clock(iso) {
  return new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function dayOf(iso) {
  return new Date(iso).toLocaleDateString([], { weekday: "short", day: "numeric", month: "short", year: "numeric" });
}

// hueFor turns a sender string into a stable label hue. Only the numeric
// hash ever reaches a style — never the string itself.
function hueFor(s) {
  let h = 5381;
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0;
  return Math.abs(h) % 360;
}

// prettyLabel makes a raw JID readable when nothing on record supplied a
// name: phone JIDs read as the phone number, privacy LIDs as a short
// stand-in. Anything that isn't a bare JID passes through untouched.
function prettyLabel(name) {
  let m = /^(\d+)@s\.whatsapp\.net$/.exec(name);
  if (m) return "+" + m[1];
  m = /^(\d+)@lid$/.exec(name);
  if (m) return "user …" + m[1].slice(-5);
  return name;
}

/* ---------- header + pulse ---------- */

async function refreshStatus() {
  try {
    const s = await api("/api/status");
    document.getElementById("state-name").textContent = s.state;
    document.getElementById("state").className = "badge " + s.state;
    document.getElementById("pulse").className = "pulse " + s.state;
    document.getElementById("version").textContent = "v" + s.version;
    statusLine(s.messages + " messages · " + s.chats + " chats" + (s.since ? " · " + s.state + " " + ago(s.since) : ""));

    const t = document.getElementById("status-table");
    t.replaceChildren();
    row(t, ["state", s.state + "  (for " + ago(s.since) + ")"]);
    row(t, ["last event", s.last_event ? ago(s.last_event) + " ago" : "none yet"]);
    row(t, ["reconnects", String(s.reconnects)]);
    row(t, ["ingest errors", String(s.ingest_errors)], s.ingest_errors > 0 ? "fail" : "");
    if (s.last_disconnect) row(t, ["last disconnect", s.last_disconnect]);
    row(t, ["stored", s.chats + " chats · " + s.messages + " messages · " + s.contacts + " contacts · " + s.calls + " calls"]);
    if (!document.getElementById("tab-pair").hidden) renderPair(s);
    return s;
  } catch (e) { return null; }
}

async function refreshDoctor() {
  try {
    const findings = await api("/api/doctor");
    const t = document.getElementById("doctor-table");
    t.replaceChildren();
    for (const f of findings) row(t, [f.status, f.check, f.detail, f.fix], f.status);
    stagger(t);
  } catch (e) { /* header already reflects auth loss */ }
}

async function refreshDraftsCount() {
  try {
    const drafts = await api("/api/drafts");
    const c = document.getElementById("drafts-count");
    c.hidden = drafts.length === 0;
    c.textContent = String(drafts.length);
  } catch (e) { /* non-essential */ }
}

/* ---------- tabs ---------- */

const loaders = { chats: loadChats, trust: loadTrust, schedules: loadSchedules, drafts: loadDrafts };
const tabs = document.querySelectorAll("nav button");
for (const b of tabs) b.addEventListener("click", () => {
  for (const t of tabs) t.classList.toggle("active", t === b);
  for (const s of document.querySelectorAll("main > section")) {
    const show = s.id === "tab-" + b.dataset.tab;
    if (show && s.hidden) { s.style.animation = "none"; void s.offsetHeight; s.style.animation = ""; }
    s.hidden = !show;
  }
  const load = loaders[b.dataset.tab];
  if (load) load();
  if (b.dataset.tab === "pair") pollPair();
});

skeletonRows(document.getElementById("status-table"), 5);
skeletonRows(document.getElementById("doctor-table"), 6);
refreshStatus().then(refreshDoctor);
refreshDraftsCount();
setInterval(refreshStatus, 5000);
setInterval(refreshDoctor, 30000);
setInterval(refreshDraftsCount, 15000);

/* ---------- pairing + unlink ---------- */

// renderPair decides which face the pair tab shows: linked (with the
// unlink control) or the pairing stage (hint, start button, QR).
function renderPair(status) {
  const isLinked = Boolean(status && !status.needs_pairing);
  document.getElementById("pair-linked").hidden = !isLinked;
  document.getElementById("pair-stage").hidden = isLinked;
  if (isLinked) document.getElementById("pair-frame").hidden = true;
}

document.getElementById("pair-start").addEventListener("click", (ev) => withBusy(ev.currentTarget, async () => {
  try {
    say("pair-msg", "");
    await api("/api/pair/start", { method: "POST" });
    pollPair();
  } catch (e) {
    say("pair-msg", errorMessage(e), "bad");
  }
}));

// Unlink is destructive, so it arms on the first click and fires only on
// a second click within three seconds — no blocking browser dialogs.
let unlinkTimer = null;
document.getElementById("unlink").addEventListener("click", (ev) => {
  const btn = ev.currentTarget;
  if (!btn.classList.contains("armed")) {
    btn.classList.add("armed");
    btn.textContent = "Click again to unlink";
    unlinkTimer = setTimeout(() => {
      btn.classList.remove("armed");
      btn.textContent = "Unlink from WhatsApp";
    }, 3000);
    return;
  }
  clearTimeout(unlinkTimer);
  btn.classList.remove("armed");
  btn.textContent = "Unlink from WhatsApp";
  withBusy(btn, async () => {
    try {
      say("pair-msg", "");
      await api("/api/pair/logout", { method: "POST" });
      say("pair-msg", "Unlinked. This server is signed out of your WhatsApp.", "ok");
      const s = await refreshStatus();
      renderPair(s);
    } catch (e) {
      say("pair-msg", errorMessage(e), "bad");
    }
  });
});

async function pollPair() {
  let info;
  try { info = await api("/api/pair"); } catch (e) { return; }
  const frame = document.getElementById("pair-frame");
  const img = document.getElementById("pair-qr");
  const hint = document.getElementById("pair-hint");

  if (info.error) {
    say("pair-msg", "pairing failed: " + info.error, "bad");
    frame.hidden = true;
    return;
  }
  if (!info.pairing) {
    frame.hidden = true;
    const s = await refreshStatus();
    renderPair(s);
    if (s && !s.needs_pairing) say("pair-msg", "Paired. This server is linked to your WhatsApp.", "ok");
    return;
  }
  hint.textContent = "Scan with WhatsApp > Linked devices > Link a device";
  if (info.has_code) {
    img.src = "/api/pair/qr.png?t=" + Date.now();
    frame.hidden = false;
  }
  setTimeout(pollPair, 1000);
}

/* ---------- chats: the WhatsApp-style window ---------- */

function messageText(m) {
  if (m.has_media) {
    const label = "[" + m.kind + "]";
    return m.text ? label + " " + m.text : label;
  }
  // A message with no body — a system event, a removed reaction, a poll
  // vote — renders as a dim kind placeholder instead of an empty bubble.
  if (!m.text) return "[" + m.kind + "]";
  return m.text;
}

function isPlaceholder(m) { return m.has_media || !m.text; }

// renderMessages fills the message pane WhatsApp-style: day dividers,
// incoming bubbles left, own bubbles right, time inside the bubble, and —
// when senders vary — a colored sender label. scrollBottom pins the view
// to the newest message (a chat); search results stay scrolled to top.
function renderMessages(title, msgs, showSenders, scrollBottom) {
  const head = document.getElementById("messages-title");
  head.hidden = false;
  head.textContent = title;
  const list = document.getElementById("messages-list");
  list.replaceChildren();
  if (msgs.length === 0) { empty(list, "No messages stored for this chat yet."); return; }

  let lastDay = "";
  for (const m of msgs) {
    const day = dayOf(m.ts);
    if (day !== lastDay) {
      lastDay = day;
      list.appendChild(el("div", day, "day"));
    }
    const box = el("div", undefined, m.from_me ? "msg me" : "msg");
    if (showSenders && !m.from_me) {
      const who = el("span", prettyLabel(m.sender), "sender");
      who.style.color = "hsl(" + hueFor(m.sender) + " 55% 65%)";
      box.appendChild(who);
    }
    const body = el("span", messageText(m), isPlaceholder(m) ? "body media" : "body");
    box.appendChild(body);
    box.appendChild(el("span", clock(m.ts), "time"));
    list.appendChild(box);
  }
  const pin = () => { list.scrollTop = scrollBottom ? list.scrollHeight : 0; };
  pin();
  requestAnimationFrame(pin); // re-pin after entrance animations settle layout
}

async function loadChats() {
  const list = document.getElementById("chats-list");
  skeleton(list, 8, "3rem");
  let chats;
  try { chats = await api("/api/chats?limit=50"); } catch (e) { empty(list, "Couldn't load chats."); return; }
  list.replaceChildren();
  if (chats.length === 0) { empty(list, "No chats stored yet. Once paired, history lands here."); return; }
  for (const c of chats) {
    const name = prettyLabel(c.name || c.jid);
    const b = el("button", undefined, "chat-row");
    b.appendChild(el("span", Array.from(name.replace(/^\+|^user …/, "") || name)[0].toUpperCase(), "avatar"));
    const label = el("span", name, "chat-name");
    if (c.is_group) label.appendChild(el("span", "group", "tag"));
    b.appendChild(label);
    b.appendChild(el("span", ago(c.last_message_at), "chat-when"));
    b.addEventListener("click", async () => {
      for (const other of list.children) other.classList.remove("active");
      b.classList.add("active");
      skeleton(document.getElementById("messages-list"), 6, "2.6rem");
      try {
        const msgs = await api("/api/messages?chat=" + encodeURIComponent(c.jid) + "&limit=50");
        renderMessages(name, msgs.reverse(), c.is_group, true);
      } catch (e) {
        empty(document.getElementById("messages-list"), "Couldn't load messages.");
      }
    });
    list.appendChild(b);
  }
  stagger(list);
}

document.getElementById("search-go").addEventListener("click", (ev) => withBusy(ev.currentTarget, async () => {
  const q = document.getElementById("search-box").value.trim();
  if (!q) return;
  skeleton(document.getElementById("messages-list"), 6, "2.6rem");
  try {
    const msgs = await api("/api/search?q=" + encodeURIComponent(q) + "&limit=50");
    renderMessages("search: " + q, msgs, true, false);
  } catch (e) {
    empty(document.getElementById("messages-list"), "Search failed. Try again.");
  }
}));
document.getElementById("search-box").addEventListener("keydown", (ev) => {
  if (ev.key === "Enter") document.getElementById("search-go").click();
});

/* ---------- trust ---------- */

async function loadTrust() {
  const ul = document.getElementById("trust-list");
  skeleton(ul, 3, "2.4rem");
  let jids;
  try { jids = await api("/api/trust"); } catch (e) { empty(ul, "Couldn't load the trust list."); return; }
  ul.replaceChildren();
  if (jids.length === 0) { empty(ul, "No trusted contacts. Every send drafts first."); return; }
  for (const j of jids) {
    const li = document.createElement("li");
    li.appendChild(el("span", j));
    const del = el("button", "remove", "danger");
    del.addEventListener("click", (ev) => withBusy(ev.currentTarget, async () => {
      try {
        say("trust-msg", "");
        await api("/api/trust/" + encodeURIComponent(j), { method: "DELETE" });
        loadTrust();
      } catch (e) { say("trust-msg", errorMessage(e), "bad"); }
    }));
    li.appendChild(del);
    ul.appendChild(li);
  }
  stagger(ul);
}

document.getElementById("trust-add").addEventListener("click", (ev) => withBusy(ev.currentTarget, async () => {
  const jid = document.getElementById("trust-jid").value.trim();
  if (!jid) return;
  try {
    say("trust-msg", "");
    await api("/api/trust", { method: "POST", body: JSON.stringify({ jid }) });
    document.getElementById("trust-jid").value = "";
    say("trust-msg", "trusted: " + jid, "ok");
    loadTrust();
  } catch (e) { say("trust-msg", errorMessage(e), "bad"); }
}));

/* ---------- schedules ---------- */

async function loadSchedules() {
  const t = document.getElementById("schedules-table");
  skeletonRows(t, 3);
  let rows;
  try { rows = await api("/api/schedules"); } catch (e) { emptyRow(t, "Couldn't load schedules."); return; }
  t.replaceChildren();
  if (rows.length === 0) { emptyRow(t, "No scheduled sends."); return; }
  for (const s of rows) {
    const tr = row(t, [s.fire_at, s.preview]);
    const td = document.createElement("td");
    const cancel = el("button", "cancel", "danger");
    cancel.addEventListener("click", (ev) => withBusy(ev.currentTarget, async () => {
      try {
        say("schedules-msg", "");
        await api("/api/schedules/" + encodeURIComponent(s.id), { method: "DELETE" });
        loadSchedules();
      } catch (e) { say("schedules-msg", errorMessage(e), "bad"); }
    }));
    td.appendChild(cancel);
    tr.appendChild(td);
  }
  stagger(t);
}

/* ---------- drafts ---------- */

async function loadDrafts() {
  const t = document.getElementById("drafts-table");
  skeletonRows(t, 2);
  let rows;
  try { rows = await api("/api/drafts"); } catch (e) { emptyRow(t, "Couldn't load drafts."); return; }
  t.replaceChildren();
  refreshDraftsCount();
  if (rows.length === 0) { emptyRow(t, "Nothing waiting on you."); return; }
  for (const d of rows) {
    const tr = row(t, ["expires " + until(d.expires), d.preview]);
    const td = document.createElement("td");
    const ok = el("button", "approve", "primary");
    ok.addEventListener("click", (ev) => withBusy(ev.currentTarget, async () => {
      try {
        say("drafts-msg", "");
        await api("/api/drafts/" + encodeURIComponent(d.token) + "/approve", { method: "POST" });
        say("drafts-msg", "sent", "ok");
        loadDrafts();
      } catch (e) { say("drafts-msg", errorMessage(e), "bad"); }
    }));
    const no = el("button", "discard", "danger");
    no.addEventListener("click", (ev) => withBusy(ev.currentTarget, async () => {
      try {
        say("drafts-msg", "");
        await api("/api/drafts/" + encodeURIComponent(d.token), { method: "DELETE" });
        loadDrafts();
      } catch (e) { say("drafts-msg", errorMessage(e), "bad"); }
    }));
    td.appendChild(ok);
    td.appendChild(document.createTextNode(" "));
    td.appendChild(no);
    tr.appendChild(td);
  }
  stagger(t);
}

/* ---------- backup ---------- */

document.getElementById("backup-go").addEventListener("click", (ev) => withBusy(ev.currentTarget, async () => {
  try {
    say("backup-result", "");
    const r = await api("/api/backup", { method: "POST" });
    say("backup-result", "written: " + r.path + " (" + r.size + " bytes)", "ok");
  } catch (e) { say("backup-result", errorMessage(e), "bad"); }
}));
