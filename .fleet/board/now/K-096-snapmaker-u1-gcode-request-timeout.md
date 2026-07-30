---
id: K-096
title: Snapmaker U1 Z home state desync — Klipper rejects homing with "Must home Z axis first"
initiative_id: null
claimed_by: null
claimed_at: null
blocks: null
blocked_by: null
related_cards: [K-005, K-081]
---

# K-096 — Snapmaker U1 Z home state desync: Klipper rejects homing with "Must home Z axis first"

## Context

**Original issue (still valid):** Snapmaker U1 G-code requests to `POST /printer/gcode/script` time out after 10s (snapmaker.go:103) with no retry logic. This is a separate concern — covered in the original research section below.

**New issue (reported Jul 30):** User tried to do Z-up on the Snapmaker U1. The UI correctly prompted to home (based on client-side home state tracking from K-081). User hit home, but Klipper returned HTTP 400:

```
Must home Z axis first: 5.130 4.947 199.982 [11251.005]
```

This is a Klipper behavior: after power cycle or when Klippy reconnects, Klipper loads the last known Z position from its saved config/position, so the coordinates show Z at ~200mm. **But Klipper still requires a fresh physical Z home** before allowing any movement or homing commands. The dashboard's client-side home state tracker thinks the printer is homed (because K-081 tracks homing via G28 commands), but Klipper itself has not been physically homed for Z.

This creates a false sense of readiness: the dashboard shows the printer as "homed" and allows jog commands, but Klipper silently has no valid Z home, and when the user tries to home or move Z, they get the "Must home Z axis first" error.

## Research findings (original K-096 timeout issue)
- `sendGCode()` in snapmaker.go:678-711 makes a single synchronous HTTP POST with 10s timeout
- No polling, no retries, no context-level timeout added at the handler layer
- Moonraker's `printer.gcode.script` queues G-code for Klipper; HTTP 200 means "accepted" not "done"
- If Klippy is not connected to Moonraker, the endpoint may hang

## Plan
1. [x] Implementer: Fix `handleHomeAll` (server.go) — detect "Must home" in HomeAll error response; return 400 instead of marking homed=true
2. [x] Implementer: Fix `sendJog` (onboarding.go) — detect "Must home" in jog error response; update cache homed=false and trigger homing modal instead of raw alert
3. [x] Implementer: Fix MockPrinter — add missing `SetHomed` method (pre-existing test bug)
4. [ ] Researcher: Understand Klipper Z home persistence — when does Klipper report Z as "homed" vs when does it require a fresh home?
5. [ ] Researcher: Investigate what Klipper status/macro exposes the actual Z home state (not just saved Z position)
6. [ ] Implementer: Consider polling Klipper's `printer.query_endstops` or equivalent to verify physical home state vs saved state
7. [ ] Implementer: Fix the original 10s timeout issue (separate concern) — add configurable timeout and retry for G-code/script endpoint

## Signals

## Decision log
- 2026-07-30: Fixed `handleHomeAll` (server.go:984-1003) — added "Must home" detection in error path. When Klipper rejects G28 with "Must home Z axis first", returns 400 and skips `SetHomed(true)` so backend state stays consistent.
- 2026-07-30: Fixed `sendJog` (onboarding.go:1884-1917) — detects "Must home" in jog error response; updates `window._printerCache[id].homed = false` and opens homing modal instead of showing raw alert. This closes the UX gap where the user got an ugly alert instead of being offered homing.
- 2026-07-30: Fixed MockPrinter — added missing `SetHomed` method (pre-existing test bug causing panic in TestHandleHomeAll/idle_and_online_succeeds). Added test case "400 on Must home error — does not mark homed".

## Handoff notes
