---
id: K-091
title: Snapmaker CurrentFile never clears (no clearing logic exists at all)
initiative_id: null
claimed_by: null
claimed_at: null
blocks: null
blocked_by: null
related_cards: [K-007, K-003]
---

# K-091 — Snapmaker CurrentFile never clears (no clearing logic exists at all)

## Context

Discovered as a side-finding while researching K-007 (Bambu CurrentFile
clearing) — a distinct, real gap, not the same bug.

Bambu's `CurrentFile` clearing (added by K-003, commit `ee5445d`) lives in
`internal/printers/bambu/client.go:439` and is keyed off the derived
`State` field reaching `"idle"`. Snapmaker has no equivalent at all:
`CurrentFile` is populated in `handleQueryReport`
(`internal/printers/snapmaker/snapmaker.go:475-477`) from Moonraker's
`print_stats.filename` — a completely separate code path from
`handleStatusReport`'s `State` latch (lines 403-436) — and nothing ever
clears it. Moonraker's `print_stats.filename` typically stays populated
until a *new* print starts in stock Klipper (doesn't go empty on
completion), so in practice the dashboard likely shows a Snapmaker
printer's last-printed filename indefinitely after it finishes, until the
next print begins.

## Plan
1. [ ] Researcher/Implementer: confirm the above by checking actual
   Moonraker `print_stats` behavior (docs or the parsed response shape
   already handled in `internal/printers/snapmaker/parser.go`) — does
   `filename` ever go empty on its own, or does this app need to
   explicitly clear `CurrentFile` client-side when `State` reaches
   `"idle"`/`"complete"` (mirroring Bambu's approach)?
2. [ ] Implementer: add clearing logic to `handleStatusReport` or
   `handleQueryReport` (whichever fires last/is authoritative) — clear
   `CurrentFile` when the printer settles to a non-printing state,
   analogous to Bambu's fix. Be mindful of K-004's hysteresis latch on
   Snapmaker's `State` field (`completeIdleStreak`,
   `snapmaker.go:394-414`) — likely want to key off the same signal Bambu
   uses (post-latch settled idle) for consistency, but confirm.
3. [ ] Add test coverage mirroring Bambu's
   `TestHandleReport_IdleClearsCurrentFile` /
   `TestHandleReport_NewPrintPopulatesCurrentFile` for the Snapmaker path.
4. [ ] Run full test suite, commit, push, open PR.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-20T02:50Z — filed from K-007 research side-findings, not started -->

## Working context
- Relevant files: `internal/printers/snapmaker/snapmaker.go`
  (`handleQueryReport` ~L465-488 sets `CurrentFile`; `handleStatusReport`
  ~L403-436 has the `State` latch but no CurrentFile interaction at all).
- Compare against Bambu's fix: `internal/printers/bambu/client.go:439`
  and K-003's test suite in `client_test.go`.

## Decision log
- 2026-07-20 — gentle-loris-hazel: filed to backlog per §2 (self-discovered
  follow-up, not started) from K-007 research side-findings.

## Handoff notes
Not started. Lower priority than K-007 itself (K-007's Bambu behavior
turned out to be fine) — this is a real but separate, longer-standing gap.
