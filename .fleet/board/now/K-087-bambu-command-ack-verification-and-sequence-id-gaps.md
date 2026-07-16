---
id: K-087
title: Bambu command-send path lacks firmware-ack verification and log; sequence_id inconsistently set
initiative_id: null
claimed_by: gentle-loris-hazel
claimed_at: 2026-07-16T14:21Z
blocks: null
blocked_by: null
related_cards: [K-079, K-086]
---

# K-087 — Bambu command-send path lacks firmware-ack verification and log; sequence_id inconsistently set

## Context

Discovered as a side-finding while researching K-086 (H2S "MQTT Command
verification failed" HMS) — not the cause of that HMS (see K-086 decision
log), but two real, separate gaps in `internal/printers/bambu/client.go`
and `commands.go` worth tracking:

1. **No firmware-ack verification or audit log on the command-send path.**
   `publishCommand` (`client.go:446-458`) and all its callers (`Pause`,
   `Resume`, `Cancel`, `SkipObject`, `SetBedTemp`, `SetNozzleTemp`,
   `SetChamberTemp`, `SetLight` at `client.go:461-500`) only check
   MQTT-transport-level `token.Error()` — there's no visibility into
   whether the printer firmware actually accepted the command content, and
   zero `log.Printf` calls anywhere on the send path (only connect/
   reconnect/report-parse are logged). This is the same class of blind
   spot K-079 tracks for Snapmaker's Moonraker call sites, but K-079 turned
   out to be Snapmaker-specific in scope — this card is the Bambu/MQTT
   equivalent. There's prior precedent for firmware silently rejecting
   under-specified commands: commit `111da6a` ("Fix Bambu light control...")
   found the light-toggle command was missing required fields and firmware
   silently no-op'd it, with no error surfaced anywhere.
2. **`sequence_id` inconsistently set.** `printCommand`
   (`commands.go:13-20`) never sets `SequenceID` for any print-namespace
   command (pause/resume/stop/skip/set_ctt/gcode_line) — only
   `systemCommand` (the light-control payload) sets `SequenceID: "0"`
   (`commands.go:99`). Unexplained asymmetry; no evidence yet that it
   causes user-visible harm, but worth a look.

Neither issue was shown to cause K-086's HMS — that was root-caused to a
firmware-side client identity/version gate, unrelated to payload content.

## Plan
1. [ ] Researcher/Implementer: decide whether `sequence_id` omission on
   print-namespace commands is intentional (protocol may not require it
   there) or a latent bug — check Bambu's MQTT protocol docs/vendored
   references, and whether omission has ever correlated with a dropped
   command.
2. [ ] Implementer: add a lightweight audit log (command name + printer ID
   + timestamp, no payload/secrets) on the Bambu command-send path, so
   future HMS/behavior reports can be timestamp-correlated against actual
   commands sent — mirrors what's missing for K-086-style investigations.
3. [ ] Implementer: consider whether firmware-level ack verification is
   feasible for Bambu's MQTT protocol (may not be — command acks may not
   exist in the report stream) before committing to a specific approach.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: gentle-loris-hazel 2026-07-16T13:06Z — filed from K-086 research side-findings, not started -->
<!-- signal: gentle-loris-hazel 2026-07-16T14:21Z — claiming, dispatching researcher for sequence_id investigation + ack-verification feasibility -->

## Working context

- Relevant files: `internal/printers/bambu/client.go` (command-send path,
  `publishCommand` ~L446-458, callers L461-500), `internal/printers/bambu/commands.go`
  (`printCommand` L13-20 vs `systemCommand` L99).
- K-079 is Snapmaker/Moonraker-scoped (doCommand/SkipObject/fetchStatus/
  fetchQueryStatus) — do not conflate; this card is the Bambu analog.

## Decision log
- 2026-07-16 — gentle-loris-hazel: filed to backlog per §2 (self-discovered
  follow-up, not started) from K-086 research side-findings.

## Handoff notes
Not started. Two independent sub-issues bundled here since they're both
small, adjacent, and found in the same investigation — may split into
separate cards if scope grows once someone picks this up.
