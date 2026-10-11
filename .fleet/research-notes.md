# Research notes

Read-only research write-ups migrated verbatim from the old
`.agent/STATE-OVERFLOW.md` system (F-057, 2026-07-16) — dense
protocol/API reference material that active backlog cards depend on, kept
separate from `.fleet/notes-archive.md`'s pure historical narrative
because future implementers of K-032/K-033/K-034/K-037/K-081/K-082 will
actually need to consult it.

## K-054 — Full printer controls feasibility research
READ-ONLY research, no code changes. User asked: are Z/X/Y movement,
target temp setting (all heaters), light on/off, status, camera,
pause/resume/stop/skip-object all feasible across all 3 printers (Bambu
P1S, Bambu H2S, Snapmaker U1)?

**ANSWER: YES — every requested control is feasible on all 3 printers.**

### Cross-printer capability matrix

| Control | Bambu P1S/H2S (Cloud MQTT) | Snapmaker U1 (Moonraker REST) |
|---------|----------------------------|-------------------------------|
| Status | Already done | Already done |
| Camera | Already done (bambus:// / rtsps://) | Already done (/webcam, /screen) |
| Pause | Already done | Already done |
| Resume | Already done | Already done |
| Stop/Cancel | Already done | Already done |
| Skip Object | Already done | Already done |
| Set bed target temp | `{"print":{"command":"gcode_line","param":"M140 S{temp}\n"}}` | `POST /printer/gcode/script` `{"script":"M140 S{temp}"}` |
| Set nozzle target temp | `{"print":{"command":"gcode_line","param":"M104 S{temp}\n"}}` | `POST /printer/gcode/script` `{"script":"M104 S{temp}"}` |
| Set chamber target temp | `{"print":{"command":"set_ctt","ctt_val":{temp},"temper_check":true}}` (H2S/X1C only — P1S has no active chamber heater) | N/A (U1 has no chamber heater) |
| Light on/off | `{"system":{"command":"ledctrl","led_node":"chamber_light","led_mode":"on/off"}}` | `POST /printer/gcode/script` `{"script":"SET_LED LED=cavity_led WHITE=1"}` |
| Home all axes | `{"print":{"command":"gcode_line","param":"G28\n"}}` | `{"script":"G28"}` |
| Move X/Y (relative) | `{"print":{"command":"gcode_line","param":"G91\nG1 X{mm} Y{mm} F{speed}\n"}}` | `{"script":"G91\nG1 X{mm} Y{mm} F{speed}"}` |
| Move Z (relative) | `{"print":{"command":"gcode_line","param":"G91\nG1 Z{mm} F{speed}\n"}}` | `{"script":"G91\nG1 Z{mm} F{speed}"}` |
| Fan control | `{"print":{"command":"gcode_line","param":"M106 P{0-2} S{0-255}\n"}}` (P0=part, P1=aux, P2=chamber) | `{"script":"M106 S{0-255}"}` |

### Key architectural insight: both printers use GCode as the control substrate
- **Bambu**: wraps G-code in `{"print":{"command":"gcode_line","param":"G28\n"}}` via Cloud MQTT
- **Snapmaker**: sends raw G-code via `POST /printer/gcode/script {"script":"G28"}` (Moonraker API)
- Both also have a few native non-GCode commands (Bambu's `ledctrl`, `set_ctt`, `print_speed`; Snapmaker's `SET_LED` Klipper command)
- **Implication**: the new `Printer` interface methods can be thin GCode dispatchers in both drivers, not protocol-specific implementations. A shared "send gcode" helper could reduce duplication.

### Bambu-specific notes
- Cloud MQTT supports the SAME command set as LAN MQTT (including all GCode passthrough)
- Firmware safety: movement GCode is rejected during active prints; only works when idle
- P1S = single nozzle; H2S = single nozzle (no multi-toolhead yet, but `M104 S{temp} T{n}` is supported)
- Light uses `{"system":...}` key (not `{"print":...}`) — requires a different struct in commands.go
- Chamber temp target (`set_ctt`) only on printers with active chamber heater (H2S/X1C/X1E — NOT P1S)

### Snapmaker U1-specific notes
- Runs Klipper + Moonraker — all control goes through `POST /printer/gcode/script`
- The `GET /printer/objects/query?toolhead` endpoint returns live position `[x,y,z,e]`
- Light Klipper object = `cavity_led` (white LED, 0.0–1.0 brightness range)
- Multiple toolheads: `M104 S{temp} T0` through `T3` for per-toolhead nozzle temps
- Moonraker also supports WebSocket JSON-RPC for GCode: `{"jsonrpc":"2.0","method":"printer.gcode.script","params":{"script":"G28"},"id":1}`

### Recommended implementation approach (unscoped — just guidance for future card)
1. **Extend `Printer` interface** with: `SetBedTemp(ctx, temp)`, `SetNozzleTemp(ctx, index, temp)`, `SetChamberTemp(ctx, temp)`, `SetLight(ctx, on)`, `HomeAll(ctx)`, `Move(ctx, x, y, z, relative, speed)` — or group into a `Control` sub-interface if not all printers support everything.
2. **Add REST endpoints**: `POST /api/printers/{id}/control/temp/bed`, `.../nozzle/{index}`, `.../chamber`, `.../light`, `.../home`, `.../move`
3. **Wire frontend controls**: replace the stubbed `setTargetTemp()` with real fetch calls; add a movement pad section; add a light toggle button
4. **Safety gates**: movement/home commands only when state == "idle"; temp commands probably safe during print but worth guarding with a confirmation on high values
5. **Bambu commands.go**: add `gcodeCommand(gcode)`, `ledCommand(mode)`, `setChamberTempCommand(temp)` builders alongside existing pause/resume/stop/skip
6. **Snapmaker snapmaker.go**: add a `sendGCode(script string)` helper (may already partially exist for SkipObject), then build typed methods on top

### References (for implementation)
- OpenBambuAPI MQTT reference: https://github.com/Doridian/OpenBambuAPI/blob/main/mqtt.md
- Bambu printer manager protocol docs: https://synman.github.io/bambu-printer-manager/mqtt-protocol-reference/
- Moonraker API: https://moonraker.readthedocs.io/en/latest/web_api/
- Klipper GCode: https://www.klipper3d.org/G-Codes.html
- Existing stubbed UI: `onboarding.go:974-984` (targetInput + setTargetTemp console.log)
- Related backlog cards: K-031 (movement), K-037 (chamber temp H2S), K-032 (filament loaded), K-033/K-034 (AMS)

## K-055 — AMS / filament status & loading/unloading feasibility research
READ-ONLY research, no code changes. User asked: is AMS status, filament
status, and loading/unloading functionality feasible for all 3 printers
(P1S+AMS1, H2S+AMS2Pro, U1 with built-in autofeeders)?

**ANSWER: YES — every requested capability is feasible on all 3 printers.**

### Current codebase state (as of this research): ZERO AMS/filament support existed
- `printStatus` struct in `parser.go` had no AMS fields — MQTT AMS data was silently discarded
- `PrinterStatus` in `interface.go` had no filament/AMS fields
- `handleReport()` in `client.go` ignored the `ams` key entirely
- Snapmaker parser only extracted toolhead temperatures, not filament data
- No AMS/filament UI in dashboard — no card section, no filament display

### Cross-printer capability matrix

| Capability | Bambu P1S + AMS 1 | Bambu H2S + AMS 2 Pro | Snapmaker U1 (4 toolheads) |
|------------|-------------------|----------------------|---------------------------|
| Filament type per slot | `tray_type` in MQTT report | Same | RFID `MAIN_TYPE` via Moonraker |
| Filament color per slot | `tray_color` (RRGGBBAA) | Same | `ARGB_COLOR` via RFID |
| Remaining weight/length | `remain` (mm), `tray_weight` (g) | Same | Not reported (no RFID remaining calc) |
| Humidity per AMS unit | AMS 1 has no sensor | `humidity` (0-5), `humidity_raw` (%) | N/A (no AMS unit) |
| Temperature per AMS unit | AMS 1 has no sensor | `temp` (°C) | N/A |
| Drying support | Not supported | `ams_filament_drying` command (max 65°C) | N/A |
| Load filament (per slot) | `ams_change_filament` with target tray ID | Same | `FEED_AUTO MODULE=left/right CHANNEL=0/1 LOAD=1` |
| Unload filament | `unload_filament` command | Same | `FEED_AUTO MODULE=left/right CHANNEL=0/1 UNLOAD=1` |
| Currently active tray | `tray_now` (global tray ID encoding) | Same | Query `filament_feed` for `channel_state` |
| Filament runout detection | Via MQTT `ams_status` bits | Via MQTT `ams_status` bits | `filament_motion_sensor` objects |
| Set filament properties | `ams_filament_setting` (for 3rd-party spools) | Same | N/A (RFID auto-detect) |
| AMS RFID read control | `ams_user_setting`, `ams_get_rfid` | Same | `FILAMENT_DT_QUERY/UPDATE/CLEAR` |

### Bambu AMS — detailed protocol

#### MQTT report structure (`ams` key inside `print`)
```json
{
  "ams": {
    "ams": [{
      "id": "0",                    // AMS unit index (0-3 for AMS/AMS2Pro, 128-135 for AMS-HT)
      "humidity": "4",              // Humidity index 0-5 (lower=drier)
      "humidity_raw": "23",         // Raw humidity %
      "temp": "25.0",              // AMS internal temp °C (H2S only, absent on P1S)
      "tray": [{
        "id": "0",                  // Tray/slot index (0-3)
        "state": 3,                 // 0=empty, 3=loaded/ready, 10=reading, 11=loaded+data
        "tray_type": "PLA",        // Material type string
        "tray_color": "000000FF",  // RRGGBBAA hex
        "tray_info_idx": "GFA00",  // Bambu filament profile ID
        "nozzle_temp_min": "190",  // Min nozzle temp °C (string!)
        "nozzle_temp_max": "230",  // Max nozzle temp °C
        "remain": 750,             // Remaining length mm (-1=unknown)
        "tray_weight": "250",      // Spool weight grams
        "k": 0.02,                 // K-value for linear advance
        "tag_uid": "8A160AB5...",  // RFID tag UID (zeros if no RFID)
      }]
    }],
    "tray_now": "0",               // Active tray: (ams_id*4)+tray_id, 254=external, 255=none
    "ams_exist_bits": "1",         // Bitmask: which AMS units connected
    "tray_exist_bits": "e"         // Bitmask: which trays have filament
  }
}
```

#### Key encoding: `tray_now` = `(ams_id * 4) + tray_id`
- AMS 0, slot 0 → 0; AMS 0, slot 3 → 3; AMS 1, slot 0 → 4
- 254 = external spool; 255 = none

#### P1S AMS 1 vs H2S AMS 2 Pro differences
- P1S sends **delta updates** (only changed fields); must call `pushall` periodically
- H2S sends **full updates** always
- P1S AMS 1 has NO `temp`, NO `humidity` sensors
- H2S AMS 2 Pro has temp + humidity + drying support

#### Filament load command
```json
{"print":{"command":"ams_change_filament","target":0,"curr_temp":0,"tar_temp":220}}
```
- `target` = absolute tray ID (ams_id*4 + slot_id, or 254 for external)
- `tar_temp` = nozzle temp for new filament (midpoint of min/max is typical)

#### Filament unload command
```json
{"print":{"command":"unload_filament"}}
```

#### AMS control (resume/retry/done)
```json
{"print":{"command":"ams_control","param":"resume"}}  // or "done", "pause", "reset"
```

#### Set filament properties (for 3rd-party spools)
```json
{"print":{"command":"ams_filament_setting","ams_id":0,"tray_id":0,
  "tray_type":"PLA","tray_color":"FF0000FF",
  "nozzle_temp_min":190,"nozzle_temp_max":230}}
```

#### Known Bambu filament profile IDs (subset)
PLA: GFA00(Basic), GFA01(Matte), GFA02(Metal), GFA05(Silk)
ABS: GFB00; ASA: GFB01; PC: GFC00
PETG: GFG00(Basic), GFG02(HF), GFG50(CF)
PA: GFN03(CF); TPU: GFU01(95A)
Generic: GFL99(PLA), GFG99(PETG), GFB99(ABS), GFU99(TPU)

### Snapmaker U1 filament — detailed protocol

#### Architecture: 4 toolheads, 2 feeder modules (left/right), 2 channels each
- Left feeder → channels 0,1 → extruders T0,T1
- Right feeder → channels 2,3 → extruders T2,T3
- Motorized DC feeders with RFID recognition (FM175XX reader)
- Klipper config sections: `[filament_feed left]`, `[filament_feed right]`

#### Load filament (via Moonraker POST /printer/gcode/script)
```
FEED_AUTO MODULE=left CHANNEL=0 LOAD=1     ← load on left feeder, channel 0
FEED_AUTO MODULE=right CHANNEL=1 LOAD=1    ← load on right feeder, channel 1
```
Internally executes: prepare → home X/Y → pick toolhead (T<n>) → feed to nozzle → heat → extrude/purge → flush → finish

#### Unload filament (staged or all-at-once)
```
FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1                    ← full auto unload
FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1 STAGE=prepare      ← stage 1: home+heat
FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1 STAGE=doing        ← stage 2: retract
FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1 STAGE=cancel       ← cancel mid-unload
```

#### Filament feed status query
```
GET /printer/objects/query?filament_feed
```
Returns per-extruder: `channel_state` (load_finish, wait_insert, load_feeding, etc.),
`channel_error` (ok, no_filament, timeout, etc.), `filament_detected`, `disable_auto`

#### RFID filament info query
```
GET /printer/objects/query?filament_detect
```
Returns per-channel: `MAIN_TYPE` (PLA/PETG/ABS/etc), `SUB_TYPE`, `ARGB_COLOR`,
`HOTEND_MIN_TEMP`, `HOTEND_MAX_TEMP`, `BED_TEMP`, `VENDOR`, `OFFICIAL`

#### Key `channel_state` values
`wait_insert` → `preload_prepare` → `preload_feeding` → `preload_finish`
`load_prepare` → `load_homing` → `load_picking` → `load_feeding` → `load_heating` →
`load_extruding` → `load_flushing` → `load_finish`
`unload_prepare` → `unload_finish` (or `unload_fail`)

#### Known error codes
`ok`, `no_filament`, `residual_filament`, `motor_speed`, `wheel_speed`, `timeout`,
`move`, `move_home`, `move_switch`, `move_extrude`, `custom_gcode`, `heat`

#### Filament type temps (from firmware defaults)
PLA: 250°C load, 170°C clean | PETG: 270°C, 205°C | ABS: 280°C, 220°C
PA-CF: 300°C, 240°C | PC: 300°C, 220°C | TPU: 250°C, 190°C

### Recommended implementation approach (unscoped guidance)

#### Data model additions to `PrinterStatus` (interface.go)
```
- AMSUnits []AMSUnit          — per-AMS-unit data (Bambu only)
- FilamentSlots []FilamentSlot — per-slot data (both printers, different semantics)
- ActiveTrayID int            — currently loaded tray (-1=none, 254=external)
```

#### New structs needed
```
AMSUnit { ID, Humidity, HumidityRaw, Temp, Trays []FilamentSlot }
FilamentSlot { Index, Type, Color, Name, RemainingMM, RemainingG, MinTemp, MaxTemp, Loaded bool }
```

#### Printer interface extensions
```
- LoadFilament(ctx, slotID int) error      — Bambu: ams_change_filament; U1: FEED_AUTO LOAD=1
- UnloadFilament(ctx, slotID int) error    — Bambu: unload_filament; U1: FEED_AUTO UNLOAD=1
- SetFilamentType(ctx, slotID, typeName) error — Bambu: ams_filament_setting; U1: N/A (RFID auto)
```

#### Bambu parser additions (parser.go)
- Add `amsData` struct mirroring the full `ams` JSON block
- Parse in `handleReport()` when `print.ams` key present
- Handle delta updates (P1S): merge with cached state

#### Snapmaker parser additions (parser.go)
- Query `filament_feed` and `filament_detect` objects alongside existing `print_stats`
- Map `channel_state` → loaded/loading/error/empty
- Extract RFID filament type from `filament_detect.info[]`

#### Frontend additions
- AMS section in card: grid of slot tiles showing color swatch, type, remaining %
- Per-slot load/unload buttons (disabled during print for safety)
- Humidity/temp indicator for H2S AMS 2 Pro
- Active tray highlight
- Filament feed status for U1 (progress states during load/unload)

### References
- OpenBambuAPI MQTT reference: https://github.com/Doridian/OpenBambuAPI/blob/main/mqtt.md
- Bambu AMS filament API: https://github.com/coelacant1/Bambu-Lab-Cloud-API/blob/main/API_AMS_FILAMENT.md
- ha-bambulab commands: https://github.com/greghesp/ha-bambulab/blob/main/custom_components/bambu_lab/pybambu/commands.py
- Bambu filament RFID profiles: https://github.com/Bambu-Research-Group/RFID-Tag-Guide/issues/42
- Bambuddy (AMS-HT addressing): https://github.com/maziggy/bambuddy/issues/364
- Snapmaker U1 filament feed source: `filament_feed.py` in Snapmaker Klipper fork
- Related backlog cards: K-032 (filament loaded), K-033 (AMS status), K-034 (AMS humidity), K-005 (P1S cloud MQTT field audit)

## K-109 — WORKING RECIPE (verified on live H2S 2026-10-10)

`print`-namespace commands now execute on the H2S with **no Developer Mode and
no memory dump**. `G28` (home all) returned `result:"success", err_code:0`.

The blocker was a single field: **`payload_len` must be a JSON string, not a
number.** A numeric `payload_len` yields `84033547` and looks exactly like a
certificate problem; a string one is accepted. Bambu stringifies these fields
(`sequence_id` likewise).

Full procedure, all steps measured:

1. Take the publicly extracted Bambu Connect cert + key (the pair bundled in
   `schwarztim/bambu-mcp`, `src/types.ts` `getAppCert()`). Serial
   `ee3cac0712ceeacda8b7fbc42e74ba2c`, expired 2025-12-12 — **expiry is not
   enforced by the printer**.
2. Mint any CRL whose issuer CN is `GLOF3813734089.bambulab.com`. The printer
   does **not** validate the CRL signature; it only requires the field to be
   non-empty (empty gives `FAILURE / "crl value is null"`).
3. Over LAN MQTT, publish
   `{"security":{"sequence_id":"26821","command":"app_cert_install","app_cert":"<leaf PEM>","crl":"<CRL PEM>"}}`
   → `SUCCESS`. Re-run after every printer reboot (RAM-only store).
4. Sign and send:

```js
const payload = { print: { sequence_id: "5100", command: "gcode_line", param: "G28\n" },
                  user_id: "1578637590" };
const str = JSON.stringify(payload);
crypto.sign("RSA-SHA256", Buffer.from(str), key).toString("base64");
// publish { ...payload, header: { sign_ver:"v1.0", sign_alg:"RSA_SHA256",
//   sign_string: sig, cert_id: "<serial-hex>CN=GLOF3813734089.bambulab.com",
//   payload_len: String(Buffer.byteLength(str,"utf8")) } }   // <-- STRING
```

Confirmed accepted: `M105`, `G28`. Confirmed rejected before install: same
frames → `84033545`.

Notes:
- Header position in the JSON does not matter (header-first also gave `547`
  while the only difference in the passing case was the string `payload_len`).
- Signing scope variants (user_id sibling / absent / inside `print`) all gave
  `547` while `payload_len` was numeric, so scope was never the problem.
- `home_flag` did not flip within 10 s of `G28` being accepted; H2S homing is
  slower than that and the printer was in `state:"error"` from an earlier HMS.
  Command **acceptance** is what was being proven.
- Whether the cloud transport accepts these frames untested; LAN works.

## K-108 — Bambu MQTT command verification and the signing path (H2S movement)

Measured on the live fleet 2026-10-10 against H2S-001 `0938AC580200372`
(192.168.1.17, fw `ver=20008`, access code `d5a78d50`) and P1S-001
`01P09C4B1600631` (192.168.1.15, access code `64894711`), cloud account
`u_1578637590`. Companion write-ups:
`docs/h2s-mqtt-signing-path.md` (current) and
`docs/h2s-mqtt-command-verification.md` (diagnosis + refuted hypotheses; its
"needs Developer Mode" conclusion is superseded).

**Bottom line: the H2S rejects every `print`-namespace MQTT command because
2024+ firmware cryptographically verifies them. This is solvable WITHOUT
Developer Mode by sending an RSA-SHA256 `header` envelope signed with a
certificate the printer trusts. Proven: signing changed the error from
`84033543` (unsigned) to `84033545` (valid signature, cert not trusted).**

### Which printers are affected

| printer | `print` namespace | why |
|---|---|---|
| P1S-001 | **works** (home + jog verified, `homed` flips from report) | firmware predates the verification gate (inference — its `ver` is unreadable, see below) |
| H2S-001 | **rejected**, both transports | verification active; not in LAN/Developer mode (liveview-over-LAN only) |

Risk: the P1S will likely start failing the same way after a firmware update.
User states they do not plan to update it.

### Measured behaviour (identical on cloud and LAN)

| namespace | cloud | LAN (`bblp` + access code) |
|---|---|---|
| `pushing.pushall` | success, full report | accepted |
| `system.ledctrl` | success, light physically toggles | success, light physically toggles |
| `print.gcode_line` | `failed` / `mqtt message verify failed` / `84033543` | identical |
| status reports | sparse; P1 deltas omit `gcode_state` | **plaintext JSON, ~1/sec** |

### LAN protocol facts (also feeds K-106)

- Broker `mqtts://<ip>:8883`, self-signed TLS (`rejectUnauthorized:false`).
- Auth username `bblp`, password = access code. **Enforced** — wrong password
  gives `Connection refused: Not authorized` (so a successful connect proves the
  stored code is valid).
- Command topic is **`device/{serial}/request`**. `device/{serial}/mqtt` is
  silently ignored on this firmware.
- Payloads are **plaintext JSON** on this firmware, both directions. No AES
  `bblp=` envelope is involved. Ciphertext to `request` is silently dropped;
  plaintext with a wrong shape gets an explicit
  `result:"failed", reason:"json wrong format"`.
- Command acks arrive on `device/{serial}/report`.
- Reports stream ~1/sec — far better than the cloud path, and enough to make
  the staleness refresher in `keepStatusFresh` unnecessary on LAN.
- P1S LAN: 8883 accepts a connection but publishes **no** reports (LAN mode
  off), so LAN is not usable for the P1S today.
- Open ports — H2S: 3000 (FTP), 6000 (camera), 8883 (MQTT). P1S: 8883 only
  (6000 is held by this app's camera connection). **No HTTP control API on
  either printer**, so there is no local Bambu-Connect endpoint to target.
- Bambu cloud is a **forwarder**, not a different command language: same topics
  and JSON whether the session terminates on the printer or the cloud broker.
  So signed frames and `app_cert_install` may work over the existing cloud
  connection — **untested, worth trying first** since it would avoid needing LAN.

### The signing format

```jsonc
// 1. payload; user_id is a TOP-LEVEL sibling, not inside the section
{ "print": { "sequence_id": "5000", "command": "gcode_line", "param": "G28\n" },
  "user_id": "1578637590" }

// 2. sign the byte-exact JSON.stringify() of that object — no canonicalisation
sign_string = base64( RSA-SHA256( privateKey, payload ) )

// 3. append header
{ ...payload, "header": {
    "sign_ver": "v1.0", "sign_alg": "RSA_SHA256",
    "sign_string": "<base64>",
    "cert_id": "<32-hex fingerprint><issuer CN>",
    "payload_len": <utf8 byte length of payload> } }
```

Traps, each of which reproduces a "verify failed":

- `sequence_id` must be **>= 2000** on signed frames (fixed floor, not a
  high-water mark) or you get `84033545` even with a trusted cert. Studio uses a
  20000–29999 window. `app_cert_list` also **requires a string** `sequence_id`
  and `"type":"app"`.
- The signature covers the whole envelope minus `header`, **including the
  trailing `user_id` sibling**. Signing only the inner `{"print":…}` is a known
  reimplementation bug.
- The printer re-checks byte-exact wire bytes — the string you sign must be the
  string you publish.
- `system` and `pushing` are not verified; keep them unsigned.

### Error codes

| err_code | meaning |
|---|---|
| `84033543` | no signature present (or signature over wrong bytes) |
| `84033544` | contested: file-verify stage (bad `md5`, plaintext `url`, `ftp:///`) **or** `sequence_id` reuse across restarts |
| `84033545` | "need reset device pub key" — **valid signature, cert not in trust store**; also returned for a valid cert with `sequence_id < 2000` |
| range | `84033543`–`84033548` all belong to this verification family |

**The family is an ordered ladder, measured on the H2S 2026-10-10:**

| stage | err | how reproduced |
|---|---|---|
| no signature | `…543` | plain unsigned `gcode_line` |
| cert not in trust store | `…545` | signed with an uninstalled cert |
| header field missing | `…546` | signed frame omitting `payload_len` |
| signature/chain rejected | `…547` | signed with an **installed but unchained** cert |

`…547` did **not** change across three payload shapes (user_id as sibling /
absent / inside `print`), so it is not a byte-layout problem — see "own-CA
test" below.

### cert_id is the certificate SERIAL, not a hash

`cert_id` = **lowercase hex of the X.509 serial number** + `"CN="` + issuer CN.
Verified: a generated leaf with serial `62F773A6607F1D9B298D08E79F3FF8F6A21EB05C`
appeared in `app_cert_list` as
`62f773a6607f1d9b298d08e79f3ff8f6a21eb05cCN=GLOF3813734089.bambulab.com`.
Bambu's own trusted IDs are 32 hex because their serials are 16 bytes; a
20-byte random serial gives 40 hex. **All earlier MD5/SHA fingerprint theories
are wrong** — do not compute hashes for this.

### Own-CA test: install works, verify still fails (chain of trust is enforced)

Generated a local CA (`CN=GLOF3813734089.bambulab.com`) + leaf
(`CN=GLOF3813734089-0a1b2c3d0000`) + empty CRL with `cryptography`, then over
LAN on the H2S:

1. `security.app_cert_install` with `app_cert` = leaf+CA PEMs and `crl` = PEM →
   **`SUCCESS`**. The install command itself needs **no signature**.
2. `app_cert_list` then returned our cert alongside the two Bambu ones — so
   install accepts arbitrary certs and does **not** chain-validate.
3. Signing `print.gcode_line` with our key and the correct `cert_id` →
   **`84033547`**, i.e. it got to signature/chain verification and failed.

**Conclusion: the printer will not accept a self-signed leaf.** Install is
permissive; verify requires the leaf to chain to Bambu's CA hierarchy
(`leaf → GLOF3813734089.bambulab.com → application_root.bambulab.com → BBL CA`).
So a usable signing key still has to be one of Bambu's real app certs — the
own-key shortcut does not work.

### Shipped Bambu Studio files are NOT the signing key

From `BambuStudio.app/Contents/Resources/cert/` (macOS):

- `printer.cer` — 5-PEM CA bundle: `BBL CA` (self-signed, to 2032-04),
  `BBL CA2 RSA`, `BBL CA2 ECC` (self-signed, to 2050) plus CA2 issued by BBL CA.
  This is the trust root hierarchy, public, no private key.
- `slicer_base64.cer` — 2 PEMs: `CN=*.bambulab.com` issued by RapidSSL/DigiCert
  (**expired 2022-07-13**) + `DigiCert Global Root CA`. This is the **cloud TLS
  pin**, not the app cert.

Neither matches the printer's trusted serials. No private key anywhere in
`~/Library/Application Support/BambuStudio` either.

### Static extraction from the plugin binary does not work

Plugin fetched from the documented resource endpoint (no auth needed):

```
GET https://api.bambulab.com/v1/iot-service/api/slicer/resource?slicer/plugins/cloud=02.06.00.00
 -> software 02.08.02.61, plugin https://public-cdn.bblmw.com/upgrade/studio/plugins/02.06.00.76/f01688f29f/win_02.06.00.76.zip
```

The zip contains only 7 DLLs (`bambu_networking.dll` 23 MB, `BambuSource.dll`,
live555, agora*). **No cert/key files.** And the binaries are string-obfuscated:
`grep -a` finds zero hits for `mqtt`, `json`, `GLOF`, `iot-service`,
`app_cert_install`, `aes256`, `cert_id` — even control strings barely appear
(34% printable). So `client_auth_secret` cannot be grepped out of the shipped
binary; the documented route is a **runtime memory dump**
(`tools/extract_key_from_dump.py`, method in `research/10.07-bearssl-crypto-island.md`).

### Remaining routes to a usable key (unchanged, both need more work)

1. Runtime memory dump of a live Bambu Studio/plugin process → recover the
   obfuscated 704-byte `SKEY` blob → decrypt with the custom-S-box AES-256-CTR
   (fixed key `00..1f`, counter from 2). Decoder verified working locally:
   `fetch_slicer_credentials.py --self-test` passes all 10 checks including a
   known-answer test.
2. `client_auth_secret` + `server_wrap_key` from that same dump, then the cloud
   cert endpoint returns the current leaf + CRL + encrypted key.

### Error codes

### Printer trust store

- `security.app_cert_list` with
  `{"security":{"sequence_id":"1","command":"app_cert_list","type":"app"}}`
  → `SUCCESS` + `cert_ids`. On this H2S:
  ```
  95554d66b3d9912fdcad7ea8e1b8fd15CN=GLOF3813734089.bambulab.com
  9bed8c27b4bf69582d58f11abaaad99fCN=GLOF3813734089.bambulab.com
  ```
  `9bed8c27…` also appears in another project's capture from a different
  printer, so these are Bambu's **shared app certs**, not per-printer.
- `security.app_cert_install` installs a chain: fields `command` /
  `sequence_id` / `app_cert` (3 PEMs) / `crl` (1 CRL). On success the printer
  replies on `report` with `printer_cert` + `"result":"SUCCESS"`. Install
  **adds**, does not replace; no known command removes a single cert.
- **The store lives in printer RAM and a reboot clears it** — re-install every
  session. Empty store answers `reason:"device-endreaddevcertfailed"`.
- "Any installed leaf verifies" — so installing our own leaf is sufficient;
  whether the printer chain-validates it is **untested**.

### Routes to a usable private key

1. **Copy what Bambu Studio already caches (easiest, pending from user).**
   Studio fetches the shared app cert/CRL/private key and keeps them in its data
   dir: Linux `~/.config/BambuStudio/`, macOS
   `~/Library/Application Support/BambuStudio/`, Windows `%AppData%\BambuStudio\`
   — `slicer_cert.pem`, `slicer_key.pem`, `slicer_crl.pem`.
2. **Fetch from the cert endpoint.**
   `GET /v1/iot-service/api/user/applications/{enc_secret}/cert?aes256=…&ver=1`.
   Request proves possession of `client_auth_secret` (43-byte string: app-cert CN
   prefix + 16 hardcoded hex) AES-GCM'd under an ephemeral session key;
   `aes256=` wraps that session key with `server_wrap_key` (RSA-2048 public PEM).
   Response `key` blob opens with a **custom-S-box AES-256-CTR**, fixed key
   `00..1f`, CTR starting at 2. Tooling:
   `open-bamboo-networking/tools/fetch_slicer_credentials.py`. Both bootstrap
   secrets must be extracted from the stock plugin
   (`tools/extract_key_from_dump.py`); they are deliberately unpublished.
   Wrong wrap key → HTTP 400 `code:101` "This application is outdated".
3. **Not a route:** the cert bundled in `schwarztim/bambu-mcp`. Genuine Bambu
   Connect cert, but expired 2025-12-12 and its MD5
   (`c0e88e3e79e826f0fbf3187bc2077952`) matches neither trusted cert — hence the
   `84033545`. Verified cert/key are a valid pair (matching modulus), so the
   failure is trust, not a broken key.

### Hypotheses tested and refuted (do not re-litigate)

1. **Payload shape / `sequence_id`** — 11 `gcode_line` variants over LAN with
   instant acks (absent / string / number / `"0"` / `id` / `mc_action` /
   `account` / `version` / no newline / `M115` / trailing space): all `84033543`.
2. **Encrypted `bblp=` envelope** — invalid test twice over: encryption is not
   used by this firmware, and the first attempt targeted the cloud broker.
3. **Wrong LAN topic** — `request` vs `mqtt` both tried; `request` is correct
   and does execute `system` commands.
4. **Printer bound to a different account** — unverifiable via cloud API (every
   `api.bambulab.com/v1/...` device endpoint 404s with the stored token), but
   `system.ledctrl` from the same identity physically actuates the light.
5. **Stale access code** — wrong passwords refused, stored one authenticates.
6. **"LAN ignores commands"** — was our own bug: invented `ledctrl` keys
   (`led_on`/`cycle_times`) instead of `led_node`/`led_mode`/`led_on_time`/
   `led_off_time`/`loop_times`/`interval_time`. The printer was replying
   `json wrong format`.

### Firmware version access

- H2S: `print.ver = 20008` readable from cloud reports; `upgrade_state` showed
  `ota_new_version_number:"01.02.00.00"`, `new_version_state:1`.
- P1S: **`ver` never appears** in its cloud reports (delta behaviour) and its
  LAN broker streams nothing, so its firmware version is unreadable by these
  means. Any claim about P1S firmware age is inference.
- H2S `home_flag = 0xc0675590` (bit0 clear) = genuinely unhomed.

### Live-state gotchas

- Failed commands raise HMS `0500-0500-0001-0007`, which puts the printer in
  `state:"error"` and therefore blocks the movement gate. The app's dismiss
  endpoint only suppresses notifications, not the printer's reported state —
  a printer restart clears it.
- Bambu's own remediation text for that HMS ("update Studio including the
  network plugin") is a hint at this signature check, not a generic nag.

### Implementation sketch (unscoped guidance for a future card)

1. Config: credentials dir for `slicer_{cert,key,crl}.pem` (access codes already
   in `config.yaml`).
2. On connect: `app_cert_install`, verify with `app_cert_list`; re-run after
   printer reboots. Try over cloud first — it may avoid needing LAN at all.
3. In `publishCommand`, when a cert is loaded, wrap in the `header` envelope
   with `sequence_id` from a counter starting well above 2000.
4. Leave `system`/`pushing` unsigned.
5. Map `84033543` → "not signed", `84033545` → "cert not trusted by printer".

### References

- open-bamboo-networking (reverse-engineered plugin; `research/10.02-secrets.md`,
  `10.04-mqtt-signing.md`, `06.02-mqtt.md`):
  https://github.com/ClusterM/open-bamboo-networking
- bambu-mcp (working signing implementation: `src/mqtt-client.ts`
  `signMessage`, `src/types.ts` `getAppCert`, `docs/mqtt-protocol.md`):
  https://github.com/schwarztim/bambu-mcp
- Bambu third-party integration / authorization control:
  https://wiki.bambulab.com/en/software/third-party-integration
- Bambu Developer Mode instructions:
  https://wiki.bambulab.com/en/knowledge-sharing/enable-developer-mode
- Hackaday on the extracted Bambu Connect X.509 cert + key (Jan 2025):
  https://hackaday.com/2025-01-19/bambu-connects-authentication-x-509-certificate-and-private-key-extracted/
- OpenBambuAPI: https://github.com/Doridian/OpenBambuAPI
- Related cards: K-106 (LAN MQTT), K-092 (state gating on other commands),
  K-095 (movement/homing chain)
