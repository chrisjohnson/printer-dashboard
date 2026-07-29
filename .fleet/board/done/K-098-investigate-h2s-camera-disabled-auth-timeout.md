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
The H2S camera is reportedly disabled on the printer dashboard. Suspected cause is an MQTT auth token timeout (AMS/TUTK P2P credentials expiring). Investigated whether the root cause is an auth timeout and, per user request, if it's NOT, spun the auth timeout UI into K-099.

## Plan
1. [x] Inspect H2S camera code paths — camera proxy uses go2rtc for RTSPS→MJPEG transcoding (proxy.go, go2rtc.go)
2. [x] Identify root cause — NOT auth timeout. H2S camera WORKS in Docker (go2rtc 1.9.14 installed, RTSPS connects, MJPEG stream returns 200). Initial 502 was from running a local Go binary without go2rtc in PATH.
3. [x] Create K-099 for auth timeout UI (separate ticket per user request)
4. [x] This card is done — no fix needed, camera works in Docker

## Signals
<!-- signal: opencode 2026-07-29T14:30:00Z — created card, starting investigation -->
<!-- signal: opencode 2026-07-29T14:45:00Z — initial finding: go2rtc not in PATH for local binary. Testing Docker container. -->
<!-- signal: opencode 2026-07-29T15:00:00Z — H2S camera works in Docker: go2rtc 1.9.14 started, RTSPS connected, proxy returns 200, frame returns 200. Not an auth timeout. K-099 created for auth UI. -->

## Decision log
- 2026-07-29: Investigated H2S camera code paths. CameraStreams() in bambu/client.go returns `rtsps://bblp:ACCESS_CODE@HOST:322/streaming/live/1` for H2S. Proxy handler (proxy.go:67) uses go2rtc for RTSPS→MJPEG transcoding.
- 2026-07-29: Initial local binary test showed 502 — go2rtc not in PATH (`exec: "go2rtc": executable file not found`). However, Docker image includes go2rtc at `/usr/local/bin/go2rtc` (Dockerfile Stage 4, line 66).
- 2026-07-29: Tested in Docker container. go2trc 1.9.14 started successfully. RTSPS stream proxy endpoint returned HTTP 200. Frame endpoint returned HTTP 200. **H2S camera works correctly in Docker.**
- 2026-07-29: Conclusion: NOT an auth timeout. Camera is functional. The 502 only occurs when running a local Go binary without go2rtc installed. No code fix needed for K-098.
- 2026-07-29: Per user request, created K-099 for auth timeout UI — surfacing auth expiration with clickable refresh.

## Handoff notes
**K-098 (this card): DONE** — H2S camera works in Docker. No fix needed. The only failure case is running a local binary without go2rtc in PATH, which is expected (go2rtc is a Docker-stage dependency).
**K-099 (new card):** Auth timeout UI — surface auth expiration errors with clickable refresh action. This is a proactive feature: even though H2S camera works, auth tokens (MQTT cloud, TUTK P2P, RTSPS access code) can expire and should be surfaced in the UI with a refresh action.
