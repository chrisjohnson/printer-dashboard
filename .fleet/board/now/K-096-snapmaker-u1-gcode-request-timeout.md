---
id: K-096
title: Snapmaker U1 G-code request timeout
initiative_id: null
claimed_by: null
claimed_at: null
blocks: null
blocked_by: null
related_cards: [K-005]
---

# K-096 — Snapmaker U1 G-code request timeout

## Context

When homing a Snapmaker U1, the user got: `gcode request failed: Post "http://192.168.1.10:80/printer/gcode/script": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`

The Snapmaker driver HTTP client has a 10-second timeout (snapmaker.go:103) which applies to all REST calls including `POST /printer/gcode/script`. Moonraker's gcode.script endpoint is designed to queue G-code for Klipper to execute asynchronously, but if the Moonraker process hangs (e.g., Klippy not connected), the HTTP request may sit unresolved until the 10s client timeout fires.

There is no retry logic — a single 10-second timeout causes the entire operation to fail with an error returned to the frontend.

## Research findings
- `sendGCode()` in snapmaker.go:678-711 makes a single synchronous HTTP POST with 10s timeout
- No polling, no retries, no context-level timeout added at the handler layer
- Moonraker's `printer.gcode.script` queues G-code for Klipper; HTTP 200 means "accepted" not "done"
- If Klippy is not connected to Moonraker, the endpoint may hang

## Plan
1. [ ] Researcher: Investigate Klipper/Moonraker G-code execution flow
2. [ ] Implementer: Add configurable timeout (longer default, e.g. 30s) for G-code/script endpoint
3. [ ] Implementer: Add retry logic for transient timeout failures
4. [ ] Implementer: Consider async queue + status polling for G-code completion

## Signals

## Decision log

## Handoff notes
