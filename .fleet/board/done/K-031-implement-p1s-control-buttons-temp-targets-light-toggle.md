---
id: K-031
# Filename pattern: {ID}-{slugified-title}.md
title: Implement P1S control buttons (temp targets + light toggle)
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: null                 # pet name of the agent session working this card, e.g. otter
claimed_at: null                 # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-031 — Implement P1S control buttons (temp targets + light toggle)

## Context
Bambu P1S printer needed temperature-target controls and a light toggle exposed through the dashboard.

## Plan
1. [x] Added SetBedTemp/SetNozzleTemp/SetChamberTemp/SetLight to the Printer interface, wired Bambu GCode/system commands, added REST endpoints and frontend controls (commit e32f5e0).
2. [x] Fixed light state being read from the wrong field (system.ledctrl, an ACK-only channel) — now reads print.lights_report; fixed the toggle command to include required fields (sequence_id, led_on_time, etc.) that firmware silently required (commit 111da6a).

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->

## Working context
- internal/printers/bambu/client.go
- internal/printers/bambu/client_test.go
- internal/printers/bambu/commands.go
- internal/printers/bambu/commands_test.go
- internal/printers/bambu/parser.go
- internal/printers/bambu/parser_test.go
- internal/printers/interface.go
- internal/printers/snapmaker/snapmaker.go (interface stub)

## Decision log
- 2026-07-12: Done because both the initial implementation and the follow-up correctness fix are merged to main and verified against real firmware behavior (commits e32f5e0, 111da6a).

## Handoff notes
This card was backfilled retroactively from git history when the fleet board was onboarded to this repo; no live session worked it via the board.
