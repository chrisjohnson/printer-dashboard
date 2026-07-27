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
5. [x] Implementer: Apply all improvements to `client.go`.

## Signals
<!-- signal: opencode 2025-07-27T12:00:00Z — claimed card and started research -->
<!-- signal: opencode 2025-07-27T12:05:00Z — completed initial research on MQTT structure and fallback logic -->
<!-- signal: opencode 2025-07-27T12:10:00Z — completed full audit of file fields and temperature fallback logic -->
<!-- signal: opencode 2025-07-27T12:15:00Z — implemented all parser improvements -->
<!-- signal: opencode 2025-07-27T12:16:00Z — all tests pass -->

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
- Applied `validateTemp(id, value, min, max) *float64` helper at `client.go:46-58` to centralize all temperature validation logic.
- Added range validation to direct-wire `ChamberTemper` and `ChamberTargetTemper` in `client.go:517-522` and `client.go:552-557`.
- Added truncation warning for `info.temp` fractional parts at `client.go:530-533`.
- Documented asymmetric range validation: `-50..100` for ambient (can dip below zero) vs `0..100` for heater target (always non-negative).
- All tests pass (`go test ./internal/printers/bambu/...`).

## Handoff notes

### Changes Applied to `client.go`

1. **New helper**: `validateTemp(id, value, min, max) *float64` — returns pointer to value if in range, or nil with log warning if out of range.

2. **Direct-wire validation**: `ChamberTemper` now validates `-50..100`, `ChamberTargetTemper` validates `0..100`. Both skip update if out of range (preserving previous readings).

3. **Truncation warning**: Logs when `info.temp` has a fractional part that would be discarded by `int64()`.

4. **Unified validation**: Replaced inline range checks with calls to `validateTemp` for `info.temp` decoded current/target values.

### Related Cards
- K-032: Parser work (this card's improvements may affect HMS/AMS parsing in K-032).

