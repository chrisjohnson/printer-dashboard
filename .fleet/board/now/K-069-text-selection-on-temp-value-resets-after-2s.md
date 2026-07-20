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
2. [ ] Implementer: add a shared `setValText(el, newText)` helper in
   `internal/server/onboarding.go` applying two guards — (a) skip write
   if `el.textContent === newText` already (handles the common
   value-unchanged case, confirmed updates fire even when unchanged), and
   (b) skip write if there's a non-collapsed text selection anchored
   inside `el` (`window.getSelection()`, mirroring `setTargetInput`'s
   `document.activeElement` guard at ~line 987-990, adapted for selection
   instead of focus since `.val` is a plain span not an input). Use it at
   all 4 `.val` write sites in `updateCard` (bed ~996, nozzle ~1002,
   extra nozzles ~1013, chamber ~1027).
3. [ ] Implementer: add/extend a Playwright test (per K-080's precedent
   in `tests/dashboard.test.ts`) simulating a WS update with an unchanged
   or changed temp value while a selection is active inside `.val`,
   asserting the selection survives.
4. [ ] Implementer: run full test suite (Go + Playwright), commit, push,
   open PR.

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

## Handoff notes
Implementer dispatched by gentle-loris-hazel 2026-07-20T02:56Z, working
in `.fleet/worktrees/gentle-loris-hazel` on fresh branch
`worktree-gentle-loris-hazel-k069` (off origin/main). Awaiting
completion.
