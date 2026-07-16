---
id: K-013
# Filename pattern: {ID}-{slugified-title}.md
title: Retry MQTT connect on failure
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: gentle-loris-hazel   # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-16T13:54Z    # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-013 — Retry MQTT connect on failure

## Context

Retry MQTT connect on failure with exponential backoff. Improves resilience when the MQTT broker is temporarily unavailable.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [x] Researcher: determine what's actually missing. Done — see Decision log.
2. [ ] Implementer: wrap the initial `mqttClient.Connect()`/`WaitTimeout`
   call in `internal/printers/bambu/client.go` (~line 181) in a retry loop
   with doubling backoff (1s→2s→4s...) capped at `MaxReconnectInterval`
   (30s), respecting `ctx.Done()` for shutdown cancellation. Retry
   indefinitely (no attempt ceiling — see Decision log for rationale).
   Do NOT enable Paho's `SetConnectRetry` (fixed-interval, not exponential,
   and conflicts with a custom loop). Leave `AutoReconnect`/
   `MaxReconnectInterval` as-is — they already correctly handle
   connection-lost-after-success.
3. [ ] Implementer: add a test exercising the initial-connect-failure retry
   path (dial to a closed/refusing port, or inject a failing dialer if the
   client is structured to allow that — check existing test patterns in
   `client_test.go` for how MQTT connection is mocked/faked).
4. [ ] Implementer: run full test suite, commit, push, open PR.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-16T13:54Z — claiming, dispatching researcher to check current connect/reconnect behavior before implementing -->

## Working context
<!-- curated facts a teammate picking this up needs, ~15 lines max. Bigger context
     belongs in a linked doc, not here. -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->
- 2026-07-16 — gentle-loris-hazel: Research found `AutoReconnect(true)` +
  `MaxReconnectInterval(30s)` (client.go:172-173) already correctly handle
  connection-lost-after-success with Paho's own backoff. What's actually
  missing is *initial*-connect retry: `SetConnectRetry` is never called
  (defaults false), so a failed first `Connect()` just returns an error
  immediately (client.go:181-192) and the caller (server.go:331-336) logs
  it and gives up — that printer is stuck in "error" until process
  restart. Paho's `ConnectRetry` option, if enabled, is fixed-interval not
  exponential, and would make `Connect()` async in a way that conflicts
  with the current synchronous `WaitTimeout` pattern — so this needs a
  small hand-rolled retry loop around the initial connect, not an
  options-only fix. No existing backoff utility in the repo to reuse; a
  local loop is appropriate for this single call site.
- 2026-07-16 — gentle-loris-hazel: judgment call — retry indefinitely (no
  attempt ceiling), matching the existing precedent of both
  `AutoReconnect` (retries forever post-connect) and Snapmaker's
  reconnect loop (retries forever via ticker) elsewhere in this codebase.
  The card didn't specify a ceiling and there's no "give up permanently"
  UX in the current status model — indefinite retry with capped backoff
  is the lower-risk, more-consistent choice; can add a ceiling later if a
  human wants one.

## Handoff notes
Implementer dispatched by gentle-loris-hazel 2026-07-16T14:05Z, working in
`.fleet/worktrees/gentle-loris-hazel` on a fresh branch
`worktree-gentle-loris-hazel-k013` (branched directly off origin/main, no
unrelated commits this time — learned from the K-080 branch-mixing
mistake). Awaiting completion.
