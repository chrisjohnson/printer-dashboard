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
- [ ] **Light controls** — Per-printer light on/off/toggle controls.
- [ ] **P1S cloud camera** — Access P1S camera stream without LAN mode (via cloud API/proxy).
- [ ] **H2S cloud camera stream** — Research how to access H2S camera stream from Bambu Cloud (WebRTC/TUTK).
- [ ] **H2S status icon hysteresis** — H2S is flipping between idle and complete; needs more attention on state transitions.
- [ ] **Control pad section** — UI for bed/printhead movements, homing, etc.
- [ ] **Filament loaded status** — Show which filament is loaded in each tool.
- [ ] **AMS status display** — Show AMS (Auto Material System) status per printer.
- [ ] **AMS humidity display** — Show humidity readings from AMS.
- [ ] **Print recordings** — Show available recordings per printer.
- [ ] **Download recordings** — Download/save printer recordings.
- [ ] **Chamber target temp for H2S** — Display target chamber temperature (H2S has chamber heater).
- [ ] **P1S chamber temp investigation** — Determine if P1S has a chamber temp sensor (shows 5°C — is this real?).
- [ ] **Longer temperature labels** — Use "Heatbed Temp", "Nozzle 1 Temp", "Nozzle 2 Temp", "Chamber Temp" instead of short codes.

---

## 🐛 Known Bugs

Issues discovered during testing that need fixing.

- [ ] **P1S gcode file not shown** — `printStatus` only parses `"gcode_file"` but P1S likely sends the file path under `"subtask_name"` instead. Need field audit + fallback in `handleReport`.
- [ ] **CurrentFile not cleared on idle** — When a print finishes, `CurrentFile` retains the last filename because `handleReport` only *sets* it, never clears it. After `SUCCESS→IDLE` transition, state shows "idle" but old filename persists.
- [ ] **COMPLETE state is purely transient** — `SUCCESS`/`FINISH` maps to `"complete"` but the next `IDLE` report immediately overwrites it. No hysteresis — user never sees "complete" in the UI unless they catch it mid-report-cycle.
- [x] **Snapmaker U1 ErrorMsg not shown** — Fixed: `ErrorMsg` now renders as a red error banner in the dashboard card when state is "error".

---

## 📋 To Do

Scoped, prioritised, ready to pick up.

- [ ] **P1S cloud MQTT field audit** — Investigate which optional status fields (`gcode_file` vs `subtask_name`, temperatures, etc.) the P1S vs H2S report via cloud MQTT. Add fallback parsing for alternative field names.
- [ ] **Hysteresis for COMPLETE state** — Retain `"complete"` state after `SUCCESS`/`FINISH` instead of letting `IDLE` overwrite it immediately. E.g., stay "complete" until a new print starts or user dismisses.
- [ ] **Clear CurrentFile on idle** — In `handleReport`, clear `CurrentFile` when `gcode_state` transitions to `IDLE` and no `gcode_file` is present in the report.
- [ ] **Camera stream proxy** [must have tests] — Proxy MJPEG/RTSP streams through the server with auth.
- [ ] **Authentication** [must have tests] — Login page, session management.
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
- [x] **No-2FA login fixed** — `LoginStep1` now sets token, fetches userID, and persists token when API returns token directly (previously only `LoginStep2` did this).
- [x] **Device serial ID collision fixed** — Printer IDs now use full serial instead of `strings.ToLower(serial)[:8]`. No more `SERIAL001` / `SERIAL002` collision.
- [x] **Invalid snapmaker port validation** — Port validated with `strconv.Atoi` + range 1–65535, returns 400 on invalid input. Blank port still defaults to 8080.
- [x] **Snapmaker Paxx client: parser** — `parseReport()` for JSON status, `mapState()` with 11 mapping cases, `paxxStatus` struct with pointer-field nil-guards. 17 subtests.
- [x] **Snapmaker Paxx client: commands** — Pause, Resume, Cancel, SkipObject via `POST /api/print/{action}` with access-code auth (header + query param). HTTP error handling (500/401/unreachable). 12 tests.
- [x] **Snapmaker Paxx client: Connect lifecycle** — Initial REST fetch, WebSocket dial with ping/pong, WS message read loop with status merge, REST polling fallback with 3s interval, WS retry at 15s. `handleStatusReport` with nil-guard value preservation. 8 new tests (handleStatusReport, fetchStatus, Connect with WS messages, partial update preservation).
- [x] **Snapmaker Paxx client: partial report fix** — `Progress`, `File`, `Error` changed from value to pointer types so absent JSON fields don't overwrite cached values (matches existing pattern for temp/layer fields).
- [x] **Snapmaker UX — server integration** — `StatusCh` wired to WebSocket hub in `initPrinters()` and `connectAllPrinters()` for Snapmaker printers (same pattern as Bambu). Extracted `startStatusForwarder` helper to avoid duplication. Error messages now rendered in UI as red `.error-banner`. 3 new tests: Snapmaker WS forwarding, error_msg forwarding, template banner presence.
- [x] **Touchscreen rendered as `<img>` instead of `<iframe>`** — Touchscreen PNG snapshots now render as `<img>` with `width: 100%` and natural aspect ratio, filling the card width. Added 3-second auto-refresh with cache-busting for live feel. Removed unused iframe CSS.
- [x] **Dockerfile + .dockerignore** — Multi-stage Docker build (golang:1.26-alpine → alpine:latest). Static binary, non-root user, ~20 MB image. Run command documented below.

---

## 📋 Running

Standard commands to run the dashboard in Docker:

```bash
# Build the image (after any code changes)
docker build -t printer-dashboard .

# Run the container (replace $(pwd) with your project root if needed)
docker run -d \
  --name printer-dashboard \
  --restart unless-stopped \
  -p 8080:8080 \
  -v "$(pwd)/config.yaml:/app/config.yaml:rw" \
  -v "$HOME/.printer-dashboard:/home/app/.printer-dashboard:rw" \
  printer-dashboard

# View logs
docker logs printer-dashboard

# Stop
docker stop printer-dashboard

# Remove and start fresh
docker rm -f printer-dashboard && docker run -d --name printer-dashboard ...(flags above)
```

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

*Last updated: 2026-07-10* (session 6: Docker build & container — multi-stage Dockerfile, .dockerignore, port mapping run, run commands documented)
