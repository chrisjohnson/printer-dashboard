# Per-printer transport selection: LAN-first MQTT with cloud fallback

## Problem

The Bambu driver is cloud-only by design (`client.go`'s header comment: "Default
mode: cloud MQTT via Bambu's infrastructure"). Every other part of the system
already talks to the printers locally — cameras use `rtsps://:322` / `bambus://:6000`
with the device access code — but telemetry and commands are forced through
Bambu's cloud broker.

The consequence was demonstrated live: an expired Bambu cloud token left two
perfectly reachable printers blind for eight days. Both accepted LAN MQTT with
`bblp`/access_code and streamed full reports the entire time.

## Why not classify by firmware or model

The obvious design is a table: "P1S ≤ 1.07 → LAN available", "H2S → cloud", and so
on. Reject it, for reasons this repo's own protocol reference already documents:

- **Toggle names differ across firmware generations** — "LAN Only", "Developer
  Mode", "LAN Mode" — "and the sub-toggles differ too. Treat the *behavior* … as
  the reliable part and the exact menu wording as firmware-specific."
- **An open port is not evidence.** "On a fleet configured for Path A/B the local
  MQTT port is open on every unit but refuses third-party clients; authentication
  behavior, not port reachability, is the signal."
- **The axes are independent.** Reachability is set by the printer's toggles;
  whether `print` commands need signing is set by firmware *date* (~Jan 2025),
  "regardless of whether you are on Path A, B, or C". A model→capability table
  conflates two things that do not move together.
- **It rots.** Every new firmware generation needs a PR.

The codebase already solved this exact problem once, for signing: send unsigned,
"let the rejection teach us", latch `signRequired`, retry signed
(`publishCommand`). No firmware table involved. **Generalize that pattern.**

## Design

### Probe behaviour, cache the verdict, re-probe on change

Per printer, rank the candidate MQTT transports and use the first that
*demonstrably works* — connects, and produces a report. Record which one is
live. On connection loss, demote the failed candidate and walk the list again.

### One MQTT transport per printer, camera independent

Telemetry and commands share a single MQTT session (same broker, same
`device/<serial>/report` + `/request` topic pair; the protocol reference
confirms "Command behavior over LAN is **identical** to cloud"). Splitting them
across two brokers would mean two live sessions, two ack streams into one
`ackWaiters` map keyed only by command string, and two sources racing to write
one status — split-brain for no benefit.

The camera is already selected independently of MQTT (LAN stream, or the
cloud-reported `ipcam_url` when present), which is where genuine per-channel
mixing is actually needed. That stays as-is.

So the granularity the fleet needs is **per printer**, not per channel — and a
mixed fleet (some printers on LAN, some on cloud) falls out for free.

### Candidate ranking (`auto`)

| rank | candidate | requires |
|---|---|---|
| 1 | LAN (`ssl://<host>:8883`, `bblp` / access code) | `host` + `access_code` in config |
| 2 | Cloud (`ssl://<region>.mqtt.bambulab.com:8883`, `u_<uid>` / token) | authenticated cloud client |

LAN first: lower latency, keeps working when the WAN is down, and never depends
on a credential that expires. Cloud is the remote-access path and the fallback.

Override per printer with `mqtt_transport: auto | lan | cloud` for anyone who
wants to pin it.

### Auth semantics change (important)

With LAN available, a dead cloud token is **no longer blindness**. It becomes a
degradation: the printer keeps streaming, and what is lost is cloud-only
capability (remote access, and the cloud-reported camera URL). So:

- `AuthInvalid` is only set when the *active* transport depends on the rejected
  credential — i.e. when we are actually running on cloud, or when LAN was
  unavailable.
- When running on LAN with a dead cloud token, surface a distinct, lower-key
  notice ("cloud sign-in expired — running locally; remote access unavailable")
  rather than the red "cannot see this printer" banner.

This is a correction to the expired-sign-in work in PR #40, which assumed cloud
was the only path.

### Observability

Expose the live transport on `PrinterStatus` (`transport: "lan" | "cloud"`) and
show it on the card. In a mixed fleet this is the first thing you need to know,
and it is also how a bad probe verdict becomes visible instead of mysterious.

## Verification performed for this spec

Against the live fleet (both printers, `bblp` + configured access code):

| printer | LAN MQTT connect | reports | parsed via existing `parseReport` |
|---|---|---|---|
| H2S-001 (Bambu Connect) | OK | 6, largest 19 357 B | `gcode_state=IDLE`, 3 HMS, 3 lights → `idle` |
| P1S-001 (pre-Bambu-Connect) | OK | 5, largest 4 122 B | `gcode_state=FAILED`, `print_error=0x0500C011` → `error` |

Both match what the cloud path reported for the same printers, and **no parser
change was required** — the LAN payload schema is the schema the driver already
handles. `installAppCert(client)` and `publishCommand` already take the
`mqtt.Client` as a parameter, so command signing is transport-agnostic today.

## Implementation

1. `internal/printers/bambu/transport.go` — candidate ranking, option builders,
   probe-and-verify, verdict cache.
2. `client.go` — `Connect()` walks candidates instead of hard-coding cloud;
   tracks `activeTransport`; demotes on loss; auth watchdog scoped to the cloud
   path.
3. `interface.go` — `Transport string`.
4. `config.go` — `mqtt_transport` on `PrinterDef`.
5. UI — transport badge on the card; LAN-aware auth notice wording.

## Non-goals

- Simultaneous dual-broker MQTT sessions.
- Firmware/model capability tables.
- Changing the camera path (already LAN-capable and independently chosen).

## Post-implementation verification (live fleet)

Both Bambu printers came up on local MQTT immediately, without a cloud
connection, with sub-second data age:

| printer | transport | state | data age |
|---|---|---|---|
| P1S-001 | `lan` | error (a real FAILED print) | 3.6 s |
| H2S-001 | `lan` | idle | 0.1 s |

With the cloud token deliberately replaced by garbage in an isolated config
copy, the printers **stayed live** and reported `cloud_degraded: true` /
`auth_invalid: false` / `stale: false` — the scenario that previously cost eight
days of blindness now degrades to a blue notice with a re-auth link.

A `system`-namespace command (chamber light off, then restored) round-tripped
over LAN and was confirmed by the printer's own report.

### Two bugs this verification caught

1. **`cloudDegraded` was being cleared by live LAN reports.** A report arriving
   proves the transport works; it says nothing about the account token. Clearing
   on report made the notice blink off every push, and one printer showed
   `cloud_degraded: false` while the other showed `true` — i.e. the expired
   sign-in would have gone unreported again. Only the watchdog, which actually
   asks Bambu, may clear it.
2. **Command signing silently died with the token.** `ensureSigner()` took the
   user ID from `cloud.UserID()`, which is only populated by a *successful*
   cloud login. A dead token therefore produced no signer, so every
   `print`-namespace command (pause, cancel, movement) would be rejected by
   post-January-2025 firmware — on precisely the printers that were still
   reachable over LAN. Fixed by seeding the user ID from `bambu_account.user_id`
   in config, which is already required for token-based accounts.

### Not yet exercised

Signed `print`-namespace commands over LAN. The light toggle lives in the
`system` namespace, which the protocol reference states is never signature-gated,
so proving it needs a movement command on a homed, idle printer. The evidence it
will work is indirect but strong: `app_cert_install` is now sent on every LAN
session (confirmed in logs), `installAppCert`/`publishCommand` take the
`mqtt.Client` as a parameter, and the protocol reference states command
behaviour over LAN is identical to cloud including the signature gate.
