---
id: K-009
# Filename pattern: {ID}-{slugified-title}.md
title: Authentication must have tests
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: gentle-loris-hazel   # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-16T14:07Z    # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-009 — Authentication must have tests

## Context

Authentication — login page, sessions. Must have tests. Needed for remote access.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [ ]

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-16T14:07Z — claiming, dispatching researcher to map auth code + existing coverage before writing tests -->
<!-- signal: gentle-loris-hazel 2026-07-16T14:20Z — blocked: no auth feature exists to test, scope mismatch, needs human triage; moved to blocked/ -->

## Working context
- `internal/config/config.go:20-26` defines `AuthConfig{Enabled, Username,
  Password, Secret}`, wired into `Config.Auth` — but it's dead data. Loads/
  saves fine, never read anywhere else.
- `grep -rn "cfg.Auth\|\.Auth\."` across `cmd/`/`internal/` (excl.
  config.go/config_test.go) returns **nothing**. No `handleLogin`, no
  session store, no cookie issuance, no auth middleware. `Server.New`
  (`server.go:60-61`) registers every route — including printer control
  endpoints like pause/resume/cancel/temp-set — completely unauthenticated,
  regardless of `Auth.Enabled`.
- The only "login"/"auth" hits in `internal/server/` are the unrelated
  Bambu Cloud *printer-account* linking flow
  (`internal/printers/bambu/auth.go`, `onboarding.go`
  `handleOnboardingBambuLogin*`) — not dashboard access control.
- Test harness convention for whenever this gets built:
  `newTestServer(printersMap)` (`server_test.go:118`) +
  `httptest.NewServer(s.mux)`, asserting on status/JSON via `http.Client`.
- Checked `git log --all` and all local/remote branches for any
  unmerged auth work — none found.

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-16 — gentle-loris-hazel: **scope mismatch, not proceeding as
  filed.** This card says "must have tests," but research confirmed there
  is no authentication feature to test — `Auth.Enabled` is parsed from
  config and never checked anywhere; the dashboard (including printer
  control endpoints) is fully unauthenticated today regardless of config.
  "Add tests" silently expanding into "design and build a dashboard login/
  session system" is not an ordinary judgment call I'm willing to make
  unilaterally — it's a security-sensitive feature with real design
  decisions (session mechanism, password hashing, CSRF posture, cookie
  flags) that a human should weigh in on, not something to improvise via a
  worker's discretion. Not implementing speculatively. Moving to blocked/
  rather than done/ or leaving in now/, and flagging clearly for human
  triage: is this still wanted, and if so, should it be re-filed as an
  implementation card (with K-009 becoming the follow-up test card once
  that lands)?

## Handoff notes
**Needs human triage before further work.** Auth doesn't exist in this
codebase — `AuthConfig` is fully wired into config load/save but never
consumed. Recommend: re-file this as an implementation card ("build
dashboard login/session auth") separate from a future K-009-style test
card, and have a human confirm the design (cookie-session vs. token,
password hashing via bcrypt against `Auth.Password`, CSRF protection on
state-changing POST routes since printer control endpoints currently have
none). Worth flagging as a real security gap independent of this card:
every `/api/printers/*` control endpoint (pause/resume/cancel/temp-set) is
open to anyone who can reach the server today.
