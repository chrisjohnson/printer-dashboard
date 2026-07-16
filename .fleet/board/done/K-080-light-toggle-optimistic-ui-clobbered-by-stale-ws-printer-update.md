---
id: K-080
# Filename pattern: {ID}-{slugified-title}.md
title: Light-toggle optimistic UI clobbered by stale WS printer_update
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: gentle-loris-hazel   # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-16T13:40Z    # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-080 — Light-toggle optimistic UI clobbered by stale WS printer_update

## Context

Light-toggle optimistic UI can be clobbered by a stale WS printer_update. toggleLight() writes optimistic DOM but never updates window._printerCache; a WS push that arrives before the fetch resolves snaps the toggle back.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [x] Implementer: fix `toggleLight()` in `internal/server/onboarding.go` to
   write the optimistic value into `window._printerCache` and mark it
   pending until the fetch settles; `mergeWithCache()` skips pending fields
   so a stale WS push can't clobber the in-flight optimistic update.
2. [x] Add Playwright regression test simulating the race (verified it
   fails on pre-fix code, passes on fixed code).
3. [x] Run full test suite, commit, push, open PR. PR:
   https://github.com/chrisjohnson/printer-dashboard/pull/4

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-16T13:40Z — claiming, root cause already scoped in Context, dispatching implementer directly -->
<!-- signal: gentle-loris-hazel 2026-07-16T14:00Z — done, PR #4 open, moved to done/ -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-16 — gentle-loris-hazel: Implementer confirmed and fixed the race
  exactly as diagnosed in Context. `mergeWithCache()` skips fields marked
  pending in `window._pendingFields` until the in-flight `/light` fetch
  settles, so a stale `printer_update` (carrying the driver's pre-command
  `light_on`, which is a concrete value not an omitted field) can't clobber
  the optimistic DOM/cache write. Non-pending fields still merge normally,
  and the pending flag clears on settle so future genuine updates aren't
  suppressed.
- 2026-07-16 — gentle-loris-hazel: **process note** — the Implementer was
  (by my own instruction) dispatched onto the same worktree branch as the
  already-open K-004 PR (#3). To avoid conflating two unrelated fixes into
  one PR, moved the K-080 commit onto a fresh branch
  (`worktree-gentle-loris-hazel-k080`) cherry-picked cleanly onto current
  main via a temporary scratch worktree, then reset the original worktree
  back to its PR #3 state. Verified the new branch's diff against main
  contains only the K-080 change (2 files, no leakage from K-004).
- 2026-07-16 — gentle-loris-hazel: PR #4 opened
  (https://github.com/chrisjohnson/printer-dashboard/pull/4), build/vet/Go
  tests all pass; Playwright suite 13/14 pass (1 pre-existing, unrelated
  sandbox-network failure, reproduces on unmodified main). Closing as done
  — review/merge is outside this card's scope.

## Handoff notes
PR #4 open against main, not yet merged. If review feedback requires
changes, that's follow-up work on branch `worktree-gentle-loris-hazel-k080`.
