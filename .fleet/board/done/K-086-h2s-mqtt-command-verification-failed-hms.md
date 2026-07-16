---
id: K-086
title: H2S showing "MQTT Command verification failed" HMS (HMS_0500-0500-0001-0007)
initiative_id: null
claimed_by: gentle-loris-hazel
claimed_at: 2026-07-16T13:06Z
blocks: null
blocked_by: null
related_cards: [K-004, K-006, K-030, K-073, K-075, K-079]
---

# K-086 — H2S showing "MQTT Command verification failed" HMS (HMS_0500-0500-0001-0007)

## Context

Human-reported (2026-07-16), **highest priority**: the H2S printer is
surfacing HMS code `HMS_0500-0500-0001-0007` in the dashboard, message text:
"MQTT Command verification failed. Please update Studio (including the
network plugin) or Handy to the latest version, then restart the software
and try again."

That message text is written for Bambu Studio/Handy (official apps), not for
this dashboard — but the printer's firmware doesn't know which client it's
talking to, so it fires the same HMS code regardless of source. Two very
different possible root causes to distinguish:

1. **This app sent something the H2S firmware rejected** — a malformed or
   version-mismatched MQTT command from `internal/printers/bambu/client.go`
   or `commands.go` (e.g. missing sequence/field the firmware expects,
   protocol-version skew) actually triggered a real verification failure on
   the printer side. If so, this is a correctness bug in our command-sending
   code and needs a fix there.
2. **The printer/firmware surfaced this HMS independent of us** (e.g. stale
   cached credential, a real Bambu Studio elsewhere sent a bad command, or
   firmware-side glitch) and we're just faithfully displaying an HMS code
   that has nothing to do with this app. If so, this may not be "our" bug at
   all beyond correctly displaying/dismissing it (K-075's dismiss flow may
   already be the right tool once this is understood).

Needs Research first — do not guess at a fix before knowing which case this
is.

## Plan
1. [x] Researcher: check `internal/server` logs / recent MQTT command history
   around when this HMS was reported — was a command (light toggle, temp
   set, dismiss, skip-object, pause/resume) sent to the H2S shortly before
   this HMS fired? Correlate timestamps if any logs/history are available.
   → No command-send audit log exists anywhere on the Bambu path; this
   correlation is not recoverable from current app state.
2. [x] Researcher: read `internal/printers/bambu/commands.go` and
   `client.go`'s command-sending path (`publish`/`sendGCode`/`doCommand`
   etc.) for anything H2S-specific or version-sensitive — H2S is a newer
   Bambu model, check if it needs a different command shape/sequence field
   than P1S/X1 that this app doesn't yet send. Also check `hms_messages.go`
   (vendored HA-Bambu table) to confirm this code's decoded text matches
   what the human reported (rule out a message-table bug).
   → No H2S-specific branching exists anywhere; all Bambu models share the
   same command construction. Message table confirmed correct (exact text
   match, `hms_messages_en.json` key `0500050000010007`).
3. [x] Researcher: check whether this HMS is one-shot/transient or
   persistent (relates to K-004/K-006's known lack of state hysteresis —
   don't want to chase a ghost caused by a different unrelated bug). Cross
   reference K-079 (embedded-error-detection gaps) — if a recent command
   from this app silently "succeeded" per HTTP status while the firmware
   actually rejected it, that's the same class of bug K-079 already
   describes, and this card may be its first concrete real-world instance.
   → HMS display already has anti-flicker hysteresis
   (`hmsHealthyStreakThreshold`), which only delays clearing, never
   fabricates — no phantom-display mechanism found. K-079 turned out to be
   Snapmaker/Moonraker-scoped, not applicable to Bambu's MQTT path; filed
   K-087 instead for the adjacent Bambu-specific gap.
4. [x] Report findings, root cause determined to be firmware-side (see
   Decision log) — no app-side fix warranted, so no Implementer handoff.

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: faint-skunk-cairn 2026-07-16T03:48Z — claiming, highest priority per human, dispatching researcher -->
<!-- signal: gentle-loris-hazel 2026-07-16T13:06Z — reclaiming, prior claim stale (>9h, no follow-up signals, no related commits on faint-skunk-cairn worktree); dispatching researcher -->

## Working context

- HMS code: `HMS_0500-0500-0001-0007`
- Affected printer: H2S
- Message: "MQTT Command verification failed. Please update Studio (including
  the network plugin) or Handy to the latest version, then restart the
  software and try again." — this is Bambu's stock text for this code, aimed
  at their own official clients; not necessarily diagnostic of what's wrong
  here.
- Related open work: K-079 (embedded firmware-error detection gaps across
  most commands), K-004/K-006/K-030 (state hysteresis — rule out before
  concluding this HMS is "real"), K-073 (HMS architecture), K-075 (dismiss
  flow, now shipped — may be the resolution once root cause is triaged).

## Decision log
- 2026-07-16 — orchestrator (faint-skunk-cairn): filed directly in now/ per
  §2 (human-requested, highest priority — their request is the promotion).
  Claiming under orchestrator session identity to dispatch Research
  immediately rather than leaving for queue pickup.
- 2026-07-16 — gentle-loris-hazel: reclaimed stale claim (>9h idle, no
  commits on faint-skunk-cairn worktree), dispatched Research.
- 2026-07-16 — gentle-loris-hazel: Research concluded **hypothesis 2**
  (firmware-side, not app-caused). `HMS_0500-0500-0001-0007` is a
  documented firmware-level MQTT client identity/version gate on newer
  Bambu firmware (X1/H2S-class) — it fires based on the client's declared
  software identity/version, independent of command payload correctness.
  Confirmed via upstream `greghesp/ha-bambulab` issue #1250 (same error,
  labeled external/wontfix for third-party clients) and community reports
  of it firing even for official Studio/Handy on version mismatch. No
  H2S-specific command bug found in `commands.go`/`client.go` — all Bambu
  models share identical command construction, so there's no
  H2S-vs-P1S divergence to fix. Message table decode confirmed correct.
  No phantom/stuck-display mechanism found (hysteresis only delays
  clearing, never fabricates). Closing without an app-side code change:
  the existing K-075 dismiss flow (shipped) is the correct resolution for
  this specific alert. Filed K-087 for two adjacent, unrelated
  code-quality gaps the research surfaced (Bambu ack-verification gap,
  print-command `sequence_id` omission) — neither shown to cause this HMS.

## Handoff notes
Resolved as not-an-app-bug. If this HMS recurs or a human wants deeper
firmware-side investigation (e.g. contacting Bambu support, checking
printer's own auth-token cache), that would be new work — this card's
research scope is exhausted. Use the existing dismiss flow (K-075) to
clear the alert in the dashboard. See K-087 for unrelated follow-up
code-quality items found along the way.
