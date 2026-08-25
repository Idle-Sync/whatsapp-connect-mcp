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

// lastStatus remembers the most recent good status so tab switches render
// the right panel instantly instead of waiting on (or mis-guessing from) a
// fetch in flight.
let lastStatus = null;

async function refreshStatus() {
  try {
    const s = await api("/api/status");
    lastStatus = s;
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
  if (b.dataset.tab === "pair") { renderPair(lastStatus); pollPair(); }
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

// watchedPairing tracks whether this page actually displayed an active
// pairing attempt, so "Paired." announces a completion the user watched —
// not every visit to the tab of an already-linked server.
let watchedPairing = false;

async function pollPair() {
  let info;
  try { info = await api("/api/pair"); } catch (e) { return; }
  const frame = document.getElementById("pair-frame");
  const img = document.getElementById("pair-qr");
  const hint = document.getElementById("pair-hint");

  if (info.error) {
    watchedPairing = false;
    say("pair-msg", "pairing failed: " + info.error, "bad");
    frame.hidden = true;
    return;
  }
  if (!info.pairing) {
    frame.hidden = true;
    const s = lastStatus && !watchedPairing ? lastStatus : await refreshStatus();
    renderPair(s);
    if (watchedPairing && s && !s.needs_pairing) {
      watchedPairing = false;
      say("pair-msg", "Paired. This server is linked to your WhatsApp.", "ok");
    }
    return;
  }
  watchedPairing = true;
  hint.textContent = "Scan with WhatsApp > Linked devices > Link a device";
  if (info.has_code) {
    img.src = "/api/pair/qr.png?t=" + Date.now();
    frame.hidden = false;
  }
  setTimeout(pollPair, 1000);
}

/* ---------- chats: the WhatsApp-style window ---------- */

function mediaURL(m) {
  return "/api/media?chat=" + encodeURIComponent(m.chat) + "&id=" + encodeURIComponent(m.id);
}

/* lightbox: a clicked picture comes forward full-size over the blurred
   page; click anywhere outside it, the close control, or Escape returns.
   The picture is this server's own /api/media URL, never a remote one. */
const lightbox = document.getElementById("lightbox");
const lightboxImg = document.getElementById("lightbox-img");

function openLightbox(src, alt) {
  lightboxImg.src = src;
  lightboxImg.alt = alt;
  lightbox.hidden = false;
  document.body.classList.add("lightbox-open");
  document.getElementById("lightbox-close").focus();
}

function closeLightbox() {
  if (lightbox.hidden) return;
  lightbox.hidden = true;
  lightboxImg.removeAttribute("src");
  document.body.classList.remove("lightbox-open");
}

lightbox.addEventListener("click", (ev) => { if (ev.target !== lightboxImg) closeLightbox(); });
document.getElementById("lightbox-close").addEventListener("click", closeLightbox);
document.addEventListener("keydown", (ev) => { if (ev.key === "Escape") closeLightbox(); });

// mediaNode renders a media message's payload: images and stickers as an
// inline lazy-loaded picture (the server only serves verified raster
// types inline; a refused or failed load falls back to the placeholder)
// that opens in the lightbox on click, everything else as the kind
// placeholder plus a download link. Media bytes come from this server's
// own /api/media — the page still makes zero external requests.
function mediaNode(m) {
  const label = "[" + m.kind + "]";
  if (m.kind === "image" || m.kind === "sticker") {
    const img = document.createElement("img");
    img.className = "media-img";
    img.loading = "lazy";
    img.alt = label;
    img.addEventListener("error", () => {
      img.replaceWith(el("span", label, "body media"));
    });
    img.addEventListener("click", () => openLightbox(img.src, label));
    img.src = mediaURL(m);
    return img;
  }
  const line = el("span", label + " ", "body media");
  const a = el("a", "download", "dl");
  a.href = mediaURL(m);
  line.appendChild(a);
  return line;
}

// reactionsByTarget maps a target message id to the reactions on it
// (sender key -> emoji); bubbleById indexes rendered bubbles by message id
// so a reaction arriving in any batch can attach to its target whether the
// target was drawn before or after it. Both are reset per chat load in
// renderMessages, then accumulate across refresh/scroll-older appends.
const reactionsByTarget = new Map();
const bubbleById = new Map();

// reactionKey identifies who reacted, so a later reaction from the same
// person replaces their earlier one (and an empty text removes it) — one
// reaction per person, like the app.
function reactionKey(m) { return m.from_me ? "me" : (m.sender || "?"); }

// applyReaction records a reaction row (never a bubble of its own) and, if
// its target is already on screen, refreshes that bubble's pill.
function applyReaction(m) {
  let on = reactionsByTarget.get(m.quoted_id);
  if (!on) { on = new Map(); reactionsByTarget.set(m.quoted_id, on); }
  if (m.text) on.set(reactionKey(m), m.text); else on.delete(reactionKey(m));
  const box = bubbleById.get(m.quoted_id);
  if (box) renderReactionPill(box, m.quoted_id);
}

// renderReactionPill (re)builds the reaction pill on a bubble from the
// accumulated reactions for its id: one chip per distinct emoji, with a
// small count when more than one person used it. Built node by node — no
// HTML parsing.
function renderReactionPill(box, id) {
  const on = reactionsByTarget.get(id);
  let pill = box.querySelector(".reactions");
  if (!on || on.size === 0) { if (pill) pill.remove(); box.classList.remove("has-reactions"); return; }
  if (!pill) { pill = el("span", undefined, "reactions"); box.appendChild(pill); }
  const counts = new Map();
  for (const emo of on.values()) counts.set(emo, (counts.get(emo) || 0) + 1);
  pill.replaceChildren();
  for (const [emo, n] of counts) {
    const chip = el("span", undefined, "reaction-chip");
    chip.appendChild(el("span", emo));
    if (n > 1) chip.appendChild(el("span", String(n), "n"));
    pill.appendChild(chip);
  }
  box.classList.add("has-reactions");
}

// currentChat is the open chat's state — jid, display bits, the server's
// replay-safe rowid cursor, the last day divider rendered, and whether the
// pane is parked mid-history on a search hit (parked: no cursor to refresh
// from; the "latest" control reloads the tail). null when nothing is open.
let currentChat = null;

// lastChats caches the chat list so leaving a search restores it without
// another fetch.
let lastChats = [];

// appendMessages adds bubbles (and day dividers as the day rolls over) to
// the pane without touching what's already there. Returns the new lastDay
// so a later append continues the divider sequence.
function appendMessages(list, msgs, showSenders, lastDay) {
  for (const m of msgs) {
    // Reactions are not messages — they attach to the message they target.
    if (m.kind === "reaction") { applyReaction(m); continue; }
    const day = dayOf(m.ts);
    if (day !== lastDay) {
      lastDay = day;
      list.appendChild(el("div", day, "day"));
    }
    const box = el("div", undefined, m.from_me ? "msg me" : "msg");
    box.dataset.id = m.id;
    box.dataset.ts = m.ts;
    if (showSenders && !m.from_me) {
      const who = el("span", prettyLabel(m.sender), "sender");
      who.style.color = "hsl(" + hueFor(m.sender) + " 55% 65%)";
      box.appendChild(who);
    }
    if (m.has_media) {
      box.appendChild(mediaNode(m));
      if (m.text) box.appendChild(el("span", m.text, "body"));
    } else {
      // A message with no body — a system event, a removed reaction, a
      // poll vote — renders as a dim kind placeholder, not an empty bubble.
      box.appendChild(el("span", m.text || "[" + m.kind + "]", m.text ? "body" : "body media"));
    }
    box.appendChild(el("span", clock(m.ts), "time"));
    list.appendChild(box);
    bubbleById.set(m.id, box);
    if (reactionsByTarget.has(m.id)) renderReactionPill(box, m.id);
  }
  return lastDay;
}

function pinList(list, toBottom) {
  const pin = () => { list.scrollTop = toBottom ? list.scrollHeight : 0; };
  pin();
  requestAnimationFrame(pin); // re-pin after entrance animations settle layout
}

// oldestOf returns the (ts, id) cursor for the oldest message of a page —
// the top bubble — for loading further back. null for an empty page.
function oldestOf(msgs) {
  if (!msgs || msgs.length === 0) return null;
  const m = msgs[0];
  return { ts: m.ts_unix, id: m.id };
}

// prependMessages builds the older block and inserts it above the current
// top, keeping the day dividers coherent and the viewport anchored where
// the reader was (so loading older history doesn't jump the scroll). The
// first existing bubble's leading day divider is dropped when the newly
// prepended tail already ends on that same day.
function prependMessages(list, msgs, showSenders) {
  if (msgs.length === 0) return;
  const frag = document.createDocumentFragment();
  const lastDay = appendMessages(frag, msgs, showSenders, "");
  // Drop a duplicate divider: the first existing .day equal to lastDay.
  const firstDay = list.querySelector(".day");
  if (firstDay && firstDay.textContent === lastDay) firstDay.remove();
  const anchor = list.firstElementChild;
  const before = list.scrollHeight;
  list.insertBefore(frag, anchor);
  // Preserve the reader's position: grow above, keep what they were on.
  list.scrollTop += list.scrollHeight - before;
}

// loadOlder fetches the page before the oldest shown and prepends it, once
// at a time, stopping when the store has no more. It fires when the reader
// scrolls near the top of the pane.
async function loadOlder() {
  const chat = currentChat;
  if (!chat || !chat.moreOlder || chat.loadingOlder || !chat.oldest) return;
  chat.loadingOlder = true;
  try {
    const page = await api("/api/messages?chat=" + encodeURIComponent(chat.jid) +
      "&before_ts=" + encodeURIComponent(chat.oldest.ts) + "&before_id=" + encodeURIComponent(chat.oldest.id) + "&limit=50");
    if (currentChat !== chat) return;
    if (page.messages.length > 0) {
      prependMessages(document.getElementById("messages-list"), page.messages, chat.isGroup);
      chat.oldest = oldestOf(page.messages);
    }
    chat.moreOlder = Boolean(page.more);
  } catch (e) {
    // leave moreOlder set so a later scroll retries
  } finally {
    chat.loadingOlder = false;
  }
}

document.getElementById("messages-list").addEventListener("scroll", (ev) => {
  if (ev.currentTarget.scrollTop < 120) loadOlder();
});

// renderMessages fills the message pane WhatsApp-style: day dividers,
// incoming bubbles left, own bubbles right, time inside the bubble, and —
// when senders vary — a colored sender label. scrollBottom pins the view
// to the newest message (a live chat); a parked context stays put for the
// caller to scroll to its hit. The header offers refresh for a live chat
// and "latest" for a parked one.
function renderMessages(title, msgs, showSenders, scrollBottom) {
  document.getElementById("messages-head").hidden = false;
  document.getElementById("messages-title").textContent = title;
  document.getElementById("messages-refresh").hidden = !(currentChat && !currentChat.parked);
  document.getElementById("messages-latest").hidden = !(currentChat && currentChat.parked);
  document.getElementById("messages-older").hidden = !currentChat;
  const list = document.getElementById("messages-list");
  list.replaceChildren();
  reactionsByTarget.clear();
  bubbleById.clear();
  if (msgs.length === 0) { empty(list, "No messages stored for this chat yet."); return ""; }
  const lastDay = appendMessages(list, msgs, showSenders, "");
  pinList(list, scrollBottom);
  return lastDay;
}

// avatarFor builds the round avatar: the initial letter stays underneath
// as the fallback; the picture (served by this server, never a remote
// URL) lazily covers it, and a 404 (no picture), 429 (rate cap), or
// failed load just removes the image again.
function avatarFor(name, jid) {
  const avatar = el("span", Array.from(name.replace(/^\+|^user …/, "") || name)[0].toUpperCase(), "avatar");
  const pic = document.createElement("img");
  pic.className = "avatar-img";
  pic.loading = "lazy";
  pic.alt = "";
  pic.addEventListener("error", () => pic.remove());
  pic.src = "/api/avatar?jid=" + encodeURIComponent(jid);
  avatar.appendChild(pic);
  return avatar;
}

function markActiveRow(jid) {
  for (const r of document.getElementById("chats-list").querySelectorAll(".chat-row")) {
    r.classList.toggle("active", r.dataset.jid === jid);
  }
}

// openChat loads a chat's live tail: the newest page plus the cursor a
// refresh continues from.
async function openChat(jid, name, isGroup) {
  markActiveRow(jid);
  skeleton(document.getElementById("messages-list"), 6, "2.6rem");
  try {
    const page = await api("/api/messages?chat=" + encodeURIComponent(jid) + "&limit=50");
    currentChat = { jid, name, isGroup, cursor: page.cursor, lastDay: "", parked: false, oldest: oldestOf(page.messages), moreOlder: page.messages.length >= 50, loadingOlder: false };
    currentChat.lastDay = renderMessages(name, page.messages, isGroup, true);
  } catch (e) {
    currentChat = null;
    empty(document.getElementById("messages-list"), "Couldn't load messages.");
  }
}

// jumpTo opens a chat parked on one message — the window of messages
// around it — scrolls that message to the centre and rings it.
async function jumpTo(jid, id, name, isGroup) {
  markActiveRow(jid);
  const list = document.getElementById("messages-list");
  skeleton(list, 6, "2.6rem");
  try {
    const page = await api("/api/messages?chat=" + encodeURIComponent(jid) + "&around=" + encodeURIComponent(id) + "&limit=60");
    currentChat = { jid, name, isGroup, cursor: 0, lastDay: "", parked: true, oldest: oldestOf(page.messages), moreOlder: page.messages.length > 0, loadingOlder: false };
    currentChat.lastDay = renderMessages(name, page.messages, isGroup, false);
    const hit = Array.from(list.children).find((x) => x.dataset.id === id);
    if (!hit) return;
    hit.classList.add("hit");
    // After pinList's own frame, so the pin doesn't undo the scroll.
    requestAnimationFrame(() => requestAnimationFrame(() => hit.scrollIntoView({ block: "center" })));
  } catch (e) {
    currentChat = null;
    empty(list, "Couldn't open that message.");
  }
}

document.getElementById("messages-latest").addEventListener("click", (ev) => withBusy(ev.currentTarget, async () => {
  if (currentChat) await openChat(currentChat.jid, currentChat.name, currentChat.isGroup);
}));

// paneToast shows a brief line under the chat header (history requested, or
// a cooldown) and clears any earlier one.
let paneToastTimer = null;
function paneToast(text, bad) {
  const head = document.getElementById("messages-head");
  let t = document.getElementById("pane-toast");
  if (!t) {
    t = el("div", undefined, "pane-toast");
    t.id = "pane-toast";
    head.insertAdjacentElement("afterend", t);
  }
  t.textContent = text;
  t.className = "pane-toast" + (bad ? " bad" : "");
  clearTimeout(paneToastTimer);
  paneToastTimer = setTimeout(() => t.remove(), 6000);
}

// "older" asks the phone for messages before the oldest one stored for the
// open chat — the way to pull a chat's history in from scratch, one bounded
// page at a time. The server rate-limits this; a 429 is shown as a wait,
// not an error. On success the pane refreshes shortly after, since the
// answer lands asynchronously; a parked (search-opened) pane returns to the
// live tail first so the new history is visible in order.
document.getElementById("messages-older").addEventListener("click", (ev) => withBusy(ev.currentTarget, async () => {
  const chat = currentChat;
  if (!chat) return;
  try {
    const r = await api("/api/history?chat=" + encodeURIComponent(chat.jid) + "&count=50", { method: "POST" });
    paneToast(r.status || "Requested older messages.");
    // The answer lands asynchronously below the oldest shown; re-arm the
    // scroll-up loader and pull the first older page in shortly after.
    setTimeout(() => {
      if (currentChat !== chat) return;
      chat.moreOlder = true;
      loadOlder();
    }, 2500);
  } catch (e) {
    const wait = e.body && e.body.retry_after;
    paneToast(wait ? "Asked very recently — try again in " + wait + "s." : errorMessage(e), true);
  }
}));

function renderChatList(chats) {
  const list = document.getElementById("chats-list");
  list.replaceChildren();
  if (chats.length === 0) { empty(list, "No chats stored yet. Once paired, history lands here."); return; }
  for (const c of chats) {
    const name = prettyLabel(c.name || c.jid);
    const b = el("button", undefined, "chat-row");
    b.dataset.jid = c.jid;
    b.appendChild(avatarFor(name, c.jid));
    const label = el("span", name, "chat-name");
    if (c.is_group) label.appendChild(el("span", "group", "tag"));
    b.appendChild(label);
    b.appendChild(el("span", ago(c.last_message_at), "chat-when"));
    if (currentChat && currentChat.jid === c.jid) b.classList.add("active");
    b.addEventListener("click", () => openChat(c.jid, name, c.is_group));
    list.appendChild(b);
  }
  stagger(list);
}

// loadChats fetches the chat list. While a search is showing its results
// the fresh list is only cached — the results stay on screen until the
// search is cleared.
async function loadChats() {
  const list = document.getElementById("chats-list");
  if (!search.q) skeleton(list, 8, "3rem");
  let chats;
  try { chats = await api("/api/chats?limit=50"); } catch (e) { if (!search.q) empty(list, "Couldn't load chats."); return; }
  lastChats = chats;
  if (!search.q) renderChatList(chats);
}

// refreshMessages fetches only what landed after the open chat's cursor
// and appends it; the view follows only if it was already pinned to the
// bottom. A response landing after the user switched chats is dropped.
document.getElementById("messages-refresh").addEventListener("click", (ev) => withBusy(ev.currentTarget, async () => {
  const chat = currentChat;
  if (!chat || chat.parked) return;
  const cur = chat.cursor || { ts: 0, id: "" };
  let page;
  try {
    page = await api("/api/messages?chat=" + encodeURIComponent(chat.jid) +
      "&after_ts=" + encodeURIComponent(cur.ts) + "&after_id=" + encodeURIComponent(cur.id) + "&limit=50");
  } catch (e) { return; }
  if (currentChat !== chat) return;
  chat.cursor = page.cursor;
  if (page.messages.length === 0) return;
  const list = document.getElementById("messages-list");
  const wasEmpty = list.firstElementChild && list.firstElementChild.classList.contains("empty");
  if (wasEmpty) list.replaceChildren();
  const pinned = wasEmpty || list.scrollTop + list.clientHeight >= list.scrollHeight - 40;
  chat.lastDay = appendMessages(list, page.messages, chat.isGroup, chat.lastDay);
  if (pinned) pinList(list, true);
}));

document.getElementById("chats-refresh").addEventListener("click", (ev) => withBusy(ev.currentTarget, loadChats));

/* ---------- search: results as you type, in the chat list's place ---------- */

// search is the live query state: q is the query the results on screen
// answer ("" while the chat list shows), scope narrows to the open chat,
// seq drops responses that arrive after a newer query was typed.
const search = { q: "", scope: "all", timer: null, seq: 0 };
const searchBox = document.getElementById("search-box");
const searchClear = document.getElementById("search-clear");
const SEARCH_LIMIT = 100;

function escapeRegExp(s) { return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"); }

// snippet trims text to a window around the first term match so the hit
// is visible in a two-line row, with ellipses where it was cut.
function snippet(text, terms) {
  const flat = text.replace(/\s+/g, " ").trim();
  const re = new RegExp(terms.map(escapeRegExp).join("|"), "i");
  const at = flat.search(re);
  const start = at > 40 ? at - 40 : 0;
  const end = Math.min(flat.length, Math.max(at, 0) + 110);
  return (start > 0 ? "…" : "") + flat.slice(start, end) + (end < flat.length ? "…" : "");
}

// highlightInto appends text to parent with every term match wrapped in a
// <mark> — built node by node, so the text is never parsed as HTML.
function highlightInto(parent, text, terms) {
  if (terms.length === 0) { parent.appendChild(document.createTextNode(text)); return; }
  const re = new RegExp(terms.map(escapeRegExp).join("|"), "ig");
  let last = 0;
  for (const m of text.matchAll(re)) {
    if (m.index > last) parent.appendChild(document.createTextNode(text.slice(last, m.index)));
    parent.appendChild(el("mark", m[0], "hit"));
    last = m.index + m[0].length;
  }
  if (last < text.length) parent.appendChild(document.createTextNode(text.slice(last)));
}

// when renders a result's time the way a list does: the clock for today,
// otherwise the date.
function when(iso) {
  const d = new Date(iso);
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return clock(iso);
  const opts = { day: "numeric", month: "short" };
  if (d.getFullYear() !== now.getFullYear()) opts.year = "numeric";
  return d.toLocaleDateString([], opts);
}

function renderResults(q, rows) {
  const list = document.getElementById("chats-list");
  list.replaceChildren();

  const head = el("div", undefined, "results-head");
  const n = rows.length;
  head.appendChild(el("span", n === 0 ? "no results" : n + (n >= SEARCH_LIMIT ? "+" : "") + (n === 1 ? " result" : " results")));
  if (currentChat) {
    const scope = el("span", undefined, "scope");
    for (const [key, label] of [["all", "All chats"], ["chat", "This chat"]]) {
      const b = el("button", label, "scope-btn" + (search.scope === key ? " active" : ""));
      b.addEventListener("click", () => { search.scope = key; runSearch(true); });
      scope.appendChild(b);
    }
    head.appendChild(scope);
  }
  list.appendChild(head);

  if (n === 0) { list.appendChild(el("div", "No messages match “" + q + "”.", "empty")); return; }

  const terms = q.split(/\s+/).filter(Boolean);
  for (const m of rows) {
    const chatName = prettyLabel(m.chat_name || m.chat);
    const b = el("button", undefined, "result-row");
    b.appendChild(avatarFor(chatName, m.chat));
    const main = el("span", undefined, "result-main");
    const top = el("span", undefined, "result-top");
    top.appendChild(el("span", chatName, "result-chat"));
    top.appendChild(el("span", when(m.ts), "result-when"));
    main.appendChild(top);
    const snip = el("span", undefined, "result-snippet");
    const who = m.from_me ? "You" : (m.chat_is_group ? prettyLabel(m.sender) : "");
    if (who) snip.appendChild(el("span", who + ": ", "result-sender"));
    highlightInto(snip, snippet(m.text || "[" + m.kind + "]", terms), terms);
    main.appendChild(snip);
    b.appendChild(main);
    b.addEventListener("click", () => {
      for (const o of list.querySelectorAll(".result-row")) o.classList.remove("active");
      b.classList.add("active");
      jumpTo(m.chat, m.id, chatName, Boolean(m.chat_is_group));
    });
    list.appendChild(b);
  }
  stagger(list);
}

// runSearch reacts to the box: under two characters shows the chat list,
// otherwise queries after a short pause (or at once on Enter / scope
// change). Results already on screen stay until the new ones land, so
// refining a query never flashes a skeleton.
function runSearch(immediate) {
  const q = searchBox.value.trim();
  searchClear.hidden = q === "";
  clearTimeout(search.timer);
  if (q.length < 2) {
    if (search.q) { search.q = ""; renderChatList(lastChats); }
    return;
  }
  const go = async () => {
    const seq = ++search.seq;
    const list = document.getElementById("chats-list");
    if (!search.q) skeleton(list, 6, "3.4rem");
    search.q = q;
    let url = "/api/search?q=" + encodeURIComponent(q) + "&limit=" + SEARCH_LIMIT;
    if (search.scope === "chat" && currentChat) url += "&chat=" + encodeURIComponent(currentChat.jid);
    let rows;
    try { rows = await api(url); } catch (e) {
      if (seq === search.seq) empty(list, "Search failed. Try again.");
      return;
    }
    if (seq !== search.seq) return;
    renderResults(q, rows);
  };
  if (immediate) go(); else search.timer = setTimeout(go, 250);
}

function clearSearch() {
  searchBox.value = "";
  searchBox.focus();
  runSearch(true);
}

searchBox.addEventListener("input", () => runSearch(false));
searchBox.addEventListener("keydown", (ev) => {
  if (ev.key === "Enter") runSearch(true);
  if (ev.key === "Escape" && searchBox.value) { ev.stopPropagation(); clearSearch(); }
});
searchClear.addEventListener("click", clearSearch);

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
