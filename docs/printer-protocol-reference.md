# Printer Protocol Reference

Reverse-engineered reference for interacting with and managing 3D printers over the network:
Bambu Lab machines (cloud and local control) and the Snapmaker U1 (local control).

This document describes **printer behavior only** — endpoints, transports, credentials, payloads,
and the observable consequences of each printer-side setting. It is written so a reader can locate
their own printer and configuration and follow only the parts that apply.

---

## 0. How to use this document

1. Identify your printer family: [Bambu Lab](#2-bambu-lab) or [Snapmaker U1](#9-snapmaker-u1).
2. Determine your **access path** — the single most important determinant of what will and will not
   work. See [§1](#1-choosing-your-access-path).
3. Read the authentication section for that path, then the transport, then the commands you need.

Two rules account for most integration failures:

- **An absent field is not a zero value.** Status messages are partial. A field that is missing from a
  given message means "unchanged / not reported", never `0`, `""`, or `idle`.
- **A successful send is not a successful command.** Transports report that the message was delivered,
  not that the printer accepted it. Verification requires reading the resulting state back.

---

## 1. Choosing your access path

Printer behavior depends more on **which network features are enabled** than on the model. Determine
your configuration first.

### 1.1 Bambu Lab access paths

| Path | Printer-side setting | Cloud control | Local control | Local camera | Signing required for `print` commands |
|---|---|---|---|---|---|
| **A. Cloud only** | Nothing enabled (factory default) | Yes | No | No | Depends on firmware date |
| **B. Cloud + LAN camera** | "LAN Only Liveview" (or equivalent liveview toggle) enabled, Developer Mode **off** | **Yes, still works** | No | **Yes** | Depends on firmware date |
| **C. Local only** | Developer Mode / LAN mode **on** | **Stops** | Yes | Yes | Envelope reported optional |

The three rows are genuinely different products from an integrator's point of view.

**Path B is the important subtlety.** The liveview toggle opens the local camera stream *without*
disabling cloud control. A printer can simultaneously be cloud-controlled over MQTT and be streaming
video over the local network. These are independent settings and are frequently confused.

**Path C has a hard consequence:** putting the printer into LAN mode causes it to **stop publishing to
the cloud entirely**. This is correct behavior, not a fault. A cloud-based client sees the device go
offline with no error and no explanation. If a device silently stops reporting, check this setting
before debugging anything else.

> **Naming caveat.** The local-mode toggle is labelled differently across firmware generations
> ("LAN Only", "Developer Mode", "LAN Mode"), and the sub-toggles differ too. Treat the *behavior* —
> cloud detaches, local broker opens — as the reliable part and the exact menu wording as
> firmware-specific.

> **Measured confirmation of the difference.** On a fleet configured for Path A/B, the local MQTT port
> is *open* on both printers but refuses third-party clients, while the camera port is open only on the
> unit with liveview enabled. An open port is not evidence that Developer Mode is on; authentication
> behavior is.

### 1.2 Firmware date determines command signing (independent of path)

Bambu firmware released after roughly **January 2025** cryptographically verifies every command in the
`print` MQTT namespace.

- Machines whose firmware **predates** the gate accept unsigned commands.
- Firmware **after** that date requires a signed envelope — regardless of whether you are on Path A, B,
  or C.
- The gate applies to the `print` namespace only. `system` (lights) and `pushing` (status requests)
  are never verified. This is why a light toggle can work while every movement command fails.
- **Developer Mode does not remove the requirement in practice.** Upstream documentation states the
  envelope becomes optional in that mode; this has not been independently confirmed, and signing works
  in every mode, so it is the correct solution regardless.

See [§5](#5-command-signing).

### 1.3 Snapmaker U1 access path

The U1 is local-only. It runs Klipper with the Moonraker API server on **port 80**, with no cloud
dependency. An access code may optionally be configured. See [§9](#9-snapmaker-u1).

A second listener exists on **port 8883** that requires a **client certificate** (mutual TLS). It is
not the Moonraker API and is not required for normal control.

### 1.4 Quick port reference

| Printer / state | 80 | 322 | 3000 | 6000 | 8883 |
|---|---|---|---|---|---|
| H2-class, liveview enabled | closed | **open** (RTSPS) | open | open | open, refuses third-party clients |
| H2-class, liveview off | closed | **closed** | open | open | open, refuses third-party clients |
| P1-class | closed | closed | closed | **single-session — see below** | open, refuses third-party clients |
| Snapmaker U1 | **open** (Moonraker HTTP) | closed | closed | closed | open, **requires client certificate** |

**The port list is not a reliable indicator of what will work.** An open local MQTT port does not mean
third-party clients are accepted — authentication behavior is the real signal.

**Port 6000 is single-session, so a scan cannot tell you whether it is available.** The camera service
accepts at most one client. While a session is live, additional connection attempts are **silently
dropped — no SYN-ACK and no RST** — which scanners and `connect()` probes report as "closed",
indistinguishable from a disabled listener. Measured on a P1-class unit with all LAN toggles off: an
established session streaming ~58 KB/s (~1 FPS) while fresh SYNs to 6000 timed out, while 80/322/3000
returned RST and 8883 completed. An **idle** Bambu camera port does answer SYNs.

Therefore "6000 is closed" is **not** evidence the camera is unavailable, and "6000 is open" is not
evidence it is free. Confirm by completing the TLS handshake, sending the auth packet and reading a
frame — and verify no other client (including a second dashboard instance) already holds the session.
A dropped SYN and a closed port surface identically as a connect timeout, which is why this is easy to
misdiagnose.

---

## 2. Bambu Lab

### 2.1 Authentication flows

Bambu has **three distinct authentication contexts**. They use different credentials and are easy to
conflate.

#### Flow 1 — Cloud account (controls everything cloud-side)

```
POST https://api.bambulab.com/v1/user-service/user/login
     { "account": "<email>", "password": "<pw>", "apiError": "" }
  → { "accessToken": "...", "success": true }
```

- Base URL is `https://api.bambulab.com` (global) or `https://api.bambulab.cn` (China).
- Headers: `Content-Type: application/json` and `Authorization: Bearer <accessToken>`. No client
  impersonation headers are required for any account endpoint.
- **Two-factor accounts:** the first response indicates a code is required. Send
  `POST /v1/user-service/user/sendemail/code` with `{"email": ..., "type": "codeLogin"}`, then
  re-`POST /user/login` with `{"account": <email>, "code": "<code>"}`.
  **The 2FA response may omit the `success` field** — treat any non-empty `accessToken` as success,
  or login will fail spuriously.
- **Single-sign-on accounts** (Google/Apple) generally cannot use password login. The fallback is a
  browser session token: after signing in on the web, read the `eyJ…` JWT from `localStorage` and use
  that as the access token. This is reported behavior rather than something verified end to end.

**Two token species share one field.** The value returned as `accessToken` by the login service is an
opaque string (not a JWT, roughly 144 characters). A browser-derived SSO token is a JWT. Both are
valid in the same slot and both work as the MQTT password. Code must not assume either shape.

**Token lifetime is usually not reported.** A token file may carry an expiry of `-62135596800`, which
is the Unix timestamp of Go's zero `time.Time`. Loaders that ignore non-positive expiry values will
treat the token as valid forever and never refresh proactively. Practical consequence: **design for
the token dying silently**, and validate it by making a device-list call at startup rather than
trusting the stored expiry.

#### Flow 2 — Device identity and the access code

```
GET https://api.bambulab.com/v1/iot-service/api/user/bind
  → { "devices": [ { "dev_id", "name", "online", "print_status",
                     "dev_model_name", "dev_product_name", "dev_access_code" } ] }
```

- `dev_id` **is** the serial number and is the value used in MQTT topic names.
- Derive local identifiers from the **full** serial. Truncating it causes distinct devices to
  silently overwrite one another.
- The **access code** is the device credential used for all local access (MQTT, RTSPS, camera). It is
  displayed on the printer's LCD under Settings → Network, and is also returned by this endpoint as
  `dev_access_code`.

#### Flow 3 — MQTT credentials

Derived entirely from Flow 1:

| Field | Value |
|---|---|
| Username | `u_<user_id>` — the numeric uid, obtained from `GET /v1/design-user-service/my/preference` |
| Password | the `accessToken` from Flow 1, verbatim |
| Broker | `ssl://us.mqtt.bambulab.com:8883` (global) / `ssl://cn.mqtt.bambulab.com:8883` (China) |

The `/preference` endpoint supplies the uid needed to build the MQTT username, so it doubles as an
identity check. In practice a **device-list call is the more effective validity probe** — it fails
loudly on a dead token and can be used to discard a bad cached token at startup.

#### Flow 4 — Command signing credentials

Separate from user authentication. The printer must be told to trust a certificate before it will
accept signed commands. See [§5.3](#53-the-printer-trust-store).

### 2.2 Cloud MQTT transport

| Parameter | Value |
|---|---|
| Transport | MQTT over TLS, port 8883 |
| TLS verification | frequently disabled in clients, on the grounds that the cloud certificate is not reliably present in minimal trust stores. This is a widely repeated rationale rather than a measured requirement. |
| Protocol level | MQTT 3.1.1. MQTT 5 is refused. |
| Client ID | any unique string; a new one per process start |
| Clean session | `true` — no retained state across restarts |
| Keepalive | 30 s |
| Subscribe | `device/<serial>/report`, QoS 0 |
| Publish | `device/<serial>/request`, QoS 0, retain `false` |

**There is no separate ack topic, but acknowledgements do exist.** The firmware replies with
`result` / `reason` / `err_code` **on the `report` topic**, interleaved with status messages. Two
consequences:

1. Acks must be demultiplexed from status before parsing.
2. An ack must never be fed into status parsing. An ack carries no `home_flag`, which a naive parser
   reads as zero — silently reporting a homed printer as unhomed.

Register the ack waiter **before** publishing. Keep ack timeouts non-fatal, because some firmware
never acks certain commands and should not be blamed for it.

### 2.3 Requesting a full status snapshot

```json
{"pushing":{"command":"pushall","version":1,"push_target":1}}
```

Published to `device/<serial>/request`. Returns a complete report rather than a delta.

Send it on connect. A **periodic** re-request is also necessary — some models, notably the H2S class,
publish almost nothing to the cloud unprompted, so state can go permanently stale after connecting.
Gate the re-request on a genuinely quiet period rather than a fixed timer: upstream guidance warns that
frequent `pushall` requests can disturb P1P-class hardware.

### 2.4 Local (LAN) MQTT

Available only with Developer Mode enabled.

| Parameter | Value |
|---|---|
| Broker | `ssl://<printer_ip>:8883` |
| Username | the literal string `bblp` |
| Password | the device access code |
| Command topic | `device/<serial>/request` — **identical to cloud** |
| Report topic | `device/<serial>/report` |
| Payloads | plaintext JSON in both directions; no additional encryption layer |
| Report rate | approximately 1 per second, sparse |
| Auth | enforced — a wrong access code yields `Connection refused: Not authorized` |

`device/<serial>/mqtt` is accepted on connect but **silently ignored** as a command topic; it is a
publish-side topic on a different API family. Using it produces no error and no effect.

Command behavior over LAN is **identical** to cloud, including the signature gate. The cloud broker is
a forwarder, not a different command language. Consequently, no LAN transport is needed in order to
use signed commands.

---

## 3. Command reference (Bambu)

All commands are published to `device/<serial>/request`.

> On firmware after the January 2025 gate, every `print`-namespace command below must be wrapped in
> the signing envelope from [§5](#5-command-signing). The payloads shown are the **inner** payload.
> `system` and `pushing` commands are never verified.

### 3.1 Job control

| Action | Payload |
|---|---|
| Pause | `{"print":{"command":"pause"}}` |
| Resume | `{"print":{"command":"resume"}}` |
| Stop | `{"print":{"command":"stop"}}` |
| Skip current object | `{"print":{"command":"project_file","param":"skip_object"}}` |

### 3.2 Temperature

| Action | Payload | Notes |
|---|---|---|
| Bed target | `{"print":{"command":"gcode_line","param":"M140 S60\n"}}` | |
| Nozzle target | `{"print":{"command":"gcode_line","param":"M104 S210\n"}}` | |
| Chamber target | `{"print":{"command":"set_ctt","ctt_val":45,"temper_check":true}}` | Only on models with a chamber heater |

The trailing `\n` inside `param` is required on every `gcode_line`.

### 3.3 Motion

Motion is issued as G-code through `gcode_line`.

**Jog convention:** switch to relative moves, emit a single `G1` containing **only** the axes with a
non-zero delta, then restore absolute mode.

```json
{"print":{"command":"gcode_line","param":"G91\nG1 X10 F3000\nG90\n"}}
```

Omit an axis term entirely rather than emitting `Z0`. Leaving a printer in relative mode is a
persistent, hard-to-spot fault.

**Home all:** `{"print":{"command":"gcode_line","param":"G28\n"}}`

**Movement-safe states.** A finished print parks the toolhead and is as safe to move from as an idle
machine. Gating movement on "idle" alone is a bug on models that omit `gcode_state` from delta
reports, because such a printer can remain latched on `complete` indefinitely. Treat `idle` and
`complete` as movement-safe.

### 3.4 Chamber light

```json
{"system":{"sequence_id":"0","command":"ledctrl","led_node":"chamber_light",
  "led_mode":"on","led_on_time":500,"led_off_time":500,"loop_times":0,"interval_time":0}}
```

**Every field is mandatory.** Firmware silently discards a light command missing `sequence_id`,
`led_on_time`, `led_off_time`, `loop_times`, or `interval_time`. The publish succeeds and no error is
reported anywhere — the light simply does not change. Treat "missing required field ⇒ silent ignore"
as a general pattern for this firmware, not a light-specific quirk.

`sequence_id` here is the literal string `"0"`. On **unsigned** `print` commands it may be omitted
entirely; on **signed** commands it is mandatory and must be ≥ 2000 (see §5).

**Reading light state:** the live state is reported in `print.lights_report`, not in the
`system.ledctrl` acknowledgement, which is only a command receipt.

### 3.5 Documented but not verified

The following AMS payloads appear in upstream research but have not been confirmed against hardware.
They are `print`-namespace commands, so on current firmware they require signing. Treat them as
untested — see the side-asks file.

```json
{"print":{"command":"ams_change_filament","target":0,"curr_temp":0,"tar_temp":220}}
{"print":{"command":"unload_filament"}}
{"print":{"command":"ams_control","param":"resume"}}
{"print":{"command":"ams_filament_setting","ams_id":0,"tray_id":0,"tray_type":"PLA",
          "tray_color":"FF0000FF","nozzle_temp_min":190,"nozzle_temp_max":230}}
```

---

## 4. Telemetry (Bambu)

Reports arrive on `device/<serial>/report`. Top-level keys: `print`, `camera`, `system`. A message
with no `print` key is valid and should be ignored for status purposes.

### 4.1 The presence rule

Status messages are **partial**. A heartbeat may omit most fields. Writing an absent field as a zero
value causes state to revert, filenames to blank out, and temperatures to display as unknown — the
entire class of "flickering" defects.

**Only update a field when the key was actually present in that message.** Represent "not reported"
as an explicitly nullable value and render it as `—`, never as `0` or `off`.

Some firmware resolves a condition by **stopping sending the key** rather than sending an empty
value. Clearing state only on an explicit empty value will latch a printer into a stuck condition
forever. Decay such state after a small number of consecutive healthy reports that omit the key.

### 4.2 Field catalog

| Field | Type | Notes |
|---|---|---|
| `gcode_state` | string | **may be absent** on heartbeats |
| `gcode_file` | string | populated on newer models; often empty on P1-class |
| `subtask_name` | string | on P1-class this carries the filename |
| `mc_percent` | int | 0–100 |
| `mc_remaining_time` | int | **minutes** — multiply by 60 for seconds |
| `bed_temper` / `bed_target_temper` | float | |
| `nozzle_temper` / `nozzle_target_temper` | float | |
| `chamber_temper` / `chamber_target_temper` | float | |
| `info.temp` | number | **packed 32-bit** — see §4.4 |
| `layer_num` / `total_layer_num` | int | |
| `home_flag` | int | bitmask; bit 0 = axes homed |
| `print_error` | int | see §4.5 |
| `hms` | array | absent ≠ empty — see §7 |
| `lights_report` | array | `{node:"chamber_light",mode:"on"}` — the live light state |
| `ams` | object | see §8 |
| `camera.ipcam_url` | string | if present it should be preferred over any constructed camera URL. Whether real firmware populates it is unconfirmed. |
| `wifi_signal`, `stg_cur`, `stg_total` | — | available, frequently unused |

### 4.3 State map

| `gcode_state` | Meaning |
|---|---|
| `RUNNING`, `PREPARE` | printing |
| `PAUSE` | paused |
| `SUCCESS`, `FINISH` | complete |
| `FAILED` | error |
| `IDLE`, `STANDBY`, `""` | idle |
| anything else | unknown |

**The `SUCCESS` → `IDLE` transition.** Firmware reports `SUCCESS` when a print finishes and settles
to `IDLE` on the very next message. Applying that immediately makes a "complete" indication flash and
vanish within a couple of messages. Require two consecutive idle reports before overwriting a latched
`complete`; any non-idle state should still override immediately.

### 4.4 `info.temp` is a packed integer

```
current °C =  value & 0xFFFF
target  °C = (value >> 16) & 0xFFFF
```

A reported chamber value of `3932184` is exactly `(60 << 16) | 24` — target 60 °C, current 24 °C.
Scaling heuristics such as "divide by 100000 if the value is large" produce plausible but wrong
numbers. **When a Bambu temperature looks absurd, suspect bit packing before suspecting a unit
scale.**

### 4.5 `print_error` is misleading in both directions

- **False negative.** A print can fail — for example a cover coming off — with `print_error` at `0`
  the entire time. That class of failure surfaces only through HMS.
- **False positive.** `print_error = 83935249` (`0x0500C011`) is an **SD-card-degradation advisory**,
  not a job fault. It will hold a healthy printer in an error display. Companion mobile apps show
  nothing because the advisory arrives on the `print_error` channel while the `hms` array is empty.

Correct policy: `print_error` outranks `gcode_state` **only** when `gcode_state` is present and
unhealthy. A non-zero `print_error` under a healthy state should be surfaced as a non-blocking
advisory.

A latched error that nothing can clear is worse than a missed one. If the clear path is gated on
"not already in error", the condition can never be escaped — not even by a restart, because the next
full status push re-asserts the same stale value.

Two lookup details: the `device_error` message table uses **8-hex-digit** keys while `device_hms` uses
**16-hex-digit** keys, and the tables key model lists by friendly names (`P1S`) while configuration
often carries internal product codes (`C12`). Both must be normalized or model-specific text will
never resolve.

---

## 5. Command signing

### 5.1 Scope

Firmware released after roughly January 2025 verifies every `print`-namespace MQTT command. An
unsigned command is rejected with `err_code 84033543`.

| Firmware | `print` namespace |
|---|---|
| Predates the gate | accepts unsigned |
| After the gate | requires a signed envelope |

`system` and `pushing` are never verified. Developer Mode does not remove the requirement; it only
makes the envelope optional.

### 5.2 The envelope

```jsonc
// 1. Build the payload. user_id is a top-level SIBLING of "print", not inside it.
{ "print": { "sequence_id": "5000", "command": "gcode_line", "param": "G28\n" },
  "user_id": "<numeric uid>" }

// 2. Sign the exact bytes that will be published. No re-serialisation after signing.
sign_string = base64( RSA-SHA256( privateKey, payload ) )

// 3. Append the header and publish the result unmodified.
{ ...payload, "header": {
    "sign_ver": "v1.0", "sign_alg": "RSA_SHA256",
    "sign_string": "<base64>",
    "cert_id": "<lowercase hex serial>CN=<issuer CN>",
    "payload_len": "<utf-8 byte length as a JSON STRING>" } }
```

Four requirements, each of which produces a generic verification failure if violated:

| Requirement | Failure mode |
|---|---|
| `payload_len` must be a JSON **string** | A number yields `84033547`, which is indistinguishable from a certificate problem. This is the single easiest detail to get wrong. |
| `cert_id` is the lowercase hex X.509 **serial** followed by `CN=` and the issuer common name | It is **not a hash**. Bambu's own identifiers are 32 hex digits because their serials are 16 bytes; a 20-byte serial yields 40. |
| `sequence_id` must be a **string ≥ 2000** on signed frames | Below the floor the printer answers `84033545` even with a trusted certificate. It is a fixed floor, not a high-water mark. |
| The signature covers the exact bytes published, minus `header`, including the trailing `user_id` sibling | The invariant is **signed bytes == published bytes**. Re-serialising after signing changes the bytes and breaks verification — key order, spacing, and dropped fields all matter. Build the payload once, sign it, and publish it unchanged. |

### 5.3 The printer trust store

- `security.app_cert_install` installs `{command, sequence_id, app_cert, crl}`. **The install command
  is itself unverified**, which is what makes bootstrapping possible.
- `crl` must be **non-empty** — an empty one is rejected with `crl value is null` — but **its signature
  is not validated**, so a self-minted CRL with a matching issuer CN is accepted.
- **The trust store lives in printer RAM and a reboot clears it.** Installation must therefore happen
  on **every connection**, not once at setup. Immediately after a reboot, `app_cert_list` answers
  `device-endreaddevcertfailed`.
- Installation **adds**; there is no command to remove an individual certificate.
- `security.app_cert_list` requires `"type":"app"` and a **string** `sequence_id`. Firmware that
  predates the verification gate does not answer `app_cert_list` at all, so a missing response there is
  expected rather than an error.
- A certificate only needs to be installed; it need not have been provisioned through the vendor's
  cloud. Whether signing with a self-signed certificate that has been installed is accepted has **not**
  been established — see the side-asks file. Plan on using the published shared app credentials.

### 5.4 Error codes

`84033543`–`84033548` are one verification family, and each stage is distinguishable.

| err_code | Meaning |
|---|---|
| `84033543` | No signature present |
| `84033544` | Contested: file-verify stage (bad `md5`, plaintext `url`, `ftp:///`) **or** `sequence_id` reuse across restarts |
| `84033545` | Signature valid but `cert_id` not in the trust store; **also** returned for a trusted certificate with `sequence_id < 2000` |
| `84033546` | Required header field missing or malformed |
| `84033547` | Signature rejected — wrong key, wrong signed bytes, or numeric `payload_len` |

### 5.5 Adoption strategy

Send the first `print` command **unsigned**. If it is rejected with `84033543`, mark the printer as
requiring signatures and retry signed. Machines whose firmware predates the gate never pay a rejected
command, so they are unaffected. Carry the numeric `err_code` on the resulting error object so callers
can branch on the verification family rather than matching message text.

### 5.6 What does not influence verification

Established by experiment, so it need not be re-tested:

- Identical behavior over cloud and LAN — transport, encoding, topic, and payload delivery are all
  excluded as causes.
- Payload shape is irrelevant. Numerous `gcode_line` variants (absent, string, or numeric
  `sequence_id`; extra fields; missing trailing newline; different G-code) all produce the same
  rejection when unsigned.

---

## 6. HMS (Health Management System)

HMS is an error channel independent of `print_error` and `gcode_state`, with a bit-packed wire format
and no explicit severity field.

```jsonc
// print.hms
{ "attr": 201327360, "code": 196615 }
```

```
human id = "HMS_%04X-%04X-%04X-%04X" % (attr >> 16, attr & 0xFFFF, code >> 16, code & 0xFFFF)
module   = (attr >> 24) & 0xFF
severity = code >> 16
```

**Module bytes** — the table is deliberately non-exhaustive; unknown values should map to
`"unknown"`, which is correct behavior rather than a bug.

| Byte | Module |
|---|---|
| `0x03` | mc |
| `0x05` | mainboard |
| `0x07` | ams |
| `0x08` | toolhead |
| `0x0C` | xcam |

**Severity values**

| `code >> 16` | Severity |
|---|---|
| 1 | fatal |
| 2 | serious |
| 3 | common |
| 4 | info |

Severity **1 or 2 should trip an error state independently** of `print_error` and `gcode_state`. This
is the only channel that reports cover-off and disturbance failures.

Note that `home_flag` is a plain integer on the wire, not an optional field — it is applied on every
report that carries it, which is exactly why an acknowledgement (which omits it) must never be routed
into status parsing.

**`hms: []` is an explicit clear signal.** An absent `hms` key must not wipe reported state — but see
§4.1: some firmware stops sending the key instead, so state must decay after a couple of healthy
reports rather than only on an explicit empty array.

Example: `attr = 0x03001200`, `code = 0x00020001` → `HMS_0300-1200-0002-0001`, severity *serious* —
"The front cover of the toolhead fell off."

### 6.1 `HMS_0500-0500-0001-0007`

*"MQTT Command verification failed. Please update Studio (including the network plugin) or Handy."*

This is the **command-signature gate** from §5, not a client identity or version problem. A rejected
`print` command raises this HMS entry, which sets the printer state to `error`, which in turn blocks
any movement gate — so the visible symptom is that every movement control appears dead. **Signing the
commands resolves it.**

Two operational notes: dismissing the notification does not clear the underlying state, and a printer
restart does. The stock message text is written for the vendor's own applications and is not
diagnostic of the actual cause.

---

## 7. Camera access (Bambu)

Two entirely different local camera protocols, selected by model class. Neither is available unless
the printer's LAN camera / liveview feature is enabled.

### 7.1 RTSPS on port 322 — H2-class and X1-class

```
rtsps://bblp:<access_code>@<printer_ip>:322/streaming/live/1
```

- Username is the literal `bblp`; password is the device access code.
- Requires the LAN camera / liveview toggle to be enabled. **This works with Developer Mode off** and
  does not disturb cloud control.
- The stream is H.264 over TLS, which browsers cannot display directly. Transcoding to MJPEG or HLS
  is required for a web UI.
- The certificate is self-signed; verification must be disabled.
- A second toolhead camera path (`/streaming/live/2`) has been reported as absent on real hardware.
  Verify with an RTSP client before assuming any stream beyond `/live/1` exists.

### 7.2 `bambus://` binary protocol on port 6000 — P1/A1-class

Not HTTP. A raw TLS socket with custom binary framing, yielding roughly one frame per second.

```
bambus://<printer_ip>:6000?token=<access_code>
```

Sequence: TCP connect → TLS handshake (self-signed; verification disabled) → send the 80-byte auth
packet → receive continuous frames, reported at roughly one frame per second.

**Auth packet — 80 bytes, little-endian**

| Bytes | Content |
|---|---|
| 0–3 | `0x00000040` (64) |
| 4–7 | `0x00003000` (12288) |
| 8–15 | zero |
| 16–47 | username `bblp`, NUL-padded to 32 bytes |
| 48–79 | access code, NUL-padded to 32 bytes |

The two leading constants are magic values; they are not derived from the packet length and should not
be "cleaned up".

**Frame format**

```
16-byte header: [0:4] uint32 LE payload size | [4:7] 0x00 | [8:11] 0x01 | [12:15] 0x00
then payload_size bytes of raw JPEG (FF D8 FF … FF D9)
```

**Cipher constraint.** Some models offer only `TLS_RSA_WITH_AES_256_GCM_SHA384` on port 6000 — a
static-RSA suite that Go excludes from its default configuration. It must be explicitly enabled, and
the remaining defaults re-listed, or the handshake simply fails.

**Framing has no validation and no resync.** Header bytes 4–15 and the JPEG markers are not checked,
there is no maximum frame size, and the read deadline is cleared after the first frame. A desync or a
silently stalled connection blocks indefinitely and recovers only via reconnect backoff. Connections
keyed by `host:port` alone will reuse a stale connection after an access-code rotation.

### 7.3 Snapmaker U1

Plain HTTP on the printer's own port — see §9.

---

## 8. AMS and filament (Bambu)

```jsonc
// print.ams — note the mixed types
"ams": {
  "ams": [{
    "id": "0",              // STRING. 0–3 for AMS / AMS 2 Pro
    "humidity": "4",        // index 0–5, lower is drier. ABSENT on AMS 1
    "humidity_raw": "23",   // percent. ABSENT on AMS 1
    "temp": "25.0",         // °C. ABSENT on AMS 1
    "tray": [{
      "id": "0", "state": 3,          // NUMBER: 0 empty, 2 loaded, 3 ready, 10 reading, 11 loaded+data
      "tray_type": "PLA", "tray_color": "000000FF",   // RRGGBBAA
      "tray_info_idx": "GFA00",
      "nozzle_temp_min": "190", "nozzle_temp_max": "230",  // STRINGS
      "remain": 750,        // NUMBER, millimetres remaining
      "tray_weight": "250", "tag_uid": "8A160AB5…"
    }]
  }],
  "tray_now": "0"           // (ams_id * 4) + tray_id; 254 = external spool, 255 = none
}
```

**The mixed types are a parse hazard.** A type mismatch aborts the parse of the **entire** report, not
just the AMS section — one wrong type silently kills all status for that message.

### 8.1 Determining whether a tray is loaded

Some AMS units report `state: 0` for **every** tray, including loaded ones. Deriving "loaded" from
`state` alone therefore reports a fully loaded unit as empty.

Use: `loaded = state ∈ {2, 3, 10, 11} OR tray_type != ""`. The fallback is safe because units that do
report `state` correctly send `tray_type: ""` for genuinely empty trays.

### 8.2 Delta versus full AMS updates

Some AMS units send **delta** updates; others send full snapshots. A delta may arrive as an `ams`
object with an **empty inner array** (only `tray_now` changed), or with unit objects that **lack the
`tray` array** (only humidity changed).

Replacing cached AMS state on every message therefore erases filament type, color, and remaining
length on nearly every report. **Merge instead of replacing:**

- Merge units by `id`; merge trays by index.
- Retain cached entries absent from a delta.
- An empty `tray` array means keep the cached trays wholesale.
- For scalars, prefer the new value but fall back to the cached one when the new one is empty.
- Update the active tray only when `tray_now` is non-empty.

---

## 9. Snapmaker U1

The U1 runs **Klipper with the Moonraker API server** under its custom firmware. The API surface is a
**hybrid** of OctoPrint-style compatibility shims and native Moonraker routes, and it matters which is
which. Default port is **80**.

### 9.1 Authentication

An access code may be configured. When set, send it **both** as a `?access_code=` query parameter and
as an `X-Access-Code` header. There is no session and no expiry.

Whether the firmware actually **enforces** the code is unconfirmed — the commonly exercised path is the
unauthenticated one, and stock Moonraker uses a different parameter name (`access_token`) and a bearer
header. Do not assume the code is optional, and do not assume it is required; test against the target
machine. Note that the WebSocket endpoint carries **no** access code at all, so if enforcement exists
the socket handshake is the first thing to fail.

> When appending the parameter, use `&` if the URL already contains a query string. Appending `?` to a
> URL that already has one corrupts the final parameter — which silently degrades the multi-object
> status query used for progress, layers, and Z position.

### 9.2 Endpoints

| Method | Path | Family | Purpose |
|---|---|---|---|
| `GET` | `/api/printer` | OctoPrint-style | temperatures + state (primary telemetry) |
| `POST` | `/api/print/{pause,resume,cancel}` | OctoPrint-style | job control |
| `POST` | `/api/printer/command` | OctoPrint-style | command passthrough |
| `GET` | `/printer/objects/query?print_stats&virtual_sdcard&gcode_move` | **Moonraker** | file, layers, progress, position |
| `POST` | `/printer/gcode/script` | **Moonraker** | `{"script":"G28"}` — all motion control |
| `WS` | `/websocket` | Moonraker | status push — see §9.3 |
| `GET` | `/webcam/stream.mjpg` | — | built-in webcam, MJPEG |
| `GET` | `/screen/snapshot` | — | touchscreen snapshot, PNG |

### 9.3 The WebSocket requires a subscribe handshake

Moonraker emits no status until a client sends the JSON-RPC **`printer.objects.subscribe`** message.
Without it the socket is silent. A client that parses frames as the flat `/api/printer` shape rather
than `notify_status_update` will also see nothing useful.

Additionally, if the read deadline is shorter than the ping interval, a quiet connection will always
time out before the next keepalive can rescue it — set the read wait longer than the ping period.

A polling fallback is worthwhile regardless: some fields needed for the UI are not carried on the
WebSocket path at all.

### 9.4 Moonraker returns HTTP 200 with an embedded error

```json
{"error": {"message": "Klippy Not Connected"}}
```

This arrives with a **200** status. Any caller that trusts the status code alone will report success
for a failed operation. **Check every Moonraker response body for an `error` field**, not just the
G-code path.

### 9.5 State mapping

Flags take priority over text:

| Source | Maps to |
|---|---|
| `flags.printing` | printing |
| `flags.paused` | paused |
| `flags.error` | error |
| text `Operational` | idle |
| text `Printing` | printing |
| text `Paused` | paused |
| text `Error` | error |
| text `Complete`, `Cancelled` | complete |

Matching is **case-sensitive** on the OctoPrint-cased strings. Anything unmatched becomes `unknown`,
which will then block movement controls if the UI gates on state.

### 9.6 Hardware and firmware behaviors

| Behavior | Detail |
|---|---|
| LED state is unqueryable | The Moonraker `led` object query returns `null` for `cavity_led`. Real state cannot be read; store the last **commanded** state. |
| `cavity_led` is RGB-driven | It appears white via R=G=B=1. `SET_LED LED=cavity_led WHITE=1` is a **no-op on real hardware**. Set all four channels. |
| Homing is slow | `G28` takes 30+ seconds. Use a dedicated long-timeout client (~120 s); a normal 10 s client will time out. |
| `homed_axes` is not exposed | Infer homing: successful `G28` ⇒ homed; print start ⇒ not homed; a "Must home" error ⇒ not homed. |
| Do not infer homed from Z | The printer **parks Z at roughly 9.9 mm** after homing — far from the bed — so Z position says nothing about homing state. |
| "Must home" errors | Real text: `Must home Z axis first: 5.130 4.947 199.982 […]`. Klipper retains the saved Z but still demands a fresh home. Detect the substring and return a client error; do not mark the machine homed. |
| Last filename never clears | Moonraker keeps reporting the most recent filename with no idle clear, so a "current file" field can persist long after a job ends. |
| Skip-object needs explicit support | There is no implicit object-exclusion behavior; skipping requires `EXCLUDE_OBJECT` support and must not be reported as done unless it was actually issued. |

**Timeouts in use:** 10 s status and commands, 120 s G-code, 5 s WebSocket handshake, 3 s poll
interval, 15 s reconnect backoff. No retries are attempted on failure.

### 9.7 Filament system (documented, not verified)

```
FEED_AUTO MODULE=left CHANNEL=0 LOAD=1
FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1 STAGE=prepare|doing|cancel
GET /printer/objects/query?filament_feed     -> channel_state, channel_error
GET /printer/objects/query?filament_detect   -> MAIN_TYPE, ARGB_COLOR, HOTEND_MIN/MAX_TEMP
```

`channel_state`: `wait_insert → preload_prepare → preload_feeding → preload_finish`;
`load_prepare → load_homing → load_picking → load_feeding → load_heating → load_extruding →
load_flushing → load_finish`; `unload_prepare → unload_finish | unload_fail`.

`channel_error`: `ok, no_filament, residual_filament, motor_speed, wheel_speed, timeout, move,
move_home, move_switch, move_extrude, custom_gcode, heat`.

Remaining filament length and weight are not reported.

---

## 10. Cross-printer capability matrix

| Capability | Bambu, pre-gate firmware | Bambu, post-gate firmware | Snapmaker U1 |
|---|---|---|---|
| Transport | cloud or LAN MQTT | cloud or LAN MQTT | HTTP on 80 |
| Payload encryption | TLS only | TLS only | none |
| Command signing | not required | **required for `print`** | n/a |
| Job pause / resume / stop | yes | yes, signed | yes |
| Skip object | `project_file` param | signed | not supported |
| Bed / nozzle target | yes | signed | yes |
| Chamber target | only on heated-chamber models | signed, same restriction | no chamber heater |
| Chamber light | yes (`system`, unverified) | yes, unsigned | yes, but state unreadable |
| Jog / home | yes | signed | yes, long timeout |
| Homing state | `home_flag` bit 0 | `home_flag` bit 0 | inferred only |
| Independent error channel | HMS | HMS | Moonraker error state |
| Camera | `bambus://` :6000 | RTSPS :322 | MJPEG :80 |
| Camera needs LAN enabled | yes | yes | no |
| Cloud control | yes (unless Developer Mode) | yes (unless Developer Mode) | no cloud |

---

## 11. Model code reference

Internal product codes differ from marketing names, and matching on display names silently misroutes
behavior such as camera protocol selection.

| Product code | Product | Camera protocol | Chamber heater |
|---|---|---|---|
| `C12` | P1S | `bambus://` :6000 | no |
| `P1P` | P1P | `bambus://` :6000 | no |
| `BL-P001` | X1 Carbon | RTSPS :322 | no |
| `O1S` | H2S | RTSPS :322 | yes |
| `H2S`, `H2D`, `H2C`, `H2D PRO`, `P2S`, `X2D` | H2 / X2 class | RTSPS :322 | yes |

Keep camera-routing decisions separate from feature decisions: a model can need RTSPS without
inheriting other semantics such as a chamber heater.

Message tables key their model lists by **friendly name** (`P1S`), so configuration carrying product
codes (`C12`) must be normalized before lookup or model-specific text will never resolve.
