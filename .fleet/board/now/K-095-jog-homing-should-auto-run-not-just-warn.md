---
id: K-095
# Filename pattern: {ID}-{slugified-title}.md
title: Jog homing prompt should auto-run homing, not just warn
initiative_id: null
claimed_by: opencode
claimed_at: 2025-07-27T12:00:00Z
blocks: null
blocked_by: null
related_cards: []
---

# K-095 — Jog homing prompt should auto-run homing, not just warn

## Context

When a user tries to jog a Snapmaker printer that hasn't been homed, the frontend currently shows a warning and blocks the jog. The user requested that instead of just warning, it should offer/prompt to run homing first, and unless the user hits cancel, automatically execute the homing sequence before proceeding with the jog.

## Plan
1. [x] Determine if printer is homed — check `p.homed` in `window._printerCache[id]`
2. [x] If homed=true: show existing Z-jog confirmation modal (unchanged behavior)
3. [x] If homed=false: show modified modal with "Run Homing" button text
4. [x] Implement homing flow: modify homeAll() to accept optional callback, used in openZJogModal
5. [x] Update tests in dashboard.test.ts — all 15 tests pass, including 4 Movement pad tests. Fixed `id` → `p.id` bug in updateCard homed-clearing logic.

## Signals
<!-- signal: opencode 2025-07-27T12:00:00Z — claimed card and started research -->
<!-- signal: opencode 2025-07-27T12:20:00Z — implemented modal changes: check homed state, offer "Run Homing" when not homed -->
<!-- signal: opencode 2025-07-27T12:21:00Z — container rebuilt and redeployed with changes -->
<!-- signal: opencode 2025-07-27T12:30:00Z — fixed p.id bug in updateCard, all 15 tests pass, ready for user testing -->

## Decision log

## Handoff notes

### Changes to `internal/server/onboarding.go`

1. **`zJogModalHtml(id)`** — Added `h3` with id `zjog-modal-title-{id}` for dynamic title text.

2. **`openZJogModal(id, delta, body)`** — Now checks `window._printerCache[id]?.homed`:
   - `homed === false`: Shows "Printer not homed" title, "Run Homing" button, calls `homeAll(id, callback)` on confirm
   - `homed === true` or `null`: Shows existing warning text and "Move Z" button (unchanged behavior)

3. **`homeAll(id, callback)`** — Added optional `callback` parameter. On success, invokes the callback (used by `openZJogModal` to chain homing → jog).

### Implementation details
- Snapmaker U1 returns `homed: null` (never set by snapmaker.go), so existing test behavior is preserved
- Bambu printers return `homed: true/false` from MQTT `home_flag`, so they'll use the new behavior when `homed === false`
- Container redeployed: http://localhost:55001
