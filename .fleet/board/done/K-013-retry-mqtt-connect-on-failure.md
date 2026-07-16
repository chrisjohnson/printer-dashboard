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
2. [x] Implementer: added `connectWithRetry` wrapping the initial connect in
   a doubling-backoff loop (1s→2s→4s..., capped at 30s), retrying
   indefinitely, respecting `ctx.Done()`. `SetConnectRetry` left disabled;
   `AutoReconnect`/`MaxReconnectInterval` untouched.
3. [x] Implementer: added `TestConnect_RetriesInitialConnectFailure` and
   `TestConnect_RetryLoopRespectsContextCancellation`, using a real local
   TCP listener + injected test-only backoff fields. Re-run 15x with
   `-race` during development, no flakiness.
4. [x] Implementer: full test suite passes, committed. PR:
   https://github.com/chrisjohnson/printer-dashboard/pull/5

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-16T13:54Z — claiming, dispatching researcher to check current connect/reconnect behavior before implementing -->
<!-- signal: gentle-loris-hazel 2026-07-16T14:15Z — done, PR #5 open, moved to done/ -->

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
- 2026-07-16 — gentle-loris-hazel: verified locally (rebased branch onto
  latest main for a clean diff, re-ran `go build`/`go vet`/full test
  suite/`-race` on the new tests myself before pushing). PR #5 opened
  (https://github.com/chrisjohnson/printer-dashboard/pull/5). On ctx
  cancellation the retry loop makes `Connect()` return nil (clean
  shutdown) rather than propagating an error — intentional, preserves the
  pre-existing contract that a non-nil return means a genuine connect
  failure, and cancellation only happens on shutdown. Closing as done —
  review/merge is outside this card's scope.

## Handoff notes
PR #5 open against main, not yet merged. If review feedback requires
changes, that's follow-up work on branch `worktree-gentle-loris-hazel-k013`.
