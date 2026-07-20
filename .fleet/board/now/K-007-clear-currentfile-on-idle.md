---
id: K-007
# Filename pattern: {ID}-{slugified-title}.md
title: Clear CurrentFile on idle
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: gentle-loris-hazel   # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-20T02:36Z    # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-007 — Clear CurrentFile on idle

## Context

Clear CurrentFile on idle. Currently CurrentFile persists after a job completes. Same as K-003 which was closed but the fix may be incomplete.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [x] Researcher: traced current behavior end-to-end. Done — see
   Decision log. Verdict: Bambu clearing is not actually broken (K-004
   shifted timing by one report, arguably improved UX), but there's a
   latent coupling worth fixing. Real gap found on Snapmaker (separate
   scope, filed as K-091).
2. [ ] Implementer: decouple Bambu's `CurrentFile` clear
   (`client.go:439`) from the K-004-latched `s.State` — key it off
   `mapState(p.GcodeState) == "idle"` (the raw per-report mapped state)
   instead, so CurrentFile-clearing semantics don't silently ride on
   future changes to the latch threshold. Add/update a test covering the
   3-report SUCCESS→IDLE→IDLE sequence to lock in the intended timing.
3. [ ] Implementer: run full test suite, commit, push, open PR.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-20T02:36Z — claiming, dispatching researcher (note: K-004's complete/idle state hysteresis fix already merged, may interact with this) -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-20 — gentle-loris-hazel: Research traced the real firmware
  sequence (SUCCESS→IDLE→IDLE) live via `go test`. Bambu's clear
  (`client.go:439`, `if p.GcodeState != "" && s.State == "idle"`) reads
  the *post-latch* `State`, so K-004 delayed the clear by exactly one
  report — CurrentFile and the COMPLETE badge now disappear together
  instead of the filename vanishing one beat early. Not a regression;
  arguably better UX. K-003 (commit `ee5445d`) already added this Bambu
  logic + full test coverage — its tests just don't exercise the 3-report
  real-world sequence, which is why the shift wasn't caught. `TestHandleReport_NewPrintPopulatesCurrentFile`
  confirms a new print immediately overwrites CurrentFile regardless of
  the latch — no "stale filename on new print" scenario exists.
  Snapmaker has **no CurrentFile-clearing logic at all** — a real,
  separate, lower-priority gap unrelated to K-004 (Snapmaker's
  `CurrentFile` comes from a totally independent Moonraker query path,
  `handleQueryReport`, not the `State`-latch path). Filed as K-091 rather
  than folding into this card, since K-007 was always Bambu-scoped
  ("Same as K-003" which was Bambu-only) and the fix shape is unrelated.
- 2026-07-20 — gentle-loris-hazel: proceeding with the minor Bambu
  robustness cleanup (decouple from latched State) even though it's not
  a user-visible bug — ordinary judgment call, low-risk, directly
  addresses the "does K-004 affect this" coupling the research
  identified, and locks in the 3-report timing with a test so it can't
  silently drift again if the latch threshold changes later.

## Handoff notes
Research complete, scope set. Dispatching Implementer next for the Bambu
decoupling fix. K-091 filed separately for the Snapmaker gap (not
started, backlog).
