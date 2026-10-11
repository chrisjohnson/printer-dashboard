# Printer Behavior & Protocol Matrix

**Scope.** Everything this project has established about how these printers actually behave on the
wire — endpoints, payloads, quirks, and the failure modes that cost real debugging time. Written so
that an agent working on *this* project or a sibling one can integrate against Bambu and Snapmaker
hardware without repeating the research.

**Fleet covered:** Bambu Lab **P1S** (stock, pre-Bambu-Connect firmware), Bambu Lab **H2S** (Bambu
Connect firmware from day one), X1-series knowledge captured incidentally (`BL-P001`), Snapmaker
**U1** running **Paxx** custom firmware.

**Date compiled:** 2026-10. Sources: full git history (266 commits), `.fleet/` board + archives,
`PLAN.md`, `docs/`, and every Go source/test file in the repo. Citations are `file:line` against
this worktree.

---

## 0. How to read this

Every claim carries a confidence marker:

| Marker | Meaning |
|---|---|
| **[CONFIRMED]** | Observed against real hardware in this project. Trust it. |
| **[CODE]** | Implemented and unit-tested here; wire format is what our code sends/parses, but not necessarily re-verified against hardware. |
| **[DOC]** | Recorded from upstream community research (pybambu / OpenBambuAPI / Moonraker) and never independently verified here. Treat as a hypothesis. |
| **[UNKNOWN]** | Explicitly unresolved. Do not guess; re-derive. |

A claim that is `[DOC]` here may be load-bearing in your project. When it is, verify it — several
`[DOC]` items in this repo turned out to be wrong when finally tested (see §9).

---

## 1. Fleet at a glance

| | **P1S** (pre-Bambu-Connect) | **H2S** (Bambu Connect) | **Snapmaker U1** (Paxx) |
|---|---|---|---|
| Control transport | Bambu **Cloud MQTT** | Bambu **Cloud MQTT** | Local **HTTP** (Moonraker + OctoPrint-style shim) |
| Broker / host | `us.mqtt.bambulab.com:8883` | same | n/a |
| Local control transport | *not implemented here* | *not implemented here* | `http://<ip>:80` |
| Auth | Bambu account token | same | optional `access_code` |
| Telemetry | pushed MQTT `report` | pushed MQTT `report` | **3 s REST poll** (WS is effectively dead) |
| Camera | `bambus://` binary TLS, **port 6000** | **RTSPS port 322** via go2rtc | MJPEG `/webcam/stream.mjpg` |
| `print`-namespace signing | not required (firmware predates the gate — **will change on update**) | **REQUIRED** — unsigned ⇒ `err_code 84033543` | n/a |
| Chamber heater | no | **yes** | no |
| AMS | AMS 1 — **delta** updates, no humidity/temp | AMS 2 Pro — **full** updates, humidity+temp | n/a (RFID autofeeder, unimplemented) |
| Filename field | `subtask_name` (not `gcode_file`) | `gcode_file` | Moonraker `print_stats.filename` |
| Homed state | `home_flag` bit 0 | `home_flag` bit 0 | **not exposed** — inferred from `G28` success |

**The single most important architectural fact:** both Bambu printers are driven *entirely through
Bambu's cloud* in this deployment. No LAN mode, no developer mode, no local MQTT broker. The one
thing that requires a LAN path is the H2S camera. **[CONFIRMED]**

---

## 2. Bambu Lab — shared cloud control plane

### 2.1 Account authentication

Base URLs: `https://api.bambulab.com` (global) / `https://api.bambulab.cn` (china).
Selected by config `bambu_account.region` ∈ `{"global","china"}`. **[CODE]** (`auth.go:19-21,129-139`)

Only **five** endpoints are used in production:

| Method | Path | Request | Response consumed |
|---|---|---|---|
| `POST` | `/v1/user-service/user/login` | `{"account","password","apiError":""}` | `success`, `accessToken`, `loginType`, `expiresIn` |
| `POST` | `/v1/user-service/user/sendemail/code` | `{"email","type":"codeLogin"}` | — (2FA email code) |
| `POST` | `/v1/user-service/user/login` *(2nd leg)* | `{"account","code"}` | `accessToken` |
| `GET` | `/v1/design-user-service/my/preference` | — | `uid` (numeric user id) |
| `GET` | `/v1/iot-service/api/user/bind` | — | `devices[]` (see §2.3) |
| `POST` | `/v1/iot-service/api/user/ttcode` | `{"dev_id"}` | `ttcode`, `authkey`, `passwd`, `region` |

Headers: `Content-Type: application/json`, and `Authorization: Bearer <token>` on authenticated
calls. **[CODE]** (`auth.go:690,724-725`)

> **Gotcha — no client-impersonation headers needed.** There is *no* `X-BBL-Client-Type` /
> `X-BBL-Client-Name` / User-Agent spoofing anywhere in this codebase, and every endpoint above
> works fine with Go's default `Go-http-client/1.1` UA. **[CONFIRMED]** The `X-BBL-*` headers only
> appear in the `Dockerfile` when fetching the slicer plugin resource feed — that endpoint needs
> them, the account endpoints do not. Don't go hunting for a UA-spoofing requirement that isn't
> there.

**2FA flow.** First `login` returns `success:false, loginType:"verifyCode"`. You then POST
`sendemail/code` with `type:"codeLogin"`, and re-POST `login` with `{"account","code"}`.
**[CODE]** After 2FA the API may return an `accessToken` while *omitting* `success` — treat any
non-empty `accessToken` as success or you will spuriously fail login (`auth.go:385-389`). **[CONFIRMED]**

### 2.2 Tokens — two species, one slot

**This is the most confusing thing in the whole Bambu integration.**

`MQTTPassword()` returns exactly the same string used as the HTTP `Bearer` value
(`auth.go:725,747-754`). But that string can be one of two different things:

1. **The opaque login token** — what `POST /user/login` returns as `accessToken`. Real-world
   sample on disk begins `AQBK…`, ~144 chars, **not** a JWT. **[CONFIRMED]**
2. **A browser JWT** — `eyJ…`, extracted from `localStorage` on `bambulab.com` after an SSO
   (Google/Apple) sign-in, where email/password login is unavailable. **[CONFIRMED]**

Both are accepted by the same validator: `GET /v1/design-user-service/my/preference`. That endpoint
is the *only* token validator in the codebase. **[CODE]**

Consequences worth knowing:

- **Do not assume the MQTT password is a JWT.** Code that tries to `ParseJWT()` it will fail on
  species (1). Our code handles this: expiry falls back to "unknown", and unknown expiry is treated
  as **valid** (`auth.go:273-286`). **[CODE]**
- **Expiry is frequently unknown.** A real token file contained `"expires_at": -62135596800`. That
  is `time.Time{}.Unix()` — Go's zero time serialized. `LoadToken` ignores non-positive values, so
  expiry stays zero and `TokenValid()` returns `true` unconditionally: **the app never proactively
  refreshes.** The only thing that catches a dead token is the startup `GetDevices()` probe, which
  deletes the file and requires a fresh login. **[CONFIRMED]** Budget for this: community reports
  put the token lifetime around 3 months. **[DOC]**
- **SSO accounts cannot use email/password login at all.** Token extraction from the browser is the
  only path. **[CONFIRMED]**
- Token cache: `~/.printer-dashboard/bambu_token_<email>.json` with `@`→`_at_`, `.`→`_dot_`
  substitution (despite a code comment calling it a hash). Schema:
  `{token, user_id, region, email?, expires_at}`. Dir `0700`, file `0600`. **[CODE]**
- In Docker this is mounted from a **shared** `~/.printer-dashboard/` so concurrent dev worktrees
  reuse one authenticated session. **[CODE]**

### 2.3 Device discovery

`GET /v1/iot-service/api/user/bind` →

```json
{ "message": "...",
  "devices": [ { "dev_id": "...", "name": "...", "online": true,
                 "print_status": "...", "dev_model_name": "...",
                 "dev_product_name": "...", "dev_access_code": "..." } ] }
```
**[CODE]** (`auth.go:48-59`)

- `dev_id` **is** the printer serial and **is** the MQTT topic component. **[CONFIRMED]**
- `dev_access_code` is returned by the cloud but **never consumed** by this app — the access code
  used for cameras is read off the printer's own LCD (Settings → Network) and put in config.
  **[CONFIRMED]** Whether the cloud value equals the LCD value is **[UNKNOWN]** and worth testing:
  if they match, camera setup can be fully automated.
- `print_status` is parsed and unused.
- **ID-collision trap:** deriving a local printer ID from a truncated serial (first 8 chars) caused
  one device to silently overwrite another. Use the full serial. **[CONFIRMED]** (commit `8a843bb`)

### 2.4 Cloud MQTT connection

| Parameter | Value |
|---|---|
| Broker | `ssl://us.mqtt.bambulab.com:8883` (global) / `cn.mqtt.bambulab.com:8883` (china) **[CODE]** |
| Transport | MQTT over TLS (**encrypted**) **[CONFIRMED]** |
| TLS verification | `InsecureSkipVerify: true` — "Bambu's cloud cert may not be in system store" **[CODE]** (`client.go:258-260`) |
| Username | `u_{user_id}` **[CODE]** |
| Password | the account token (§2.2) **[CODE]** |
| Client ID | `printer-dashboard-{id}-{unixnano}` — unique per process start **[CODE]** |
| Clean session | `true` **[CODE]** |
| Keepalive / ping timeout | 30 s / 10 s **[CODE]** |
| Auto-reconnect | on, max interval 30 s **[CODE]** |
| Initial connect | retried **indefinitely**, backoff 1 s doubling → 30 s cap; 15 s connect timeout **[CODE]** |

> **Gotcha — `InsecureSkipVerify` is load-bearing.** If you "harden" this by enabling verification
> against a stock CA bundle, connections may break. Bambu's cloud certificate chain is not reliably
> present in a minimal container trust store. **[CONFIRMED]**

### 2.5 Topics and envelopes

| Direction | Topic | QoS | Notes |
|---|---|---|---|
| Subscribe | `device/{dev_id}/report` | 0 | all telemetry **[CONFIRMED]** |
| Publish | `device/{dev_id}/request` | 0, retain=false | all commands **[CONFIRMED]** |

**There is no separate ack topic — but acks do exist.** **[CONFIRMED]** (K-087 then corrected by
K-109) The firmware's `result`/`reason`/`err_code` reply comes back **inline on the `report` topic**,
interleaved with status. Two consequences: you must demultiplex acks from status, and you must not let
an ack fall through into status parsing — an ack carries no `home_flag`, which reads as zero and
**silently marks a homed printer unhomed**. Transport-level publish success alone is never evidence
the firmware accepted the command. See §8.6.

Top-level report keys: `print`, `camera`, `system`. A report with no `print` key is legal and should
be ignored for status purposes (`client.go:468-470`). **[CODE]**

### 2.6 Command catalog — exact wire JSON

All commands publish to `device/{dev_id}/request`.

> **Read §8 first.** Everything below in the `print` namespace is **rejected outright** on post-Jan
> 2025 firmware (H2S) unless wrapped in an RSA-SHA256 `header` envelope. The payloads shown are the
> *inner* payload; signing wraps them.

```jsonc
// pause / resume / stop
{"print":{"command":"pause"}}
{"print":{"command":"resume"}}
{"print":{"command":"stop"}}

// skip object — note: project_file namespace, not a dedicated command
{"print":{"command":"project_file","param":"skip_object"}}

// chamber target temp (H2S/X1C/X1E only — printers WITH an active chamber heater)
{"print":{"command":"set_ctt","ctt_val":45,"temper_check":true}}

// G-code passthrough — the trailing \n is present in every one we send
{"print":{"command":"gcode_line","param":"M140 S60\n"}}          // bed target
{"print":{"command":"gcode_line","param":"M104 S210\n"}}         // nozzle target
{"print":{"command":"gcode_line","param":"G28\n"}}               // home all
{"print":{"command":"gcode_line","param":"G91\nG1 X10 F3000\nG90\n"}}  // jog

// chamber light — DIFFERENT top-level key ("system"), and see the required-fields trap below
{"system":{"sequence_id":"0","command":"ledctrl","led_node":"chamber_light",
            "led_mode":"on","led_on_time":500,"led_off_time":500,
            "loop_times":0,"interval_time":0}}

// force a full state snapshot
{"pushing":{"command":"pushall","version":1,"push_target":1}}
```
**[CODE]** (`commands.go`, `client.go:406`)

> **GOTCHA — `ledctrl` silently no-ops if any field is missing.** **[CONFIRMED]** (commit `111da6a`)
> Firmware requires `sequence_id`, `led_on_time`, `led_off_time`, `loop_times`, **and**
> `interval_time` alongside `led_node`/`led_mode`. Omit them and you get **HTTP-success-equivalent
> silence**: MQTT publishes fine, no error anywhere, light doesn't change. This was a real bug that
> took a debugging session to find. Assume the same "missing field ⇒ silent ignore" pattern applies
> to other command families.

> **`sequence_id` — the old "probably inconsequential" note is now superseded.** `print`-namespace
> commands historically omitted it and the `system` light command sets `"0"`. That is still fine for
> **unsigned** frames. But on **signed** frames a `sequence_id` **string ≥ 2000 is mandatory** — below
> the floor the printer returns `84033545` even with a trusted certificate. `sign()` injects one when
> the command builder omitted it. See §8.1. **[CODE]**

**Documented but NOT implemented** (payloads from upstream research, `[DOC]` — verify before use):
`ams_change_filament`, `unload_filament`, `ams_control`, `ams_filament_setting`,
`ams_user_setting`, `ams_get_rfid`, `ams_filament_drying`. See §6.

### 2.7 Report field catalog (`print.*`)

| Wire field | Type | Notes |
|---|---|---|
| `gcode_state` | string | see §2.8. **May be absent** on heartbeat reports |
| `gcode_file` | string | H2S populates; **P1S often leaves empty** |
| `subtask_name` | string | **P1S carries the filename here** |
| `mc_percent` | int | percent (0-100), not 0-1 |
| `mc_remaining_time` | int | **minutes** — multiply by 60 for seconds |
| `bed_temper` / `bed_target_temper` | float | |
| `nozzle_temper` / `nozzle_target_temper` | float | |
| `chamber_temper` / `chamber_target_temper` | float | direct-wire form |
| `info.temp` | number | **packed** — see below |
| `layer_num` / `total_layer_num` | int | |
| `home_flag` | int | **bitmask**; bit 0 = axes homed |
| `stg_cur` / `stg_total` | int | stage progress |
| `print_error` | int | 0 = none; **stays 0 during some real failures**, and non-zero for some **non**-faults (see below) |
| `hms` | array | see §2.9. Absent ≠ empty |
| `lights_report` | array | `{"node":"chamber_light","mode":"on"}` |
| `ams` | object | see §6 |
| `wifi_signal` | string | parsed, unused |
| `lifecycle` | string | parsed, unused |

Other top-level keys: `camera.ipcam_url`, `camera.timelapse_url`, `system.ledctrl`.

> **GOTCHA — `system.ledctrl` is a command ACK, not live state.** **[CONFIRMED]** (commit `111da6a`)
> The recurring light *state* arrives in `print.lights_report`. Parsing `system.ledctrl` for state
> gives you a toggle that never reflects reality.

> **GOTCHA — `print_error` is bidirectionally misleading.** **[CONFIRMED]**
> ① **False negative:** a P1S print failed because the cover came off; `print_error` was 0 the whole
> time. That class of event surfaces only through **HMS**.
> ② **False positive:** `print_error=83935249` (`0x0500C011`) froze a healthy P1S in `error`. That
> code is an **SD-card-degradation advisory** from the vendored `device_error` table, not a job
> fault — Bambu Handy shows nothing because the advisory arrives on the `print_error` channel while
> `hms` was `[]`. Bambu documents it nowhere (see bambulab/BambuStudio#4495).
>
> Correct policy (now implemented): `print_error` outranks `gcode_state` **only** when `gcode_state`
> is present *and* unhealthy; a non-zero `print_error` under a healthy state becomes a non-blocking
> **advisory**. A latched error that nothing can un-latch is worse than a missed one — the old code
> gated the clear path on `State != "error"`, so a restart did not help because the next `pushall`
> re-asserted the same stale value.
>
> Two lookup traps: `lookupHMSMessage` only consulted `device_hms` (16-hex keys) and never
> `device_error` (8-hex keys), and the table's model lists use friendly names (`P1S`) while config
> declares internal codes (`C12`) — so `normalizeModel()`/`modelAliases` (`C12`→`P1S`, `O1S`→`H2S`)
> are required for model-specific text to resolve at all.

#### `info.temp` is a packed 32-bit integer

**[CONFIRMED]** (`client.go:566-588`) H2S reports chamber temperature as a single number where:

```
current °C =  value & 0xFFFF
target  °C = (value >> 16) & 0xFFFF
```

Forensic confirmation: the original bug report showed a chamber reading of **3932184**. That is
exactly `(60 << 16) | 24` → **target 60 °C, current 24 °C**. The first attempted fix — "divide by
100000 if > 500", which produced a plausible-looking 39.3 °C — was wrong and got replaced. If you
see an absurd temperature from a Bambu printer, suspect packing before you suspect a unit scale.

Validation bounds we apply: current `-50..100`, target `0..100` (a chamber can read below freezing
but can only be heated above ambient). Out-of-range values are dropped and the previous good reading
is retained. **[CODE]**

### 2.8 State mapping

| `gcode_state` | canonical |
|---|---|
| `RUNNING` | `printing` |
| `PREPARE` | `printing` (warming/homing) |
| `PAUSE` | `paused` |
| `SUCCESS`, `FINISH` | `complete` |
| `FAILED` | `error` |
| `IDLE`, `""` | `idle` |
| `STANDBY` | `idle` |
| anything else | `unknown` |

**[CODE]** (`parser.go:204-224`)

> **GOTCHA — the SUCCESS→IDLE flicker.** **[CONFIRMED]** Firmware reports `SUCCESS` when a print
> finishes and settles to `IDLE` on the **very next push**. Applying that immediately makes a
> "complete" state flash and vanish within ~2 reports. Fix: require **2 consecutive** idle reports
> before overwriting a latched `complete`. Any non-idle state (e.g. a new print starting) still
> overrides immediately. Only the complete→idle edge is latched.
>
> Related subtlety: `CurrentFile` clearing is deliberately keyed off the *raw* per-report state, not
> the latched one — so the filename disappears one report **before** the COMPLETE badge clears.
> That asymmetry is intentional (`client.go:521-533`).

### 2.9 HMS (Health Management System)

Wire shape — `print.hms` is an array of plain integers, **no severity field**:

```json
{"attr": 201327360, "code": 196615}
```

Decoding **[CODE]** (`parser.go:86-145`, ported from pybambu):

```
human code = "HMS_%04X-%04X-%04X-%04X" % (attr>>16, attr&0xFFFF, code>>16, code&0xFFFF)
module     = (attr >> 24) & 0xFF   -> {0x05 mainboard, 0x0C xcam, 0x07 ams, 0x08 toolhead, 0x03 mc}
severity   = code >> 16            -> {1 fatal, 2 serious, 3 common, 4 info}
```

- Severity **1 or 2 trips `State="error"` independently** of `print_error` and `gcode_state`. This
  is the only channel that catches cover-off / disturbance failures. **[CONFIRMED]**
- The module table is deliberately **non-exhaustive**; unknown module bytes → `"unknown"`. That is
  correct behavior, not a bug.
- Message lookup uses a vendored code→text table keyed on the 16-hex-digit concatenation
  (e.g. `0500050000010007`). Unrecognized codes yield an empty message — expected, not an error.
  The vendored table holds **4998 `device_hms` entries plus 946 `device_error` entries**; the
  `device_error` half is **never queried** by the lookup. **[CODE]**
- Real user-confirmed example: `attr=0x03001200, code=0x00020001` →
  **`HMS_0300-1200-0002-0001`**, severity *serious*, *"The front cover of the toolhead fell off."*
  **[CONFIRMED]**
- **`hms` absent ≠ `hms: []`.** An explicit empty array is the *clear* signal. Absence must not wipe
  reported HMS state. But firmware sometimes just **stops sending the key** when a condition
  resolves, so a pure "only clear on explicit []" policy latches the printer into `error` forever.
  Fix: decay after **2 consecutive** healthy reports that omit `hms`. **[CODE]** (`client.go:623-657`)

> **`HMS_0500-0500-0001-0007` — "MQTT Command verification failed."** **[CONFIRMED, corrected]**
> An earlier write-up here called this an unfixable firmware client-identity gate and advised
> dismissing it. **That was wrong.** It is the **command-signature verification gate** from §8: a
> rejected `print` command raises this HMS, which puts the printer in `state:"error"`, which then
> blocks the movement gate — so it presented as "all move buttons dead". **Signing the commands fixes
> it.** Two operational notes: the dismiss endpoint only suppresses *notifications* and does not clear
> the state, and a printer restart does clear it. The stock message text is aimed at Bambu
> Studio/Handy and is not diagnostic of what is actually wrong.

### 2.10 The golden rule: presence semantics

**Bambu reports are partial.** A heartbeat report may omit most fields. Therefore:

> **An absent field means "unchanged", never "zero" or "none".**

Every field we care about is a pointer/optional and is only written when present. Violating this
produces the flicker class of bug that this project hit repeatedly: state reverting to `idle`,
filename blanking, temperatures showing `?`. **[CONFIRMED]**

Two specific instances:
- `gcode_state` absent ⇒ **do not** touch `State` (heartbeat reports legitimately omit it).
- `tray_now` absent ⇒ **do not** reset the active tray to "unknown".

Corollary for UIs: `null` means "not yet reported", and must render as `—`, **not** as `off`/`0`/`?`.
A chamber-light toggle that renders `null` as "off" tells the user their physically-on light is off.
**[CONFIRMED]** (K-097)

---

## 3. Per-printer / per-mode behavior

### 3.1 P1S — stock, pre-Bambu-Connect firmware

| Aspect | Behavior | Marker |
|---|---|---|
| Control | Cloud MQTT only, as §2 | [CONFIRMED] |
| Filename | arrives in **`subtask_name`**; `gcode_file` empty during print | [CONFIRMED] |
| Chamber heater | **none** — `HasChamber=false`; `set_ctt` not applicable | [CONFIRMED] |
| Chamber temp | reported via direct `chamber_temper` | [CONFIRMED] |
| AMS | AMS 1: **delta updates only**; **no** humidity, **no** temp sensors | [CONFIRMED] |
| AMS tray `state` | reports **`0` for every tray, including loaded ones** | [CONFIRMED] |
| Camera | `bambus://` binary TLS on **port 6000** | [CONFIRMED] |
| Cover/door sensor | **none** (open gantry) — cover-off shows up as a force/vibration disturbance HMS code | [DOC] |
| Local camera access code | shown on printer LCD (Settings → Network); works **without** LAN mode on this older firmware | [CONFIRMED] |

> **AMS `state: 0` trap.** **[CONFIRMED]** Because P1S reports `state:0` on loaded trays, any
> "is this slot loaded?" logic derived purely from `state` says *empty* for a fully-loaded P1S AMS.
> Our rule: `loaded = state ∈ {2,3,10,11} || tray_type != ""`. The `tray_type` fallback is safe
> because H2S/X1 always send `tray_type:""` for genuinely empty trays.

### 3.2 H2S — Bambu Connect firmware

| Aspect | Behavior | Marker |
|---|---|---|
| Control | Cloud MQTT only, as §2 | [CONFIRMED] |
| Filename | `gcode_file` populated normally | [CONFIRMED] |
| Chamber heater | **yes** — `HasChamber=true`; `set_ctt` works | [CONFIRMED] |
| Chamber temp | arrives as **packed `info.temp`** (§2.7) | [CONFIRMED] |
| AMS | AMS 2 Pro: **full updates**; `humidity` (0-5 index), `humidity_raw` (%), `temp` (°C) present | [CONFIRMED] |
| Cameras | **exactly one** physical camera: `/streaming/live/1`. `/live/2` **404s on real hardware** | [CONFIRMED] |
| Camera transport | **RTSPS on port 322**, requires **"LAN Only Liveview"** enabled | [CONFIRMED] |
| Initial state | after power-on, `lights_report` and temperature may be absent for a while — UI must show `—` | [CONFIRMED] |

> **"LAN Only Liveview" is a *separate* toggle from full LAN Mode.** **[CONFIRMED]** This distinction
> matters enormously and is easy to get wrong:
> - **LAN Only Liveview ON** ⇒ opens RTSPS:322 for the camera. Cloud MQTT **keeps working**. This is
>   our H2S configuration.
> - **Full LAN Mode ON** ⇒ the printer **stops publishing to cloud MQTT entirely** (§3.4).
>
> So "the H2S needs a LAN path for video but is still cloud-controlled" is not a contradiction.

### 3.3 X1 series (`BL-P001` = X1 Carbon)

Known only through a routing fix, but it's a clean lesson:

- X1-series cameras serve **RTSPS on 322**, like H2 — **not** the port-6000 binary protocol used by
  P1/A1. **[CONFIRMED]** (commit `6c1a95b`, GitHub #21)
- Model codes are internal strings, not marketing names: **`BL-P001` → X1 Carbon**, **`O1S` → H2S**.
  Matching on marketing names alone silently misroutes cameras. **[CONFIRMED]**
- Keep camera-routing predicates separate from feature predicates: X1 uses RTSPS but must **not**
  inherit H2 semantics like `HasChamber`. We split `UsesRTSPS()` from `IsH2S()` for exactly this
  reason. **[CODE]**

Model → capability table (`client.go:979-1009`):

| Model string | `UsesRTSPS` | `HasChamber` |
|---|---|---|
| `H2S`, `H2D`, `H2C`, `H2D PRO`, `P2S`, `X2D`, `O1S` | yes | **yes** |
| `BL-P001` (X1 Carbon) | yes | no |
| `P1S`, `A1`, others, empty | **no** → `bambus://:6000` | no |

### 3.4 LAN Mode ON — what breaks

**[CONFIRMED]** (K-106 / GitHub #22) A printer in full LAN Mode **stops publishing to Bambu cloud
MQTT by design**. It is not broken; it is behaving correctly. Symptoms in a cloud-only client:
`online:false`, all telemetry `null`, no error message, no explanation.

Our mitigation is detection only: if a subscribed printer produces **zero reports for 60 s** after
subscribe+pushall, log a warning naming LAN Mode as the likely cause. Full local-broker support is
**not implemented**.

### Measured LAN MQTT facts (from K-108, so this is no longer guesswork)

**[CONFIRMED]** — measured with `paho` against the H2S using `ssl://<ip>:8883`, username `bblp`,
password = access code:

| Fact | Detail |
|---|---|
| Command topic on LAN | **`device/{serial}/request`** — same as cloud. `device/{serial}/mqtt` is accepted but **silently ignored**; it is a *publish* topic on the Duet-style API, not a command topic. |
| Encryption | TLS on 8883, but **payloads are plaintext JSON both ways — no AES layer** on this firmware. |
| Auth | Access code **is enforced** — wrong code ⇒ `connection refused (bad user or password)`. |
| Report rate | ~1 report/sec, sparse. |
| Command behaviour | Identical to cloud, including the signature gate. **The cloud is a forwarder, not a different command language.** |
| H2S open ports | 3000 (FTP), 6000 (camera), 8883 (MQTT). **No HTTP control API** — the web port serves only the camera page. |
| P1S open ports | 8883 only. |

So a future local-MQTT implementation needs no new protocol work — same topics, same payloads, same
signing. **Signing works over the existing cloud connection, so no LAN transport is required for it.**

### 3.5 Plaintext vs encrypted — summary

| Path | Encrypted? | Notes |
|---|---|---|
| Bambu cloud MQTT :8883 | **TLS** | verification disabled in our client (§2.4) |
| Bambu cloud API (HTTPS) | **TLS** | normal verification |
| Bambu camera `bambus://` :6000 | **TLS, self-signed** | `InsecureSkipVerify` required; see §4.1 cipher trap |
| Bambu camera RTSPS :322 | **TLS (RTSPS)** | self-signed |
| Snapmaker U1 :80 | **plaintext HTTP** | access code travels in URL + header |

There is **no AES-layer payload encryption** anywhere in this codebase. The LAN-MQTT AES scheme that
some Bambu libraries implement is not used here because we never speak local MQTT. **[CODE]**

---

## 4. Camera subsystem

Three completely different camera protocols across three printers. This is where the most
expensive reverse engineering happened.

### 4.1 Bambu `bambus://` — binary TLS on port 6000 (P1S, A1)

**Not HTTP.** Raw TLS socket, custom binary framing. **[CONFIRMED]**

```
URL: bambus://<ip>:6000?token=<access_code>
```

Sequence: TCP dial → TLS handshake → send 80-byte auth packet → receive continuous frames.

**Auth packet — 80 bytes, all little-endian:**

| Bytes | Content |
|---|---|
| 0–3 | `uint32 0x40` (64) |
| 4–7 | `uint32 0x3000` (12288) |
| 8–11 | `uint32 0` |
| 12–15 | `uint32 0` |
| 16–47 | username `"bblp"`, NUL-padded to 32 |
| 48–79 | access code, NUL-padded to 32 |

`0x40` ≠ 80 and `0x3000` are **unexplained magic constants**. Don't "clean them up". **[CODE]**

**Frame format:** 16-byte header then JPEG.

| Bytes | Content |
|---|---|
| 0–3 | `uint32 LE` payload size |
| 4–7 | `0x00` |
| 8–11 | `0x01` |
| 12–15 | `0x00` |

Payload is raw JPEG (`FF D8 FF …`). Observed rate **~1 FPS**. **[CONFIRMED]**

> **GOTCHA — the H2S/O1S cipher trap.** **[CONFIRMED]** (`bambu_stream.go:120-136`) H2S/O1S
> printers on port 6000 offer **only** `TLS_RSA_WITH_AES_256_GCM_SHA384` — a static-RSA suite that
> Go **excludes from its defaults**. You must hand-list it (and re-list Go's other defaults
> explicitly, or you lose forward secrecy on everything else). Without this the handshake simply
> fails. This is a hard-won one.

> **GOTCHA — framing is length-prefix only, with no validation.** Header bytes 4–15 and the JPEG
> `FFD8`/`FFD9` markers are documented but **never checked**; only `payloadSize == 0` errors. There
> is **no resync logic and no max frame size**, and the read deadline is **cleared after the first
> frame**. A desync or a silently-stalled camera therefore blocks forever and only recovers via
> reconnect backoff (1 s → 30 s). If you extend this, add bounds + resync. **[CODE]**

Other notes: the buffer keys connections by `host:port` only, so a **rotated access code reuses the
stale connection** until restart. **[CODE]** No single-concurrent-connection limit was ever
confirmed — PLAN.md asserts one but nothing in code or testing backs it up. **[UNKNOWN]**

### 4.2 Bambu RTSPS :322 + go2rtc (H2S, X1)

**[CONFIRMED]** URL form:

```
rtsps://bblp:<access_code>@<lan_ip>:322/streaming/live/1
```

Username is the literal `bblp`; password is the access code; **port 322**; path
`/streaming/live/1`. Requires **"LAN Only Liveview"**. `/streaming/live/2` **404s** on a real H2S —
do not add a second stream without `ffprobe`-confirming against hardware.

The camera emits **H264 over RTSPS**, which browsers can't display directly. Architecture:

```
printer --RTSPS:322--> go2rtc (per-instance subprocess) --ffmpeg--> MJPEG --> browser
```

Implementation facts **[CODE]** (`camera/go2rtc.go`):

- One **go2rtc subprocess per camera instance**, launched as `go2rtc -c <tmpfile>.yaml`, found via
  `PATH`. Each instance gets its own ports: API/HTTP `basePort + offset` (**basePort defaults to
  1984**), RTSP listener `127.0.0.1:18554 + offset`, from a monotonic offset counter.
- Readiness is detected by polling go2rtc's `/api/streams` for up to 10 s.
- **There is no supervisor.** A crashed go2rtc instance is *never* auto-restarted; recovery is the
  manual `camera-reconnect` endpoint (stop, then lazy restart on next request).
- **Why a dedicated RTSP listener per instance:** go2rtc's `ffmpeg:` pseudo-source reads back over
  RTSP; if two instances both default to `:8554` the second bind fails and its transcode is left
  with no loopback URL. Offsets are never reused, so ports march upward.
- Config declares two streams per camera:
  ```yaml
  <key>:         rtsps://bblp:CODE@host:322/streaming/live/1
  <key>_mjpeg:   ffmpeg:<key>#video=mjpeg
  ```
  The `_mjpeg` hop exists because go2rtc's MJPEG/snapshot endpoints require a JPEG/RAW source.
- Consumer URLs: `http://127.0.0.1:<port>/api/stream.mjpeg?src=<key>_mjpeg` and
  `/api/frame.jpeg?src=<key>_mjpeg`.
- **Stream key = `host:port` + path with `/`→`_`**, e.g. `10.0.0.50:322_streaming_live_1`. The path
  **must** be included: multiple feeds share a host:port and would otherwise collide and silently
  shadow each other.
- Instance lifecycle is **decoupled from the HTTP request context** — a browser navigating away must
  not kill the stream. Stopping uses SIGKILL and removes the config.
- **`go2rtc` missing from `PATH` ⇒ 502.** This is the single most common "camera is broken" false
  alarm: it works in Docker (binary baked at `/usr/local/bin/go2rtc`) and 502s when you run a bare
  local Go binary. **[CONFIRMED]** (K-098)

> **GOTCHA — false-positive camera error banners.** **[CONFIRMED]** Browsers cancel in-flight image
> loads constantly; those surface as `context canceled` / `context deadline exceeded` and were being
> reported as auth failures. Fix: use a dedicated 15 s timeout context instead of the request
> context, and filter `context.Canceled`/deadline errors out of the error state. **[CODE]**

> **GOTCHA — the K-060 "flicker" was a successful response, not a failure.** **[CONFIRMED]** The
> frame handler served a **gray 1×1 placeholder with `200 OK`** on any transient failure. The
> browser's `onload` fired happily, so the image "loaded" as gray — a success that looked like a
> broken stream. Fix: keep a last-good-frame cache and only serve frames that actually parsed as
> JPEG. When a stream "flickers", check whether you are being handed a *valid* placeholder.

**Proxy surface** **[CODE]**: `GET /api/camera/proxy?url=…` and `GET /api/camera/frame`
(`server.go:528-529`). Dispatches on URL scheme: `http`/`https` passthrough, `rtsps` → go2rtc,
`bambus` → frame buffer; anything else → 501. **There is no auth gate on these routes** — only
`noCache` middleware. This is LAN-trust by design; if you expose the dashboard beyond the LAN, the
camera proxy is an unauthenticated window into your printers.

> **Two latent camera bugs, unfixed.** **[CODE]**
> 1. `go2rtc.go:186` passes the **caller's HTTP request context** into `waitForReady`. A browser
>    aborting during the 10 s readiness window kills the instance that was just starting and returns
>    502 — the one leak in an otherwise-correct "never inherit request contexts" design.
> 2. The helpful `501 install go2rtc` branch (`proxy.go:89-93`) is **unreachable in production**
>    because the server always allocates a non-nil manager. A missing binary therefore surfaces as
>    an opaque 502 — which is exactly how K-098's "H2S camera is disabled" misdiagnosis happened.
>
> Also: three comments in `server.go` and `proxy.go` still describe a `/streaming/live/2` toolhead
> camera that **does not exist on real hardware**. Ignore them.

### 4.3 TUTK P2P (remote camera) — investigated, **not shipped**

Bambu's remote camera uses proprietary **TUTK P2P**. Credentials come from
`POST /v1/iot-service/api/user/ttcode` → `{ttcode, authkey, passwd, region}` (plus `stream_key`,
`stream_salt`, `channel_name`, `app_id` in the raw response). **[CODE]**

**`GetTTCode` and the entire TUTK chain have no production caller.** **[CONFIRMED]** The official
plugin (`libBambuSource.so`, `liblive555.so`, fetched in the Dockerfile from
`api.bambulab.com/v1/iot-service/api/slicer/resource?slicer/plugins/cloud=02.07.00.00` with
`X-BBL-Client-*` headers) is downloaded and `LD_LIBRARY_PATH`'d, and there is cgo binding code —
but LAN-only was sufficient and the feature was abandoned.

**Costs incurred here, so you don't repeat them:** the cgo binding was written against arm64 only and
**never compiled for amd64** until multi-arch CI turned it on, which then produced three separate
build breaks: a Go **reserved-keyword** struct selector (`info.type` → cgo renames it `._type`), a
missing `#include <unistd.h>` for `usleep`, and an **anonymous C union** (`Bambu_StreamInfo.format`)
that cgo maps to `[12]byte` with no Go representation, requiring an `unsafe.Pointer` reinterpret.
If you pick this up, expect that class of problem. **[CONFIRMED]**

### 4.4 Snapmaker U1 camera

**[CONFIRMED]** Plain HTTP on the printer's own port (default **80**):

- `/webcam/stream.mjpg` — built-in webcam, MJPEG
- `/screen/snapshot` — touchscreen snapshot, **PNG** (interactive page at `/screen/`)

Access code appended as `?access_code=` when configured. The U1 runs **Fluidd** (Klipper web UI) on
port 80. `PLAN.md`'s guess of `/api/v2/camera/stream` on port 8080 is **stale — do not use it**.

---

## 5. Snapmaker U1 (Paxx firmware)

### 5.1 What Paxx actually is

**[CONFIRMED]** (commit `4973f0a`) The U1 runs **Klipper + Moonraker** underneath. The original
assumption — a flat "Paxx REST API" — was wrong and the client was rewritten. The practical
consequence: the API surface is a **hybrid** of OctoPrint-style shims and true Moonraker-native
routes, and you must know which is which.

Default port is **80**, not 8080 (confirmed by the user against real hardware; commit `a28d468`).
**[CONFIRMED]**

### 5.2 Endpoint reference

| Method | Path | Family | Purpose |
|---|---|---|---|
| `GET` | `/api/printer` | OctoPrint-style | temps + state (primary telemetry) |
| `POST` | `/api/print/pause` | OctoPrint-style | pause |
| `POST` | `/api/print/resume` | OctoPrint-style | resume |
| `POST` | `/api/print/cancel` | OctoPrint-style | cancel |
| `POST` | `/api/printer/command` | OctoPrint-style | used for skip-object |
| `GET` | `/printer/objects/query?print_stats&virtual_sdcard&gcode_move` | **Moonraker** | file, layers, progress, position |
| `POST` | `/printer/gcode/script` | **Moonraker** | `{"script":"G28"}` — all control |
| `WS` | `/websocket` | Moonraker | nominally status push |
| `GET` | `/webcam/stream.mjpg` | Fluidd | camera |
| `GET` | `/screen/snapshot` | Fluidd | touchscreen PNG |

**Auth:** access code sent **both** as `?access_code=` query param **and** `X-Access-Code` header.
No session, no expiry. **[CODE]**

> **LATENT BUG — auth corrupts Moonraker query URLs.** **[CODE]** `buildRequest` appends
> `"?access_code=…"` unconditionally. The objects-query path already contains a `?`, so with an
> access code configured the URL becomes
> `…/query?print_stats&virtual_sdcard&gcode_move?access_code=X`, which corrupts the last object
> name. Untested, silently degrades progress/layers/Z-position. If you touch Snapmaker auth, fix
> this (use `&` when a query already exists).

### 5.3 The WebSocket is effectively decorative

**[CODE]** (confirmed by reading the client end to end)

- No Moonraker **JSON-RPC `printer.objects.subscribe`** handshake is ever sent. Moonraker's
  `/websocket` emits nothing meaningful without it.
- The read loop parses frames as the **flat `/api/printer` shape**, not `notify_status_update`.
- `wsReadWait` (10 s) is **shorter than** `wsPingPeriod` (30 s), so a pong can never rescue the read
  deadline — a quiet connection dies ~10 s after any frame.
- The **3 s REST poll ticker runs unconditionally**, regardless of WS state, because the WS path
  doesn't carry `gcode_move` data at all.

**Net effect: all real data comes from 3-second REST polling.** The WS is dead weight, and its
errors are never logged or surfaced. If you're debugging "why isn't Snapmaker pushing", this is why.

### 5.4 Paxx / U1 hardware quirks

| Quirk | Detail | Marker |
|---|---|---|
| **LED state is unqueryable** | Moonraker's `led` object query **always returns `null`** for `cavity_led`. Real state cannot be read. We store the last *commanded* state instead. | [CONFIRMED] |
| **`cavity_led` is RGB-driven** | It looks white via R=G=B=1. `SET_LED LED=cavity_led WHITE=1` is a **no-op on real hardware**. You must set all four channels. | [CONFIRMED] |
| **`G28` takes 30+ s** | Needs a dedicated **120 s** HTTP client; the normal 10 s client times out. | [CONFIRMED] |
| **`homed_axes` not exposed** | Paxx doesn't report it in `gcode_move`. Homing is inferred: `G28` success ⇒ homed; print start ⇒ not homed; `"Must home"` error ⇒ not homed. | [CONFIRMED] |
| **Don't infer homed from Z** | Paxx **parks Z at ~9.9 mm** after homing — far from the bed, so Z position tells you nothing. | [CONFIRMED] |
| **`"Must home"` errors** | Real string: `Must home Z axis first: 5.130 4.947 199.982 [11251.005]`. Klipper keeps saved Z but still demands a fresh home. Detect the substring, return 400, don't mark homed. | [CONFIRMED] |
| **`CurrentFile` never clears** | Moonraker keeps reporting the last filename; there is no idle-clear. Known open issue (K-091). | [CONFIRMED] |
| **State text is OctoPrint-cased** | Matching is case-sensitive on `"Operational"`, `"Complete"`, `"Cancelled"`. Anything else → `unknown`, which then hard-blocks jog/home. | [CODE] |

**State mapping:** flags take priority over text — `printing` → `printing`, `paused` → `paused`,
`error` → `error`; else text `Operational`→`idle`, `Printing`→`printing`, `Paused`→`paused`,
`Error`→`error`, `Complete`/`Cancelled`→`complete`. **[CODE]**

> **GOTCHA — Moonraker returns HTTP 200 with an embedded error.** **[CONFIRMED]** A body like
> `{"error":{"message":"Klippy Not Connected"}}` arrives with a **200** status. Only the
> `sendGCode` path checks for this. `doCommand` (pause/resume/cancel), `SkipObject`,
> `fetchStatus`, and `fetchQueryStatus` do **not** — so a Klipper-side failure on pause reads as
> success. This is tracked as K-079 and still open. Assume any Moonraker call can lie this way.

**Timeouts:** 10 s status/commands, 120 s gcode, 5 s WS handshake, 3 s poll, 15 s WS reconnect.
**Zero retries anywhere.** **[CODE]**

### 5.5 Skip-object on U1 is a no-op

**[CODE]** `SkipObject` POSTs to `/api/printer/command` with a **nil body** — no G-code at all.
There is no Klipper/Moonraker `EXCLUDE_OBJECT` integration anywhere in the codebase, yet the local
skip counter still increments, so **the UI reports a skip that the printer never performed.** If
skip-object matters to you, this is real work, not a config issue.

---

## 6. AMS / filament

### 6.1 Wire structure

```jsonc
"ams": {
  "ams": [{
    "id": "0",                    // STRING. 0-3 for AMS/AMS2Pro; 128-135 for AMS-HT [DOC]
    "humidity": "4",              // index 0-5, lower=drier. ABSENT on AMS 1
    "humidity_raw": "23",         // %. ABSENT on AMS 1
    "temp": "25.0",               // °C. ABSENT on AMS 1
    "tray": [{
      "id": "0",                  // STRING
      "state": 3,                 // NUMBER: 0 empty, 2 loaded, 3 ready, 10 reading, 11 loaded+data
      "tray_type": "PLA",
      "tray_color": "000000FF",   // RRGGBBAA
      "tray_info_idx": "GFA00",   // Bambu profile ID
      "nozzle_temp_min": "190",   // STRING
      "nozzle_temp_max": "230",   // STRING
      "remain": 750,              // NUMBER, mm; -1 unknown
      "tray_weight": "250",       // STRING, grams
      "tag_uid": "8A160AB5..."
    }]
  }],
  "tray_now": "0",                // (ams_id*4)+tray_id; 254=external, 255=none
  "ams_exist_bits": "1",
  "tray_exist_bits": "e"
}
```
**[CODE]** for what we parse; **[DOC]** for AMS-HT id ranges and bit-mask semantics.

> **GOTCHA — the type chaos is a parse bomb.** `id` and `nozzle_temp_*` and `tray_weight` are
> **strings**; `state` and `remain` are **numbers**. A type mismatch aborts the **entire report
> parse**, not just AMS — one wrong type kills all printer status. **[CODE]**

`tray_now` encoding: `(ams_id * 4) + tray_id`. AMS0/slot3 → 3; AMS1/slot0 → 4. `254` = external
spool, `255` = none, `-1` = unknown. `ams_exist_bits`/`tray_exist_bits` are hex-ish bitmasks
(`"e"` = trays 1,2,3) but are **parsed and never read**, and the spec itself admits bit meaning
"may vary by firmware". **[CODE]**

### 6.2 P1S delta updates — the expensive bug

**[CONFIRMED]** P1S sends **delta** AMS updates; H2S sends **full** updates.

A P1S delta may arrive as `ams` present with an **empty inner `ams` array** (only `tray_now`
changed), or with unit objects that **lack the `tray` array** (only humidity changed). Naive
"replace the cached AMS" logic therefore **wipes** filament type/color/remaining on nearly every
report — and with `omitempty` the field then vanishes from the API response entirely.

Fix (commit `9d9a97d`): merge instead of replace.
- Units merged by `ID`; trays merged by `Index`; cached entries absent from the delta are **retained**.
- Empty `tray` array in the delta ⇒ keep cached trays wholesale.
- `Humidity`/`HumidityRaw`/`Temp` use `firstNonEmpty(new, cached)`.
- `ActiveTrayID` is written **only** when `tray_now` is non-empty (the old `else → -1` branch was
  deleted).

> **Periodic `pushall` — nuance that matters.** **[CONFIRMED]** Status can go stale forever after
> connect, notably on the **H2S, which pushes almost nothing to the cloud broker unprompted**. A
> periodic check now re-requests a full `pushall`, but **only after a genuinely quiet window** —
> because an indiscriminate `pushall` can **stall P1P-class hardware**. Do not add a naive timer.
> Combined with the AMS merge cache, a field missed at startup still persists until the next
> re-push.

### 6.3 AMS commands — documented, none implemented

`[DOC]` — verify before use. **Zero** of these exist in the codebase.

```jsonc
{"print":{"command":"ams_change_filament","target":0,"curr_temp":0,"tar_temp":220}}
{"print":{"command":"unload_filament"}}
{"print":{"command":"ams_control","param":"resume"}}   // resume|done|pause|reset
{"print":{"command":"ams_filament_setting","ams_id":0,"tray_id":0,
          "tray_type":"PLA","tray_color":"FF0000FF",
          "nozzle_temp_min":190,"nozzle_temp_max":230}}
```
`ams_filament_drying` is named with a **65 °C max** but no payload is recorded anywhere. **[UNKNOWN]**

Note the live risk: sending newer AMS commands to stock P1S firmware may trip the client-version
gate HMS from §2.9.

### 6.4 Snapmaker U1 filament — specced, zero implemented

**[DOC]** (`.fleet/research-notes.md`) 4 toolheads, 2 feeder modules × 2 channels; RFID recognition.

```
FEED_AUTO MODULE=left CHANNEL=0 LOAD=1
FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1 STAGE=prepare|doing|cancel
GET /printer/objects/query?filament_feed      -> channel_state, channel_error
GET /printer/objects/query?filament_detect    -> MAIN_TYPE, ARGB_COLOR, HOTEND_MIN/MAX_TEMP
```
`channel_state`: `wait_insert → preload_prepare → preload_feeding → preload_finish`;
`load_prepare → load_homing → load_picking → load_feeding → load_heating → load_extruding →
load_flushing → load_finish`; `unload_prepare → unload_finish|unload_fail`.
`channel_error`: `ok, no_filament, residual_filament, motor_speed, wheel_speed, timeout, move,
move_home, move_switch, move_extrude, custom_gcode, heat`.

**None of this is implemented.** Also: U1 reports **no** remaining length/weight at all.

---

## 7. Cross-printer control matrix

Both Bambu and Snapmaker use **G-code as the control substrate**; the difference is the envelope.

| Control | Bambu (cloud MQTT) | Snapmaker U1 (Moonraker) |
|---|---|---|
| Bed target | `gcode_line` `M140 S{n}\n` | `POST /printer/gcode/script {"script":"M140 S{n}"}` |
| Nozzle target | `gcode_line` `M104 S{n}\n` | same shape |
| Chamber target | `{"print":{"command":"set_ctt",…}}` — **H2S/X1 only** | n/a |
| Light | `{"system":{"command":"ledctrl",…}}` | `SET_LED LED=cavity_led RED=1 GREEN=1 BLUE=1 WHITE=1` |
| Home all | `gcode_line` `G28\n` | `{"script":"G28"}` |
| Jog | `gcode_line` `G91\nG1 X{n} F{s}\nG90\n` | same script, no JSON wrapper |
| Fans | `M106 P{0=part,1=aux,2=chamber} S{0-255}` | `M106 S{n}` |
| Pause/resume/stop | native `print` commands | `POST /api/print/{action}` |
| Skip object | `project_file` + `param:"skip_object"` | **not implemented** (§5.5) |

**Jog convention worth copying:** switch to relative (`G91`), emit **one** `G1` containing only the
axes with non-zero deltas (omit the axis term entirely rather than emitting `Z0`), then restore
`G90`. Leaving the printer in relative mode is a nasty latent bug. **[CODE]**

**Firmware safety:** movement G-code is **rejected during active prints** on Bambu — it only works
when idle. **[DOC]** Gate movement behind an idle check in your UI and expect a hard error otherwise.

**Implemented vs not, in this codebase:** Snapmaker `SetBedTemp`/`SetNozzleTemp`/`SetChamberTemp`
return `"not implemented"` errors even though the Moonraker gcode path exists. **[CODE]**

---

## 8. Command verification & signing

**This is the single most important protocol discovery in the project, and it overturned an earlier
conclusion recorded here.** Read it before concluding a Bambu command "doesn't work".

Bambu firmware released after roughly **January 2025** cryptographically verifies **every MQTT command
in the `print` namespace**. An unsigned frame is rejected with `err_code 84035343`
(`mqtt message verify failed`). **[CONFIRMED]** (K-108/K-109, measured on H2S-001 fw `ver=20008`)

> **Developer Mode is NOT required.** It only makes the signature envelope optional. If a printer
> rejects your `print` commands, the answer is signing — not Developer Mode.

### 8.0 Which printers are affected

| Printer | `print` namespace | Why |
|---|---|---|
| P1S (this fleet) | accepts unsigned | firmware predates the gate |
| H2S (this fleet) | **rejects unsigned**, on both transports | verification active |

**The P1S will start rejecting unsigned frames as soon as it is updated.** Do not treat the P1S's
good behaviour as permanent. **[CONFIRMED]**

`system` (e.g. `ledctrl`) and `pushing` (e.g. `pushall`) are **not** verified and stay unsigned.
**[CONFIRMED]** — the light always worked while movement never did, which is exactly this split.

### 8.1 The signing envelope

```jsonc
// 1. Build the payload. user_id is a TOP-LEVEL SIBLING, not inside "print".
{ "print": { "sequence_id": "5000", "command": "gcode_line", "param": "G28\n" },
  "user_id": "1578637590" }

// 2. Sign the BYTE-EXACT JSON of that object. No canonicalisation, no re-marshalling.
sign_string = base64( RSA-SHA256( privateKey, payload ) )

// 3. Append the header and publish the result unmodified.
{ ...payload, "header": {
    "sign_ver": "v1.0", "sign_alg": "RSA_SHA256",
    "sign_string": "<base64>",
    "cert_id": "<lowercase hex serial>CN=<issuer CN>",
    "payload_len": "<utf8 byte length as a JSON STRING>" } }
```
**[CONFIRMED]** (`signing.go`)

Four requirements, each of which produces a generic "verify failed" if violated:

| Requirement | Failure mode |
|---|---|
| **`payload_len` must be a JSON *string*** | A number yields `84033547` — **indistinguishable from a bad certificate.** This one detail cost hours of misdirected debugging. |
| **`cert_id` = lowercase hex X.509 *serial* + `CN=` + issuer CN** | It is **not a hash**. Bambu's own IDs are 32 hex because their serials are 16 bytes. Every fingerprint-based theory was wrong. |
| **`sequence_id` must be a string ≥ 2000** on signed frames | Below the floor the printer answers `84033545` *even with a trusted cert*. A fixed floor, not a high-water mark. Studio uses a 20000–29999 window. |
| **Signature covers the exact wire bytes minus `header`**, including the trailing `user_id` sibling | The printer re-checks byte-exact bytes, so `sign()` **splices** the header on rather than re-marshalling. |

### 8.2 Error-code ladder

`84033543`–`84033548` are all one verification family, and each stage is distinguishable.
**[CONFIRMED]**

| err_code | Meaning |
|---|---|
| `84033543` | No signature present → triggers auto-detection of signing need |
| `84033544` | Contested: file-verify stage (bad `md5`, plaintext `url`, `ftp:///`) **or** `sequence_id` reuse across restarts |
| `84033545` | "need reset device pub key" — signature valid but `cert_id` not in the trust store; **also** returned for a trusted cert with `sequence_id < 2000` |
| `84033546` | Required header field missing or malformed (e.g. no `payload_len`) |
| `84033547` | Signature rejected — wrong key, wrong signed bytes, **or numeric `payload_len`** |

### 8.3 The printer's trust store

- `security.app_cert_install` installs a cert: `{command, sequence_id, app_cert (PEM), crl (PEM)}`.
  **The install command itself is not verified** — that is what makes bootstrapping possible.
  **[CONFIRMED]**
- `crl` must be **non-empty** (an empty one answers `FAILURE / "crl value is null"`) but **its
  signature is not validated**. A self-minted CRL with issuer CN
  `GLOF3813734089.bambulab.com` is accepted. **[CONFIRMED]**
- Install **adds**; there is no command to remove a single cert. **[CONFIRMED]**
- **The store lives in printer RAM and a reboot clears it.** So `app_cert_install` must run on
  **every connect**, not once at setup. Right after a reboot `app_cert_list` answers
  `reason: "device-endreaddevcertfailed"`. **[CONFIRMED]**
- `security.app_cert_list` requires `"type":"app"` and a **string** `sequence_id`. On the H2S it
  lists Bambu's shared app certs, e.g.
  `9bed8c27b4bf69582d58f11abaaad99fCN=GLOF3813734089.bambulab.com` — the same ID appears on different
  printers, confirming these are **shared app certs**, not per-device. **[CONFIRMED]**
- A cert only needs to be *installed*; it does not have to have been provisioned through Bambu's cloud.
  An arbitrary self-signed chain installs fine and appears in the list — but signing with it still
  yields `84033547`, so **verification does enforce the chain**. The own-key shortcut does not work.
  **[CONFIRMED]**

### 8.4 The credentials

The shared Bambu app certificate and private key have been **public since January 2025**, extracted
from Bambu Connect and shipped embedded in every copy of it. They are **not secret and not
user-specific**. Serial in use: `ee3cac0712ceeacda8b7fbc42e74ba2c`; **its 2025-12-12 expiry is not
enforced by the printer.** **[CONFIRMED]**

Implementation: embedded in the binary, cached to the data directory on first use as
`bambu_app_cert.pem` / `bambu_app_key.pem` / `bambu_app_crl.pem` (same dir as the token store), so an
operator can inspect or replace them without a rebuild. Anything on disk takes precedence; a cache
write failure never disables signing. **[CODE]**

> **Not signing material, despite looking like it:** `BambuStudio.app/.../cert/printer.cer` is the BBL
> CA bundle (`BBL CA`, `BBL CA2 RSA`, `BBL CA2 ECC`) and `slicer_base64.cer` is a `*.bambulab.com`
> DigiCert TLS pin that expired in 2022. **Neither contains a private key.** The downloadable network
> plugin ships seven DLLs with no cert/key files, and its strings are obfuscated —
> `client_auth_secret` is not recoverable by static search, only from a running process. Not needed
> for signing. **[CONFIRMED]**

### 8.5 How signing is adopted at runtime

Sentinel: the **first** `print` command goes out **unsigned**; an `84033543` rejection flips
`signRequired` and retries signed, so later commands are signed up front. Printers predating the gate
never pay a rejected command, so the P1S is behaviourally unchanged. `commandRejectedError` carries
the numeric `err_code` so callers branch on the verification family instead of matching message text.
**[CODE]**

### 8.6 Command acks — an earlier conclusion here was WRONG

**[CONFIRMED]** (K-109) Earlier research in this repo concluded that firmware acks were infeasible on
cloud MQTT. **That was incorrect.** The firmware **does** reply — on the same
`device/{serial}/report` topic — with `result` / `reason` / `err_code`.

Consequences now implemented:
- Commands register a **waiter before publishing**; a non-success ack becomes an error. Rejections
  surface as e.g. `printer rejected <cmd>: mqtt message verify failed (err_code …)`.
- **Ack timeouts stay non-fatal**, so printers that never ack are not falsely blamed.
- **Acks must not fall through into status parsing.** An ack carries no `home_flag`, which previously
  read as zero and **silently marked a homed printer unhomed**. **[CONFIRMED]** — a subtle and nasty
  one.

So §2.5's "no ack topic" should be read as: *there is no separate ack topic* — acks arrive inline on
the report topic and must be demultiplexed from status.

### 8.7 What was ruled out (so nobody re-tests it)

Identical failure over **cloud and LAN**, so transport, encoding, topic, and payload are all excluded.
Eleven `gcode_line` payload variants (absent / string / numeric `sequence_id`, `id`, `mc_action`,
`account`, `version`, missing trailing newline, alternate G-code) all produce the **same** rejection
when unsigned — **payload shape does not influence verification.** Chain-of-trust theories were
refuted: a real Bambu-chain cert failed identically while `payload_len` was still numeric.
**[CONFIRMED]**

---

## 9. Known unknowns & blind spots

Explicitly unresolved. Do not fill these in from intuition.

| Item | Status |
|---|---|
| Local (LAN) MQTT support | Protocol facts now **fully measured** (see §3.4) but still **not implemented**. |
| Does the H2S report homing via `home_flag` bit 0? | **Open.** H2S `home_flag` reads `0xc0675590` with bit 0 clear, and after an *accepted* `G28` the flag did not flip within 60 s. May need a parser change. |
| `sequence_id` reuse across restarts | `84033544` is contested between the file-verify stage and sequence reuse; which one applies is unresolved. |
| Container token persistence | Known defect: the container cannot write `bambu_token.json` on the mounted volume (host-owned, container runs as `app`), so token refresh does not persist. |
| AMS-HT unit IDs 128–135 | Documented, unmodelled. Breaks the `(id*4)+i` global-ID math (produces IDs ≥512 that can never match `tray_now`). |
| `ams_exist_bits` / `tray_exist_bits` | Parsed, never consumed; bit meaning "may vary by firmware". |
| `ams_filament_drying` payload | Only the name and 65 °C cap are recorded. |
| Does cloud `dev_access_code` equal the LCD access code? | Never tested. If yes, camera setup automates fully. |
| Bambu port-6000 concurrent-connection limit | Asserted in PLAN.md, never verified. |
| Snapmaker 200-with-embedded-error on non-gcode paths | Known gap (K-079), un-audited. |
| Snapmaker filament/RFID integration | Fully specced, zero implemented. |
| Snapmaker `CurrentFile` clearing | Open bug (K-091). |
| P1S camera regression coverage | Never re-tested since the H2S go2rtc refactor (K-040). |
| Snapmaker WS ever actually delivering | Effectively dead (§5.3); never confirmed working on hardware. |

### 9.1 Latent bugs and untested assumptions *in our own code*

Found by a full audit of the test suite. Read this before trusting a green test run as proof that a
protocol detail is right.

- **No test asserts any MQTT wire fact.** The mock publish/subscribe functions discard topic and QoS
  (`func(_ string, _ byte, _ bool, payload interface{})`). Topics, QoS 0, keepalive 30 s, client-id
  format, and `InsecureSkipVerify` live only in `client.go:255-268,365,734`. **A wrong-topic or
  QoS-change regression would pass the whole suite.** **[CODE]**
- **Model-specific behavior is not actually exercised.** The test client config has no `Model` field,
  so every "P1S"/"H2S" label in a test name is a comment, not an enforced code path. `handleReport`
  never gates parsing on model.
- **Parsed-then-dropped payload.** `wifi_signal`, `stg_cur`, `stg_total`, `lifecycle`,
  `ams_exist_bits`, `tray_exist_bits` are parsed and never read. `gcode_file_time` has no struct
  field at all. `isErrorState()` is tested but is dead code — and it returns `true` for `IDLE`,
  which would be wrong if it were wired up.
- **`chamber_target_temper` can only ever be set once.** The direct-wire field is applied only when
  the *cached* target is `nil` (`client.go:590`), so after the first value arrives, later ones are
  ignored. Untested. **[CODE]**
- **Out-of-range `chamber_temper` suppresses the `info.temp` fallback** (they are `if`/`else if`),
  so a bad direct reading blocks the good packed one. **[CODE]**
- **Bed and nozzle temperatures get zero range validation** — only chamber values are checked.
  **[CODE]**
- **AMS merge is asymmetric.** Unit-level scalars merge via `firstNonEmpty`, but whole tray objects
  replace by `Index`. "H2S full updates make merging a no-op" is only true because H2S happens to
  send every field every time.
- **`set_ctt` has no test at all.** No `mclogs` fixture exists anywhere.

**Practical rule:** the test suite proves *our parsing* is stable; it does **not** prove the wire
format is correct. For anything load-bearing, verify against hardware or a captured payload.

**Two documentation traps to avoid inheriting:**


- `README.md` still says Bambu printers use "**LAN MQTT protocol**". That is **wrong** — this
  deployment is cloud MQTT. `PLAN.md` is correct. **[CONFIRMED]**
- `PLAN.md` §4.2 (Snapmaker) is **stale**: it lists port 8080, `/ws`, and `/api/v2/camera/stream`,
  all of which were disproven (real: port 80, `/websocket`, `/webcam/stream.mjpg`).

---

## 10. Our own outward surface (for integrators)

The dashboard exposes a vendor-neutral model. Key abstractions worth reusing:

- **`PrinterStatus`** (`internal/printers/interface.go`) — the normalized state. Notable fields:
  `state` ∈ `idle|printing|paused|error|complete`; all temperatures are **nullable pointers**;
  `has_chamber` is an explicit **capability flag**, never inferred from a nil temperature;
  `homed` and `light_on` are nullable (nil = unknown *or unsupported*); `active_tray_id` uses the
  Bambu encoding with `-1` = unknown.
- **Capability flags beat inference.** `HasChamber` is set by the driver from the model code, because
  a nil `ChamberTemp` only means "not reported this cycle". **[CODE]**
- **`SetHomed(*bool)`** exists on the interface purely because some firmware (Paxx) can't report
  homed state — a vendor-specific hole poked through a neutral interface. On Bambu it's a no-op.
- HMS dismissals and skipped-object counts are **in-memory only**, deliberately: a dismissal only
  needs to suppress re-notification while the condition is live, and dismissals are reconciled away
  once a code stops being reported so a re-firing code isn't permanently silenced. **[CODE]**

---

## 11. Provenance

Where each hard fact came from, so you can judge it:

- **Real-hardware observation:** camera port-6000 protocol and cipher requirement; RTSPS:322 and the
  `/live/2` 404; packed `info.temp`; P1S `subtask_name`; P1S AMS `state:0` and delta behavior;
  `ledctrl` required fields; Snapmaker port 80, `cavity_led` RGB, `G28` 30 s, Z-park 9.9 mm,
  `"Must home"`, Moonraker 200-with-error; LAN-Mode cloud silence; **the full command-signing
  mechanism, err_code ladder, trust-store semantics and LAN protocol facts (K-108, measured on
  H2S-001 fw `ver=20008` and P1S-001)**; `print_error=83935249` as an SD-card advisory (K-107).
- **Conclusions this project reached and later REVERSED — treat any "impossible" claim here with
  suspicion:** ① "firmware acks are infeasible on cloud MQTT" → they arrive inline on `report`.
  ② "HMS `0500-0500-0001-0007` has no app-side fix" → it is the signature gate; signing fixes it.
  ③ "`sequence_id` is probably inconsequential" → mandatory ≥ 2000 on signed frames.
- **Upstream community research, unverified here:** HMS module/severity tables (pybambu), AMS command
  payloads (OpenBambuAPI / ha-bambulab), AMS-HT addressing, Moonraker/Klipper semantics, filament
  feed state machines.
- **Original assumption that proved wrong:** "U1 exposes a flat Paxx REST API" (→ Moonraker);
  "chamber temp needs /100000 scaling" (→ packed int); "H2S has two cameras" (→ one);
  "P1S AMS `state` indicates load" (→ always 0).

---

*Keep this document current. When you learn something new about these printers, add it here with a
confidence marker — this is the shared memory that saves the next person a week.*
