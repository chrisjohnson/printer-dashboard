# Printer Dashboard — Architecture Plan

## 1. Overview

A self-hosted web application that provides a unified dashboard for monitoring and controlling multiple 3D printers:

| Printer   | Firmware / API                              | Protocol                              |
|-----------|---------------------------------------------|---------------------------------------|
| Bambu P1S | Stock firmware (pre-Bambu Connect)          | **Cloud MQTT** via Bambu Cloud API    |
| Bambu H2S | Bambu Connect firmware                      | **Cloud MQTT** via Bambu Cloud API    |
| Snapmaker U1 | Paxx custom firmware                     | REST + WebSocket (local)              |

**Key constraint:** No LAN mode or developer mode is enabled on the Bambu printers.
All Bambu communication goes through Bambu's cloud infrastructure (`us.mqtt.bambulab.com:8883`),
authenticated with a JWT token from the Bambu account login flow (email + password + 2FA).

The Snapmaker U1 is accessed directly on the LAN via Paxx's REST API.

The dashboard is exposed through a public reverse proxy (Caddy / nginx) with authentication.

---

## 2. Technology Choices

| Layer        | Choice      | Rationale                                                                 |
|--------------|-------------|---------------------------------------------------------------------------|
| Language     | **Go**      | Preferred by maintainer; excellent concurrency for MQTT + WebSocket; single binary deploy. |
| Web UI       | **Vanilla JS + htmx** (tentative) | Keep it simple; no heavy SPA framework required initially. Could upgrade to React/Vue later. |
| MQTT client  | `eclipse/paho.mqtt.golang` | Mature, well-maintained Go MQTT library.                                  |
| Web framework| `net/http` + `chi` router | Lightweight, idiomatic Go.                                                |
| Auth         | `bcrypt` + session cookies | Simple; can add OIDC/OAuth2 later.                                       |
| Container    | Docker (Alpine)           | Final deployment target; multi-stage build for small image.              |
| Config       | YAML                      | Human-friendly, common in printer tools like OctoPrint, Moonraker.       |

---

## 3. High-Level Architecture

```
                    ┌───────────────┐
                    │   Browser /   │
                    │   Mobile App  │
                    └──────┬────────┘
                           │ HTTPS
                    ┌──────▼────────┐
                    │  Reverse Proxy │  ← Caddy / nginx (TLS, rate-limit, auth)
                    └──────┬────────┘
                           │
                    ┌──────▼────────┐
                    │  Go Server    │
                    │  (port 8080)  │
                    │               │
                    │  ┌─────────┐  │
                    │  │  Auth    │  │  ← session cookie + bcrypt
                    │  ├─────────┤  │
                    │  │  REST    │  │  ← printer status, actions
                    │  ├─────────┤  │
                    │  │  WS      │  │  ← real-time push to browser
                    │  ├─────────┤  │
                    │  │  MQTT   │  │  ← Bambu Cloud MQTT
                    │  ├─────────┤  │
                    │  │  HTTP   │  │  ← Snapmaker / Paxx (local)
                    │  ├─────────┤  │
                    │  │  Camera  │  │  ← proxy local + external streams
                    │  └─────────┘  │
                    └──────┬────────┘
                           │
              ┌────────────┼──────────────────┐
              │            │                  │
       ┌──────▼──────┐ ┌──▼────────┐  ┌──────▼──────┐
       │  Bambu      │ │  Bambu    │  │ Snapmaker   │
       │  Cloud MQTT │ │  Cloud    │  │ U1 (Paxx)   │
       │  (us.mqtt.  │ │  API      │  │ REST+WS     │
       │   bambulab  │ │  (auth,   │  │ (local LAN) │
       │   .com:8883)│ │  devices) │  │             │
       └──────┬──────┘ └───────────┘  └─────────────┘
              │
     ┌────────┴────────┐
     │                 │
  ┌──▼───┐        ┌───▼──┐
  │ P1S  │        │ H2S  │
  │      │        │      │
  └──────┘        └──────┘
```

### Data Flow

1. **Authentication** — Server logs into Bambu Cloud API with email/password (handling 2FA if needed). Gets JWT token and user ID.
2. **Ingestion** — Server connects to each printer and subscribes to status updates.
   - Bambu: Connect to **cloud MQTT** (`us.mqtt.bambulab.com:8883`), subscribe to `device/{dev_id}/report`
   - Snapmaker: poll REST endpoint + WebSocket for push events (local LAN)
3. **State store** — In-memory struct per printer, protected by `sync.RWMutex`. (No DB needed initially.)
4. **Web push** — On state change, broadcast via WebSocket to all authenticated browser sessions.
5. **Commands** — Browser sends REST POST → server translates to printer-native command → publishes to cloud MQTT `device/{dev_id}/request`.
6. **Cameras** — Server proxies local MJPEG streams (port 6000 for Bambu, or external RTSP cameras), injecting auth check before forwarding raw frames. Remote camera requires TUTK SDK or Bambu Handy app.

---

## 4. Printer API Research

### 4.1 Bambu Lab (P1S & H2S) — Cloud API + Cloud MQTT

**Constraint:** No LAN mode. No Developer mode. Both printers use standard cloud connectivity.

**Architecture:**
1. **Cloud API** (`https://api.bambulab.com`) — Used for authentication and device discovery
2. **Cloud MQTT** (`us.mqtt.bambulab.com:8883`) — Used for real-time printer status and commands

#### Authentication Flow

```
POST /v1/user-service/user/login  (email + password)
  → 2FA: email verification code required
  → Returns: JWT access token

GET /v1/design-user-service/my/preference  (Bearer token)
  → Returns: user_id (numeric)

GET /v1/iot-service/api/user/bind  (Bearer token)
  → Returns: list of devices with dev_id, dev_access_code, etc.
```

#### Cloud MQTT Connection

| Field    | Value                        |
|----------|------------------------------|
| Broker   | `us.mqtt.bambulab.com:8883` |
| TLS      | Required (port 8883)        |
| Username | `u_{user_id}`               |
| Password | `{jwt_access_token}`        |

**Topics:**
- `device/{dev_id}/report` — Printer pushes JSON status ~0.5-2 sec interval
- `device/{dev_id}/request` — Server sends commands here

**Commands (publish to `device/{dev_id}/request`):**
```json
{"print": {"command": "pause"}}
{"print": {"command": "resume"}}
{"print": {"command": "stop"}}
{"print": {"command": "project_file", "param": "skip_object"}}
```

**Camera:**
- **Local access** (same LAN): MJPEG stream at `http://{printer_ip}:6000/?token={access_code}`
  - The access code is available from the printer screen even without LAN mode
  - Also still frame at `http://{printer_ip}:6000/?action=snapshot`
- **Remote access**: Uses TUTK P2P protocol (proprietary SDK, used by Bambu Studio/Handy)
  - Cloud API provides TTCode credentials for TUTK via `POST /v1/iot-service/api/user/ttcode`
  - Not implementable without TUTK SDK — users should use Bambu Handy app for remote camera
- **Future**: Bambu may add WebRTC-based cloud streaming (fields already present in API but null)

### 4.2 Snapmaker U1 (Paxx Firmware)

**Connection:**
- Paxx custom firmware, likely exposes REST API on port 8080 (common)
- May also have WebSocket endpoint for real-time updates
- API key or no auth if LAN-only

**Research needed:**
- Document the exact Paxx API endpoints
- Snapmaker U1 is a newer model — need to confirm if Paxx is available and what version
- Typically: `GET /api/v1/printer/status`, `POST /api/v1/printer/print/pause`, etc.

**Camera:**
- Snapmaker U1 built-in camera provides MJPEG stream (need to confirm port/endpoint)
- Likely `http://{printer_ip}:8080/?action=snapshot` or similar

---

## 5. Directory Layout

```
printer-dashboard/
├── .gitignore
├── README.md
├── KANBAN.md
├── PLAN.md
├── go.mod
├── go.sum
├── main.go                     # Entry point
├── cmd/
│   └── server/                 # Main server binary
│       └── main.go
├── internal/
│   ├── config/                 # YAML config loading
│   ├── server/                 # HTTP server, routes, middleware
│   ├── printers/               # Printer interface & registry
│   │   ├── interface.go        # Printer interface definition
│   │   ├── bambu/              # Bambu Lab implementation
│   │   │   ├── client.go       # MQTT client wrapper
│   │   │   ├── parser.go       # Report JSON parser
│   │   │   └── commands.go     # Command builders
│   │   └── snapmaker/          # Snapmaker Paxx implementation
│   │       ├── client.go       # REST + WS client
│   │       ├── parser.go
│   │       └── commands.go
│   ├── camera/                 # Camera stream proxy
│   ├── auth/                   # Authentication & sessions
│   ├── notify/                 # Notification engine
│   └── ws/                     # WebSocket hub
├── web/
│   ├── templates/              # Go html/template files
│   └── static/                 # CSS, JS, images
├── config.example.yaml         # Example configuration
└── Dockerfile
```

---

## 6. Phased Delivery

### Phase 1 — Core (now)
- [x] Git repo, kanban, plan
- [ ] Go module + scaffolding
- [ ] Printer interface
- [ ] Bambu MQTT client (connect, subscribe, parse reports)
- [ ] In-memory printer state
- [ ] REST API (GET /printers, GET /printers/:id)
- [ ] Minimal web UI (list printers, show status + progress)

### Phase 2 — Control
- [ ] Pause / Resume / Cancel commands via REST
- [ ] Skip object command
- [ ] Camera stream proxy
- [ ] WebSocket push for live updates
- [ ] Auth (login page, sessions)

### Phase 3 — Notifications & Polish
- [ ] Job completion detection + notification
- [ ] Error alert detection
- [ ] Secondary camera support
- [ ] Dockerfile + docker-compose.yml

### Phase 4 — Hardening
- [ ] Rate limiting
- [ ] HTTPS / TLS configuration
- [ ] Public deployment guide (reverse proxy, env vars)
- [ ] Testing (unit + integration)
- [ ] Prometheus metrics (optional)

---

## 7. Security Considerations

- **LAN-only printers** — The dashboard runs on the same LAN; printers never exposed to the internet.
- **Access code** — Bambu's access code is equivalent to a password; stored in config file, never logged.
- **Auth at proxy** — Reverse proxy enforces authentication before requests reach the Go server.
- **Session management** — Short-lived sessions with secure cookies.
- **Rate limiting** — Prevent brute-force login attempts.
- **CORS** — Tight origin policy if frontend/backend are served from different ports during development.

---

## 8. Development Workflow

1. Work natively on macOS (Go toolchain, no Docker required for dev).
2. Test with real printers on LAN.
3. Use `config.example.yaml` (committed) + `config.yaml` (gitignored).
4. Commit after every logical chunk.
5. Docker multi-stage build for production.

---

*Last updated: 2026-07-08*
