---
id: K-005
# Filename pattern: {ID}-{slugified-title}.md
title: P1S cloud MQTT field audit
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: opencode
claimed_at: 2025-07-27T12:00:00Z
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                 # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-005 — P1S cloud MQTT field audit

## Context

P1S cloud MQTT field audit. gcode_file vs subtask_name, temps, fallback parsing. Closely related to K-032 parser work.

## Plan
1. [ ] Researcher: Investigate current MQTT payload structure for P1S printers.
2. [ ] Researcher: Identify discrepancies between `gcode_file` and `subtask_name` fields.
3. [ ] Researcher: Audit temperature field parsing logic and identify fallback edge cases.
4. [ ] Implementer: Propose parser improvements based on audit findings.

## Signals
<!-- signal: opencode 2025-07-27T12:00:00Z — claimed card and started research -->

## Working context

## Decision log

## Handoff notes

