---
id: K-013
# Filename pattern: {ID}-{slugified-title}.md
title: Retry MQTT connect on failure
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: gentle-loris-hazel   # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-16T13:54Z    # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-013 — Retry MQTT connect on failure

## Context

Retry MQTT connect on failure with exponential backoff. Improves resilience when the MQTT broker is temporarily unavailable.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [ ]

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-16T13:54Z — claiming, dispatching researcher to check current connect/reconnect behavior before implementing -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->

## Handoff notes
Research dispatched by gentle-loris-hazel 2026-07-16T13:54Z — checking
current Paho MQTT client options (SetConnectRetry/SetAutoReconnect/etc.)
and what happens today on initial connect failure, before implementing.
