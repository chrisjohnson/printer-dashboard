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
1. [ ] Researcher: Understand Klipper Z home persistence — when does Klipper report Z as "homed" vs when does it require a fresh home?
2. [ ] Researcher: Investigate what Klipper status/macro exposes the actual Z home state (not just saved Z position)
3. [ ] Implementer: Add detection for "Must home Z axis first" error from Klipper → clear dashboard's home state and show error to user
4. [ ] Implementer: Consider polling Klipper's `printer.query_endstops` or equivalent to verify physical home state vs saved state
5. [ ] Implementer: Fix the original 10s timeout issue (separate concern) — add configurable timeout and retry for G-code/script endpoint

## Signals

## Decision log

## Handoff notes
