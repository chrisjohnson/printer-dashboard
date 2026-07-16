---
id: K-004
# Filename pattern: {ID}-{slugified-title}.md
title: COMPLETE state transient no hysteresis
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: gentle-loris-hazel   # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-16T13:22Z    # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: [K-006, K-030]
---

# K-004 — COMPLETE state transient no hysteresis

## Context

COMPLETE state is transient — SUCCESS overwrites to complete, but the next IDLE overwrites it back. No hysteresis. Needs state machine fix.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [x] Researcher: locate root cause, reconcile K-006/K-030, recommend fix
   approach. Done — see Decision log.
2. [ ] Implementer: add streak-threshold latch (mirroring
   `hmsHealthyStreakThreshold` pattern) in `internal/printers/bambu/client.go`
   `handleReport` (~line 300-301) so `"complete"` only reverts to `"idle"`
   after 2 consecutive idle reports; a new RUNNING report still overrides
   immediately, no latch needed on that edge. Apply the same pattern in
   `internal/printers/snapmaker/snapmaker.go` (`handleStatusReport`,
   `current.State = mapMoonrakerState(...)` ~line 396).
3. [ ] Implementer: add tests — Bambu: SUCCESS→single IDLE must NOT drop
   State from "complete"; SUCCESS→IDLE×2 must drop it (model on
   `client_test.go:2096-2126` `TestHandleReport_SuccessDoesNotClearCurrentFile`
   and the existing `hmsHealthyStreak` tests). Snapmaker: add an equivalent
   Complete→subsequent-state test in `snapmaker_test.go` (none exists today).
4. [ ] Implementer: run full test suite, commit, push, open PR.
5. [ ] Close K-006 and K-030 as duplicates once this PR is up, pointing both
   at K-004 (confirmed duplicates — both cards' own Context sections say
   "folds into K-004").

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-16T13:22Z — claiming, dispatching researcher to scope the state machine fix -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-16 — gentle-loris-hazel: Research confirmed this is user-visible
  (state pushed live over WebSocket per-report, not just an API nuance).
  Root cause: `internal/printers/bambu/client.go:300-301` unconditionally
  overwrites `s.State` on every report with no latch — Bambu firmware
  reports `SUCCESS`→briefly then `IDLE`, clobbering "complete" within
  ~1-2 MQTT pushes. Same unconditional-overwrite bug exists in
  `internal/printers/snapmaker/snapmaker.go:396`, lower urgency there
  (Moonraker's "Complete" is stickier in practice) but same architectural
  gap — fixing both. K-006 and K-030 confirmed duplicates (both say "folds
  into K-004" in their own Context) — closing both once this ships.
- 2026-07-16 — gentle-loris-hazel: judgment call — using streak-threshold=2
  (matching existing `hmsHealthyStreakThreshold` constant) rather than a
  time-based latch, since Bambu's transport is MQTT-push (no fixed
  interval, so "hold for N seconds" needs a timer this codebase doesn't
  otherwise use) and this exactly mirrors an already-shipped, already-
  reviewed pattern in the same file. Exact desired product semantics
  (streak count vs. e.g. "hold until acknowledged") wasn't specified
  anywhere in the repo — proceeding with the low-risk match to precedent
  per an ordinary judgment call; can be tuned later if 2 proves
  insufficient in practice.

## Handoff notes
Implementer dispatched by gentle-loris-hazel 2026-07-16T13:31Z — adding
streak-threshold latch to Bambu + Snapmaker paths, plus tests. Awaiting
completion before push/PR.
