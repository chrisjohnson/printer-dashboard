---
id: K-098
title: Investigate H2S camera disabled — auth timeout + UI refresh flow
initiative_id: null
claimed_by: opencode
claimed_at: 2026-07-29T14:30:00Z
blocks: null
blocked_by: null
related_cards: [K-005, K-006, K-086, K-087, K-088, K-089, K-090]
---

# K-098 — Investigate H2S camera disabled — auth timeout + UI refresh flow

## Context
The H2S camera is currently disabled on the printer dashboard. Suspected cause is an MQTT auth token timeout (AMS/TUTK P2P credentials expiring). The user wants the cause identified and, if it is an auth timeout, a UI mechanism to surface the timeout and let the user click-through to refresh credentials. If the root cause turns out to be something other than auth timeout, the auth UI fix should be split into its own ticket.

## Plan
1. [ ] Inspect H2S camera code paths — how camera is enabled/disabled in frontend and backend
2. [ ] Identify root cause of disabled state (auth timeout vs. config vs. network vs. code bug)
3. [ ] If auth timeout: design UI alert with clickable refresh action
4. [ ] If different cause: create separate ticket for auth timeout UI, proceed with root cause fix
5. [ ] Implement fix
6. [ ] Test

## Signals
<!-- signal: opencode 2026-07-29T14:30:00Z — created card, starting investigation -->

## Decision log
<!-- append-only, one line per entry, newest last. -->

## Handoff notes
