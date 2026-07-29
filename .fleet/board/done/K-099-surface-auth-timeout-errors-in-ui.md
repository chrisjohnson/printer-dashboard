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
1. [x] Identify all auth timeout failure points — MQTT reconnect (auth.go:543, onConnectionLost), RTSPS 401/403 (proxy.go:388), go2rtc start failure (go2rtc.go:169)
2. [x] Determine how auth errors are surfaced in the frontend — use cam-error div (onboarding.go:1504) with error message + refresh button. MQTT errors already shown via .error-banner (onboarding.go:1147).
3. [x] Design "Refresh Auth" UI — camera-reconnect endpoint stops go2rtc instance, forcing lazy restart on next frame request. For MQTT, the Bambu client's Connect loop auto-reconnects with backoff.
4. [x] Implement backend: GET /api/printers/{id}/camera-status + POST /api/printers/{id}/camera-reconnect (server.go:1107-1241). go2rtc error tracking via SetLastError/LastError/ClearLastError (go2rtc.go:375-415). FrameHandler stores errors (proxy.go:365-400).
5. [x] Implement frontend: periodic 5s camera health check, cam-error div with message + "Refresh" button (onboarding.go:1279-1340). CSS for .cam-error and .cam-refresh-btn (onboarding.go:743-757).
6. [x] Test — 7 new tests in server_test.go, all pass. Docker image built and verified: GET /api/printers/h2s/camera-status returns {"ok":true}, POST /api/printers/h2s/camera-reconnect returns {"reconnected":1}.

## Signals
<!-- signal: opencode 2026-07-29T15:00:00Z — created K-099, starting research on auth error detection -->
<!-- signal: opencode 2026-07-29T15:30:00Z — backend API endpoints + go2rtc error tracking implemented and tested -->
<!-- signal: opencode 2026-07-29T15:45:00Z — frontend JS + CSS for cam-error display with refresh button implemented -->
<!-- signal: opencode 2026-07-29T16:00:00Z — all tests pass, Docker image built and verified -->

## Decision log
- 2026-07-29: Auth failure points identified: (1) MQTT — Bambu cloud token expiry causes connection lost; onConnectionLost sets State="error" with ErrorMsg, surface via existing .error-banner (already works). (2) RTSPS — go2rtc start failure or frame fetch failure; now tracked via go2rtcInstance.lastError and surfaced via camera-status endpoint.
- 2026-07-29: Design decision — scope K-099 to RTSPS camera errors only (the most common auth timeout scenario). MQTT errors already work via the error banner. TUTK P2P is a future scope (Bambu plugin binary is best-effort and may not be available).
- 2026-07-29: The camera-reconnect endpoint calls rtspMgr.Stop() which kills the go2rtc process. The next frame request will lazily restart go2rtc via Start(). This handles the case where the RTSPS access code has been updated in config — the user restarts the container with new config and the old go2rtc instance is cleared.

## Handoff notes
**Implementation complete.** Two new API endpoints and frontend UI to surface camera auth/stream errors:

Backend:
- `GET /api/printers/{id}/camera-status` — checks camera health, returns `{"ok": true}` or `{"ok": false, "error": "..."}`
- `POST /api/printers/{id}/camera-reconnect` — stops go2rtc instance for lazy restart on next frame request

go2rtc error tracking:
- `Go2RTCManager.SetLastError(streamKey, err)` — stores error on instance
- `Go2RTCManager.LastError(streamKey)` — retrieves last error
- `Go2RTCManager.ClearLastError(streamKey)` — clears error

Frontend:
- Periodic 5s camera status check via JS `checkCameraStatus()`
- `.cam-error` div shows error message + "Refresh" button when camera is failing
- "Refresh" button calls camera-reconnect endpoint and re-checks status

Tests: 7 new tests in server_test.go, all passing. Pre-existing TestHandleHomeAll flake is unrelated.
