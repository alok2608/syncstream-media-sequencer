# Multi-Window Media Sequencer with Sync Playback

Multiple display windows, each continuously playing its own configured playlist,
plus a global **sync** action that puts one media item on every window at the
same instant without touching any playlist.

**React + Vite frontend · Go backend · PostgreSQL · WebSocket realtime**

---

## Table of contents

1. [Live URLs](#1-live-urls)
2. [Project overview](#2-project-overview)
3. [Features](#3-features)
4. [Architecture](#4-architecture)
5. [Tech stack](#5-tech-stack)
6. [Folder structure](#6-folder-structure)
7. [Database schema](#7-database-schema)
8. [Local setup](#8-local-setup)
9. [Environment variables](#9-environment-variables)
10. [Migrations](#10-migrations)
11. [Seed data](#11-seed-data)
12. [API documentation](#12-api-documentation)
13. [WebSocket events](#13-websocket-events)
14. [Continuous playback](#14-continuous-playback)
15. [The 5-hour cycle, exactly](#15-the-5-hour-cycle-exactly)
16. [The sync algorithm](#16-the-sync-algorithm)
17. [Behaviour after sync](#17-behaviour-after-sync)
18. [Dynamic playlist updates](#18-dynamic-playlist-updates)
19. [Assumptions](#19-assumptions)
20. [Edge cases](#20-edge-cases)
21. [Tradeoffs](#21-tradeoffs)
22. [Testing](#22-testing)
23. [Deployment](#23-deployment)

---

## 1. Live URLs

| Component | URL |
|---|---|
| **Frontend** (Vercel) | **https://syncstream-media-sequencer.vercel.app** |
| **Backend** (Render) | **https://syncstream-backend-ot21.onrender.com** |
| Health check | https://syncstream-backend-ot21.onrender.com/health |
| Source | https://github.com/alok2608/syncstream-media-sequencer |

Open the frontend and three windows begin playing immediately. Press
**SYNC ALL WINDOWS** to put one media item on all of them at once.

> **First load may take up to ~50 seconds.** The backend runs on Render's free
> tier, which sleeps after ~15 minutes of inactivity and has to cold-start. The
> page will show *Backend unreachable* while that happens and recovers on its
> own — playback never stops, because it runs entirely off the local cycle
> clock. Subsequent requests are fast (~1s). Hitting the health check first
> wakes it up.

Everything also runs locally with no cloud account — see
[§8 Local setup](#8-local-setup).

---

## 2. Project overview

Each **window** owns an ordered **playlist** of **media** (image, video or an
explicitly configured blank). Every window plays its own playlist end to end,
restarts immediately, and keeps doing so — no pauses between items, no blank
filler.

The central design decision is that **the backend never streams "the next
item"**. It stores configuration and sync state; the browser computes which item
is playing right now as a pure function of time:

```
currentItem = f(cycleAnchor, serverNow, playlistDurations)
```

That one choice is what makes refresh, reconnect, multi-tab and multi-device all
work without any special handling — there is no playback state to lose, so there
is nothing to restore.

---

## 3. Features

**Playback**
- Continuous per-window playback with instantaneous item handover.
- Deterministic: any two clients agreeing on the time render the same item.
- Explicit, testable 5-hour cycle (see [§15](#15-the-5-hour-cycle-exactly)).
- Image, video and blank media, with a graceful fallback on load failure.
- Videos seek to the correct in-item offset instead of restarting on refresh.

**Global sync**
- One media item on every window, aligned to a backend-generated timestamp.
- A temporary render-layer override — playlists are never modified.
- Clients joining or refreshing mid-sync land at the correct offset.
- A newer sync supersedes the active one; sync can also be cancelled early.

**Management**
- Create windows; add, reorder and remove playlist items.
- Add media inline or reuse the library; everything persists in PostgreSQL.
- Changes broadcast over WebSocket and appear in every open browser instantly.

**Reliability**
- Automatic WebSocket reconnection with exponential backoff and jitter.
- REST polling fallback while the socket is down; playback never stops.
- Browser/server clock-skew correction (NTP-style offset estimation).
- Graceful shutdown, embedded migrations, safe concurrent broadcasting.

---

## 4. Architecture

```
┌──────────────────────────── Browser ─────────────────────────────┐
│                                                                   │
│  useServerClock ──► measured offset from the server's clock       │
│         │                                                         │
│         ▼                                                         │
│  serverNow() ──► utils/playback.js ──► which item is playing now  │
│         │                    ▲                                    │
│         │                    │ playlist configuration             │
│         ▼                    │                                    │
│  utils/sync.js ──► is the global override on screen right now?    │
│         │                                                         │
│         ▼                                                         │
│  WindowCard ──► MediaPlayer (image / video / blank / fallback)    │
└───────────────────────────────────────────────────────────────────┘
            │  REST: configuration + commands       ▲  WebSocket: events
            ▼                                       │
┌──────────────────────────── Go backend ───────────────────────────┐
│  handlers ──► service ──► repository ──► PostgreSQL               │
│                  │                                                │
│                  └──► websocket hub ──► broadcast to all clients  │
│                                                                    │
│  internal/playback — the same clock, used by GET .../current       │
└────────────────────────────────────────────────────────────────────┘
```

**Responsibility split**

| Backend owns | Frontend owns |
|---|---|
| Windows, media, playlists | Rendering media |
| Playlist mutations + broadcasting | Local playback timing |
| Sync state and its shared timestamps | Sync override rendering |
| Persistence, migrations, seeding | Operator controls |
| The authoritative clock | Clock-offset correction |

### Why the playback clock exists in both Go and JavaScript

`backend/internal/playback` and `frontend/src/utils/playback.js` implement the
same algorithm. This is deliberate duplication, and it is kept honest rather than
left to drift:

- Both test suites are driven by the **same fixture file**,
  `backend/testdata/playback_cases.json` (17 cases). If either implementation
  changes behaviour, that side's tests fail.
- The Go copy backs `GET /api/windows/{id}/current`, which lets an evaluator
  verify the 5-hour cycle from the command line with `curl` — no browser, no
  waiting five hours.

The frontend never calls that endpoint during playback; it is a reference and
debugging aid.

---

## 5. Tech stack

| Layer | Choice | Why |
|---|---|---|
| Frontend | React 18 + Vite (JavaScript) | Required; Vite for fast builds and clean env handling |
| Styling | Hand-written CSS with custom properties | No framework needed for one screen; keeps the bundle small |
| Backend | Go 1.24, `net/http` | Go 1.22+ `ServeMux` does method+pattern routing, so no router dependency |
| Realtime | `gorilla/websocket` | The de-facto standard; nothing in the stdlib covers it |
| Database | PostgreSQL via `pgx/v5` | Direct SQL, no ORM — the queries here are small and explicit |
| Migrations | Embedded SQL + advisory lock | No external tool; the binary carries its own schema |
| Tests | `go test`, Vitest | Shared JSON fixtures prove cross-language parity |

**Direct third-party dependencies: two** (`pgx`, `gorilla/websocket`) on the backend, plus React on the frontend.

---

## 6. Folder structure

```
.
├── README.md
├── render.yaml                     # Render blueprint: web service + PostgreSQL
│
├── backend/
│   ├── Dockerfile                  # Multi-stage, static binary, non-root
│   ├── .env.example
│   ├── go.mod / go.sum
│   ├── assets/                     # Demo videos embedded into the binary
│   │   ├── embed.go
│   │   ├── ATTRIBUTION.md
│   │   ├── bunny-clip.mp4
│   │   └── sintel-clip.mp4
│   ├── migrations/
│   │   ├── embed.go
│   │   └── 0001_init.sql
│   ├── testdata/
│   │   └── playback_cases.json     # Shared with the frontend test-suite
│   ├── cmd/
│   │   ├── server/main.go          # HTTP + WebSocket server
│   │   └── seed/main.go            # Seeding CLI (-force to reset)
│   └── internal/
│       ├── config/                 # Environment loading + CORS allow-list
│       ├── database/               # Pool + embedded migration runner
│       ├── models/                 # Domain types + validation
│       ├── playback/               # The deterministic playback clock
│       ├── repository/             # SQL only
│       ├── service/                # Application logic (sequencer + sync)
│       ├── handlers/               # HTTP handlers + router
│       ├── httpx/                  # JSON envelope, middleware, CORS
│       ├── websocket/              # Hub, client pumps, event types
│       ├── seed/                   # Demo dataset
│       └── testsupport/            # In-memory fakes shared by test suites
│
└── frontend/
    ├── vercel.json                 # SPA rewrites
    ├── .env.example
    ├── index.html
    ├── vite.config.js
    └── src/
        ├── main.jsx
        ├── App.jsx
        ├── api/client.js           # REST client + media URL resolution
        ├── hooks/
        │   ├── useServerClock.js   # Clock-offset estimation
        │   ├── useWebSocket.js     # Connection + reconnection
        │   ├── useSequencerState.js# Configuration state + event handling
        │   └── useNow.js           # Render tick
        ├── components/
        │   ├── WindowGrid.jsx
        │   ├── WindowCard.jsx
        │   ├── MediaPlayer.jsx
        │   ├── PlaylistManager.jsx
        │   ├── SyncControls.jsx
        │   ├── SyncBanner.jsx
        │   └── StatusBar.jsx
        ├── utils/
        │   ├── playback.js         # The playback clock (+ .test.js)
        │   ├── sync.js             # Sync phase resolution (+ .test.js)
        │   └── format.js
        └── styles/index.css
```

---

## 7. Database schema

```sql
windows
  id            bigint       PK, identity
  name          text         NOT NULL, UNIQUE, non-blank
  cycle_anchor  timestamptz  NOT NULL   -- origin of this window's 5-hour cycles
  created_at    timestamptz  NOT NULL
  updated_at    timestamptz  NOT NULL

media
  id                bigint       PK, identity
  name              text         NOT NULL, non-blank
  type              text         NOT NULL  CHECK IN ('image','video','blank')
  url               text         NOT NULL DEFAULT ''
  duration_seconds  integer      NOT NULL  CHECK BETWEEN 1 AND 3600
  created_at        timestamptz  NOT NULL
  CHECK (type IN ('image','video') AND url <> '')  OR  (type = 'blank' AND url = '')

playlist_items
  id          bigint       PK, identity
  window_id   bigint       NOT NULL  REFERENCES windows(id)  ON DELETE CASCADE
  media_id    bigint       NOT NULL  REFERENCES media(id)    ON DELETE RESTRICT
  position    integer      NOT NULL  CHECK >= 0
  created_at  timestamptz  NOT NULL
  UNIQUE (window_id, position) DEFERRABLE INITIALLY DEFERRED
  INDEX (window_id, position),  INDEX (media_id)

sync_events
  id            bigint       PK, identity
  media_id      bigint       NOT NULL  REFERENCES media(id) ON DELETE CASCADE
  start_at      timestamptz  NOT NULL   -- backend-generated shared start instant
  end_at        timestamptz  NOT NULL   CHECK (end_at > start_at)
  cancelled_at  timestamptz             -- set when superseded or cancelled
  created_at    timestamptz  NOT NULL
  INDEX (end_at DESC)
```

Two schema decisions worth calling out:

- **`UNIQUE (window_id, position) DEFERRABLE INITIALLY DEFERRED`** — reordering
  shifts a range of positions, which transiently collides. Deferring the check to
  commit lets the whole reorder happen in one clean transaction instead of
  needing negative-position tricks.
- **`cycle_anchor` is stored, not derived** — playback must survive a restart and
  be identical for every client, so the origin of the cycle is persisted.

---

## 8. Local setup

**Prerequisites:** Go 1.24+, Node 18+, PostgreSQL 14+.

```bash
git clone <your-repo-url> syncstream-media-sequencer
cd syncstream-media-sequencer
```

### Backend

```bash
cd backend
createdb sequencer            # or use any existing database

cp .env.example .env          # then edit DATABASE_URL
go run ./cmd/server
```

The server loads `backend/.env` itself, so there is nothing to source. Real
environment variables always win over the file, so the same binary reads
Render's injected config in production, where no `.env` exists.

On boot the server connects, applies migrations, seeds the demo data if the
database is empty, and listens on `:8080`:

```
{"msg":"connected to postgres"}
{"msg":"database schema is up to date"}
{"msg":"seeded window","name":"Window 1 - Lobby","items":3}
{"msg":"listening","port":"8080"}
```

Verify:

```bash
curl -s localhost:8080/health | jq
```

### Frontend

```bash
cd frontend
cp .env.example .env
npm install
npm run dev
```

Open **http://localhost:5173**. You should see three windows already playing.

The dev server is pinned to 5173 (`strictPort`), so it fails with
`Port 5173 is already in use` rather than quietly starting on 5174 — a
different port is a different origin, and a silent move makes a healthy backend
look dead. If you hit that, free the port and retry:

```bash
lsof -ti tcp:5173 | xargs kill
```

---

## 9. Environment variables

### Backend (`backend/.env`)

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `DATABASE_URL` | **yes** | — | PostgreSQL connection string |
| `PORT` | no | `8080` | Listen port (Render sets this) |
| `FRONTEND_URL` | no | `http://localhost:5173` | Comma-separated CORS/WebSocket origin allow-list. `*` allows any origin |
| `ALLOW_LOOPBACK_ORIGINS` | no | `true` | Also accept any `localhost` / `127.0.0.1` / `[::1]` origin on **any port** |
| `SYNC_LEAD_TIME_MS` | no | `1000` | How far ahead a sync is scheduled so clients can prepare (0–30000) |
| `AUTO_SEED` | no | `true` | Seed demo data on boot when no windows exist |
| `LOG_REQUESTS` | no | `true` | HTTP access logging |
| `SEED_VIDEO_BASE_URL` | no | `/api/assets` | Base URL for seeded videos |

Values are read from `backend/.env` when that file exists. Go has no built-in
`.env` support, so the server parses it on startup (`internal/config/dotenv.go`,
no dependency). Anything already exported takes precedence, which means a stale
local `.env` can never shadow what a deployment platform injects.

### Frontend (`frontend/.env`)

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `VITE_API_URL` | no | `http://localhost:8080` | Backend base URL |
| `VITE_WS_URL` | no | derived from `VITE_API_URL` | WebSocket endpoint |

`VITE_WS_URL` is derived by swapping `http`→`ws` / `https`→`wss` and appending
`/ws`, so a single `VITE_API_URL` is enough in the common case, and HTTPS
deployments automatically get `wss://`.

Any loopback origin (`localhost`, `127.0.0.1`, `[::1]`) is accepted on **any
port** in addition to `FRONTEND_URL`. This matters more than it sounds: if port
5173 is already in use, Vite silently starts on 5174 instead, and the browser's
origin then matches nothing in the allow-list. The symptom is a backend that
looks completely dead — CORS blocks every read and the WebSocket upgrade is
refused — when in fact it is running perfectly. Set
`ALLOW_LOOPBACK_ORIGINS=false` to require an exact match.

No localhost or deployment domain appears anywhere in application logic.

---

## 10. Migrations

SQL files in `backend/migrations/` are **embedded into the binary** and applied
automatically on every boot, in filename order, each inside its own transaction.
Applied versions are recorded in `schema_migrations`.

Concurrently booting instances are serialised by a PostgreSQL advisory lock, so
two Render instances starting together cannot race.

To add a migration, drop in `0002_something.sql` and restart — there is no
separate migration command to remember.

---

## 11. Seed data

Seeding runs automatically when the database contains no windows (`AUTO_SEED`),
or on demand:

```bash
cd backend
go run ./cmd/seed            # no-op if data already exists
go run ./cmd/seed -force     # wipe playback configuration and re-seed
```

The dataset mirrors the assignment's example:

| Window | Playlist | Loop length |
|---|---|---|
| Window 1 — Lobby | M1 (image 10s) → M2 (image 20s) → M3 (video 30s) | 60s |
| Window 2 — Cafeteria | M4 (video 15s) → M5 (image 12s) | 27s |
| Window 3 — Reception | M6 (image 8s) → **M7 (blank 5s)** → M8 (video 20s) | 33s |

**M7 is the explicit blank item** — the only route by which a blank frame ever
appears.

### Seed media is self-contained by design

The brief warns against depending on unstable URLs. Public sample-media hosts rot
(Google's `gtv-videos-bucket`, long the standard demo source, now returns
`403 AccessDenied`), and a playback demo that silently shows fallback cards is
impossible to evaluate. So:

- **Images** are generated as inline `data:image/svg+xml` URIs — labelled
  gradient cards, no network request at all.
- **Videos** are short CC-BY clips **embedded into the Go binary** and served by
  the backend at `/api/assets/{name}` with HTTP Range support. See
  [`backend/assets/ATTRIBUTION.md`](backend/assets/ATTRIBUTION.md).

The demo therefore works offline and behind a firewall. Seeded video URLs are
stored **root-relative** (`/api/assets/bunny-clip.mp4`) and resolved by the
frontend against `VITE_API_URL`, so the same rows work in every environment.
Point `SEED_VIDEO_BASE_URL` at your own host to seed differently, or just add
real media through the UI.

---

## 12. API documentation

Base URL: `VITE_API_URL`. All responses are JSON.

**Success** — `{ "data": ... }`
**Error** — `{ "error": { "code": "...", "message": "...", "fields": [...] } }`

| Status | Meaning |
|---|---|
| 200 / 201 | Success |
| 400 | Malformed JSON, unknown field, or bad path parameter |
| 404 | No such window, item, route or asset |
| 422 | Validation failed (`fields` lists every problem at once) |
| 500 / 503 | Server error / database unreachable |

### Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Liveness + window/media counts, socket count, sync flag |
| `GET` | `/api/time` | Server clock, for offset estimation |
| `GET` | `/api/state` | **Bootstrap snapshot** — everything a client needs |
| `GET` | `/api/windows` | All windows with playlists |
| `POST` | `/api/windows` | Create a window |
| `GET` | `/api/windows/{id}` | One window with its playlist |
| `GET` | `/api/windows/{id}/playlist` | One window's playlist |
| `POST` | `/api/windows/{id}/playlist` | Append media (by id or inline) |
| `PATCH` | `/api/windows/{id}/playlist/{itemId}` | Reorder (`{"position": n}`) |
| `DELETE` | `/api/windows/{id}/playlist/{itemId}` | Remove an item |
| `GET` | `/api/windows/{id}/current` | **Reference playback resolution** |
| `GET` | `/api/media` | Media library |
| `POST` | `/api/media` | Create media |
| `POST` | `/api/sync` | Start a global sync |
| `GET` | `/api/sync/active` | The live override, or `null` |
| `POST` | `/api/sync/cancel` | End the live override early |
| `GET` | `/api/assets/{name}` | Bundled demo media (supports Range) |
| `GET` | `/ws` | WebSocket upgrade |

### Examples

**Bootstrap snapshot**

```bash
curl -s localhost:8080/api/state | jq
```
```jsonc
{
  "data": {
    "serverTime": "2026-01-01T12:00:00Z",
    "cycleMillis": 18000000,
    "windows": [
      {
        "id": 1,
        "name": "Window 1 - Lobby",
        "cycleAnchor": "2026-01-01T09:30:00Z",
        "playlistDurationMillis": 60000,
        "playlist": [
          {
            "id": 1, "windowId": 1, "position": 0,
            "media": { "id": 1, "name": "M1 - Sunrise Board", "type": "image",
                       "url": "data:image/svg+xml;base64,...", "durationSeconds": 10 }
          }
        ]
      }
    ],
    "media": [ /* ... */ ],
    "activeSync": null
  }
}
```

**Add media to a playlist** — either reference the library:

```bash
curl -X POST localhost:8080/api/windows/2/playlist \
  -H 'Content-Type: application/json' \
  -d '{"mediaId": 3}'
```

…or define it inline (what the UI form does):

```bash
curl -X POST localhost:8080/api/windows/2/playlist \
  -H 'Content-Type: application/json' \
  -d '{"media": {"name": "M9 - Winter Sale", "type": "image",
                 "url": "https://example.com/sale.jpg", "durationSeconds": 15}}'
```

Both return the **whole updated window**, so a client never needs a follow-up
read. `type: "blank"` requires no `url`.

**Reorder / remove**

```bash
curl -X PATCH localhost:8080/api/windows/1/playlist/3 \
  -H 'Content-Type: application/json' -d '{"position": 0}'

curl -X DELETE localhost:8080/api/windows/1/playlist/3
```

Positions stay contiguous (`0..n-1`) after every operation. A `position` beyond
the end is clamped, so `999` means "last".

**Start a global sync**

```bash
curl -X POST localhost:8080/api/sync \
  -H 'Content-Type: application/json' \
  -d '{"mediaId": 2, "durationSeconds": 30}'
```
```jsonc
{
  "data": {
    "id": 7,
    "media": { "id": 2, "name": "M2 - Product Promo", "type": "image", "durationSeconds": 20 },
    "startAt": "2026-01-01T12:00:01.000Z",   // now + SYNC_LEAD_TIME_MS
    "endAt":   "2026-01-01T12:00:31.000Z",
    "createdAt": "2026-01-01T12:00:00.000Z"
  }
}
```

**Inspect the playback clock** (no browser needed):

```bash
curl -s localhost:8080/api/windows/1/current | jq '.data.state, .data.media.name'
```
```jsonc
{
  "index": 1,                       // second item in the playlist
  "cycleIndex": 0,                  // first 5-hour cycle since the anchor
  "elapsedInCycleMillis": 9015000,
  "loopIteration": 150,             // 150 complete passes so far this cycle
  "elapsedInItemMillis": 15000,
  "remainingInItemMillis": 5000,
  "truncatedByCycle": false
}
```

### Validation rules

| Field | Rule |
|---|---|
| `name` | non-blank, ≤ 120 characters |
| `type` | `image` \| `video` \| `blank` |
| `url` | required for image/video; must be `http(s)`, a `data:` URI, or root-relative. Ignored for blank |
| `durationSeconds` | 1 – 3600 |
| `durationSeconds` (sync) | 1 – 3600 |

Unknown JSON fields are rejected with a 400 so a typo never silently no-ops.

---

## 13. WebSocket events

Connect to `GET /ws`. Origin is checked against the same allow-list as CORS.

Every server message uses one envelope:

```jsonc
{ "type": "...", "serverTime": "2026-01-01T12:00:00.123Z", "payload": { } }
```

`serverTime` is on **every** message, so a client's clock estimate keeps
improving for as long as the page is open.

| Type | Direction | Payload | Meaning |
|---|---|---|---|
| `HELLO` | → client | full snapshot | Sent on connect: configuration + live sync |
| `PING` | client → | `{clientTime}` | Client heartbeat (every 10s) |
| `PONG` | → client | `{clientTime}` | Echo + `serverTime`, used for offset estimation |
| `PLAYLIST_UPDATED` | → client | window with playlist | A playlist changed |
| `WINDOW_CREATED` | → client | window with playlist | A window was created |
| `MEDIA_CREATED` | → client | media | Media was added to the library |
| `SYNC_STARTED` | → client | sync event | A global override was scheduled |
| `SYNC_ENDED` | → client | `{syncId}` | The override finished |
| `SYNC_CANCELLED` | → client | sync event | The override was ended early |

`HELLO` carries the **entire snapshot**, so a reconnecting client is immediately
consistent without an extra REST round trip.

### Concurrency

All hub state lives in one goroutine and is reached only through channels — no
shared map, no lock to forget. Each client has one reader and one writer
goroutine, so concurrent broadcasts can never interleave writes on a socket.

A client that stops draining its queue (32 messages) is dropped rather than
allowed to block the broadcast, and `Publish` is safe to call after shutdown.
The whole package is tested under `-race`.

---

## 14. Continuous playback

The frontend ticks at 10 Hz. On each tick it recomputes, from scratch:

```js
const state = resolvePlayback(timeline, cycleAnchorMillis, serverNowMillis);
```

There are **no `setTimeout` chains** and no per-item timers. The consequences:

- **Handover is exact.** At the instant an item ends, the next one already owns
  the clock — `elapsedInItemMillis === 0`, not "a few ms late".
  Asserted for all 900 transitions in a cycle, in both languages.
- **Drift cannot accumulate**, because nothing is ever derived from the previous
  tick.
- **Refresh costs nothing.** There is no playback state to restore.
- **A throttled or sleeping tab self-heals.** It resolves the correct item on its
  next tick instead of replaying a backlog of missed timers.

### Clock skew

Absolute server timestamps are useless if the browser's clock is wrong, so the
offset is measured, not assumed — NTP-style, over both REST and the WebSocket:

```
offset = serverTime − (sentAt + receivedAt) / 2
```

The lowest-round-trip sample of the last eight wins, since a fast exchange
brackets the server's clock most tightly. The measured offset is displayed in the
status bar. A browser 45 seconds fast still renders exactly the right item.

### Media behaviour

| Type | Behaviour |
|---|---|
| **Image** | Displayed for its configured duration |
| **Video** | See below |
| **Blank** | Clean black frame for its configured duration |
| **Failure** | A labelled fallback card; the clock keeps running and moves on |

**Video: the configured duration is authoritative, not the file's metadata.**
This is a deliberate choice — if progression depended on `video.duration`, timing
would hinge on network speed and codec quirks, the clock would stop being
deterministic, and windows on different machines would drift apart.

So a video occupies exactly its configured slot: one shorter than its slot loops
inside it (a 10s clip in a 30s slot plays three times); one longer is cut off
when the slot ends. On mount the element is seeked to the correct in-item offset,
so a refresh mid-video resumes in place rather than restarting.

**Drift correction.** A `<video>` free-runs once it starts, and anything that
stalls it — a backgrounded tab, a decode hiccup, a slow network — would
otherwise become permanent drift against the cycle clock. The element's position
is therefore re-checked every 2s (and immediately when a tab becomes visible)
and re-seeked if it is more than 1s out. The tolerance is wide enough that
ordinary playback is never interrupted; only a real stall trips it.

Measured: a tab backgrounded for several seconds during a synced video came back
6.3s out of step before this, and 0.1s after.

---

## 15. The 5-hour cycle, exactly

> *"The total play size for each window must be treated as 5 hours… Blank is only
> a configured playlist item when included; the rest of the cycle should not
> become blank playback by default."*

**Interpretation: the 5-hour cycle is a re-anchor clock, not a container to
fill.**

Each window has a persisted `cycle_anchor`. Given `CYCLE = 18,000,000 ms`:

```
delta            = now − cycleAnchor
cycleIndex       = floor(delta / CYCLE)          // which 5-hour cycle
elapsedInCycle   = delta − cycleIndex × CYCLE    // 0 … CYCLE-1
loopIteration    = floor(elapsedInCycle / playlistTotal)
offsetInPlaylist = elapsedInCycle mod playlistTotal
currentItem      = the item covering offsetInPlaylist
```

Concretely, with the assignment's own example — M1 = 10s, M2 = 20s, M3 = 30s,
playlist total 60s:

- The playlist repeats **300 times** back to back across the five hours.
- `18,000,000 / 60,000 = 300` exactly, so the 300th pass ends precisely on the
  boundary and the next cycle begins cleanly at M1.
- **17,940 seconds of "unused" time do not exist.** There is no remainder to
  blank out, because the playlist loops for the whole cycle.

**What happens at the boundary when the playlist does *not* divide evenly?**
The final pass is **truncated** and the next cycle re-anchors at position 0. With
a 7s playlist: 2,571 whole passes (17,997,000 ms) then a 3,000 ms partial pass,
cut off at the boundary. This is the one place the cycle is observable at all,
and it is asserted directly (`truncatedByCycle: true`).

**Blank playback happens only when a blank item is configured.** Unused cycle
time is never converted into blank output — the mechanism to do so does not
exist in the code. An *empty* playlist renders a clearly-labelled fallback card,
which is distinct from blank media.

### It is testable, not decorative

The cycle is not a five-hour timer that quietly does nothing. It is arithmetic,
so it is tested at every interesting instant without waiting:

| Assertion | Where |
|---|---|
| Last millisecond of a cycle still plays real media | fixture case 7, both languages |
| Boundary re-anchors to position 0, `cycleIndex` increments | fixture case 8 |
| Uneven playlists truncate at the boundary | fixture case 10 |
| Times *before* the anchor resolve into the previous cycle | fixture case 11 |
| An item exactly one cycle long | fixture case 12 |
| A playlist *longer* than a cycle can never complete an item | fixture case 13 |
| The tail of a cycle never goes blank | `TestCycleBoundaryNeverBlanks` |
| All 900 handovers in a cycle are gapless | `TestPlaybackIsContinuous` |

Or check it live, with no waiting:

```bash
curl -s localhost:8080/api/windows/1/current | jq .data.state
```

---

## 16. The sync algorithm

**Sync is a temporary, global, render-layer override.** It shows one media item
on every window at the same instant and modifies nothing.

```
1. Operator picks media + duration, presses SYNC ALL WINDOWS.

2. Backend computes the shared instants:
       startAt = now + SYNC_LEAD_TIME_MS   (default 1000ms)
       endAt   = startAt + duration
   ...persists the event (superseding any live one, atomically), then
   broadcasts SYNC_STARTED with those absolute timestamps.

3. Each client, every tick, asks a pure question:
       startAt <= serverNow() < endAt  ?
   If yes it renders the synced media instead of its own item.

4. At endAt every client stops overriding, independently and simultaneously.
```

### Why a lead-in, and why timestamps

The brief warns against *"client receives event → immediately starts local
timer"*, and rightly: messages reach different browsers milliseconds apart, and
that spread would show up directly as windows falling out of step.

Here, **arrival time is never used for anything.** Clients align to `startAt`.
The ~1s lead-in gives every connected client time to receive the event and
preload the media *before* the shared instant arrives, so the switch is
simultaneous rather than staggered by network luck. During the lead-in the UI
shows `GLOBAL SYNC STARTING` and the override is not yet on screen.

This is directly asserted:

```js
it('resolves identically for clients that received the event at different times')
```

### Joining late, and refreshing mid-sync

`GET /api/sync/active` returns the live override — including one still inside its
lead-in — and `HELLO` carries it too. A page that loads mid-sync gets the same
absolute timestamps and lands at exactly the right offset. Verified in a real
browser: refreshing during a sync rejoins with the correct remaining time.

### Overlapping syncs

**The newest sync wins.** Creating one cancels any live override in the *same
transaction*, so two operators pressing SYNC simultaneously can never leave two
overrides live. The superseded event's original `end_at` no longer keeps anything
alive. An operator can also end a sync early with `POST /api/sync/cancel`.

### Reliability of the end

Clients already know `endAt` and drop the override themselves, so a missed, late
or undelivered `SYNC_ENDED` **cannot strand a window in sync**. The broadcast is
a convenience that refreshes idle clients' status banners. The backend also
re-arms that timer on boot, so a redeploy mid-sync still announces the end.

---

## 17. Behaviour after sync

**Resume semantics: the cycle clock never stopped.**

Sync overrides only what is *rendered*. Underneath, each window's playback state
keeps advancing the entire time, because it is computed from the clock rather
than stored. When the override ends, each window resumes at exactly the item it
would have been showing had the sync never happened — no drift, no
double-playing, no rewind, no restart.

You can watch this happen: during a sync each card shows a small
`underneath: <item>` label. In the verified run, Window 3 moved from M6 to M7 to
M8 *while* it was displaying the synced M2.

This is the strongest form of the requirement: sync is purely visual, and
playlist progression is mathematically untouched by it.

**Playlists are never modified.** Syncing M2 does not insert M2 into Window 2's
playlist. Asserted by `TestSyncDoesNotModifyPlaylists`, which fingerprints every
window's playlist before, during and after a sync.

---

## 18. Dynamic playlist updates

Mutations are REST writes; the resulting state is broadcast over WebSocket, so
every open browser converges without a reload and without restarting anything.

**Why WebSocket rather than polling:** the sync feature needs low-latency,
server-initiated delivery, and once a socket exists for that, playlist updates
travel the same way for free. Polling is kept as the **fallback**, active only
while the socket is down (every 5s), so a healthy connection costs nothing.

**Effect of a playlist change on a running window:** the timeline is rebuilt and
the position is recomputed from the same cycle clock. Playback may therefore jump
to a different item — this is intentional. Re-anchoring the cycle on every edit
would make playback depend on edit history, so two browsers that connected at
different times would disagree. Keeping the anchor fixed means *every* client
agrees on what a window is showing at any instant, which is the property the
whole design rests on.

---

## 19. Assumptions

1. **The 5-hour cycle re-anchors playback to position 0 at each boundary**, and
   loops the playlist within the cycle. Unused time is never blanked. ([§15](#15-the-5-hour-cycle-exactly))
2. **Configured duration drives video progression**, not video metadata; short
   videos loop within their slot, long ones are cut off. ([§14](#14-continuous-playback))
3. **All clients viewing a window see the same item**, since playback is a
   function of time. This is treated as a feature (a wall of screens agrees), not
   a limitation.
4. **The newest sync supersedes the active one.** ([§16](#16-the-sync-algorithm))
5. **Sync applies to every window**, matching the brief. There is no per-window
   targeting.
6. **No authentication.** The brief does not ask for it and the admin panel is
   part of the demo. Any deployment beyond a demo would need it.
7. **A window created later starts its own cycle** from its creation time. The
   three seeded windows share one anchor so their cycles align, which makes the
   demo easier to reason about.
8. **Playlist positions are contiguous** (`0..n-1`), maintained transactionally.
9. **Times are UTC** everywhere on the wire.
10. **Seed media is self-contained** rather than linked from a public sample
    host. ([§11](#11-seed-data))

---

## 20. Edge cases

| Case | Behaviour |
|---|---|
| **Empty playlist** | Labelled fallback card, explicitly *not* blank media. The window resumes the instant an item is added |
| **All items zero/negative duration** | Skipped when building the timeline; a zero-length infinite loop is impossible |
| **Invalid media URL** | Rejected at three layers: form, service validation (422 with the offending field), and a DB `CHECK` |
| **Image/video fails to load** | `onError` → fallback card; the clock keeps running and moves to the next item on schedule |
| **Video shorter than its slot** | Loops within the slot |
| **Video longer than its slot** | Cut off when the slot ends |
| **Backend unavailable** | Banner shown; **playback continues** from the last known configuration; REST polling and socket reconnection retry automatically |
| **WebSocket disconnect** | Exponential backoff with jitter; polling fallback meanwhile; on reconnect `HELLO` restores full state |
| **Playlist edited mid-playback** | Timeline rebuilt, position recomputed from the same clock ([§18](#18-dynamic-playlist-updates)) |
| **Sync triggered while windows show different items** | Irrelevant — the override is absolute, not relative to what a window was doing |
| **Page refresh during playback** | No state to restore; the correct item renders on the first tick |
| **Page refresh during sync** | Fetches the live sync and rejoins at the correct offset (verified in-browser) |
| **Two syncs at once** | Newest wins; superseded atomically in one transaction |
| **Sync ends while the backend is down** | Clients drop the override on their own at `endAt` |
| **Backend restarts mid-sync** | Override is in PostgreSQL; the end-announcement timer is re-armed on boot |
| **Database restart** | Pool reconnects; `/health` reports 503 while it is down |
| **Application restart** | All configuration persists, including `cycle_anchor`, so playback resumes at the same position |
| **Browser clock badly wrong** | Corrected by measured offset ([§14](#14-continuous-playback)) |
| **Tab backgrounded/throttled** | Resolves the correct item on its next tick; also catches up immediately on `visibilitychange`, and a stalled video is re-seeked back onto the clock |
| **Client joins or refreshes mid-sync during a video** | Seeks to the shared sync offset, so it shows the same frame as every other window |
| **Slow WebSocket client** | Dropped after 32 queued messages rather than blocking the broadcast |
| **Concurrent playlist edits** | Serialised by a row lock on the window |
| **Concurrent server boots** | Migrations serialised by an advisory lock |
| **Deleting media still in a playlist** | Blocked by `ON DELETE RESTRICT` |
| **Path traversal on `/api/assets`** | Rejected; only plain filenames resolve |

---

## 21. Tradeoffs

**Playback computed client-side, not streamed from the server.**
Buys determinism, zero-cost refresh and a backend that does not scale with the
number of screens. Costs: clients must agree on time (solved by offset
estimation), and the playback clock exists in two languages (kept in parity by
shared fixtures).

**The playback algorithm is duplicated in Go and JavaScript.**
Normally a smell. Accepted here because the Go copy makes the 5-hour cycle
verifiable with `curl` instead of a five-hour wait, and because both sides are
driven by one fixture file — divergence fails a test.

**WebSocket primary, polling fallback.**
More moving parts than polling alone, but sync genuinely needs low-latency
server-initiated delivery. Keeping polling as a fallback means a proxy that
blocks WebSockets degrades rather than breaks.

**Raw SQL via pgx instead of an ORM.**
The query set is small and two operations (deferred-constraint reordering,
atomic sync supersede) are clearer written directly. Costs some boilerplate in
the repository layer.

**Playlist edits do not re-anchor the cycle.**
Keeps every client in agreement at the cost of a visible jump when a playlist is
edited mid-playback. ([§18](#18-dynamic-playlist-updates))

**Demo media bundled into the binary (~2 MB).**
A larger repository and image in exchange for a demo that cannot break because
someone else's CDN changed its permissions — which is exactly what happened to
the usual sample bucket while this was being built.

**Sync is global only.**
Matches the brief. Per-window or per-group targeting would need a scope column
and selection UI.

**No authentication.**
Out of scope per the brief; called out as the first thing to add for real use.

---

## 22. Testing

```bash
# Backend — unit + integration, race detector on
cd backend
go test ./... -race

# Repository tests against real PostgreSQL (skipped without this variable)
createdb sequencer_test
TEST_DATABASE_URL='postgres://localhost:5432/sequencer_test?sslmode=disable' \
  go test ./internal/repository/ -race -v

# Frontend
cd frontend
npm test
```

### What is covered

| Area | Tests |
|---|---|
| **Playback clock** | 17 shared fixtures × 2 languages; gapless handover across a whole cycle; cycle boundaries; truncation; empty playlists; exact media boundaries; pre-anchor times; `floorDiv` |
| **Sync phases** | idle → pending → active → ended; exact boundaries; late join; refresh mid-sync; identical resolution for clients receiving the event at different times; skewed clocks |
| **Sync service** | Lead-in scheduling; validation; playlists provably unmodified before/during/after; supersede; cancel; end-announcement fires |
| **HTTP handlers** | Full stack over in-memory storage — routing, status codes, envelopes, validation (422 + fields), malformed JSON, unknown fields, CORS allowed/blocked, asset Range requests, path traversal |
| **WebSocket hub** | HELLO on connect; broadcast to all; **broadcast survives a disconnected client**; ping/pong clock exchange; malformed frames don't drop clients; origin enforcement; publish-after-close; **no goroutine leak after shutdown**; concurrent connect/broadcast/close stress test |
| **Repository (real SQL)** | Contiguous positions across append/remove/reorder; deferred unique constraint in both directions; cross-window scoping; single-live-sync invariant; **config survives reconnect**; schema CHECK constraints |
| **Config** | Required variables; defaults; origin matching; wildcard; range validation |

### Manual verification performed

The following were executed against a live stack (Go server + PostgreSQL 14 +
Vite + headless Chrome), not merely implemented:

- Windows play continuously and loop; items advance on schedule.
- Videos load, play, and **seek to the correct in-item offset** (observed
  `currentTime = 2.31s` on mount, not 0).
- `SYNC ALL WINDOWS` puts M2 on all three windows simultaneously.
- `underneath:` labels confirm playlists keep advancing during sync.
- Playlist fingerprints identical before, during and after sync.
- **Refresh mid-sync rejoins** with the correct remaining time.
- After sync, windows resume on their own playlists at the right position.
- Add / reorder / remove reflect instantly in other browsers via WebSocket.
- Restarting the backend preserves all configuration and playback position.
- Responsive down to 400px with no horizontal overflow; zero console errors.

---

## 23. Deployment

### Step 1 — PostgreSQL (Neon)

1. Create a project at [neon.tech](https://neon.tech).
2. Copy the connection string (it includes `?sslmode=require`).

Render's own PostgreSQL works too — `render.yaml` provisions one automatically.

### Step 2 — Backend (Render)

**Option A — Blueprint (recommended).** Push the repo, then in Render choose
**New → Blueprint** and point it at the repository. `render.yaml` provisions the
web service *and* a PostgreSQL instance and wires `DATABASE_URL` for you.
Afterwards set `FRONTEND_URL` (Step 4).

**Option B — Manual.**

| Setting | Value |
|---|---|
| Type | Web Service |
| Runtime | Docker |
| Dockerfile path | `./backend/Dockerfile` |
| Docker context | `./backend` |
| Health check path | `/health` |

Environment variables:

| Key | Value |
|---|---|
| `DATABASE_URL` | your Neon/Render connection string |
| `FRONTEND_URL` | your Vercel URL (set after Step 3) |
| `AUTO_SEED` | `true` |

Do **not** set `PORT` — Render provides it.

Migrations run automatically on boot, and the demo data seeds itself on the first
deploy. Verify:

```bash
curl -s https://<your-backend>.onrender.com/health
```

> Render's free tier sleeps after inactivity; the first request may take ~30s.

### Step 3 — Frontend (Vercel)

| Setting | Value |
|---|---|
| Root directory | `frontend` |
| Framework preset | Vite |
| Build command | `npm run build` |
| Output directory | `dist` |

Environment variable:

| Key | Value |
|---|---|
| `VITE_API_URL` | `https://<your-backend>.onrender.com` |

`VITE_WS_URL` is optional — it is derived automatically, and becomes `wss://`
over HTTPS.

> Vite inlines env vars at **build** time. Changing `VITE_API_URL` requires a
> redeploy, not just a restart.

### Step 4 — Connect them (important)

Set `FRONTEND_URL` on Render to your Vercel origin, with **no trailing slash**:

```
FRONTEND_URL=https://your-app.vercel.app
```

This is the CORS **and** WebSocket origin allow-list. Without it the browser will
report CORS failures and the socket will be refused with a 403. Comma-separate
multiple origins (e.g. a preview deployment), or use `*` to allow any.

### Step 5 — Verify

1. `GET <backend>/health` → `{"status":"ok", "windows":3, ...}`
2. Open the Vercel URL → three windows playing.
3. Status bar shows **Backend connected** and **WebSocket connected**.
4. Press **SYNC ALL WINDOWS** → all windows switch together.
5. Refresh mid-sync → the page rejoins the running sync.

### The deployed environment

This project is live at the URLs in [§1](#1-live-urls). What runs there:

| | |
|---|---|
| Frontend | Vercel, Vite preset, root directory `frontend` |
| Backend | Render web service, Docker, 12.7 MB image, non-root user |
| Database | Render PostgreSQL 16 (free plan) |
| Env on Vercel | `VITE_API_URL` only |
| Env on Render | `DATABASE_URL` (wired by the blueprint), `FRONTEND_URL`, `AUTO_SEED`, `SYNC_LEAD_TIME_MS` |

Migrations and seeding both ran automatically on the first boot — the deployed
database was never touched by hand.

**`VITE_WS_URL` is deliberately not set in production.** Leaving it unset lets
the WebSocket URL derive from `VITE_API_URL`, which yields `wss://` over HTTPS
automatically. Pinning it to an `ws://` value is the easy way to ship a build
whose socket silently never connects.

**Two free-tier limits worth knowing.** The web service sleeps after ~15 minutes
idle (see the cold-start note in [§1](#1-live-urls)), and Render's free
PostgreSQL is deleted 30 days after creation. Moving to Neon later is a
one-variable change: swap `DATABASE_URL` in the Render dashboard and restart —
migrations and seeding run themselves on boot.

### Verifying the live deployment yourself

```bash
BACKEND=https://syncstream-backend-ot21.onrender.com

# Health, window and media counts
curl -s $BACKEND/health

# The 5-hour cycle, observable without waiting five hours
curl -s $BACKEND/api/windows/1/current | jq .data.state

# Trigger a global sync across every window (watch the frontend as you run it)
MEDIA=$(curl -s $BACKEND/api/media | jq '.data[] | select(.name | startswith("M2")) | .id')
curl -s -X POST $BACKEND/api/sync -H 'Content-Type: application/json' \
  -d "{\"mediaId\": $MEDIA, \"durationSeconds\": 20}" | jq .data
```

### Local production build

```bash
cd backend && docker build -t sequencer-backend . && \
  docker run -p 8080:8080 -e DATABASE_URL='...' -e FRONTEND_URL='*' sequencer-backend

cd frontend && npm run build && npm run preview
```

### Troubleshooting

**`Error: Port 5173 is already in use` when starting the frontend.**
Intentional — the dev server is pinned with `strictPort` so it can never move to
a port the backend does not expect. Free it and retry:

```bash
lsof -ti tcp:5173 | xargs kill
```

**The UI says "Backend unreachable" but the backend is running.**
Check the port in your browser's address bar. A different port is a different
origin, so the CORS allow-list stops matching. Loopback origins are accepted on
any port by default, so this should just work — but if you set
`ALLOW_LOOPBACK_ORIGINS=false`, add the actual origin to `FRONTEND_URL`. The
backend log names the cause explicitly:

```
"msg":"websocket upgrade failed","error":"websocket: request origin not allowed"
```

**In production**, `FRONTEND_URL` must be your exact Vercel origin with no
trailing slash — loopback allowances do not help there.

### Security note

No credentials are committed. `.env` files are git-ignored; `.env.example` holds
placeholders only. Set real values through Render's and Vercel's dashboards.
