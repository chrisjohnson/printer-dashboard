---
id: K-077
# Filename pattern: {ID}-{slugified-title}.md
title: Fix Snapmaker light control and track commanded LED state
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: null                 # pet name of the agent session working this card, e.g. otter
claimed_at: null                 # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-077 — Fix Snapmaker light control and track commanded LED state

## Context
Snapmaker U1 light control needed to work via Moonraker GCode, but the cavity_led fixture turned out to be RGB-driven rather than a simple on/off.

## Plan
1. [x] Implemented Snapmaker U1 light control via Moonraker GCode (SET_LED LED=cavity_led) (commit 07e05fa).
2. [x] Fixed the fixture being RGB-driven (white = R=G=B=1) so sending only WHITE=1/0 was a silent no-op on hardware; added commanded-state tracking since Moonraker can't query real LED state; made sendGCode detect embedded error bodies on HTTP 200 responses; updated the UI toggle with optimistic revert-on-failure (commit 54d257f).

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->

## Working context
- internal/printers/snapmaker/snapmaker.go
- internal/printers/snapmaker/snapmaker_test.go
- frontend toggle-switch component

## Decision log
- 2026-07-12: Done because the fix was verified against real Snapmaker U1 hardware behavior (RGB fixture quirk) and commanded-state tracking now works around Moonraker's lack of LED state query (commits 07e05fa, 54d257f).

## Handoff notes
This card was backfilled retroactively from git history when the fleet board was onboarded to this repo; no live session worked it via the board.
