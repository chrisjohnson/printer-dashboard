---
id: K-030
# Filename pattern: {ID}-{slugified-title}.md
title: H2S status icon hysteresis
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: null                 # pet name of the agent session working this card, e.g. otter
claimed_at: null                 # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: [K-004]
---

# K-030 — H2S status icon hysteresis

## Context

H2S status icon hysteresis. Folds into K-004/K-006 — same state machine hysteresis problem for the Snapmaker H2S status icons. Deferred.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [ ]

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-16 — gentle-loris-hazel: closing as duplicate. K-004 shipped the
  hysteresis fix this card describes, applied to both the Bambu and
  Snapmaker paths (PR #3,
  https://github.com/chrisjohnson/printer-dashboard/pull/3) — this card's
  own Context said it folds into K-004/K-006.

## Handoff notes
Resolved via K-004 / PR #3. Nothing further here.
