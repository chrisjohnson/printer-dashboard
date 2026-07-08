# Printer Dashboard — Kanban Board

> Project management for the multi-printer web dashboard.
> Columns: **Backlog** → **To Do** → **In Progress** → **Done** → **Archived**

---

## 🗂 Backlog

Ideas / feature requests that haven't been scoped yet.

- [ ] **Secondary camera support** — Allow adding external IP cameras (e.g., Wyze, RTSP) to printer cabinets. Show feeds alongside built-in printer cameras.
- [ ] **Print time estimator** — Use historical data + slicer metadata to estimate remaining time.
- [ ] **Time-lapse generation** — Compile camera snapshots into time-lapse videos on job completion.
- [ ] **Filament usage tracking** — Track grams/metres used per spool, alert on low filament.
- [ ] **Mobile push notifications** — Integrate with Pushover / Gotify / ntfy for phone alerts.
- [ ] **Printer grouping / tags** — Organise printers by location, type, or material.
- [ ] **Multi-user support** — Granular permissions per printer.
- [ ] **G-code preview** — Render toolpaths in browser for queued jobs.
- [ ] **Temperature graphs** — Historical chart of nozzle/bed/ enclosure temps.
- [ ] **Webhook API** — Let external services (Home Assistant, etc.) subscribe to printer events.
- [ ] **Dark mode** — Theme toggle for the UI.

---

## 📋 To Do

Scoped, prioritised, ready to pick up.

- [ ] **Snapmaker Paxx client** — Connect to U1 via Paxx REST + WebSocket API.
- [ ] **Camera stream proxy** — Proxy MJPEG/RTSP streams through the server with auth.
- [ ] **WebSocket push** — Replace 5-second polling with real-time push from server to browser.
- [ ] **Authentication** — Login page, session management.
- [ ] **Job completion notifications** — Detect and notify when a print finishes.
- [ ] **Error & failure notifications** — Detect and alert on printer errors.
- [ ] **Dockerfile + Docker Compose** — Multi-stage build and `docker compose up` for full stack.
- [ ] **Retry MQTT connect on failure** — Bambu client should retry initial connection in a loop.
- [ ] **Graceful printer disconnect on shutdown** — Ensure printers disconnect cleanly when server stops.
- [ ] **Run `bambu-login` to get JWT token** — User runs CLI tool to authenticate with Bambu Cloud.

---

## 🏗 In Progress

> *Nothing currently in progress.*

---

## ✅ Done

- [x] **Git repo initialised** — Empty repo with root KANBAN.md.
- [x] **Kanban board created** — This file.
- [x] **Architecture plan drafted** — See PLAN.md.
- [x] **Project scaffolding** — Go module, directory layout, skeleton files.
- [x] **Printer interface (Go)** — Defined `Printer` interface (Status, Pause, Resume, Cancel, SkipObject, Cameras).
- [x] **Bambu Lab Cloud MQTT client** — Connect via Bambu Cloud MQTT (`us.mqtt.bambulab.com:8883`) with JWT auth. No LAN mode or dev mode needed.
- [x] **REST API layer** — Expose printer state and actions via HTTP endpoints (with real data).
- [x] **Basic web UI** — HTML page with printer cards, progress bars, temperatures, and control buttons (polling-based).
- [x] **Skip object support** — UI button + Bambu command for skip-object.
- [x] **Configuration file** — YAML config loader with Bambu account support.
- [x] **Bambu Cloud authentication** — Auth module with email/password login, 2FA handling, token management.
- [x] **`bambu-login` CLI tool** — Interactive login to get JWT token and user ID for config.

---

## 🗄 Archived

- *(none yet)*

---

*Last updated: 2026-07-08*
