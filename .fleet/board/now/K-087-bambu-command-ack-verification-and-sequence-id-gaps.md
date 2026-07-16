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
1. [x] Researcher: `sequence_id` omission on print-namespace commands is
   intentional/inconsequential, not a bug — no code change. See Decision
   log for evidence.
2. [ ] Implementer: add a lightweight audit log (command name + printer ID
   + timestamp, no payload/secrets) in `publishCommand`
   (`client.go:446-458`) and/or its callers.
3. [x] Researcher: confirmed firmware-ack verification is NOT feasible —
   client subscribes to only `device/%s/report`, publishes to only
   `device/%s/request`, no separate ack topic, no correlation ID in the
   parsed report struct at all. Audit log (item 2) is the full scope; no
   further "verification" work to do.
4. [ ] Implementer: run full test suite, commit, push, open PR.

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
- 2026-07-16 — gentle-loris-hazel: Research resolved both open questions.
  **sequence_id**: present-since-inception unused field on `printCommand`
  (commit `5f2b801`, comment "Optional sequence ID for operations like
  skip") — never populated, never removed, in any commit. `commands_test.go`
  `TestCommand_BasicCommands` asserts exact JSON output for pause/resume/
  stop/skip_object with no `sequence_id` key — its absence is the tested,
  intended behavior. The one real prior bug (`111da6a`, light control
  missing required fields) was only ever found on the system/light path,
  never print-namespace. No in-repo protocol doc confirms print-namespace
  commands require it either way — genuinely undeterminable with 100%
  certainty, but weight of evidence favors "intentional." Leaving as-is;
  too risky to add unverified fields to a working command path without
  firmware ground truth.
  **Ack verification**: confirmed infeasible — `client.go:212` subscribes
  to only `device/%s/report`, `client.go:452` publishes to only
  `device/%s/request`; no separate ack/response topic exists in this
  implementation, and the parsed report struct (parser.go:12-44) has zero
  fields resembling a command-correlation ID. True firmware-ack
  verification would require protocol reverse-engineering beyond this
  card's scope. Scope narrows to just the audit log (Plan item 2).

## Handoff notes
Research complete, scope narrowed to just adding the audit log (Plan item
2) — sequence_id needs no change, true ack verification is infeasible.
Dispatching Implementer next for the audit log only.
