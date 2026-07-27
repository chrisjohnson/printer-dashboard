---
id: K-005
# Filename pattern: {ID}-{slugified-title}.md
title: P1S cloud MQTT field audit
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: opencode
claimed_at: 2025-07-27T12:00:00Z
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                 # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-005 — P1S cloud MQTT field audit

## Context

P1S cloud MQTT field audit. gcode_file vs subtask_name, temps, fallback parsing. Closely related to K-032 parser work.

## Plan
1. [x] Researcher: Investigate current MQTT payload structure for P1S printers.
2. [x] Researcher: Identify discrepancies between `gcode_file` and `subtask_name` fields.
3. [x] Researcher: Audit temperature field parsing logic and identify fallback edge cases.
4. [x] Implementer: Propose parser improvements based on audit findings.

## Signals
<!-- signal: opencode 2025-07-27T12:00:00Z — claimed card and started research -->
<!-- signal: opencode 2025-07-27T12:05:00Z — completed initial research on MQTT structure and fallback logic -->
<!-- signal: opencode 2025-07-27T12:10:00Z — completed full audit of file fields and temperature fallback logic -->

## Working context

### `gcode_file` vs `subtask_name` findings
No functional discrepancies. `client.go:452-460` implements a clean fallback chain:
1. Clear `CurrentFile` when `gcode_state != ""` AND state is `idle`
2. Prefer `gcode_file` if present and non-empty
3. Fallback to `subtask_name` if present and non-empty
4. Preserve existing `CurrentFile` if neither field is available

Test coverage is thorough (`client_test.go` lines 1900-2298). Idle-clear logic works correctly for both fields. Heartbeat reports preserve existing filenames properly.

### Chamber temperature parsing findings
**Finding 1: Asymmetric range validation (client.go:486 vs 492)**
- `ChamberTemp` (current): validates `-50 <= value <= 100`
- `ChamberTargetTemp` (target): validates `0 <= value <= 100`
- Likely intentional (chamber can read below freezing, heater target cannot), but unexplained. A target of -5 would be silently dropped.

**Finding 2: Direct-wire fields lack range validation (client.go:477-478, 497-498)**
- `ChamberTemper` and `ChamberTargetTemper` (direct Bambu wire fields) are assigned wholesale with no bounds check.
- The `info.temp` packed-integer path validates both decoded values aggressively.
- A corrupted direct field (e.g., -999 or 9999) would silently pass through.

**Finding 3: Float-to-int truncation (client.go:483)**
- `int64(*p.Info.Temp)` silently truncates if `info.temp` is ever non-integer.
- Currently not a problem (Bambu always sends integers), but latent fragility with no defensive logging.

## Decision log

## Handoff notes

### Recommended Parser Improvements

1. **Add range validation to direct-wire temperature fields**: Apply the same `-50 <= temp <= 100` bounds check to `ChamberTemper` and `ChamberTargetTemper` from the Bambu wire to prevent silently accepting corrupted values.

2. **Log truncation warnings for `info.temp`**: When `int64(*p.Info.Temp)` is called, add a check: if `*p.Info.Temp != float64(int64(*p.Info.Temp))`, log a warning. This catches future firmware changes that might send non-integer values.

3. **Document the asymmetric range validation**: Add a comment explaining why `ChamberTargetTemp` uses `0-100` while `ChamberTemp` uses `-50-100`. This prevents future developers from thinking it's a bug.

4. **Consider unifying temperature validation**: Extract a `validateTemp(value float64, min, max float64) *float64` helper to centralize validation logic and ensure all paths use consistent bounds.

These improvements should be applied in `client.go` as part of the temperature mapping logic in `handleReport`.

