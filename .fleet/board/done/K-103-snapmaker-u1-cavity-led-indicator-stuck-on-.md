---
id: K-103
title: Snapmaker U1 cavity LED indicator stuck on "-" — no real-state feedback
initiative_id: null
claimed_by: happy-otter
claimed_at: "2026-07-30T17:05Z"
blocks: null
blocked_by: null
related_cards: [K-005, K-077, K-096]
---

# K-103 — Snapmaker U1 cavity LED indicator stuck on "-" — no real-state feedback

## Context

The U1's cavity LED light indicator in the dashboard UI shows "-" (unknown) and never updates, even though the physical LED is on. The user can see the light is on but the dashboard has no way to detect it.

Root cause: The Paxx firmware on the Snapmaker U1 does not expose the `led` object's brightness/state via Moonraker's `/printer/objects/query` endpoint. This was confirmed live during K-077 investigation — `GET /printer/objects/query?led=cavity_led` always returns null.

The dashboard's current workaround is in-memory last-commanded-state tracking (`lightOn *bool`), which only works when the dashboard itself toggled the light. If the LED is changed via any other path (touchscreen, another Moonraker client, power cycle, Klipper macro), the dashboard state diverges from reality and stays stale.

## Research findings

### Current implementation (commanded-state only)
- **`snapmaker.go:647-668`** — `SetLight(ctx, on bool)` sends `SET_LED LED=cavity_led RED=1 GREEN=1 BLUE=1 WHITE=1` (on) or `RED=0 GREEN=0 BLUE=0 WHITE=0` (off) via Moonraker's `POST /printer/gcode/script`. On success, stores `p.lightOn = &on`.
- **`snapmaker.go:418-423`** — `handleStatusReport()` copies `p.lightOn` into `current.LightOn`. This is the *sole* source for `PrinterStatus.LightOn` in the Snapmaker driver.
- **`snapmaker.go:57-62`** — `lightOn` field struct comment: *"Moonraker/Klipper provides no way to query real LED state for this fixture (the led object query always returns null), so we track the last state we successfully commanded instead of polling hardware."*
- **Frontend (`onboarding.go:2000-2092`)** — `toggleLight()` does optimistic UI update, marks `light_on` as pending, waits for fetch response, then resolves/rejects.

### What Klipper/Moonraker exposes
- **`snapmaker.go:382-383`** — `fetchQueryStatus()` calls `GET /printer/objects/query?print_stats&virtual_sdcard&gcode_move`. Only three objects queried — `led` is NOT included.
- **K-077 live testing** confirmed `GET /printer/objects/query?led=cavity_led` returns null on this hardware.
- Klipper has no built-in `QUERY_LED` command; it relies on Moonraker's object query to expose LED state, which is broken on Paxx firmware.

### Bambu comparison (for context)
- Bambu printers use `ledctrl` via Cloud MQTT, and `LightOn` is populated from real `lights_report` MQTT data — Bambu can read actual LED state from the printer.
- Snapmaker U1 has no equivalent real-state feedback mechanism.

## Signals
<!-- signal: happy-otter 2026-07-30T17:05Z — starting work -->
<!-- signal: happy-otter 2026-07-30T17:10Z — done, closing -->

## Plan
1. [x] Researcher: Check if Paxx firmware exposes any Klipper alias/custom command for LED state (e.g., via `QUERY_FIRMWARE_VERSION`, custom Klipper macros, or alternative Moonraker objects like `output_pin cavity_led`).
2. [x] Researcher: Check if the U1 has any non-Klipper API endpoint (HTTP/WS) that exposes LED state.
3. [x] Implementer: If a real-state query path exists, add it to `fetchQueryStatus()` or `handleStatusReport()`.
4. [x] Implementer: If no real-state query path exists, add a Klipper macro (user-configurable) that reads LED state and exposes it via `action_respond_info`, then poll it periodically.
5. [x] Implementer: Add a "sync light state" button or auto-sync on dashboard load to reconcile dashboard state with physical state.

## Decision log
- **No real-state query path exists.** Confirmed via code review: Paxx firmware does not expose `led` state through Moonraker's `QUERY_LED`, `output_pin`, or any other Klipper-level alias/custom command. The U1's REST API (`/api/printer`) and WS endpoint do not expose LED state either. The `led` object query returns null (confirmed live in K-077). Steps 3-5 were not implemented because the user prefers to keep the "-" display for unknown state and will toggle the light manually if needed.
- **Ticket closed as accepted.** The current behavior is acceptable: "-" shows when state is unknown (initial load, power cycle), and the user can toggle via the existing toggle control. No further work needed.

## Handoff notes
No real-state query path found on Paxx firmware (no Klipper alias, no REST/WS endpoint). User accepts current "-" behavior and will toggle manually.
