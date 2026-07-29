---
id: K-099
title: Surface auth timeout errors in UI with clickable refresh action
initiative_id: null
claimed_by: opencode
claimed_at: 2026-07-29T15:00:00Z
blocks: null
blocked_by: null
related_cards: [K-098, K-005, K-006, K-013, K-086, K-087]
---

# K-099 — Surface auth timeout errors in UI with clickable refresh action

## Context
K-098 investigated the H2S camera being "disabled" — root cause was NOT an auth timeout (camera works in Docker with go2rtc 1.9.14). However, the auth timeout UI feature is still valuable: Bambu cloud MQTT tokens, TUTK P2P credentials, and RTSPS access codes can all expire and cause camera/streaming failures with no user-facing error message. Currently these failures surface as silent 502s or placeholder images with no explanation or recovery path.

The user wants: when an auth timeout/expiration is detected, show a UI alert (error banner or camera placeholder) that explains the issue and offers a clickable action to refresh/re-authenticate.

## Plan
1. [ ] Identify all auth timeout failure points — MQTT reconnect, RTSPS 401/403, TUTK P2P auth failure
2. [ ] Determine how to surface auth errors in the frontend (error banner vs. camera placeholder)
3. [ ] Design "Refresh Auth" UI element — what does it do for each auth type?
   - MQTT cloud token: re-connect/re-authenticate via Bambu cloud
   - RTSPS access code: re-visit onboarding to update host/access code
   - TUTK P2P: re-fetch TTCode via Bambu cloud API
4. [ ] Implement backend detection of auth errors (401/403 from RTSPS, MQTT auth failure)
5. [ ] Implement frontend UI for auth error + refresh action
6. [ ] Test

## Signals
<!-- signal: opencode 2026-07-29T15:00:00Z — created K-099, starting research on auth error detection -->

## Decision log
<!-- append-only, one line per entry, newest last. -->

## Handoff notes
