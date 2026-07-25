# Agent guide — Pouch

Read `docs/ARCHITECTURE.md` first for the mental model; this file is the
operational how-to for working on Pouch.

## Build, run, verify

```sh
go build ./... && go vet ./...           # must stay clean

# Dev loop (no real Wallabag needed):
go run ./cmd/mockwallabag -addr :8091 &
POUCH_ADDR=:8090 WALLABAG_URL=http://localhost:8091 \
WALLABAG_CLIENT_ID=mock WALLABAG_CLIENT_SECRET=mock \
WALLABAG_USERNAME=mock WALLABAG_PASSWORD=mock go run ./cmd/pouch
```

- Frontend assets are **embedded at compile time** (`web/embed.go`); after editing
  `web/static/*` you must restart `cmd/pouch` to see changes.
- Ports 8080/8081 are often taken on the dev machine — use 8090/8091.
- Verify UI changes in a real browser (chrome-devtools MCP works well):
  list view, reader (`#/read/1` has rich content; `#/read/2` and `#/read/3`
  must bounce to their source URLs — placeholder and bot-wall cases), typo
  panel, `#/settings`, mobile at 390×844, `/add?url=…` share-target shim,
  `/api/quick-add?url=…` page. Watch the console for errors; there should be
  none. Beware same-URL/hash-only navigations: they don't reload the page, so
  the tab keeps running the previously served app.js.
- Local runs create `./data/progress.json` (scroll memory) and
  `./data/notes.json` (inline notes) — both gitignored; override the location
  with `POUCH_DATA_DIR`.
- Notes are driven by text selection, which is awkward to click through by
  hand: build the selection with a `Range` + `getSelection().addRange(…)` in
  the console, then dispatch `pointerdown` on `#noteAddBtn`.

## Conventions

- Vanilla JS/CSS only; no frameworks, no build tooling, no external fonts/CDNs.
- Design language: warm editorial (see bottom of ARCHITECTURE.md). Keep new UI in
  that vocabulary — CSS custom properties for theming, serif display type, the
  existing spacing/radius scale.
- Icons: extend the inline SVG sprite in `index.html` (24×24 viewBox, stroke 1.8,
  round caps); don't add icon fonts or files.
- New themes = a `:root[data-theme="…"]` block in `style.css` + swatch buttons in
  both `#themeSwatches` and `#typoSwatches` (swatch colors are hardcoded in the
  `.swatch[data-theme-pick=…]` rules).
- Backend: keep handlers thin; anything touching wallabago's non-200 quirk goes
  through `apiCall` in `internal/server/api.go`.
- No auth in Pouch by owner decision — don't add login flows; access control
  belongs to the reverse proxy.

## App icon regeneration

After editing `web/static/icons/icon.svg` (macOS):

```sh
cd web/static/icons
qlmanage -t -s 512 -o . icon.svg && mv icon.svg.png icon-512.png
sips -z 192 192 icon-512.png --out icon-192.png
sips -z 180 180 icon-512.png --out apple-touch-icon.png
```

## Service worker gotcha

`sw.js` is **network-first for everything** (cache is only an offline
fallback), so deploys show up on the next reload. History: v1 used
stale-while-revalidate for assets, which served a one-deploy-old app.js and
broke a feature test — don't reintroduce cache-first without a cache-busting
scheme. Bump the `CACHE` name when changing the SHELL list or the strategy.

## Deployment

Deployment specifics (host, domain, runbook) are intentionally kept out of
the repo; they live in the owner's private notes.

## Release checklist

1. `go build ./... && go vet ./...`
2. Manual browser pass against the mock (see above).
3. Bump `VERSION` in `web/static/app.js` if user-visible.
4. Commit, push to GitHub (`momja/pouch`).
5. Deploy per the private runbook; verify the production URL and `/healthz`.
