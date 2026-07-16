---
id: K-083
# Filename pattern: {ID}-{slugified-title}.md
title: Update PLAN.md camera architecture to reflect go2rtc/RTSPS
initiative_id: null
claimed_by: happy-sloth-dune
claimed_at: 2026-07-16T03:10:00Z
blocks: null
blocked_by: null
related_cards: [K-001, K-040, K-047]
---

# K-083 — Update PLAN.md camera architecture to reflect go2rtc/RTSPS

## Context
Surfaced during F-057 (bootstrapping this repo onto the new fleet model,
migrating `.agent/STATE.md`). STATE.md's "Working context" noted PLAN.md's
§4.1 camera section still described the original design (binary-TLS on
port 6000 for local access, TUTK P2P for remote) as the plan for Bambu
cameras generically, but what actually shipped (K-001, K-047) diverged:
H2S uses a `go2rtc`-subprocess consuming the printer's native RTSPS feed
over LAN (port 322, requires "LAN Only Liveview"), not port 6000 or TUTK.
P1S still uses the original binary-TLS/port-6000 design as planned, but
hasn't been regression-tested since the H2S refactor (K-040, still open).
Never carded before — found buried in STATE.md prose, not the Kanban
section.

## Plan
1. [x] Read PLAN.md's existing camera section (§4.1) and K-001/K-047's
   full resolution detail (STATE-OVERFLOW.md) to get the shipped
   architecture right before editing.
2. [x] Update PLAN.md: keep the binary-TLS design documented (still
   accurate for P1S), add the actual H2S go2rtc/RTSPS path, and mark TUTK
   P2P as never implemented.

## Signals
<!-- signal: happy-sloth-dune 2026-07-16T03:10:00Z — claiming, doc-only fix, part of F-057 live-verification step -->
<!-- signal: happy-sloth-dune 2026-07-16T03:10:00Z — done -->

## Decision log
- 2026-07-16: Doc-only change (PLAN.md), zero code risk — used as F-057's
  live verification that claim → work → done works end-to-end on this
  repo's board under the new fleet model.

## Handoff notes
None — closed same session.
