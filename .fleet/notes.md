# Project notes

Durable, still-true facts carried forward from the old `.agent/STATE.md`
system when this repo moved onto the fleet board model (F-057, 2026-07-16).
Full historical detail (closed-card resolution narratives, archived
decision log, archived plans) lives in `.fleet/notes-archive.md`. Deep
protocol/API reference research lives in `.fleet/research-notes.md`.

## Project
Self-hosted dashboard for Bambu P1S/H2S + Snapmaker U1 printers. Go
backend, vanilla JS/htmx UI, WebSocket push, Cloud-MQTT-only for Bambu (no
LAN mode for control; H2S camera is the one thing that needs a LAN path).

PLAN.md (repo root) is the tracked, git-committed architecture/roadmap
doc — update it directly for architecture/phase-status changes, not this
file. Phase status as of 2026-07-13: Phase 1 (Core) done, Phase 3
(Snapmaker) done, Phase 2 (Control) partial — camera proxy & auth
outstanding, Phase 4 (Notifications/Polish) partial, Hardening not
started.

## Codebase gotchas
- `internal/server/onboarding.go` is a single large file: dashboard + 5
  onboarding wizard templates, no CSS/JS build step or linter, no shared
  stylesheet — verify changes only via `go build`/`go test`/Playwright.
  Generic class names (`.card`, `.tag`, `.temps`) apply uniformly across
  Bambu/Snapmaker card types. `renderCard()`/`updateCard()` are two
  independently-maintained JS code paths that must be kept in sync by
  hand on any markup change.
  Templates use `html/template` syntax with no editor safety net — broken
  syntax only surfaces at request time.
- H2S camera streaming: printer needs "LAN Only Liveview" enabled
  (separate toggle from full LAN mode), which opens RTSPS on port 322; H2S
  has exactly **one** physical camera (`/live/1`; `/live/2` 404s on real
  hardware — don't re-add a second stream without confirming via
  `ffprobe` against real hardware first). go2rtc runs one RTSP listen
  port per camera instance (18554 + offset) with its own subprocess
  lifetime decoupled from the triggering HTTP request's context. P1S uses
  a different, older binary-TLS/port-6000 path (`bambus://`) — see
  `PLAN.md` §4.1 and K-040 (P1S camera regression test, not yet done).
- Deploy: Docker container on `:8080`, `--restart unless-stopped`, config
  mounted read-only from `config.yaml`, data volume at
  `~/.printer-dashboard`. Container ID drifts between sessions — verify
  with `docker ps`, don't trust a stale note.
- Debugging heuristic: intermittent LAN-egress connectivity failures
  during agent tool use (`curl`/python HTTP clients failing "No route to
  host" while `nc`/ping succeed on the identical host:port) aren't
  necessarily a sandbox-specific bug — confirmed once (2026-07-13) to be a
  real transient issue that also hit the user's own terminal. Test from
  the user's terminal before concluding it's tooling-specific.

## Standing user authorizations (2026-07-11, still in effect)
1. The `printer-dashboard` Docker container on `:8080` is the working
   agent's to stop/restart/rebuild as needed for verification — no need
   to ask each time; after shipping any committed code change,
   proactively rebuild + swap the container without waiting to be asked.
2. Commit and push regularly — one card's reviewed work per commit,
   don't batch multiple cards into one commit.

Note (2026-07-13): a permission classifier requires a *fresh*
in-conversation confirmation for a direct-to-main push even with the
above standing note on file — treat this as "generally authorized, but
expect to reconfirm sensitive actions (pushes, sandbox bypasses)
explicitly each time a classifier flags one," not a blanket override.

## Testing philosophy (PLAN.md)
TDD, table-driven tests, coverage targets 100% pure logic / ≥85% new
packages / ≥70% existing; run via `go test ./... -race -count=1`.

## Known ID collision — K-078
Two unrelated things both ended up called "K-078" across the old
`.agent/STATE.md` system and this repo's fleet board:
`.fleet/board/done/K-078.md` is a **closed** styling task (light-toggle
CSS restyling). The *other* K-078 — a WS-race bug where an optimistic
light-toggle UI update gets clobbered by a stale `printer_update`
message — is a **separate, still-open** card living at
`.fleet/board/backlog/K-080.md`. Don't assume that bug is resolved just
because `done/K-078.md` exists.
