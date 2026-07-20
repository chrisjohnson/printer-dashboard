---
id: K-069
# Filename pattern: {ID}-{slugified-title}.md
title: Text selection on temp value resets after ~2s
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: gentle-loris-hazel   # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-20T02:48Z    # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-069 — Text selection on temp value resets after ~2s

## Context

Text selection on the temp value resets after ~2s. Periodic WS/polling update rewrites .val textContent, clobbering in-progress text selection.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [x] Researcher: found exact code, cadence, and existing pattern. Done
   — see Decision log.
2. [x] Implementer: added `setValText(el, newText)` helper with both
   guards, used at all 4 `.val` write sites. Done.
3. [x] Implementer: added Playwright test. **Caught a second, deeper bug
   during testing** — see Decision log (`reorderCard` unconditional DOM
   move). Dispatched a follow-up fix on the same branch before accepting.
4. [ ] Implementer (follow-up): add "skip if already in correct position"
   guard to `reorderCard()` (`updateCard` calls it unconditionally on
   every WS push, and it always does `insertBefore`/`appendChild` even
   when position is unchanged — moving a DOM node collapses any active
   selection inside it in all major browsers, independent of
   `setValText`'s guard, so this was silently defeating the whole fix for
   the common case). Add an end-to-end test through the real
   `mergeWithCache`→`updateCard` pipeline (not just `setValText` in
   isolation) proving the selection now survives a real WS push.
5. [ ] Run full test suite, commit, push, open PR.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-20T02:48Z — claiming, dispatching researcher to find exact update code and fix approach -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-20 — gentle-loris-hazel: Research confirmed: updates are WS
  push-driven (not client polling), forwarded 1:1 from printer status
  reports with no throttling (`internal/server/server.go:343-371`
  `startStatusForwarder`) — the "~2s" cadence traces to the printer's own
  MQTT push interval. `mergeWithCache()` does no equality diffing, so
  `updateCard()`'s unconditional `.val.textContent = ...` writes (4
  sites: bed, nozzle, extra nozzles, chamber) fire on every push
  regardless of whether the value actually changed — confirming
  "skip-if-unchanged" alone fixes the common case (temp holding steady).
  A directly analogous pattern already exists for a related problem:
  `setTargetInput()` (~line 987-990) guards its input write with
  `document.activeElement !== inp` specifically so a live WS update never
  clobbers active typing. This fix mirrors that same intent for text
  selection instead of input focus.
- 2026-07-20 — gentle-loris-hazel: Implementer's first-pass test caught a
  real, separate problem: testing `setValText` through the *actual*
  `mergeWithCache`→`updateCard` pipeline (not in isolation) showed the
  selection still got destroyed, because `updateCard` unconditionally
  calls `reorderCard()` on every invocation, and `reorderCard()` always
  does `insertBefore`/`appendChild` — moving the DOM node — even when the
  card's position hasn't changed. Moving a node (detach+reinsert, which
  is what `insertBefore`/`appendChild` do per the DOM spec regardless of
  whether the position actually changes) collapses any active Selection
  anchored inside it, in all major browsers. Verified this independently
  (dispatched a separate check) before accepting — confirmed real,
  confirmed `reorderCard` has no "already correct position" guard
  (unlike `setTargetInput`/`setValText`'s established skip-if-unchanged
  pattern elsewhere in this file). Without also fixing this, K-069's
  `setValText` fix would not actually solve the user-reported bug in the
  common case. Judgment call: fixing this on the same branch/PR rather
  than filing a separate follow-up card, since it's directly load-bearing
  for K-069's stated goal, not a tangential finding — shipping K-069
  without it would mean claiming a fix that doesn't actually work
  end-to-end.

## Handoff notes
Follow-up implementer dispatched 2026-07-20T03:20Z (same branch,
`worktree-gentle-loris-hazel-k069`) to add the `reorderCard` position
guard and an end-to-end test through the real update pipeline. Awaiting
completion before push/PR.
