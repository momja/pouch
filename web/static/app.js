/* Pouch frontend — dependency-free SPA over Pouch's REST API. */

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => [...document.querySelectorAll(sel)];

const VERSION = "1.3.0";
const PER_PAGE = 30;

const FILTERS = {
  unread: { title: "Unread", emptyTitle: "The pouch is empty", emptyText: "Save a link to start reading." },
  starred: { title: "Starred", emptyTitle: "No stars yet", emptyText: "Star articles you love to find them here." },
  archive: { title: "Archive", emptyTitle: "Nothing archived", emptyText: "Finished reads land here." },
  all: { title: "Everything", emptyTitle: "Nothing saved yet", emptyText: "Save your first link to get going." },
};

const state = {
  filter: "unread",
  tag: null,
  query: "",
  items: [],
  page: 1,
  pages: 1,
  total: 0,
  loading: false,
  current: null, // entry open in the reader
  aborter: null,
};

/* ---------- Preferences ---------- */

const PREF_KEY = "pouch:prefs";
const defaultPrefs = { theme: "paper", font: "serif", size: 18, lh: 17, measure: 42, autoArchive: true };
let prefs = { ...defaultPrefs };
try {
  prefs = { ...defaultPrefs, ...JSON.parse(localStorage.getItem(PREF_KEY) || "{}") };
} catch { /* corrupted prefs -> defaults */ }

function savePrefs() {
  localStorage.setItem(PREF_KEY, JSON.stringify(prefs));
}

function applyPrefs() {
  const root = document.documentElement;
  root.dataset.theme = prefs.theme;
  root.style.setProperty("--reader-font", `var(--font-${prefs.font})`);
  root.style.setProperty("--reader-size", `${prefs.size}px`);
  root.style.setProperty("--reader-lh", (prefs.lh / 10).toFixed(1));
  root.style.setProperty("--measure", `${prefs.measure}rem`);
  const bg = getComputedStyle(root).getPropertyValue("--bg").trim();
  $('meta[name="theme-color"]').setAttribute("content", bg);
  syncPrefControls();
}

function syncPrefControls() {
  $$("[data-theme-pick]").forEach((b) => b.classList.toggle("active", b.dataset.themePick === prefs.theme));
  $$(".pill[data-font]").forEach((b) => b.classList.toggle("active", b.dataset.font === prefs.font));
  $$(".pill[data-measure]").forEach((b) => b.classList.toggle("active", Number(b.dataset.measure) === prefs.measure));
  for (const id of ["sizeRange", "typoSizeRange"]) $(`#${id}`).value = prefs.size;
  for (const id of ["lhRange", "typoLhRange"]) $(`#${id}`).value = prefs.lh;
  $("#sizeLabel").textContent = `${prefs.size}px`;
  $("#lhLabel").textContent = (prefs.lh / 10).toFixed(1);
  $("#autoArchiveToggle").checked = prefs.autoArchive;
}

function bindPrefControls() {
  document.addEventListener("click", (e) => {
    const swatch = e.target.closest("[data-theme-pick]");
    if (swatch) { prefs.theme = swatch.dataset.themePick; savePrefs(); applyPrefs(); return; }
    const font = e.target.closest(".pill[data-font]");
    if (font) { prefs.font = font.dataset.font; savePrefs(); applyPrefs(); return; }
    const measure = e.target.closest(".pill[data-measure]");
    if (measure) { prefs.measure = Number(measure.dataset.measure); savePrefs(); applyPrefs(); }
  });
  for (const id of ["sizeRange", "typoSizeRange"]) {
    $(`#${id}`).addEventListener("input", (e) => { prefs.size = Number(e.target.value); savePrefs(); applyPrefs(); });
  }
  for (const id of ["lhRange", "typoLhRange"]) {
    $(`#${id}`).addEventListener("input", (e) => { prefs.lh = Number(e.target.value); savePrefs(); applyPrefs(); });
  }
  $("#autoArchiveToggle").addEventListener("change", (e) => {
    prefs.autoArchive = e.target.checked;
    savePrefs();
    if (prefs.autoArchive) armFinishDetection();
    else disarmFinishDetection();
  });
}

/* ---------- API ---------- */

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: opts.body ? { "Content-Type": "application/json" } : {},
    ...opts,
  });
  if (res.status === 204) return null;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `${res.status} ${res.statusText}`);
  return data;
}

/* ---------- Helpers ---------- */

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function timeAgo(iso) {
  if (!iso) return "";
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 90) return "just now";
  const units = [[31536000, "y"], [2592000, "mo"], [604800, "w"], [86400, "d"], [3600, "h"], [60, "m"]];
  for (const [sec, label] of units) {
    if (s >= sec) return `${Math.floor(s / sec)}${label} ago`;
  }
  return "just now";
}

function toast(msg, ms = 2600) {
  const el = $("#toast");
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(toast._t);
  toast._t = setTimeout(() => { el.hidden = true; }, ms);
}

/* Strip scripts/handlers from wallabag content and polish media/links. */
function sanitize(html) {
  const doc = new DOMParser().parseFromString(html || "", "text/html");
  doc.querySelectorAll("script, iframe, object, embed, form, link, meta").forEach((n) => n.remove());
  doc.querySelectorAll("*").forEach((n) => {
    for (const attr of [...n.attributes]) {
      const name = attr.name.toLowerCase();
      if (name.startsWith("on")) n.removeAttribute(attr.name);
      if ((name === "href" || name === "src") && attr.value.trim().toLowerCase().startsWith("javascript:")) {
        n.removeAttribute(attr.name);
      }
    }
  });
  doc.querySelectorAll("img").forEach((img) => {
    img.loading = "lazy";
    img.referrerPolicy = "no-referrer";
    img.removeAttribute("width");
    img.removeAttribute("height");
  });
  doc.querySelectorAll("a[href]").forEach((a) => {
    a.target = "_blank";
    a.rel = "noopener noreferrer";
  });
  return doc.body.innerHTML;
}

/* ---------- List ---------- */

function visibleItems() {
  const q = state.query.trim().toLowerCase();
  if (!q) return state.items;
  return state.items.filter((it) =>
    it.title.toLowerCase().includes(q) || it.domain.toLowerCase().includes(q) ||
    it.tags.some((t) => t.toLowerCase().includes(q)));
}

function cardHTML(it) {
  const meta = [
    it.domain && `<span>${esc(it.domain)}</span>`,
    it.readingTime > 0 && `<span class="dot"></span><span>${it.readingTime} min</span>`,
    it.createdAt && `<span class="dot"></span><span>${timeAgo(it.createdAt)}</span>`,
    it.noteCount > 0 && `<span class="dot"></span><span class="has-notes"><svg class="icon"><use href="#i-note"/></svg>${it.noteCount}</span>`,
  ].filter(Boolean).join("");
  const tags = it.tags.length
    ? `<div class="card-tags">${it.tags.map((t) => `<span class="chip">${esc(t)}</span>`).join("")}</div>` : "";
  const thumb = it.preview
    ? `<img class="thumb" src="${esc(it.preview)}" alt="" loading="lazy" referrerpolicy="no-referrer" onerror="this.remove()">` : "";
  return `<article class="card${it.archived ? " archived" : ""}" data-id="${it.id}" tabindex="0" role="button" aria-label="${esc(it.title)}">
    <div class="card-body">
      <h2 class="card-title">${esc(it.title)}</h2>
      <div class="card-meta">${meta}</div>
      ${tags}
    </div>
    <div class="card-side">
      ${thumb}
      <div class="card-actions">
        <button class="icon-btn${it.starred ? " on" : ""}" data-action="star" title="${it.starred ? "Unstar" : "Star"}"><svg class="icon"><use href="#i-star"/></svg></button>
        <button class="icon-btn${it.archived ? " on" : ""}" data-action="archive" title="${it.archived ? "Move to unread" : "Archive"}"><svg class="icon"><use href="#i-archive"/></svg></button>
        <button class="icon-btn" data-action="delete" title="Delete"><svg class="icon"><use href="#i-trash"/></svg></button>
      </div>
    </div>
  </article>`;
}

function renderList() {
  const items = visibleItems();
  $("#entryList").innerHTML = items.map(cardHTML).join("");
  const empty = items.length === 0 && !state.loading;
  $("#emptyState").hidden = !empty;
  if (empty) {
    const f = FILTERS[state.filter];
    $("#emptyTitle").textContent = state.query ? "No matches" : f.emptyTitle;
    $("#emptyText").textContent = state.query ? "Try a different filter." : f.emptyText;
  }
  $("#loadMore").hidden = state.page >= state.pages || state.loading;
  $("#listCount").textContent = state.total > 0 ? String(state.total) : "";
}

async function loadEntries(reset = true) {
  if (state.aborter) state.aborter.abort();
  const aborter = new AbortController();
  state.aborter = aborter;
  if (reset) {
    state.items = [];
    state.page = 1;
    state.total = 0;
    renderList();
  }
  state.loading = true;
  $("#listStatus").hidden = false;
  try {
    const params = new URLSearchParams({ filter: state.filter, page: state.page, perPage: PER_PAGE });
    if (state.tag) params.set("tag", state.tag);
    const data = await api(`/api/entries?${params}`, { signal: aborter.signal });
    if (aborter.signal.aborted) return;
    state.items = state.page === 1 ? data.items : state.items.concat(data.items);
    state.pages = data.pages;
    state.total = data.total;
  } catch (err) {
    if (aborter.signal.aborted) return;
    toast(`Couldn't load entries: ${err.message}`);
  } finally {
    if (!aborter.signal.aborted) {
      state.loading = false;
      $("#listStatus").hidden = true;
      renderList();
    }
  }
}

async function refreshCounts() {
  for (const f of ["unread", "starred", "archive", "all"]) {
    api(`/api/entries?filter=${f}&perPage=1`)
      .then((d) => { $(`[data-count="${f}"]`).textContent = d.total > 0 ? d.total : ""; })
      .catch(() => {});
  }
}

async function loadTags() {
  try {
    const tags = await api("/api/tags");
    $("#tagsBlock").hidden = tags.length === 0;
    $("#tagList").innerHTML = tags
      .map((t) => `<button class="chip${state.tag === t.label ? " active" : ""}" data-tag="${esc(t.label)}">${esc(t.label)}</button>`)
      .join("");
  } catch { /* tags are decorative; ignore failures */ }
}

function setTag(tag) {
  state.tag = tag;
  const bar = $("#activeTag");
  if (tag) {
    bar.hidden = false;
    bar.innerHTML = `Filtering by <span class="chip active" style="pointer-events:none">${esc(tag)}</span>
      <button class="btn-ghost" id="clearTag" style="padding:0.2rem 0.7rem;font-size:0.8rem">Clear</button>`;
    $("#clearTag").onclick = () => { setTag(null); loadEntries(); loadTags(); };
  } else {
    bar.hidden = true;
    bar.innerHTML = "";
  }
  loadTags();
}

/* ---------- Entry actions (shared by list + reader) ---------- */

function itemById(id) {
  return state.items.find((it) => it.id === id) || (state.current?.id === id ? state.current : null);
}

function applyItemUpdate(updated) {
  const idx = state.items.findIndex((it) => it.id === updated.id);
  const matchesFilter =
    (state.filter === "unread" && !updated.archived) ||
    (state.filter === "archive" && updated.archived) ||
    (state.filter === "starred" && updated.starred) ||
    state.filter === "all";
  if (idx >= 0) {
    if (matchesFilter) state.items[idx] = { ...state.items[idx], ...updated };
    else { state.items.splice(idx, 1); state.total = Math.max(0, state.total - 1); }
  }
  if (state.current?.id === updated.id) {
    state.current = { ...state.current, ...updated };
    syncReaderButtons();
  }
  renderList();
  refreshCounts();
}

async function toggleStar(id) {
  const it = itemById(id);
  if (!it) return;
  try {
    const updated = await api(`/api/entries/${id}`, { method: "PATCH", body: JSON.stringify({ starred: it.starred ? 0 : 1 }) });
    applyItemUpdate(updated);
    toast(updated.starred ? "Starred" : "Star removed");
  } catch (err) { toast(`Failed: ${err.message}`); }
}

async function toggleArchive(id) {
  const it = itemById(id);
  if (!it) return;
  try {
    const updated = await api(`/api/entries/${id}`, { method: "PATCH", body: JSON.stringify({ archive: it.archived ? 0 : 1 }) });
    applyItemUpdate(updated);
    toast(updated.archived ? "Archived" : "Moved to unread");
  } catch (err) { toast(`Failed: ${err.message}`); }
}

/* Two-step delete: first tap arms the button, second tap within 3s deletes. */
function requestDelete(id, btn) {
  if (btn.dataset.armed) { doDelete(id); return; }
  btn.dataset.armed = "1";
  btn.style.color = "var(--accent)";
  btn.title = "Tap again to delete";
  toast("Tap delete again to confirm");
  setTimeout(() => { delete btn.dataset.armed; btn.style.color = ""; btn.title = "Delete"; }, 3000);
}

async function doDelete(id) {
  try {
    await api(`/api/entries/${id}`, { method: "DELETE" });
    state.items = state.items.filter((it) => it.id !== id);
    state.total = Math.max(0, state.total - 1);
    if (state.current?.id === id) closeReader();
    renderList();
    refreshCounts();
    toast("Deleted");
  } catch (err) { toast(`Failed: ${err.message}`); }
}

/* ---------- Scroll memory ---------- */

/* The reader reports how far you've scrolled — a 0..1 fraction of the scroll
   range, so it transfers between devices — to the server, throttled. The
   server keeps the furthest position per entry (conflict rule) and remembers
   the last-open article so a plain visit resumes it (see resumeOrRoute). */
const PROGRESS_DONE = 0.98; // past this, treat the article as finished
let progressSent = 0;       // last fraction reported for the open entry
let progressTimer = null;
let progressRAF = 0;

function readerProgress() {
  const el = $("#reader");
  const range = el.scrollHeight - el.clientHeight;
  return range > 0 ? Math.min(1, Math.max(0, el.scrollTop / range)) : 1;
}

function paintProgress(p = readerProgress()) {
  $("#readProgress").style.transform = `scaleX(${p.toFixed(4)})`;
}

function reportProgress({ beacon = false, force = false } = {}) {
  const it = state.current;
  if (!it) return;
  const p = readerProgress();
  if (!force && Math.abs(p - progressSent) < 0.01) return;
  progressSent = p;
  const url = `/api/entries/${it.id}/progress`;
  const body = JSON.stringify({ progress: p });
  if (beacon && navigator.sendBeacon) {
    navigator.sendBeacon(url, new Blob([body], { type: "application/json" }));
  } else {
    fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body, keepalive: true }).catch(() => {});
  }
}

function onReaderScroll() {
  if (!progressRAF) {
    progressRAF = requestAnimationFrame(() => { progressRAF = 0; paintProgress(); positionNoteButton(); });
  }
  if (!progressTimer) {
    progressTimer = setTimeout(() => { progressTimer = null; reportProgress(); }, 2500);
  }
}

/* ---------- Reader ---------- */

/* Finish detection: a sentinel after the article content is watched with an
   IntersectionObserver; reaching it archives the entry ("mark read when you
   finish"). Armed 2s after open so a quick peek never marks anything. */
let finishObserver = null;
let finishTimer = null;

function disarmFinishDetection() {
  clearTimeout(finishTimer);
  finishTimer = null;
  if (finishObserver) finishObserver.disconnect();
  finishObserver = null;
}

function armFinishDetection() {
  disarmFinishDetection();
  const it = state.current;
  if (!prefs.autoArchive || !it || it.archived || $("#reader").hidden) return;
  finishTimer = setTimeout(() => {
    finishObserver = new IntersectionObserver((entries) => {
      if (!entries[0].isIntersecting) return;
      const cur = state.current;
      if (!cur || cur.archived || !prefs.autoArchive) return;
      disarmFinishDetection();
      autoArchive(cur.id);
    }, { root: $("#reader") });
    finishObserver.observe($("#artEnd"));
  }, 2000);
}

async function autoArchive(id) {
  try {
    const updated = await api(`/api/entries/${id}`, { method: "PATCH", body: JSON.stringify({ archive: 1 }) });
    applyItemUpdate(updated);
    toast("Marked as read");
  } catch { /* keep quiet; the archive button still works */ }
}

/* Wallabag stores a placeholder body when it couldn't fetch a page
   ("wallabag can't retrieve contents for this article…") — and sometimes it
   "succeeds" but what it fetched was a bot wall (Cloudflare "Just a moment",
   Akamai "Access Denied", captcha pages). Either way there's nothing to read,
   so the reader bounces to the source. Only short bodies qualify: a real
   article that merely mentions these phrases is longer than 600 chars. */
function looksUnretrieved(it) {
  const text = new DOMParser().parseFromString(it.content || "", "text/html").body.textContent.trim();
  if (text.length >= 600) return false;
  const probe = `${it.title || ""}\n${text}`;
  return /(can.{0,2}t|cannot|unable to) retrieve (the )?(full )?contents?/i.test(probe) ||
    /\b(access denied|just a moment|bot challenge|attention required|checking your browser|verify(ing)? (that )?you are|are you a (robot|human)|captcha|don.{0,2}t have permission to access)\b/i.test(probe);
}

function syncReaderButtons() {
  const it = state.current;
  if (!it) return;
  $("#starBtn").classList.toggle("on", it.starred);
  $("#starBtn").title = it.starred ? "Unstar" : "Star";
  $("#archiveBtn").classList.toggle("on", it.archived);
  $("#archiveBtn").title = it.archived ? "Move to unread" : "Archive";
}

async function openReader(id) {
  const readerEl = $("#reader");
  readerEl.hidden = false;
  document.body.style.overflow = "hidden";
  $("#artTitle").textContent = "";
  $("#artMeta").innerHTML = "";
  $("#artTags").innerHTML = "";
  $("#artContent").innerHTML = '<div class="spinner" style="margin:3rem auto"></div>';
  paintProgress(0);
  try {
    const it = await api(`/api/entries/${id}`);
    if (looksUnretrieved(it)) {
      // Wallabag has no readable copy — bounce straight to the source.
      // Clear last-open first so a later visit doesn't resume into the bounce.
      fetch("/api/state/last", { method: "DELETE" }).catch(() => {});
      closeReader();
      window.location.href = it.url;
      return;
    }
    state.current = it;
    $("#artTitle").textContent = it.title;
    const bits = [];
    if (it.domain) bits.push(`<a href="${esc(it.url)}" target="_blank" rel="noopener noreferrer">${esc(it.domain)}</a>`);
    if (it.readingTime > 0) bits.push(`<span class="dot"></span><span>${it.readingTime} min read</span>`);
    if (it.createdAt) bits.push(`<span class="dot"></span><span>saved ${timeAgo(it.createdAt)}</span>`);
    $("#artMeta").innerHTML = bits.join("");
    $("#artTags").innerHTML = it.tags.map((t) => `<span class="chip" style="pointer-events:none">${esc(t)}</span>`).join("");
    $("#openOriginal").href = it.url;
    artHTML = sanitize(it.content) || `<p class="muted">No stored content. <a href="${esc(it.url)}" target="_blank" rel="noopener">Read the original ↗</a></p>`;
    notes = it.notes || [];
    pendingNote = null;
    editingNote = 0;
    renderNotes();
    syncReaderButtons();
    // Scroll memory: drop back to the stored position; fresh reads and
    // finished articles start from the top.
    const saved = Number(it.progress) || 0;
    readerEl.scrollTop = 0;
    // Register as the last-open article even if the user never scrolls
    // (the server keeps the furthest position, so sending 0 is safe).
    reportProgress({ force: true });
    progressSent = saved;
    if (saved > 0 && saved < PROGRESS_DONE) {
      requestAnimationFrame(() => {
        readerEl.scrollTop = saved * (readerEl.scrollHeight - readerEl.clientHeight);
        paintProgress();
      });
    }
    armFinishDetection();
  } catch (err) {
    $("#artContent").innerHTML = `<p class="muted">Couldn't load this entry: ${esc(err.message)}</p>`;
  }
}

function closeReader() {
  disarmFinishDetection();
  // Flush the final position, then clear last-open: a deliberate close means
  // the next plain visit should show the list, not resume this article.
  reportProgress();
  clearTimeout(progressTimer);
  progressTimer = null;
  if (state.current) fetch("/api/state/last", { method: "DELETE" }).catch(() => {});
  $("#reader").hidden = true;
  $("#typoPanel").hidden = true;
  $("#noteAddBtn").hidden = true;
  document.body.style.overflow = "";
  notes = [];
  pendingNote = null;
  editingNote = 0;
  selRange = null;
  state.current = null;
  if (location.hash.startsWith("#/read/")) {
    history.replaceState(null, "", `#/${state.filter}`);
  }
}

/* ---------- Inline notes ---------- */

/* Highlight a passage, say something about it, and the remark is waiting in
   the margin next time you open the article.

   A note anchors to character offsets into the article's text — the text
   nodes of #artContent concatenated, with note cards skipped so the notes
   themselves never shift the coordinate system. The quote is stored too and
   wins when the offsets no longer match (wallabag re-fetches change content).

   Rendering repaints from the pristine sanitized HTML: wrap every note's
   range in a <mark>, then drop each note's card after the block it lives in.
   Card-only changes (editing) skip the repaint so scroll stays put. */

let artHTML = "";       // sanitized article HTML, before marks and cards
let notes = [];         // notes on the open entry
let pendingNote = null; // {start, end, quote} being composed
let editingNote = 0;    // id of the note open for editing
let selRange = null;    // {start, end, quote, range} under the "Note" button
let selTimer = null;

function clipQuote(s) {
  const t = (s || "").replace(/\s+/g, " ").trim();
  return t.length > 180 ? `${t.slice(0, 180)}…` : t;
}

/* Text nodes of the article with their global offsets; note cards excluded. */
function textPieces(root) {
  const pieces = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: (n) => (n.parentElement?.closest(".note-card") ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT),
  });
  let off = 0, n;
  while ((n = walker.nextNode())) {
    pieces.push({ node: n, start: off });
    off += n.nodeValue.length;
  }
  return pieces;
}

/* Text of a cloned range, minus any note cards caught inside it. */
function plainText(frag) {
  frag.querySelectorAll(".note-card").forEach((n) => n.remove());
  return frag.textContent;
}

/* Where a note sits today: its stored offsets if the quote still matches
   there, else wherever the quote turns up. null = the passage is gone. */
function locateNote(n, text) {
  if (!n.quote || text.slice(n.start, n.end) === n.quote) return { start: n.start, end: n.end };
  const i = text.indexOf(n.quote);
  return i >= 0 ? { start: i, end: i + n.quote.length } : null;
}

/* Wrap each range in <mark>: split every text node at the range boundaries it
   contains, then wrap the fragments that land inside a range. */
function markRanges(pieces, ranges) {
  if (!ranges.length) return;
  const bounds = [...new Set(ranges.flatMap((r) => [r.start, r.end]))].sort((a, b) => a - b);
  for (const piece of pieces) {
    let node = piece.node;
    let start = piece.start;
    const frags = [];
    for (const b of bounds) {
      const local = b - start;
      if (local <= 0 || local >= node.nodeValue.length) continue;
      const tail = node.splitText(local);
      frags.push({ node, start });
      node = tail;
      start = b;
    }
    frags.push({ node, start });
    for (const f of frags) {
      const mid = f.start + f.node.nodeValue.length / 2;
      const covering = ranges.filter((r) => r.start <= mid && mid < r.end);
      if (!covering.length) continue;
      const owner = covering[covering.length - 1]; // a pending note is pushed last
      const mark = document.createElement("mark");
      mark.className = owner.id === 0 ? "note-mark pending" : "note-mark";
      mark.dataset.note = String(owner.id);
      f.node.parentNode.insertBefore(mark, f.node);
      mark.appendChild(f.node);
    }
  }
}

/* The top-level block a note's highlight ends in — the card goes after it,
   so a passage spanning several blocks is never interrupted by its own note. */
function anchorOf(art, id) {
  const marks = art.querySelectorAll(`.note-mark[data-note="${id}"]`);
  let el = marks[marks.length - 1];
  if (!el) return null;
  while (el.parentElement && el.parentElement !== art) el = el.parentElement;
  return el.parentElement === art ? el : null;
}

function placeCard(art, block, card) {
  if (!block) { art.append(card); return; } // passage gone: keep the note visible
  let at = block;
  while (at.nextElementSibling?.classList.contains("note-card")) at = at.nextElementSibling;
  at.after(card);
}

function noteCard(n) {
  const card = document.createElement("aside");
  card.className = "note-card";
  card.dataset.note = String(n.id);
  const quote = `<div class="note-quote">${esc(clipQuote(n.quote))}</div>`;
  card.innerHTML = editingNote === n.id
    ? `${quote}
       <textarea class="note-input">${esc(n.body)}</textarea>
       <div class="note-actions">
         <button data-note-act="cancel-edit">Cancel</button>
         <button class="strong" data-note-act="save-edit">Save</button>
       </div>`
    : `${quote}
       <div class="note-body">${esc(n.body)}</div>
       <div class="note-actions">
         <button data-note-act="edit">Edit</button>
         <button data-note-act="delete">Delete</button>
       </div>`;
  return card;
}

function composerCard() {
  const card = document.createElement("aside");
  card.className = "note-card composing";
  card.innerHTML = `<div class="note-quote">${esc(clipQuote(pendingNote.quote))}</div>
    <textarea class="note-input" placeholder="What do you make of this?"></textarea>
    <div class="note-actions">
      <button data-note-act="cancel-new">Cancel</button>
      <button class="strong" data-note-act="save-new">Add note</button>
    </div>`;
  return card;
}

/* Re-place the cards against the marks already in the article. */
function renderCards() {
  const art = $("#artContent");
  art.querySelectorAll(".note-card").forEach((c) => c.remove());
  for (const n of [...notes].sort((a, b) => a.start - b.start)) {
    placeCard(art, anchorOf(art, n.id), noteCard(n));
  }
  if (pendingNote) {
    const card = composerCard();
    placeCard(art, anchorOf(art, 0), card);
    card.querySelector("textarea").focus();
  }
}

/* Full repaint: highlights change (note added, deleted, composer opened). */
function renderNotes() {
  const art = $("#artContent");
  const reader = $("#reader");
  const keep = reader.scrollTop;
  art.innerHTML = artHTML;
  const pieces = textPieces(art);
  const text = pieces.map((p) => p.node.nodeValue).join("");
  const ranges = [];
  for (const n of notes) {
    const at = locateNote(n, text);
    if (at) ranges.push({ ...at, id: n.id });
  }
  if (pendingNote) ranges.push({ start: pendingNote.start, end: pendingNote.end, id: 0 });
  markRanges(pieces, ranges);
  renderCards();
  reader.scrollTop = keep;
}

function syncNoteCount() {
  if (state.current) state.current.noteCount = notes.length;
  const it = state.items.find((x) => x.id === state.current?.id);
  if (it) { it.noteCount = notes.length; renderList(); }
}

async function saveNote(body) {
  if (!pendingNote || !state.current || !body.trim()) return;
  const { start, end, quote } = pendingNote;
  try {
    const created = await api(`/api/entries/${state.current.id}/notes`, {
      method: "POST",
      body: JSON.stringify({ start, end, quote, body: body.trim() }),
    });
    notes.push(created);
    pendingNote = null;
    renderNotes();
    syncNoteCount();
  } catch (err) { toast(`Couldn't save the note: ${err.message}`); }
}

async function updateNote(id, body) {
  if (!state.current || !body.trim()) return;
  try {
    const saved = await api(`/api/entries/${state.current.id}/notes/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ body: body.trim() }),
    });
    notes = notes.map((n) => (n.id === id ? saved : n));
    editingNote = 0;
    renderCards();
  } catch (err) { toast(`Couldn't save the note: ${err.message}`); }
}

async function deleteNote(id) {
  if (!state.current) return;
  try {
    await api(`/api/entries/${state.current.id}/notes/${id}`, { method: "DELETE" });
    notes = notes.filter((n) => n.id !== id);
    renderNotes();
    syncNoteCount();
  } catch (err) { toast(`Couldn't delete the note: ${err.message}`); }
}

/* Two-step delete, same idea as entries: tap once to arm, again to confirm. */
function armNoteDelete(id, btn) {
  if (btn.dataset.armed) { deleteNote(id); return; }
  btn.dataset.armed = "1";
  btn.textContent = "Really?";
  btn.classList.add("danger");
  setTimeout(() => {
    if (!btn.isConnected) return;
    delete btn.dataset.armed;
    btn.textContent = "Delete";
    btn.classList.remove("danger");
  }, 3000);
}

function focusNote(id) {
  const card = $(`#artContent .note-card[data-note="${id}"]`);
  if (!card) return;
  card.scrollIntoView({ block: "center", behavior: "smooth" });
  card.classList.remove("flash");
  void card.offsetWidth; // restart the animation
  card.classList.add("flash");
}

function onNoteClick(e) {
  const act = e.target.closest("[data-note-act]");
  if (act) {
    const card = act.closest(".note-card");
    const id = Number(card.dataset.note || 0);
    const body = () => card.querySelector("textarea").value;
    switch (act.dataset.noteAct) {
      case "save-new": saveNote(body()); break;
      case "cancel-new": pendingNote = null; renderNotes(); break;
      case "edit": editingNote = id; renderCards(); break;
      case "cancel-edit": editingNote = 0; renderCards(); break;
      case "save-edit": updateNote(id, body()); break;
      case "delete": armNoteDelete(id, act); break;
    }
    return;
  }
  const mark = e.target.closest(".note-mark[data-note]");
  if (mark) focusNote(Number(mark.dataset.note));
}

/* Selecting a passage floats a "Note" button over it. */
function positionNoteButton() {
  const btn = $("#noteAddBtn");
  if (btn.hidden || !selRange) return;
  const rect = selRange.range.getBoundingClientRect();
  const w = btn.offsetWidth;
  const h = btn.offsetHeight;
  btn.style.left = `${Math.min(innerWidth - w - 8, Math.max(8, rect.left + rect.width / 2 - w / 2))}px`;
  const above = rect.top - h - 10;
  btn.style.top = `${above > 8 ? above : Math.min(innerHeight - h - 8, rect.bottom + 10)}px`;
}

function updateNoteButton() {
  const btn = $("#noteAddBtn");
  const art = $("#artContent");
  const sel = getSelection();
  const inArticle = (n) =>
    art.contains(n) && !(n.nodeType === 1 ? n : n.parentElement)?.closest(".note-card");
  if ($("#reader").hidden || pendingNote || !sel?.rangeCount || sel.isCollapsed) { btn.hidden = true; return; }
  const range = sel.getRangeAt(0);
  if (!inArticle(range.startContainer) || !inArticle(range.endContainer)) { btn.hidden = true; return; }
  const quote = plainText(range.cloneContents());
  if (!quote.trim()) { btn.hidden = true; return; }
  const pre = document.createRange();
  pre.selectNodeContents(art);
  pre.setEnd(range.startContainer, range.startOffset);
  const start = plainText(pre.cloneContents()).length;
  selRange = { start, end: start + quote.length, quote, range };
  btn.hidden = false;
  positionNoteButton();
}

function startNote() {
  if (!selRange) return;
  const { start, end, quote } = selRange;
  pendingNote = { start, end, quote };
  selRange = null;
  $("#noteAddBtn").hidden = true;
  getSelection()?.removeAllRanges();
  renderNotes();
}

function cancelNoteEditing() {
  if (!pendingNote && !editingNote) return false;
  const wasComposing = !!pendingNote;
  pendingNote = null;
  editingNote = 0;
  if (wasComposing) renderNotes(); else renderCards();
  return true;
}

/* ---------- Add flow ---------- */

function openAddDialog(url = "", tags = "") {
  $("#addUrl").value = url;
  $("#addTags").value = tags;
  $("#addDialog").showModal();
}

async function submitAdd(url, tags) {
  const btn = $("#addSubmit");
  btn.disabled = true;
  btn.textContent = "Saving…";
  try {
    const created = await api("/api/entries", { method: "POST", body: JSON.stringify({ url, tags }) });
    $("#addDialog").close();
    if (state.filter === "unread" || state.filter === "all") {
      state.items.unshift(created);
      state.total += 1;
      renderList();
    }
    refreshCounts();
    loadTags();
    toast(`Saved: ${created.title}`);
  } catch (err) {
    toast(`Couldn't save: ${err.message}`);
  } finally {
    btn.disabled = false;
    btn.textContent = "Save";
  }
}

/* Handle the PWA share target (GET /add?url=…&text=…&title=…). */
function handleShareTarget() {
  if (location.pathname !== "/add") return;
  const params = new URLSearchParams(location.search);
  const fields = [params.get("url"), params.get("text"), params.get("title")];
  let shared = "";
  for (const f of fields) {
    const m = (f || "").match(/https?:\/\/\S+/);
    if (m) { shared = m[0]; break; }
  }
  history.replaceState(null, "", "/#/unread");
  if (shared) {
    submitAdd(shared, "");
  } else {
    openAddDialog(fields.find(Boolean) || "");
  }
}

/* ---------- Routing ---------- */

function route() {
  const hash = location.hash || "#/unread";
  if (hash.startsWith("#/read/")) {
    const id = Number(hash.slice(7));
    if (id) openReader(id);
    // Direct link (or redirect bounce-back): hydrate the list behind the reader.
    if (state.items.length === 0 && !state.loading) loadEntries();
    return;
  }
  if (!$("#reader").hidden) closeReader();
  const name = hash.slice(2) || "unread";
  const isSettings = name === "settings";
  $("#settingsView").hidden = !isSettings;
  $("#listView").hidden = isSettings;
  $$("[data-route]").forEach((el) => el.classList.toggle("active", el.dataset.route === name));
  if (!isSettings) {
    const known = FILTERS[name] ? name : "unread";
    const changed = state.filter !== known;
    state.filter = known;
    $("#listTitle").textContent = FILTERS[known].title;
    if (changed || state.items.length === 0) {
      state.query = "";
      $("#searchInput").value = "";
      loadEntries();
    }
  }
}

/* On a plain visit (no hash at all), resume the article that was last open
   at its stored position; anything explicit — #/starred, #/read/…, the /add
   share target — is honored as requested. Finished articles don't resume. */
async function resumeOrRoute() {
  if (!location.hash) {
    try {
      const st = await api("/api/state");
      if (st.last && st.last.progress < PROGRESS_DONE) {
        history.replaceState(null, "", `#/read/${st.last.id}`);
      }
    } catch { /* resume is a nicety; fall through to the normal route */ }
  }
  route();
}

/* ---------- Setup ---------- */

function bindEvents() {
  window.addEventListener("hashchange", route);

  $("#addBtn").onclick = () => openAddDialog();
  $("#addBtnMobile").onclick = () => openAddDialog();
  $("#addCancel").onclick = () => $("#addDialog").close();
  $("#addForm").addEventListener("submit", (e) => {
    e.preventDefault();
    submitAdd($("#addUrl").value.trim(), $("#addTags").value.trim());
  });

  $$(".tabbar [data-route]").forEach((btn) => {
    btn.onclick = () => { location.hash = `#/${btn.dataset.route}`; };
  });

  $("#entryList").addEventListener("click", (e) => {
    const actionBtn = e.target.closest("[data-action]");
    const card = e.target.closest(".card");
    if (!card) return;
    const id = Number(card.dataset.id);
    if (actionBtn) {
      e.stopPropagation();
      if (actionBtn.dataset.action === "star") toggleStar(id);
      else if (actionBtn.dataset.action === "archive") toggleArchive(id);
      else if (actionBtn.dataset.action === "delete") requestDelete(id, actionBtn);
      return;
    }
    location.hash = `#/read/${id}`;
  });
  $("#entryList").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && e.target.classList.contains("card")) {
      location.hash = `#/read/${e.target.dataset.id}`;
    }
  });

  $("#tagList").addEventListener("click", (e) => {
    const chip = e.target.closest("[data-tag]");
    if (!chip) return;
    setTag(state.tag === chip.dataset.tag ? null : chip.dataset.tag);
    if (!FILTERS[state.filter]) location.hash = "#/all";
    else loadEntries();
  });

  $("#searchInput").addEventListener("input", (e) => {
    state.query = e.target.value;
    renderList();
  });

  $("#loadMore").onclick = () => {
    if (state.page < state.pages && !state.loading) {
      state.page += 1;
      loadEntries(false);
    }
  };
  new IntersectionObserver((entries) => {
    if (entries[0].isIntersecting) $("#loadMore").click();
  }).observe($("#loadMore"));

  $("#backBtn").onclick = () => closeReader();
  $("#starBtn").onclick = () => state.current && toggleStar(state.current.id);
  $("#archiveBtn").onclick = () => state.current && toggleArchive(state.current.id);
  $("#deleteBtn").onclick = (e) => state.current && requestDelete(state.current.id, e.currentTarget);
  $("#typoBtn").onclick = (e) => {
    e.stopPropagation();
    $("#typoPanel").hidden = !$("#typoPanel").hidden;
  };
  $("#reader").addEventListener("click", (e) => {
    if (!$("#typoPanel").hidden && !e.target.closest(".typo-panel") && !e.target.closest("#typoBtn")) {
      $("#typoPanel").hidden = true;
    }
  });
  $("#reader").addEventListener("scroll", onReaderScroll, { passive: true });

  // Inline notes: selection → floating button → composer card in the flow.
  document.addEventListener("selectionchange", () => {
    clearTimeout(selTimer);
    selTimer = setTimeout(updateNoteButton, 150);
  });
  // pointerdown, not click: on touch the tap would collapse the selection
  // (and hide the button) before a click ever lands.
  $("#noteAddBtn").addEventListener("pointerdown", (e) => { e.preventDefault(); startNote(); });
  $("#artContent").addEventListener("click", onNoteClick);
  $("#artContent").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.target.closest(".note-card")?.querySelector("[data-note-act^='save']")?.click();
    }
  });
  // Flush the reading position when the tab is backgrounded or closed —
  // that's the position another visit (or device) resumes from.
  addEventListener("pagehide", () => reportProgress({ beacon: true }));
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") reportProgress({ beacon: true });
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      if (!$("#typoPanel").hidden) { $("#typoPanel").hidden = true; return; }
      if (cancelNoteEditing()) return;
      if (!$("#reader").hidden) closeReader();
      return;
    }
    const typing = /^(input|textarea|select)$/i.test(document.activeElement?.tagName || "") || $("#addDialog").open;
    if (typing) return;
    if (e.key === "a") { e.preventDefault(); openAddDialog(); }
    if (e.key === "/") { e.preventDefault(); $("#searchInput").focus(); }
  });
}

function initSetupHelpers() {
  const origin = location.origin;
  $("#quickAddURL").textContent = `${origin}/api/quick-add?url=`;
  const bm = $("#bookmarklet");
  bm.href = `javascript:void(window.open('${origin}/api/quick-add?url='+encodeURIComponent(location.href),'_blank','noopener'))`;
  bm.onclick = (e) => e.preventDefault();
  $("#versionInfo").textContent = `v${VERSION}.`;
}

applyPrefs();
bindPrefControls();
bindEvents();
initSetupHelpers();
handleShareTarget();
resumeOrRoute();
refreshCounts();
loadTags();

if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js").catch(() => {});
}
