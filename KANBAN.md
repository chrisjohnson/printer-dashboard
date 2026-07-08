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

- [ ] **Project scaffolding** — Go module, directory layout, Dockerfile skeleton, CI config.
- [ ] **Printer interface (Go)** — Define `Printer` interface (Status, Pause, Resume, Cancel, SkipObject, Cameras).
- [ ] **Bambu Lab MQTT client** — Connect to P1S & H2S via LAN MQTT, parse status reports.
- [ ] **Snapmaker Paxx client** — Connect to U1 via Paxx REST + WebSocket API.
- [ ] **Polling / event loop** — Background goroutine that keeps printer state fresh.
- [ ] **REST API layer** — Expose printer state and actions via HTTP endpoints.
- [ ] **WebSocket events** — Push state changes / notifications to connected browsers.
- [ ] **Basic web UI** — React (or vanilla) SPA showing printer cards with status, progress, controls.
- [ ] **Camera stream proxy** — Proxy MJPEG/RTSP streams through the server with auth.
- [ ] **Authentication** — Login page, session management, optional OIDC.
- [ ] **Docker Compose** — Single `docker compose up` to run full stack.
- [ ] **Job completion notifications** — Desktop/browser notifications when a print finishes.
- [ ] **Error & failure notifications** — Detect and alert on printer errors, pauses, or thermal runaway.
- [ ] **Skip object support** — UI button that sends skip-object command per printer protocol.
- [ ] **Configuration file** — YAML/TOML config for printer definitions, credentials, camera URLs.

---

## 🏗 In Progress

> *Nothing currently in progress.*

---

## ✅ Done

- [x] **Git repo initialised** — Empty repo with root `KANBAN.md`.
- [x] **Kanban board created** — This file.
- [x] **Architecture plan drafted** — See `PLAN.md`.

---

## 🗄 Archived

- *(none yet)*

---

*Last updated: 2026-07-08*
