---
id: K-092
title: Add real backend-side state gating to existing printer commands (pause/resume/cancel/temp/light)
initiative_id: null
claimed_by: null
claimed_at: null
blocks: null
blocked_by: K-081
related_cards: [K-081, K-031]
---

# K-092 — Add real backend-side state gating to existing printer commands

## Context

Discovered while scoping K-081 (movement/homing control pad) — a full
read of every command handler in `internal/server/server.go`
(`handlePause`, `handleResume`, `handleCancel`, `handleSkipObject`,
`handleSetBedTemp`, `handleSetNozzleTemp`, `handleSetChamberTemp`,
`handleSetLight`) confirmed **none of them have any server-side state
gating**. Every one calls the printer method unconditionally regardless
of `p.Status().State`. The only gating anywhere in the app is client-side
button-disable in `internal/server/onboarding.go`'s `renderCard()` (e.g.
`st !== 'printing' ? 'disabled' : ''`), which is trivially bypassable via
a direct API call (curl, a stale client, etc.).

For pause/resume/cancel/temp/light this is a real but relatively low-risk
gap (firmware likely no-ops a redundant pause, a temp-set command sent
while idle isn't harmful). K-081 is adding the app's first *real* backend
gating specifically for movement/homing, where the stakes are physical
(an unsolicited jog mid-print can crash the toolhead into the part).

Human decision (2026-07-20, during K-081 scoping): fix K-081's movement
commands with real backend gating now, and file this card separately to
retrofit the same pattern onto the existing, already-shipped commands —
the gap isn't unique to movement, movement is just the first place it's
actually dangerous enough to demand it immediately.

## Plan
1. [ ] Once K-081 lands, use its backend-gating implementation
   (`internal/server/server.go` movement handlers) as the reference
   pattern — likely a shared helper (e.g. `requireState(p, allowedStates...)`)
   rather than duplicated per-handler checks.
2. [ ] Add the same state-check pattern to `handlePause` (only from
   `printing`), `handleResume` (only from `paused`), `handleCancel`
   (only from `printing`/`paused`), `handleSkipObject` (only from
   `printing`), `handleSetBedTemp`/`handleSetNozzleTemp`/
   `handleSetChamberTemp`/`handleSetLight` (decide: these are lower-risk
   — may not need strict state gating, but at minimum should reject if
   `!p.Status().Online`; use judgment on whether temp/light truly need
   state restrictions beyond "printer is reachable").
3. [ ] Add tests for each new 409-on-wrong-state case, mirroring K-081's
   test pattern.
4. [ ] Run full test suite, commit, push, open PR.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-20T03:45Z — filed from K-081 scoping, human-directed follow-up, not started -->

## Working context
- Reference implementation will land in K-081 first — check that card's
  PR(s) for the actual gating helper/pattern before starting this one.
- File: `internal/server/server.go`, all `handle*` command handlers.

## Decision log
- 2026-07-20 — gentle-loris-hazel: filed to backlog per §2 (human-
  directed follow-up during K-081 scoping — human explicitly asked for
  this to be carded rather than folded into K-081's scope).

## Handoff notes
Not started. Blocked in spirit (not formally `blocked_by`) on K-081
landing first, since it establishes the gating pattern this card should
reuse rather than reinvent.
