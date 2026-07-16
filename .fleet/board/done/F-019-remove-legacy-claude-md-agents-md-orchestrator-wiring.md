---
id: F-019
# Filename pattern: {ID}-{slugified-title}.md
title: Remove legacy CLAUDE.md/AGENTS.md orchestrator wiring
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: null                 # pet name of the agent session working this card, e.g. otter
claimed_at: null                 # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# F-019 — Remove legacy CLAUDE.md/AGENTS.md orchestrator wiring

## Context
The old single-repo orchestrator import in CLAUDE.md/AGENTS.md pointed unconditionally at `~/src/chrisjohnson/agents/AGENTS.md` with no `.fleet/`-gating, which would have run alongside the new agentic-fleet gated roster. Removed to avoid double-running orchestrator rules.

## Plan
1. [x] Removed legacy CLAUDE.md/AGENTS.md orchestrator import wiring (commit f8ac742).
2. [x] Added `.fleet/` board scaffolding (backlog/now/blocked/done dirs) to this repo (commit 5900bc1).

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->

## Working context
- AGENTS.md
- CLAUDE.md
- .fleet/board/**/.gitkeep

## Decision log
- 2026-07-13: Done because the legacy wiring was fully replaced by agentic-fleet's gated AGENTS.md import and this repo's board is now scaffolded (commits f8ac742, 5900bc1).

## Handoff notes
This card was backfilled retroactively from git history when the fleet board was onboarded to this repo; no live session worked it via the board.
