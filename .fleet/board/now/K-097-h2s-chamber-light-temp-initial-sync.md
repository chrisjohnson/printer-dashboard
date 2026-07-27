---
id: K-097
title: H2S chamber light/temp data not synced initially after restart
initiative_id: null
claimed_by: null
claimed_at: null
blocks: null
blocked_by: null
related_cards: [K-005]
---

# K-097 — H2S chamber light/temp data not synced initially after restart

## Context

After turning machines on fresh (powered off over the weekend), the dashboard shows:
1. All chamber light toggles show as "off" — while physical lights are on (e.g., H2S light is on but dashboard shows off)
2. H2S temp data shows "?" while U1 correctly loaded current real data

## Research findings

### Chamber light ("all off initially")
- `LightOn` starts as `nil` in bambu.New() (never initialized)
- The frontend renders `light_on === true ? 'checked' : ''` — so `nil`/`null` → unchecked/"off"
- The MQTT `lights_report` array containing `chamber_light` may not be present in the initial `pushall` response
- Light state only updates when a report arrives with `lights_report` containing a `chamber_light` entry
- For Snapmaker: `lightOn` is only set by `SetLight()` calls — Moonraker cannot report real LED state

### Temperature "?" (H2S)
- `ChamberTemp` starts as `nil` (*float64 zero value)
- Temperature is only updated when `chamber_temper` or `info.temp` is present in a report
- Heartbeat-style reports may omit temperature fields — Bambu driver deliberately preserves last-known value
- U1 gets initial temperature immediately via `GET /api/printer` during Connect(); H2S depends on cloud MQTT pushall response timing

## Plan
1. [ ] Researcher: Investigate Bambu MQTT pushall response timing and lights_report presence
2. [ ] Implementer: Add initial state fetch/polling when printer connects
3. [ ] Implementer: Show "loading" state instead of "off" for chamber light when `LightOn == nil`
4. [ ] Implementer: Ensure temperature data refreshes on reconnect

## Signals

## Decision log

## Handoff notes
