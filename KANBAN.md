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

## 🐛 Known Bugs

Issues discovered during testing that need fixing.

- [ ] **No-2FA login leaves empty token/userID** — When `LoginStep1` returns a token directly (no 2FA required), the cloud client's `token` and `userID` fields are not set, so `Token()` and `UserID()` return empty strings after a successful no-2FA login.
- [ ] **Device serial ID collision** — Printer IDs are derived from the first 8 characters of the serial number. Devices with serials like `SERIAL001` and `SERIAL002` both map to `"serial00"`, causing the second to overwrite the first in the printer map.
- [ ] **Invalid snapmaker port silently defaults to 8080** — The onboarding form accepts non-numeric port values and uses 8080 instead of returning a validation error.

---

## 📋 To Do

Scoped, prioritised, ready to pick up.

- [ ] **Snapmaker Paxx client** [must have tests] — Connect to U1 via Paxx REST + WebSocket API.
- [ ] **Camera stream proxy** [must have tests] — Proxy MJPEG/RTSP streams through the server with auth.
- [ ] **Authentication** [must have tests] — Login page, session management.
- [ ] **P1S cloud MQTT field audit** — Investigate which optional status fields (gcode_file, temperatures, etc.) the P1S vs H2S report via cloud MQTT; some models may omit certain fields entirely.
- [ ] **Job completion notifications** [must have tests] — Detect and notify when a print finishes.
- [ ] **Error & failure notifications** [must have tests] — Detect and alert on printer errors.
- [ ] **Dockerfile + Docker Compose** [must have tests] — Multi-stage build and `docker compose up` for full stack.
- [ ] **Retry MQTT connect on failure** [must have tests] — Bambu client should retry initial connection in a loop.
- [ ] **Graceful printer disconnect on shutdown** [must have tests] — Ensure printers disconnect cleanly when server stops.
---

## 🧪 Testing

Work related to test infrastructure and coverage.

- [ ] **Integration tests** — End-to-end tests with real MQTT broker or recorded fixtures.

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
- [x] **`bambu-login` CLI tool** — Interactive login to get JWT token and user ID for config (email/password + SSO browser mode).
- [x] **SSO browser token extraction support** — CLI tool option 2 walks Google SSO users through extracting JWT from devtools.
- [x] **Token persistence** — Token saved to `~/.printer-dashboard/`, auto-loaded on server restart, ~3 month lifetime.
- [x] **Onboarding wizard (web UI)** — Full browser-based setup flow: printer type selection, Bambu Cloud login with 2FA, device selection, config save, and server hot-reload. Snapmaker manual-entry form also included. [Implemented, uncommitted.]
- [x] **Config hot-reload** — Server re-reads `config.yaml` and reconnects printers without restart (via `reloadConfig()`). Used by onboarding save flow.
- [x] **Test infrastructure** — Comprehensive test suite with table-driven tests across all packages:
  - `parser_test.go` — Report parsing, state mapping, error states (17 subtests)
  - `commands_test.go` — JSON command output, panic handling (5 subtests)
  - `config_test.go` — Load/Save/validate with temp files (27 subtests)
  - `auth_test.go` — JWT parsing, token lifecycle, login flow, token persistence (37 subtests)
  - `client_test.go` — MQTT client handleReport logic, publish, command delegation (36 subtests)
  - `server_test.go` — All API endpoints with MockPrinter (21 subtests)
  - `onboarding_test.go` — Full onboarding wizard flow with mock Bambu Cloud API (42 subtests)
- [x] **TDD mandate** — All new code must include tests first; documented in PLAN.md, KANBAN.md, README.md
- [x] **WebSocket push** — Real-time status push from server to browser via ws.Hub + ws.Client. Replaces 5-second polling. Includes exponential backoff reconnection.
- [x] **GcodeFile flicker fix** — Changed `GcodeFile` from `string` to `*string` in parser, added nil-guard in client. Prevents filename from disappearing on partial MQTT reports. Also added frontend value cache (`mergeWithCache`) for same protection on all fields.

---

## 📋 Testing Standards

- All new code must include **table-driven unit tests** written alongside the implementation.
- Follow **TDD**: write the test first, see it fail, write the implementation, see it pass, refactor.
- All PRs must pass `go test ./... -race -count=1`.
- Coverage targets: **100%** for pure logic (parsers, commands, mappers), **≥85%** for new packages, **≥70%** for existing packages (increasing over time).
- Mocks are hand-written in `_test.go` files — no external mock frameworks.
- Tests use only Go standard library (plus `gorilla/websocket` for WebSocket handler tests).

---

## 🗄 Archived

- *(none yet)*

---

*Last updated: 2026-07-09* (session 2: tests + WebSocket push + bug tracking)
