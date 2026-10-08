---
id: K-107
title: P1S stuck on "print_error=83935249" — latched print_error overrode gcode_state and could never clear
initiative_id: null
claimed_by: silent-fern
claimed_at: 2026-10-08T13:40Z
blocks: null
blocked_by: null
related_cards: [K-004, K-006, K-030, K-072, K-073, K-075, K-079, K-086]
---

# K-107 — P1S stuck on "print_error=83935249" while printing normally and after a successful print

## Context

Human-reported (2026-10-08). The P1S card showed `error` /
`print_error=83935249` **during** an active job and still showed it after the
job completed successfully, while Bambu Handy showed no error at all.
Reproduced on the stable instance (`printer-dashboard.nas.johnsonlab.dev`)
with the identical symptom, so this was a pre-existing `main` defect, not a
regression in a worktree.

Three separate defects stacked on top of one benign printer behaviour:

**The printer behaviour (real, but not a fault).** `83935249 == 0x0500C011`.
The vendored `hms_messages_en.json` already had the text for it under the
`device_error` section: *"SD card performance has degraded, which may affect
printing, logging, and video. Try formatting it. If issues persist, please
replace the SD card."* (model list: `P1P`, `P1S`). It is an advisory about the
SD card, not about the job. Bambu documents it nowhere — not in
`e.bambulab.com/query.php`, and Bambu Studio has an open issue for exactly
this value ([bambulab/BambuStudio#4495](https://github.com/bambulab/BambuStudio/issues/4495)).
Bambu Handy shows nothing because the advisory arrives on the `print_error`
channel, not the `hms` array — and `hms` was `[]`.

Raw cloud-MQTT capture against the live P1S confirmed the printer's own
full-status push asserts the fault and its own health simultaneously:

```
"print_error": 83935249, "gcode_state": "FINISH", "hms": [],
"mc_percent": 100, "layer_num": 24, "total_layer_num": 24, "print_type": "idle"
```

and that between full pushes the P1S emits only:

```
{"print":{"wifi_signal":"-48dBm","command":"push_status","msg":1,"sequence_id":"718"}}
```

— no `print_error`, no `gcode_state`, no `hms`.

**Defect A — severity.** `handleReport` tripped `State="error"` on *any*
non-zero `print_error`, overriding a healthy `gcode_state`. That is why a
`RUNNING` print displayed as `error`.

**Defect B — no un-latch.** Once `State=="error"` came from `print_error`,
nothing could release it: the clear path was gated on `s.State != "error"`,
and the HMS-specific un-latch required `hadHMSErrors`. Since the deltas carry
neither `print_error` nor `gcode_state`, the card was frozen — a restart did
not help either, because the next `pushall` re-asserted the same stale value.

**Defect C — unreadable.** `print_error` rendered as a bare decimal. The
`device_error` table was already vendored but unreachable:
`lookupHMSMessage` only consulted `device_hms` (16-hex keys), never
`device_error` (8-hex keys). Compounding it, the table's model lists use
friendly names (`P1S`) while config declares internal product codes
(`C12`), so even a reachable model-specific entry would miss — and this
particular code has *only* a model-specific entry, its universal slot being
the empty-string placeholder.

## Plan

1. [x] Capture raw cloud MQTT for the P1S and confirm what the printer
     actually sends, versus what the dashboard cached.
2. [x] Decode `83935249` against the vendored table (`device_error/0500C011`).
3. [x] Confirm the same symptom on the stable instance (pre-existing, not a
     worktree regression).
4. [x] Add `lookupDeviceError()` + `deviceErrorKey()` and factor the
     model-preference logic into `pickMessage()` shared with
     `lookupHMSMessage()`.
5. [x] Add `normalizeModel()` + `modelAliases` so internal product codes
     (`C12`→`P1S`, `O1S`→`H2S`, …) resolve against the table's friendly names.
     This also un-blocks model-specific HMS text for the H2S.
6. [x] Add `printErrorCodeString()`/`describePrintError()`: known codes render
     as `"<message> (0500-C011)"`, unknown ones as `print_error=503 (0000-01F7)`.
7. [x] Add vendor-neutral `printers.Advisory` + `PrinterStatus.Advisories`
     (kept out of `HMSEntry` per K-073).
8. [x] Restructure the `handleReport` error decision: `print_error` outranks
     `gcode_state` only when `gcode_state` is present **and** unhealthy; a
     non-zero `print_error` under a healthy state becomes an advisory. An
     absent `gcode_state` deliberately trips nothing (see the comment in
     `handleReport` — that shape is what would re-latch with nothing left to
     un-latch).
9. [x] Render advisories as warning-styled rows with no Dismiss button (the
     printer re-asserts them; a dismiss control would promise what the server
     cannot honour).
10. [x] Update the four tests that encoded the old policy; add regressions
      pinned to the captured live payloads.
11. [x] Build, deploy to `:8080`, verify against the real printer.

## Signals

<!-- signal: silent-fern 2026-10-08T13:05Z — claiming; raw MQTT capture shows print_error absent from live deltas, so this is a latch bug not a live fault -->
<!-- signal: silent-fern 2026-10-08T13:40Z — done, moved to done/ -->

## Decision log

- 2026-10-08: `print_error` is a latched register, not a live fault. Bambu's
  authoritative failure signal is `gcode_state: FAILED`; health events are
  HMS. `print_error` now only owns the error banner when `gcode_state` is
  present and unhealthy, which preserves every real failure path (FAILED, and
  the K-075 cover-off-via-HMS case) while removing the false positive.
- 2026-10-08: chose a new vendor-neutral `Advisory` type over reusing
  `HMSWarnings`. Advisories are not HMS entries, have no severity, and are not
  dismissible; folding them in would also have entangled them with the HMS
  staleness-decay streak and the K-075 dismiss flow. K-073 already argues for
  keeping Bambu vocabulary out of `PrinterStatus`.
- 2026-10-08: advisories are refreshed only when a report actually carries
  `print_error`, matching the existing "only update on presence" convention
  used for `hms`/`ams`/temperatures. Wiping on deltas would flicker the row on
  every heartbeat; an explicit `print_error: 0` is present and is the
  printer's own clear signal.
- 2026-10-08: kept the decimal in the unknown-code fallback (`print_error=503`)
  *and* added the dashed-hex form, because the decimal is what appears in
  captured payloads while the hex is what Bambu's catalog and community
  references use.
- 2026-10-08: `FAILED` with neither `print_error` nor HMS leaves `ErrorMsg`
  untouched rather than clearing it, so a detail-free `FAILED` delta does not
  blank out the explanation an earlier report supplied.
- 2026-10-08: verified live on the real P1S after deploy — `state=complete`,
  `error_msg` gone, one advisory carrying the SD-card text, and stable across
  45s+ of `wifi_signal`-only deltas.

## Handoff notes

The SD-card advisory itself is a genuine hardware condition on P1S-001 and
will keep showing until the card is formatted or replaced — that is the
advisory working as intended, not a leftover bug. If the user wants it
silenceable, the Bambu-side mechanism is a `clean_print_error` command on the
request topic (bambuddy documents using it for exactly this); that would be a
new card, and it needs care because it clears the printer's whole latched
error register, not one entry.

`modelAliases` is deliberately non-exhaustive, mirroring `hmsModules`. Codes
not listed still resolve to a universal message when one exists; they just
cannot win a model-specific variant. Add to it when a new printer model is
onboarded and its messages read as generic.
