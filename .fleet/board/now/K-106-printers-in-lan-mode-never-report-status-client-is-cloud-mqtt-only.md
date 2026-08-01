---
id: K-106
# Filename pattern: {ID}-{slugified-title}.md
title: Printers in LAN Mode never report status — client is cloud-MQTT only
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: null                 # pet name of the agent session working this card, e.g. otter
claimed_at: null                 # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                 # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-106 — Printers in LAN Mode never report status — client is cloud-MQTT only

## Context
<!-- why this card exists: root cause, links to runbooks/PRs/related cards -->

GitHub issue #22 (https://github.com/chrisjohnson/printer-dashboard/issues/22)

A printer running in **LAN Mode** never reports status. It shows `online: false` with all telemetry null. The cause: the client only ever connects to Bambu's **cloud** MQTT broker, and a printer in LAN Mode stops publishing to the cloud by design — so the subscription is to a topic that will never receive anything.

### Root cause

`Connect()` in `internal/printers/bambu/client.go:226` hardcodes the cloud broker:
```go
broker := "ssl://" + MQTTBroker(c.cloud.region)
```

`PrinterDef.Host` is documented as camera-only (`internal/config/config.go:45`), and `client.go:61` states the design intent: "via Bambu's cloud MQTT infrastructure. No LAN mode or developer mode required."

### Local broker works and has everything

Connecting to the printer's own broker on 8883 over TLS with username `bblp` and the printer's access code returns ACCEPTED. Subscribing to `device/<SERIAL>/report` and publishing the same `pushall` payload the client already uses at `client.go:354` produces a full state report — same schema the existing parser consumes, including AMS data, layer counts, temperatures.

The printer's TLS cert is self-signed, so verification must be skipped for local connections.

### Suggested direction

Allow a per-printer LAN MQTT mode — e.g. `lan_mode: true` or inferring it when `host` + `access_code` are set and cloud reports don't arrive within some window. Connect to `ssl://<host>:8883` with `bblp` / access code and `InsecureSkipVerify`.

A smaller improvement regardless: **surface the silence**. If a printer has been subscribed on cloud MQTT for N seconds with zero reports, log a warning naming LAN Mode as the likely cause, and/or reflect it in the dashboard.

## Plan
<!-- ordered checklist -->
1. [x] (Short-term) Surface the silence: detect N seconds of zero reports on cloud MQTT and warn in logs + dashboard
2. [ ] Extend config to support per-printer `lan_mode` or local broker discovery
3. [ ] Implement local MQTT connection path (ssl://<host>:8883, bblp/access code, InsecureSkipVerify)
4. [ ] Reuse existing parser for the local broker payload (already handles it)
5. [ ] Add tests or manual verification against LAN Mode hardware

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
     Examples:
     <!-- signal: otter 2025-07-15T14:30Z — claiming, will work on API layer -->
     <!-- signal: otter 2025-07-15T15:10Z — blocked on K-003, need schema first -->
     <!-- signal: otter 2025-07-15T15:45Z — done, moved to done/ -->
-->
<!-- signal: pi 2026-07-31T00:00Z — implementing silence detection -->
<!-- signal: pi 2026-07-31T00:00Z — done, moved to done/ -->

## Decision log
<!-- append-only, one line per entry, newest last. Never move this card to done/
     without a line here explaining why. -->

## Handoff notes
<!-- what's half-done, what the next agent picking this up should do first. -->
