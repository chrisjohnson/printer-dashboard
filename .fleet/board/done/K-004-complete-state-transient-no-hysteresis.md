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
2. [x] Implementer: add streak-threshold latch (mirroring
   `hmsHealthyStreakThreshold` pattern) in `internal/printers/bambu/client.go`
   `handleReport`. Applied same pattern in `internal/printers/snapmaker/snapmaker.go`.
3. [x] Implementer: add tests — done, both Bambu and Snapmaker, plus updated
   two pre-existing tests that exercised the buggy single-report transition.
4. [x] Implementer: run full test suite, commit, push, open PR. PR:
   https://github.com/chrisjohnson/printer-dashboard/pull/3
5. [x] Close K-006 and K-030 as duplicates, pointing both at K-004.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-16T13:22Z — claiming, dispatching researcher to scope the state machine fix -->
<!-- signal: gentle-loris-hazel 2026-07-16T14:10Z — done, PR #3 open, moved to done/ -->

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
- 2026-07-16 — gentle-loris-hazel: **process note** — the Implementer
  sub-agent committed the fix directly onto local `main` in the main repo
  checkout instead of the `worktree-gentle-loris-hazel` branch (violates
  AGENTS.md §2a "never push source code to main"). Caught before any push:
  moved the commit onto `worktree-gentle-loris-hazel` (fast-forwarded that
  branch onto it, since it was a stale ancestor) and hard-reset local
  `main` back to `origin/main`. No data lost, nothing shared was affected
  since the stray commit was never pushed. PR opened normally from the
  correct branch after this fix. Verified `go build`, `go vet`,
  `go test ./... -race` all pass before opening the PR.
- 2026-07-16 — gentle-loris-hazel: PR #3 opened
  (https://github.com/chrisjohnson/printer-dashboard/pull/3), build/vet/
  test all pass locally. Closing K-004 as done — code review/merge is a
  separate step outside this card's scope (PR awaits human/reviewer
  action). Also closing K-006 and K-030 as confirmed duplicates.

## Handoff notes
PR #3 open against main, not yet merged. This card's scope (root-cause,
fix, tests, PR) is complete. If the PR gets review feedback requiring
changes, that's follow-up work on `worktree-gentle-loris-hazel` — reopen
or file a new card if picked up by a different session.
