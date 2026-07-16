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
