# Printer Dashboard — Architecture Plan

## 1. Overview

A self-hosted web application that provides a unified dashboard for monitoring and controlling multiple 3D printers:

| Printer   | Firmware / API                              | Protocol                |
|-----------|---------------------------------------------|-------------------------|
| Bambu P1S | Pre-Bambu-Connect (stock LAN mode)          | MQTT (TLS, port 8883)  |
| Bambu H2S | Bambu Connect firmware                      | MQTT (TLS, port 8883)  |
| Snapmaker U1 | Paxx custom firmware                     | REST + WebSocket        |

All printers are on the same local LAN. The dashboard will be exposed through a public reverse proxy (Caddy / nginx) with authentication.

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
                    │  Reverse Proxy │  ← Caddy / nginx (TLS termination, rate-limit)
                    │  (auth gate)   │
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
                    │  │  MQTT   │  │  ← Bambu printer comms
                    │  ├─────────┤  │
                    │  │  HTTP   │  │  ← Snapmaker / Paxx comms
                    │  ├─────────┤  │
                    │  │  Camera  │  │  ← proxy streams with auth
                    │  └─────────┘  │
                    └──────┬────────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
       ┌──────▼────┐ ┌────▼────┐ ┌────▼──────┐
       │ Bambu P1S │ │Bambu H2S│ │Snapmaker  │
       │ (MQTT)    │ │ (MQTT)  │ │ U1 (Paxx) │
       │           │ │         │ │ REST+WS    │
       └───────────┘ └─────────┘ └───────────┘
```

### Data Flow

1. **Ingestion** — Server connects to each printer and subscribes to status updates.
   - Bambu: MQTT subscribe to `device/{serial}/report`
   - Snapmaker: poll REST endpoint + WebSocket for push events
2. **State store** — In-memory struct per printer, protected by `sync.RWMutex`. (No DB needed initially.)
3. **Web push** — On state change, broadcast via WebSocket to all authenticated browser sessions.
4. **Commands** — Browser sends REST POST → server translates to printer-native command → sends to printer.
5. **Cameras** — Server proxies MJPEG streams, injecting auth check before forwarding raw frames.

---

## 4. Printer API Research

### 4.1 Bambu Lab (P1S & H2S)

**Connection:**
- LAN-only mode: MQTT over TLS on port 8883
- Username: `bblp` (fixed)
- Password: printer's **access code** (found in Settings → Network → LAN mode)
- Client ID: any unique string
- CA cert: Bambu's published CA (self-signed on printer, need to extract or trust on first connect)

**Topics:**
- `device/{serial}/report` — Printer pushes JSON status ~1-2 sec interval
- `device/{serial}/request` — Server sends commands here
- `device/{serial}/upload` — For file uploads

**Report JSON highlights:**
```json
{
  "print": {
    "gcode_state": "RUNNING",
    "gcode_file": "Benchy_0.2mm_ABS_5h48m.gcode",
    "mc_percent": 42,
    "mc_remaining_time": 12500,
    "wifi_signal": -54,
    "bed_temper": 65,
    "nozzle_temper": 240,
    "home_flag": 0,
    "layer_num": 86,
    "total_layer_num": 215
  }
}
```

**Commands (publish to `device/{serial}/request`):**
```json
{"print": {"command": "pause"}}
{"print": {"command": "resume"}}
{"print": {"command": "stop"}}
{"print": {"sequence_id": "0", "command": "project_file", "param": "MetadataPlate"}}
```

**Camera:**
- MJPEG stream at `http://{printer_ip}:6000/?token={access_code}` (port 6000 for P1S)
- Port 6000 also serves a still frame at `http://{printer_ip}:6000/?action=snapshot`
- H2S may use port 6000 or 8884 depending on firmware

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
