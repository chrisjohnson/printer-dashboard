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
2. [x] Implementer: decoupled Bambu's `CurrentFile` clear from the
   K-004-latched `s.State`, keying it off the raw per-report
   `mapState(p.GcodeState)` instead. Deliberate decision, documented in
   code + commit: `CurrentFile` now clears at the first IDLE report,
   `State` stays latched to `"complete"` until the second. Added
   `TestHandleReport_SuccessIdleIdleSequence_CurrentFileClearsBeforeState`
   locking in the exact relative timing.
3. [x] Implementer: full test suite passes, committed. I reviewed the
   timing-shift implication (see Decision log), verified against the
   frontend rendering code, rebased, re-verified tests, pushed, PR:
   https://github.com/chrisjohnson/printer-dashboard/pull/9

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-20T02:36Z — claiming, dispatching researcher (note: K-004's complete/idle state hysteresis fix already merged, may interact with this) -->
<!-- signal: gentle-loris-hazel 2026-07-20T03:15Z — done, PR #9 open, moved to done/ -->

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
- 2026-07-20 — gentle-loris-hazel: Implementer's chosen timing creates a
  one-report window where State="complete"/CurrentFile="" — verified via
  a targeted frontend check (onboarding.go renderCard/updateCard render
  current_file unconditionally with a "—" fallback, not gated on state)
  that this degrades gracefully rather than looking broken. Accepted as-
  is. PR #9 opened, build/vet/test all pass. Closing as done.

## Handoff notes
PR #9 open against main, not yet merged.

Review note: the Implementer's chosen behavior creates a one-report-cycle
window (~1s at Bambu's typical push cadence) where `State="complete"` but
`CurrentFile=""`. I verified this isn't visually broken before accepting
it — dispatched a quick check of `internal/server/onboarding.go`'s
`renderCard`/`updateCard`: the frontend renders `current_file`
unconditionally with an em-dash fallback, not gated on `state` at all, so
the card just shows "Complete" + `—` for one cycle before settling to
"Idle" + `—`. Minor, brief loss of "what finished" info, not a rendering
bug — accepted as a reasonable tradeoff for the cleaner decoupling.
