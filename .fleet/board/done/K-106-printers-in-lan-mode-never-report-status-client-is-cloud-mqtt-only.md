---
id: K-106
title: Printers in LAN Mode never report status — client is cloud-MQTT only
initiative_id: null
claimed_by: pi
claimed_at: 2026-07-31T00:00Z
blocks: null
blocked_by: null
related_cards: []
---

# K-106 — Printers in LAN Mode never report status — client is cloud-MQTT only

## Context
GitHub issue #22 (https://github.com/chrisjohnson/printer-dashboard/issues/22)

A printer running in **LAN Mode** never reports status. Shows `online: false` with all telemetry null. The client only connects to Bambu's **cloud** MQTT broker; a printer in LAN Mode stops publishing to the cloud by design.

### Step 1: Surface the silence ✅ IMPLEMENTED
If a printer has been subscribed on cloud MQTT for 60 seconds with zero reports, log a warning naming LAN Mode as the likely cause.

### Step 2-5: Full LAN MQTT support (deferred)
Allow per-printer `lan_mode: true`, connect to `ssl://<host>:8883` with `bblp`/access code, reuse existing parser.

## Plan
1. [x] Surface the silence: detect 60s of zero reports on cloud MQTT and warn in logs
2. [ ] Extend config to support per-printer `lan_mode`
3. [ ] Implement local MQTT connection path
4. [ ] Reuse existing parser for local broker payload
5. [ ] Add tests/manual verification

## Decision log
2026-07-31 — Implemented step 1 only (silence detection via 60s timeout warning). Full LAN MQTT mode deferred to separate work.

## Handoff notes
