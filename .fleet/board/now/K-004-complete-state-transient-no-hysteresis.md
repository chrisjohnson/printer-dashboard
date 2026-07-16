---
id: K-004
# Filename pattern: {ID}-{slugified-title}.md
title: COMPLETE state transient no hysteresis
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: gentle-loris-hazel   # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-16T13:22Z    # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-004 — COMPLETE state transient no hysteresis

## Context

COMPLETE state is transient — SUCCESS overwrites to complete, but the next IDLE overwrites it back. No hysteresis. Needs state machine fix.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [ ]

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

## Handoff notes
Research dispatched by gentle-loris-hazel 2026-07-16T13:22Z — scoping the
state machine, reconciling against K-006 (possible duplicate/sibling) and
K-030 (possible existing hysteresis pattern to reuse). Awaiting findings
before implementation.
