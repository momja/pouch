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
  api.go                   REST handlers, wallabago glue, quick-add HTML page
web/
  embed.go                 //go:embed all:static → web.Static
  static/
    index.html             Full markup for every view + inline SVG icon sprite
    style.css              Hand-rolled design system (themes as CSS vars)
    app.js                 SPA: hash router, state, rendering, prefs
    sw.js                  Service worker (precache shell, network-first nav)
    manifest.webmanifest   PWA manifest incl. share_target
    icons/                 icon.svg + PNGs rendered from it
Dockerfile                 Two-stage build → alpine, non-root, healthcheck
docker-compose.example.yml Traefik-labelled reference deployment
```

## Backend

### wallabago usage and its quirks

- `wallabago.SetConfig(...)` is called once in `server.New`; the library keeps
  package-global state (config + oauth token, refreshed automatically).
- Listing uses `wallabago.GetEntries(wallabago.APICall, archive, starred, "created",
  "desc", page, perPage, tag, -1, -1, "metadata", "")`. Passing `-1` for
  archive/starred skips those filters; `detail=metadata` keeps list payloads light
  (server omits content).
- **Quirk:** `wallabago.APICall` returns `(nil, nil)` when Wallabag answers a
  non-200 status. `internal/server/api.go:apiCall` wraps it and converts that case
  into a real error. Use the wrapper for any direct calls.
- Create (POST), update (PATCH) and delete of entries are done with `apiCall`
  directly rather than `wallabago.PostEntry`, because the wrapper's helpers throw
  away the response body and Pouch wants the created/updated entry JSON.
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
| `GET /healthz` | Liveness |

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
- Keyboard: `a` opens save dialog, `/` focuses filter, `Esc` closes things.

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
