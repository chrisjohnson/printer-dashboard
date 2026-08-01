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

### Backend (Go)
1. [x] Add `AMSUnit` and `FilamentSlot` structs to `internal/printers/interface.go`
2. [x] Add `AmsData`, `AmsUnit`, `AmsTray` structs to `internal/printers/bambu/parser.go`
3. [x] Add `Ams` field to `printStatus` struct in parser.go
4. [x] Implement AMS parsing in `handleReport()` in `internal/printers/bambu/client.go`
5. [x] Populate `AMSUnits` and `ActiveTrayID` in `PrinterStatus`

### Frontend (Dashboard)
6. [ ] Add AMS section to printer card UI
7. [ ] Render grid of AMS unit tiles with slot information
8. [ ] Display color swatches (convert RRGGBBAA hex), filament type, remaining %
9. [ ] Highlight active tray, show external spool when tray_now=254

### Verification
10. [ ] Test with mocked MQTT data (P1S without humidity, H2S with humidity)
11. [ ] Verify delta update handling (P1S sends partial updates)
12. [ ] Add signals and move to done/

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: pi 2026-07-31T00:00Z — backend complete: AMS data structures and parsing implemented -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->

## Handoff notes
<!-- written by whichever role/session was last active on this card, before handing
     off or ending a session. What's half-done, what the next role should do first. -->
