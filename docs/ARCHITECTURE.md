# Pouch architecture

Pouch is deliberately small: one Go binary, one embedded static frontend, zero
runtime dependencies beyond a reachable Wallabag instance.

```
Browser (PWA, vanilla JS) ──HTTP──▶ Pouch (Go, :8080) ──OAuth2+REST──▶ Wallabag
```

## Repository map

```
cmd/pouch/main.go          Entrypoint: env config → server → ListenAndServe
cmd/mockwallabag/main.go   In-memory fake Wallabag API for dev/browser tests
internal/server/
  config.go                Config struct + ConfigFromEnv (env vars only)
  server.go                Routes, static file serving with SPA fallback
  api.go                   REST handlers, response mapping, quick-add HTML page
  wallabag.go              Per-server HTTP client and OAuth token recovery
  progress.go              Scroll-memory store (JSON file under POUCH_DATA_DIR)
  notes.go                 Inline-note store (JSON file under POUCH_DATA_DIR)
web/
  embed.go                 //go:embed all:static → web.Static
  static/
    index.html             Full markup for every view + inline SVG icon sprite
    style.css              Hand-rolled design system (themes as CSS vars)
    app.js                 SPA: hash router, state, rendering, prefs
    sw.js                  Service worker (network-first, cache = offline fallback)
    manifest.webmanifest   PWA manifest incl. share_target
    icons/                 icon.svg + PNGs rendered from it
Dockerfile                 Two-stage build → alpine, non-root, healthcheck
docker-compose.example.yml Traefik-labelled reference deployment
```

## Backend

### Wallabag client and token recovery

- `internal/server/wallabag.go` owns an HTTP client and OAuth tokens per server.
  wallabago supplies response types and timestamp parsing only; its global
  config/token and HTTP helpers are not used.
- Every upstream route (list, content, tags, create, patch, delete, quick-add)
  goes through `wallabagClient.apiCall`. Lists request `detail=metadata` and
  preserve archive/starred filters, sorting, pagination, and encoded tag names.
- Authentication is serialized; concurrent requests share the acquired token.
  Expired access tokens use the refresh grant. HTTP 400/401 on refresh discards
  the rejected token and attempts the configured password grant once before
  dispatching the API request. A failed fallback leaves the token cleared so
  later requests can recover without restarting Pouch.
- Transport failures, rate limits, server errors, and malformed token responses
  do not trigger password fallback. Authentication failures have a one-second
  cooldown to bound concurrent retries; later requests try again. Requests have
  a 30-second HTTP timeout and inherit the incoming request's cancellation.
- API requests are never retried, including on 401 or transport errors: a
  mutation may already have taken effect. Redirects are rejected. An access
  token rejected before its local expiry therefore still requires expiry or a
  restart; automatic API-401 recovery is outside this fix.
- All 2xx API responses are accepted (including create/delete statuses).
  OAuth response bodies and transport errors containing URLs are not exposed
  to the frontend or logs. `/healthz` remains process liveness; it does not
  verify Wallabag connectivity or authentication.
- Wallabag timestamps use a nonstandard layout (`2006-01-02T15:04:05-0700`);
  wallabago parses it into `WallabagTime`. `entry.CreatedAt` is re-emitted to the
  frontend as RFC3339.

### Pouch's own REST surface (consumed by the frontend)

| Route | Meaning |
| --- | --- |
| `GET /api/entries?filter=unread\|starred\|archive\|all&page=&perPage=&tag=` | Paged list, trimmed JSON |
| `POST /api/entries` `{url, tags}` | Save a URL, returns created entry |
| `GET /api/entries/{id}` | Full entry incl. sanitized-on-client content |
| `PATCH /api/entries/{id}` `{archive?, starred?, title?, tags?}` | Partial update |
| `DELETE /api/entries/{id}` | Delete |
| `GET /api/tags` | All tags |
| `GET /api/quick-add?url=&tags=` | Save + human-friendly HTML confirmation (share sheet / bookmarklet target) |
| `POST /api/entries/{id}/progress` `{progress: 0..1}` | Record reading position (POST, not PUT, so `sendBeacon` works) |
| `GET /api/entries/{id}/notes` | Inline notes on an entry (also inlined into `GET /api/entries/{id}`) |
| `POST /api/entries/{id}/notes` `{start, end, quote, body}` | Add a note, returns it with its id |
| `PATCH /api/entries/{id}/notes/{noteID}` `{body}` | Reword a note |
| `DELETE /api/entries/{id}/notes/{noteID}` | Remove a note |
| `GET /api/state` | `{last: {id, progress} \| null}` — the last-open article, for resume-on-visit |
| `DELETE /api/state/last` | Reader closed deliberately; forget the last-open article |
| `GET /healthz` | Liveness |

### Scroll memory (`internal/server/progress.go`)

Positions are stored as a **fraction of the reader's scroll range** (0..1), so
they transfer between devices with different fonts/viewports. The store is an
in-memory map persisted to `$POUCH_DATA_DIR/progress.json` (atomic
write-tmp-then-rename on every update; updates are already throttled
client-side). If the data dir is unwritable Pouch logs a warning and keeps
positions in memory only. Conflict rule per project owner: **furthest wins** —
`Set` never lets a stored position move backward. `GET /api/entries/{id}`
merges the stored position into the entry JSON as `progress`. Deleting an
entry drops its position; nothing is ever pruned otherwise (the file stays
tiny — one number per article ever opened).

### Inline notes (`internal/server/notes.go`)

Same shape as the progress store — an in-memory map (entry id → notes) written
atomically to `$POUCH_DATA_DIR/notes.json` on every change, ids from a single
counter. A note is `{start, end, quote, body}`: character offsets into the
article's text *as the reader renders it*, plus the quoted text so the client
can re-find the passage when the offsets drift (wallabag re-fetches shift
content). `GET /api/entries/{id}` inlines the full list; list/patch responses
carry a `noteCount` so cards can show a note badge. Deleting an entry drops
its notes.

There is intentionally **no auth** (per project owner's request); access control is
the deployment's job (private network / reverse proxy).

### Static serving

`handleStatic` serves the embedded FS; any unknown path falls back to `index.html`
so client routes (notably `/add`, the share-target action) resolve. The fallback
response carries a CSP that allows remote images (article content) but only
same-origin scripts.

## Frontend

No frameworks, no build step. `app.js` is an ES module with:

- **Hash routing**: `#/unread` `#/starred` `#/archive` `#/all` `#/settings`
  `#/read/{id}`. The reader is a full-screen overlay; closing it rewrites the hash
  back to the active list.
- **Share target shim**: manifest `share_target` does `GET /add?url&text&title`.
  On boot, `handleShareTarget()` detects `location.pathname === "/add"`, extracts
  the first http(s) URL from the params, `history.replaceState`s back to `/`, and
  auto-saves (or opens the dialog if no URL was found).
- **Preferences** (`localStorage["pouch:prefs"]`): theme, reading font, text size,
  line spacing, column width. Applied as `data-theme` on `<html>` plus CSS custom
  properties (`--reader-font`, `--reader-size`, `--reader-lh`, `--measure`).
  Theme also updates `<meta name="theme-color">`.
- **Themes** are defined entirely in `style.css` as `:root[data-theme="…"]` variable
  blocks: paper, sepia, mist, slate, ink. Add a theme = add a block + a swatch
  button in `index.html`.
- **Sanitization**: Wallabag stores readable article HTML, but the client still
  strips `script/iframe/object/embed/form`, `on*` attributes and `javascript:`
  URLs via DOMParser before injection, lazy-loads images, and forces
  `target=_blank rel=noopener` on links. CSP is the second layer.
- **Delete is two-step** (tap once to arm, tap again ≤3 s) instead of
  `window.confirm`, to avoid blocking modals.
- **Finish detection** (`armFinishDetection` in `app.js`): an
  IntersectionObserver on the `#artEnd` sentinel (root = the reader's scroll
  container) archives the open entry when the reader reaches the end. It arms
  2 s after open so a quick peek/back never marks anything, and is gated by the
  `autoArchive` preference (Settings → Reading, default on).
- **Unretrievable-content redirect** (`looksUnretrieved`): Wallabag stores a
  placeholder body when it fails to fetch a page ("wallabag can't retrieve
  contents for this article") — and sometimes "succeeds" but stored a bot wall
  (Akamai "Access Denied", Cloudflare "Just a moment…", captcha pages).
  `openReader` detects both (body text < 600 chars + regex over title+text),
  clears the server's last-open state (so resume-on-visit can't loop into the
  redirect), closes the reader, and navigates to the entry's source URL.
  Verified against the owner's real wallabag.it library (60 entries: 9 broken
  all caught, 0 false positives).
- **Scroll memory** (`readerProgress`/`reportProgress` + `resumeOrRoute`):
  the reader's scroll fraction is painted onto `#readProgress` (a 2px
  hairline fixed to the bottom edge, `transform: scaleX`) on every frame, and
  reported to `POST /api/entries/{id}/progress` at most every 2.5 s, plus a
  `sendBeacon` flush on `pagehide`/`visibilitychange`. `openReader` restores
  the stored fraction (skipped when ≥ 0.98 — finished articles restart at the
  top) and registers the entry as last-open even if the user never scrolls.
  `closeReader` flushes and then `DELETE /api/state/last`: a deliberate close
  means the next plain visit shows the list. `resumeOrRoute` (boot): a visit
  with **no hash at all** asks `/api/state` and rewrites the URL to
  `#/read/{id}` when there's an unfinished last-open article; explicit hashes
  and the `/add` share target are never hijacked.
- **Inline notes** (the `renderNotes`/`renderCards` block in `app.js`):
  selecting text in the reader floats a "Note" button over the selection
  (`selectionchange`, debounced; the button acts on `pointerdown` because a
  touch tap would collapse the selection before a click lands). Offsets come
  from cloning the range and the range before it and measuring their text,
  with `.note-card` subtrees stripped so the notes never shift the coordinate
  system they're anchored to. Rendering repaints `#artContent` from the
  pristine sanitized HTML (`artHTML`), wraps every note's range in
  `<mark class="note-mark">` by splitting text nodes at the range boundaries,
  then inserts each note's card after the top-level block its *last* mark sits
  in (so a passage spanning several blocks isn't interrupted by its own note).
  Notes whose quote is nowhere to be found still render, appended at the end,
  rather than vanishing. Editing a note re-places only the cards
  (`renderCards`) so the scroll position doesn't move.
- Keyboard: `a` opens save dialog, `/` focuses filter, `Esc` closes things
  (a note composer first, then the reader), `Cmd/Ctrl+Enter` saves a note.

## Mock Wallabag

`cmd/mockwallabag` implements just enough of the API for Pouch: the oauth token
endpoint (returns a static token), entries listing with archive/starred/tags
filters and paging, create (fakes a fetched title from the URL path), PATCH,
DELETE, and tags. It seeds ~9 hand-written articles across unread/starred/archived
states. `updated_at` is always populated — wallabago's time parser errors on empty
strings.

## Design language

Warm editorial, not Material: serif display type (Iowan Old Style/Palatino stack),
clay-red accent (`#b0523a`), soft parchment surfaces, 12 px radii, restrained
shadows. Icons are a hand-drawn inline SVG sprite (1.8 px strokes, round caps) in
`index.html`. The app icon is a tilted page dropping into a smiling pouch
(`web/static/icons/icon.svg`); PNGs are rendered from it with
`qlmanage -t -s 512` + `sips` (see AGENTS.md).
