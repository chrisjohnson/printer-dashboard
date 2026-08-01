---
id: K-033
# Filename pattern: {ID}-{slugified-title}.md
title: AMS status display
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: pi                 # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-31T00:00Z                 # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-033 — AMS status display

## Context

AMS status display. UI for parsed AMS data — slot grid, color swatches, filament type, remaining weight/length.

See `.fleet/research-notes.md`'s K-055 section for the full AMS/filament
protocol reference before starting.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [ ] Add AMS/filament structs to PrinterStatus (interface.go)
2. [ ] Add AmsData struct to bambu/parser.go mirroring MQTT ams JSON
3. [ ] Parse ams section in handleReport (client.go) when present
4. [ ] Build UI section in dashboard showing AMS grid (slot tiles with color/type/remaining)
5. [ ] Test with real AMS hardware or mocked MQTT data
6. [ ] Add signals and move to done/

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

## Handoff notes
<!-- written by whichever role/session was last active on this card, before handing
     off or ending a session. What's half-done, what the next role should do first. -->
