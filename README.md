# Pouch

A cozy, self-hosted web client for [Wallabag](https://wallabag.org) — a wallaby keeps
things in its pouch to carry around; so do you.

Pouch is a single Go binary (built on the unofficial Go API wrapper
[Strubbl/wallabago](https://github.com/Strubbl/wallabago)) that serves a fast,
installable PWA with a warm, editorial reading experience. No Material design,
no frontend frameworks, no build step.

## Features

- **Reading-first design** — serif headlines, calm paper-like surfaces, distraction-free
  reader with adjustable typography.
- **Five background themes** — Paper, Sepia, Mist, Slate (dark), Ink (black).
- **Four reading fonts** (serif / sans / humanist / mono), text size, line spacing and
  column width controls, all persisted locally.
- **Effortless saving**
  - **iOS share sheet** via a one-time Shortcut hitting `/api/quick-add?url=…`
    (instructions built into Settings).
  - **Android share sheet** via the PWA `share_target` — install the app and it appears
    natively in the share menu.
  - **Bookmarklet** for any desktop browser (drag it from Settings).
  - **In-app dialog** (`a` key, or the big button).
- **The usual Wallabag verbs** — unread/starred/archive lists, tag filtering,
  star/archive/delete, pagination, client-side filtering.
- **Installable PWA** with offline app shell.
- Cross-platform: it's a web app — anything with a browser, desktop or mobile.

## Quick start

You need a Wallabag instance and an API client (Wallabag → Settings → API clients
management → Create a new client).

```sh
WALLABAG_URL=https://wallabag.example.com \
WALLABAG_CLIENT_ID=… \
WALLABAG_CLIENT_SECRET=… \
WALLABAG_USERNAME=… \
WALLABAG_PASSWORD=… \
go run ./cmd/pouch
```

Open http://localhost:8080.

### Docker

```sh
docker build -t pouch .
docker run -p 8080:8080 \
  -e WALLABAG_URL=… -e WALLABAG_CLIENT_ID=… -e WALLABAG_CLIENT_SECRET=… \
  -e WALLABAG_USERNAME=… -e WALLABAG_PASSWORD=… \
  pouch
```

See [docker-compose.example.yml](docker-compose.example.yml) for a Traefik-labelled
deployment, and [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) for the full runbook.

## Configuration

Everything is environment variables:

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `WALLABAG_URL` | yes | — | Base URL of your Wallabag instance |
| `WALLABAG_CLIENT_ID` | yes | — | Wallabag API client id |
| `WALLABAG_CLIENT_SECRET` | yes | — | Wallabag API client secret |
| `WALLABAG_USERNAME` | yes | — | Wallabag account username |
| `WALLABAG_PASSWORD` | yes | — | Wallabag account password |
| `POUCH_ADDR` | no | `:8080` | Listen address |

Pouch itself has **no authentication layer** — it is designed to sit on a private
network or behind an authenticating reverse proxy. Anyone who can reach it can read
and save to your Wallabag.

## Saving from iOS

1. Open **Shortcuts**, create a new shortcut.
2. Add **Get contents of URL** with the URL
   `https://your-pouch-host/api/quick-add?url=` followed by the **Shortcut Input**
   variable.
3. In the shortcut settings enable **Show in Share Sheet**, name it "Save to Pouch".

Settings → *Saving links* inside the app shows the same instructions with your
actual host filled in.

## Development

A mock Wallabag server ships in-repo so you can develop without touching a real
instance:

```sh
go run ./cmd/mockwallabag -addr :8091 &

POUCH_ADDR=:8090 WALLABAG_URL=http://localhost:8091 \
WALLABAG_CLIENT_ID=mock WALLABAG_CLIENT_SECRET=mock \
WALLABAG_USERNAME=mock WALLABAG_PASSWORD=mock \
go run ./cmd/pouch
```

The frontend is embedded via `go:embed`, so restart `cmd/pouch` after editing
anything in `web/static/`.

More detail in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) and
[AGENTS.md](AGENTS.md).

## License

MIT
