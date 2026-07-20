---
id: K-081
# Filename pattern: {ID}-{slugified-title}.md
title: Control pad section movement and homing
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: gentle-loris-hazel   # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-20T03:08Z    # ISO8601, paired with claimed_by
blocks: K-092                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: [K-031, K-092]
---

# K-081 — Control pad section movement and homing

## Context

Control pad section — movement and homing. GCode passthrough for movement pad, axis buttons, homing. P1S temp/light controls already shipped (K-031); this is the remaining scope from the original K-031 card.

See `.fleet/research-notes.md`'s K-054 section for the full printer-controls
protocol reference (movement GCode shapes, safety gates) before starting.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [x] Researcher: full scope investigation — feasibility, protocol
   shapes, existing patterns, safety-gate analysis, implementation plan.
   Done — see Decision log. No feasibility blockers found (K-054 already
   confirmed Bambu cloud MQTT supports movement/homing G-code, no LAN-mode
   requirement).
2. [x] Human confirmed scope on 3 open questions (see Decision log):
   Home All only (no per-axis home); add real backend-side safety gating
   (409 on non-idle state, distance/speed clamps) — first command class
   in this app to get real backend gating, not just frontend
   button-disable; Z-axis jog gets a confirmation modal (mirroring the
   existing skip-object modal), X/Y do not.
3. [ ] Implementer (backend, stage 1): `internal/printers/interface.go`
   (`HomeAll`/`Jog` on `Printer`, `Homed *bool` on `PrinterStatus`),
   `internal/printers/bambu/commands.go` (+tests) and `client.go` (wire
   `HomeFlag` into status), `internal/printers/snapmaker/snapmaker.go`
   (`sendGCode`-based, no homed-state tracking for now — known gap, see
   Decision log), `internal/server/server.go` handlers with real state
   gating (409 if not idle) + distance/speed clamps + input validation
   (+tests including new 409-on-wrong-state case).
4. [ ] Implementer (frontend, stage 2, after stage 1 lands): movement pad
   UI in `internal/server/onboarding.go` (`renderCard`+`updateCard` kept
   in sync — watch for the K-053-class drift bug), step-size selector
   defaulting to smallest step, Home All button, Z-axis confirmation
   modal mirroring the skip-object modal pattern, Playwright tests for
   button-disable states across idle/printing/paused/error.
5. [ ] Run full test suite (Go + Playwright), commit, push, open PR(s) —
   may be 1 or 2 PRs depending on how stage 1/2 land relative to each
   other, Implementer's call.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-20T03:08Z — claiming, dispatching researcher first (safety-sensitive: physical printer movement) to review K-054 protocol notes, existing K-031 pattern, and safety gates before any implementation -->

## Working context
- Bambu: `gcode_line` command, `G28\n` for home, `G91\nG1 X.. Y.. Z.. F..\nG90\n`
  for jog. Existing `publishCommand` choke point in `client.go` already
  has audit logging (K-087) — new methods should funnel through it.
  `HomeFlag` already parsed in `parser.go:38` but never wired into
  `PrinterStatus` — do that as part of this card (low effort, serves the
  safety-gate need).
- Snapmaker: `sendGCode(ctx, script)` helper already exists in
  `snapmaker.go`, already handles Moonraker's embedded-error-in-200
  gotcha. No homed-state query wiring exists yet (`toolhead`/`homed_axes`
  — not queried anywhere today) — documented as a known gap, not blocking
  this card; Snapmaker jog/home gate on `idle` state only, no
  homed-vs-not distinction until a follow-up adds the query.
- **Critical finding**: zero backend command handlers in this app have
  any server-side state gating today (pause/resume/cancel/temp/light are
  ALL frontend-button-disable-only, trivially bypassable via direct API
  call). Confirmed via full read of every handler in `server.go`. K-081
  is the first card to add real backend gating, and per human decision
  this session, a separate card is being filed to retrofit the same
  gating onto the existing commands.
- No firmware-ack verification exists for Bambu (K-087) — backend can
  only confirm transport-level publish success, not that the printer
  physically moved. UI copy should say "command sent," not "printer
  moved."

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-20 — gentle-loris-hazel: Research (full report, ~80 tool calls)
  confirmed feasibility with no blockers — K-054's prior protocol
  research already established Bambu cloud MQTT supports movement/homing
  G-code with no LAN-mode requirement. Surfaced the critical finding that
  no command in this app has real backend safety gating today, and that
  Bambu has no firmware-ack mechanism at all. Full protocol shapes,
  file-by-file implementation plan, and 3 open scope questions delivered
  to the human rather than decided unilaterally, given the physical-
  hardware stakes and the precedent-setting nature of adding the app's
  first backend safety gate.
- 2026-07-20 — gentle-loris-hazel: human confirmed all 3 open questions:
  (1) Home All only, no per-axis home buttons — simpler, safer, matches
  K-054's own protocol matrix which only covers "home all axes"; (2) add
  real backend-side gating (409 on non-idle, distance/speed clamps) —
  AND file follow-up card(s) to retrofit the same gating onto existing
  commands that lack it (pause/resume/cancel/temp/light), since the gap
  isn't unique to movement, movement is just the first place it's
  actually dangerous; (3) Z-axis jog gets a confirmation modal (collision
  risk), X/Y do not.

## Handoff notes
Scope finalized with human input. Filed K-092 (backlog) for the backend-
gating retrofit on existing commands — separate scope, not blocking this
card. Proceeding with 2-stage implementation: backend first (interface,
Bambu/Snapmaker commands, server handlers with gating, Go tests), then
frontend (movement pad UI, Z-confirm modal, Playwright tests) once
backend lands and is verified.
