---
id: K-007
# Filename pattern: {ID}-{slugified-title}.md
title: Clear CurrentFile on idle
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: gentle-loris-hazel   # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-20T02:36Z    # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-007 — Clear CurrentFile on idle

## Context

Clear CurrentFile on idle. Currently CurrentFile persists after a job completes. Same as K-003 which was closed but the fix may be incomplete.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [ ]

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-20T02:36Z — claiming, dispatching researcher (note: K-004's complete/idle state hysteresis fix already merged, may interact with this) -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->

## Handoff notes
Research dispatched by gentle-loris-hazel 2026-07-20T02:36Z — checking
current CurrentFile clearing logic (Bambu + Snapmaker), what K-003 already
fixed, and whether K-004's hysteresis change interacts with this.
