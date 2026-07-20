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
1. [ ]

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

## Handoff notes
Research dispatched by gentle-loris-hazel 2026-07-20T02:48Z — finding the
exact `.val` update code, cadence, and checking for an existing
avoid-clobbering-user-interaction pattern to mirror (e.g. from K-080).
