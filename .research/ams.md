# AMS / filament research digest (read-only)

Fleet: **P1S** (stock pre-Bambu-Connect fw, cloud MQTT only, AMS 1) · **H2S** (Bambu Connect, AMS 2 Pro) · **Snapmaker U1** (Paxx/Klipper+Moonraker, 4 toolheads, 2 autofeeder modules × 2 channels).

Sources read in full: `docs/fix-p1s-ams-loading-spec.md` (445 L), `.fleet/research-notes.md` §K-055 (the canonical protocol ref, L73–274), `.fleet/board/done/K-033-ams-status-display-spec.md` (446 L), `done/K-033-ams-status-display.md`, `done/K-034-ams-humidity-display.md`, `backlog/K-032-filament-loaded-status-per-tool.md`, `backlog/K-082-ams-load-and-unload-commands.md`, `AGENTS-bak.md`, `backlog/K-073-*`. **No `done/K-073*.md` exists** — K-073 is backlog-only. Code cross-checked: `internal/printers/bambu/{parser,client,commands}.go`, `internal/printers/interface.go`, `internal/server/onboarding.go`, `internal/printers/bambu/{ams_delta_test,merge_test}.go`.

**Status headline:** the AMS *display* path (K-033) is shipped and merged (`60ebcd5` backend, `9d9a97d` delta-merge fix, PR #28). The AMS *command* path (K-082) and all Snapmaker filament work (K-032) are **entirely unimplemented** — `grep` finds zero AMS/filament code in `internal/printers/snapmaker/*.go` and zero AMS command builders in `internal/printers/bambu/commands.go`.

---

## 1. Bambu AMS wire structure

### 1.1 Wire structs as declared

`internal/printers/bambu/parser.go:164-193` (mounted at `printStatus.Ams *amsData \`json:"ams,omitempty"\`` — `parser.go:45`):

| Go field | JSON tag | Go type | Notes (verbatim comments) |
|---|---|---|---|
| `amsData.AMS` | `ams` | `[]amsUnitData` | "Array of AMS units (0-3)" |
| `amsData.TrayNow` | `tray_now` | `string` | "Active tray: (ams_id*4)+tray_id, 254=external, 255=none" |
| `amsData.AmsExistBits` | `ams_exist_bits` | `string` | "Bitmask: which AMS units connected" |
| `amsData.TrayExistBits` | `tray_exist_bits` | `string` | "Bitmask: which trays have filament" |
| `amsUnitData.ID` | `id` | `string` | AMS unit index (0-3) |
| `amsUnitData.Humidity` | `humidity` | `string` | index 0-5 (P1S AMS 1: absent) |
| `amsUnitData.HumidityRaw` | `humidity_raw` | `string` | raw % (P1S AMS 1: absent) |
| `amsUnitData.Temp` | `temp` | `string` | °C (P1S AMS 1: absent) |
| `amsUnitData.Tray` | `tray` | `[]amsTrayData` | up to 4 trays |
| `amsTrayData.ID` | `id` | `string` | slot index 0-3 — **string on wire** |
| `amsTrayData.State` | `state` | `int` | **number on wire** |
| `amsTrayData.TrayType` | `tray_type` | `string` | "PLA", "PETG", … |
| `amsTrayData.TrayColor` | `tray_color` | `string` | RRGGBBAA hex, no `#` |
| `amsTrayData.TrayInfoIdx` | `tray_info_idx` | `string` | Bambu profile ID e.g. "GFA00" |
| `amsTrayData.NozzleTempMin` | `nozzle_temp_min` | `string` | "string on wire" |
| `amsTrayData.NozzleTempMax` | `nozzle_temp_max` | `string` | "string on wire" |
| `amsTrayData.Remain` | `remain` | `int` | mm, -1=unknown — **number on wire** |
| `amsTrayData.TrayWeight` | `tray_weight` | `string` | grams |
| `amsTrayData.TagUID` | `tag_uid` | `string` | RFID UID, zeros if no RFID |

### 1.2 String-vs-number quirks (the trap)

The wire mixes types inconsistently — `id` is a **string** but `state` and `remain` are **numbers**; `nozzle_temp_min/max` and `tray_weight` are **strings** while `remain` is a number. `research-notes.md:121` flags `"nozzle_temp_min": "190",  // Min nozzle temp °C (string!)`. A `json.Unmarshal` type mismatch aborts the *whole* report parse (`handleReport` logs "failed to parse report" and drops the message entirely — `client.go:447-451`), so one wrong field type silently kills all status, not just AMS.

`K-033-ams-status-display-spec.md:389-399` claimed a **high-severity compile bug**: that `amsTrayData.NozzleTempMin` was declared `int` while `parseAMSData` called `strconv.Atoi(t.NozzleTempMin)` on it. The signal was echoed in `done/K-033-ams-status-display.md:51`. **This was wrong / already fixed** — shipped `parser.go:188-189` declares both as `string` and `client.go:827-828` does `strconv.Atoi(t.NozzleTempMin)`. It compiled and merged as PR #25. Treat the spec's "compilation error" claim as a false alarm, not an open defect.

### 1.3 State codes

`parser.go:184`: `0=empty, 2=loaded, 3=ready, 10=reading, 11=loaded+data`. `research-notes.md:117` lists only `0=empty, 3=loaded/ready, 10=reading, 11=loaded+data` (omits 2). `interface.go:88` comment lists only `2/3/11` (omits 10) — three slightly different lists in three files; the code (`client.go:835`) accepts **2, 3, 10, 11**.

**The P1S state-code landmine:** P1S reports `state: 0` for *every* tray, including loaded ones, while still sending valid `tray_type`/`tray_color`/`remain` (`fix-p1s-ams-loading-spec.md:417-419`, `client.go:831-834`). State alone is therefore useless on P1S.

### 1.4 `tray_now` encoding

`tray_now = (ams_id * 4) + tray_id`; **254 = external spool, 255 = none, -1 = unknown/not-applicable** (`research-notes.md:129,136-138`; `interface.go:60-63`). Worked examples from `research-notes.md:137`: AMS0/slot0→0, AMS0/slot3→3, AMS1/slot0→4. Frontend recomputes the same formula to highlight the active tile: `globalID = (unit.id * 4) + i` (`onboarding.go:1629`).

### 1.5 `ams_exist_bits` / `tray_exist_bits`

Documented meaning: `ams_exist_bits` = which AMS units are connected, `tray_exist_bits` = which trays have filament (`research-notes.md:130-131`; `parser.go:168-169`). They are **hex-ish strings**: `"1"` = one unit; `"3"` = units 0+1 present (two bits, per `ams_delta_test.go:534-535`); `"f"` = all four trays of a unit loaded; `"e"` = 0x0E = bits 1,2,3 → trays 1,2,3 loaded, tray 0 empty. `fix-p1s-ams-loading-spec.md:304-308` works through `"e"` and admits "the exact bits may vary by firmware."

**Both fields are parsed and then never read** — no code path consumes them (only struct declarations at `parser.go:168-169` and test fixtures). They are dead payload today. Note the inconsistency: the spec's sample JSON uses `"tray_exist_bits": "e"` (`:298`) while every shipped test fixture uses `"f"` (`ams_delta_test.go:46,60,75,99,122,143`).

### 1.6 Public (vendor-neutral) model

`internal/printers/interface.go:56-89`:

```go
AMSUnits []AMSUnit `json:"ams_units,omitempty"`   // :59
ActiveTrayID int   `json:"active_tray_id"`        // :63  (no omitempty — always sent)
```

`AMSUnit{ID int, Humidity, HumidityRaw, Temp string, Trays []FilamentSlot}` (`:69-75`); `FilamentSlot{Index int, Type, Color, InfoIdx string, NozzleTempMin/Max int, RemainingMM int \`json:"remain"\`, Weight, TagUID string, Loaded bool}` (`:78-89`). `AMSUnits` carries **`omitempty`** — that tag is load-bearing for the bug in §3 (nil ⇒ key absent from JSON ⇒ frontend renders nothing).

### 1.7 Full reference report (verbatim, `fix-p1s-ams-loading-spec.md:270-302`)

```json
{
  "print": {
    "gcode_state": "RUNNING",
    "gcode_file": "benchy.gcode",
    "ams": {
      "ams": [{
        "id": "0",
        "humidity": "3",
        "humidity_raw": "23",
        "temp": "25.0",
        "tray": [
          {"id": "0", "state": 3, "tray_type": "PLA", "tray_color": "FF0000FF",
           "tray_info_idx": "GFA00", "nozzle_temp_min": "190", "nozzle_temp_max": "230",
           "remain": 50000, "tray_weight": "250", "tag_uid": "8A160AB5"},
          {"id": "1", "state": 0, "tray_type": "", "tray_color": "000000FF",
           "tray_info_idx": "", "nozzle_temp_min": "0", "nozzle_temp_max": "0",
           "remain": -1, "tray_weight": "", "tag_uid": "00000000"},
          {"id": "2", "state": 3, "tray_type": "PLA", "tray_color": "00FF00FF",
           "tray_info_idx": "GFA00", "nozzle_temp_min": "190", "nozzle_temp_max": "230",
           "remain": 75000, "tray_weight": "", "tag_uid": "00000000"},
          {"id": "3", "state": 3, "tray_type": "PLA", "tray_color": "0000FFFF",
           "tray_info_idx": "GFA00", "nozzle_temp_min": "190", "nozzle_temp_max": "230",
           "remain": 100000, "tray_weight": "", "tag_uid": "00000000"}
        ]
      }],
      "tray_now": "0",
      "ams_exist_bits": "1",
      "tray_exist_bits": "e"
    }
  }
}
```

`research-notes.md:107-134` documents two extra fields the Go structs **do not model**: `"k": 0.02` (K-value for linear advance) and the AMS unit-id range note `0-3 for AMS/AMS2Pro, 128-135 for AMS-HT` (`:111`, sourced to a Bambuddy issue, `:272`). AMS-HT addressing is unimplemented and unhandled — a unit with `id: "128"` would `Atoi` to 128 and the frontend's `(id*4)+i` global-ID math would produce IDs ≥512 that can never match `tray_now`.

---

## 2. P1S vs H2S AMS differences

| Dimension | P1S + AMS 1 | H2S + AMS 2 Pro | Citation |
|---|---|---|---|
| Update mode | **delta** — only changed fields | **full** — complete `ams`+`tray[]` every report | `research-notes.md:141-142`, `spec:32-40` |
| `humidity` (index 0-5) | **no sensor** → absent/empty | present | `research-notes.md:94,143`; `K-034:24-26` |
| `humidity_raw` (%) | no sensor | present | same |
| `temp` (°C, AMS internal) | **no sensor** | present | `research-notes.md:95,143` |
| Drying | not supported | `ams_filament_drying`, **max 65 °C** | `research-notes.md:96,144` |
| `tray[].state` | **always 0**, even when loaded | meaningful (0/2/3/10/11) | `spec:417-419`, `client.go:831-834` |
| Empty-tray `tray_type` | `""` | `""` (guaranteed — this is what makes the §3.3 fix safe) | `client.go:833-834` |
| `pushall` | "must call `pushall` **periodically**" | not required | `research-notes.md:141` |
| Transport | cloud MQTT only (stock fw, no Bambu Connect) | Bambu Connect, cloud MQTT | task brief; `notes.md` "Cloud-MQTT-only for Bambu" |
| X1C + AMS 1 (reference) | no humidity/temp but **full** updates (unlike P1S) | — | `done/K-033-ams-status-display.md:78-80` |

`done/K-033-ams-status-display.md:77-81` compatibility matrix: P1S+AMS1 ✅ (no humidity/temp, delta), H2S+AMS2Pro ✅ (humidity/temp, full), X1C+AMS1 cloud ✅ (no humidity/temp, **full**), X1C+AMS1 LAN mode ⚠️ blocked by K-106.

**`pushall` — why it's needed and what the code actually does.** Payload (`client.go:406`, verbatim):

```json
{"pushing":{"command":"pushall","version":1,"push_target":1}}
```

Published to `device/{serial}/request`, QoS 0, retain false, 5 s wait (`client.go:404-413`). Its purpose: force the printer to emit a complete state snapshot, because a delta-only printer that has already "settled" will not spontaneously re-send AMS slot data. **It is called exactly once, immediately after subscribe** (`client.go:375-376`, in the connect path) — there is **no periodic re-push anywhere**, contradicting `research-notes.md:141` ("must call `pushall` periodically"). Combined with the merge cache, a missed field can persist indefinitely until reconnect.

Humidity semantics (`K-034:24-26`): `humidity` is an **index 0-5, lower = drier**; `humidity_raw` is the percentage; `temp` is AMS internal °C. K-034 (colour-coded dry/wet indicator) is in `done/` but its plan checkboxes are all unchecked — the shipped UI is the plain text line from K-033, not the K-034 indicator.

---

## 3. The AMS-loading bug (`docs/fix-p1s-ams-loading-spec.md`)

### 3.1 Symptom
P1S AMS slot type / colour / remaining never appear in the dashboard; H2S works (`spec:5-14`). Slots render as "Empty".

### 3.2 Root cause (two independent defects, shipped together in PR #28)

**(a) Wholesale replace on delta** (`spec:16-50`, `602` block). Data flow: report on `device/{serial}/report` → `parseReport` → `if p.Ams != nil { s.AMSUnits = parseAMSData(p.Ams) }` → `parseAMSData` returns `nil` when `len(ams.AMS)==0` → cache wiped → `omitempty` drops `ams_units` from the serialized status → frontend renders nothing. Two delta shapes trigger it: `ams` present with **no inner `ams` array** (only `tray_now` changed), and `ams[]` present with **no `tray` array** (only unit-level metadata like humidity changed). Second half of the defect: the `else { s.ActiveTrayID = -1 }` branch reset the active tray on any delta that omitted `tray_now` (`spec:42-44`).

Why no test caught it: `client_test.go` had **zero** AMS cases; `parseAMSData` was entirely untested; K-033's verification steps 10–11 ("Test with mocked MQTT data", "Verify delta update handling") were never done (`spec:46-50`; unchecked at `done/K-033-ams-status-display.md:40-41`).

**(b) `state: 0` on P1S** (`spec:411-441`). Even after the merge fix, every P1S tray showed "Empty" because `Loaded` was derived solely from `state ∈ {2,3,10,11}` and P1S sends `state: 0` for all trays, while the frontend gated all filament rendering on `tray.loaded`.

### 3.3 The fix (shipped)

1. `s.AMSUnits = mergeAMSData(parseAMSData(p.Ams), s.AMSUnits)` (`client.go:609`); the `else { ActiveTrayID = -1 }` branch **deleted** — `tray_now` is only written when non-empty (`client.go:614-620`).
2. New helpers `mergeAMSData` (`client.go:887`) / `mergeTrays` (`:934`) / `firstNonEmpty` (`:969`) (spec text `:102-219`). Merge rules verbatim (`spec:119-130`): empty `newAMS` → return cached as-is; per unit matched by `ID`, trays merged by `Index` (new replaces matching, cached-only retained, new-only appended; **empty new `Tray` array → keep cached trays entirely**); `Humidity`/`HumidityRaw`/`Temp` use `firstNonEmpty(new, cached)`; cached units absent from the update are retained.
3. `loaded := t.State == 2 || t.State == 3 || t.State == 10 || t.State == 11 || t.TrayType != ""` (`client.go:835`, spec `:430`). Safety argument: H2S/X1 empty trays always have `tray_type: ""`, so their behaviour is unchanged.
4. Frontend condition `!tray.loaded` → `(!tray.loaded && !tray.type)` plus an empty-colour guard (`spec:436-440`; shipped at `onboarding.go:1634`, guard at `:1642-1645`).

Rejected / not-chosen alternatives, as documented: (i) **leave the backend alone and require the P1S firmware to send full `tray[]` in every delta** — listed as the alternative in `K-033-ams-status-display-spec.md:384-386`, impossible on stock fw; (ii) **fix the type mismatch by making `NozzleTempMin/Max` `int`** — the spec's own §Open-question-2 prescription was the opposite (make them `string`), which is what shipped; (iii) **alpha→`rgba()` conversion for old browsers** was raised (`spec:407-409`) and rejected in favour of native `#RRGGBBAA` ("browsers accept `#RRGGBBAA` natively since ~2023", `spec:142-146`). Risk framing: "the merge logic is a superset of the old behavior… for H2S the merge produces identical results" (`spec:399-401`), and `parseAMSData`'s signature was deliberately left unchanged so the merge lives in `handleReport` (`spec:402-407`).

### 3.4 Test coverage now present
`ams_delta_test.go` (611 L): full update, empty-`ams`-array delta, `tray_now`-only delta, partial-tray delta, no-`tray`-array delta, no-`tray_now` delta, no-`ams`-key, **successive delta stacking** (`:426`), **H2S full update replaces cache — no stale tray bleed-through** (`:466`), multi-unit deltas (`:524`). `merge_test.go` (298 L): 13 direct cases incl. `TestMergeAMSData_EmptyTraysInNew_RetainsCachedTrays`, `TestFirstNonEmpty`.

### 3.5 Spec-vs-code drift worth knowing
`K-033-ams-status-display-spec.md:169-189` asserted the wholesale-replace behaviour was "**correct**" and its acceptance list ends with "**[x] No Go backend changes needed (already merged and verified)**" (`:446`) — while the same document's Open Question 1 flags the identical behaviour as "**Severity: high — affects display correctness on P1S**" (`:378-387`). The card was moved to `done/` with that contradiction live. Also: the spec's `amsHtml()` draft (`:238-315`) differs from shipped code — shipped adds a `humidity + '/5'` branch (`onboarding.go:1607-1610`) and a `parseFloat(unit.temp) !== 0` temp guard (`:1612`) that the spec does not have. **Placement also drifted:** the spec mandates the AMS section go "after `.temps` and before `.filename`" so hardware status groups at the top (`K-033-spec:115-122`), but shipped code puts it inside a `.bottom-sections` wrapper at the *bottom* of the card, after filename/layer/error/HMS and immediately above the collapsible Move pad (`onboarding.go:1844-1849`, skeleton `:1033-1034`).

---

## 4. Every documented AMS command payload

All Bambu payloads below are **documentation only — none are implemented** (`commands.go` contains only pause/resume/stop/project_file-skip/set_ctt/gcode_line/ledctrl; `grep` for `ams_change_filament|ams_control|ams_filament_setting|ams_user_setting|ams_get_rfid|ams_filament_drying` in `*.go` returns nothing). K-082 remains backlog with an empty plan.

| Command | Verbatim JSON | Source |
|---|---|---|
| Load / change filament | `{"print":{"command":"ams_change_filament","target":0,"curr_temp":0,"tar_temp":220}}` | `research-notes.md:147-149` |
| Unload filament | `{"print":{"command":"unload_filament"}}` | `research-notes.md:153-156` |
| AMS control | `{"print":{"command":"ams_control","param":"resume"}}  // or "done", "pause", "reset"` | `research-notes.md:158-161` |
| Set filament props (3rd-party spool) | `{"print":{"command":"ams_filament_setting","ams_id":0,"tray_id":0,\n  "tray_type":"PLA","tray_color":"FF0000FF",\n  "nozzle_temp_min":190,"nozzle_temp_max":230}}` | `research-notes.md:163-168` |
| Full-state push | `{"pushing":{"command":"pushall","version":1,"push_target":1}}` | `client.go:406` (**implemented**) |
| Drying | `ams_filament_drying` command, **max 65 °C** — *no JSON given anywhere in repo* | `research-notes.md:96,144` |
| RFID read control | `ams_user_setting`, `ams_get_rfid` — *names only, no JSON* | `research-notes.md:102` |

Notes on the payloads: `target` is the **absolute** tray ID (`ams_id*4 + slot_id`, or 254 for external) (`research-notes.md:150`); `tar_temp` is the nozzle temp for the incoming filament, "midpoint of min/max is typical" (`:151`). `ams_filament_setting` sends temps as **numbers** (190/230) whereas the *report* sends them as strings — inbound and outbound types differ for the same conceptual field.

Command transport conventions that future AMS commands must follow: publish to `device/{serial}/request`; `printCommand` **never sets `sequence_id`** for print-namespace commands (tested, intentional — `K-087:35-38,81-85`), while `systemCommand` (light) sets `SequenceID: "0"`; `publishCommand` gained an audit log line under K-087 (`K-087:49-56`). K-086 closed with HMS `MQTT Command verification failed` attributed to a **software/firmware version mismatch independent of payload correctness** (`K-086:107-119`) — a live risk when sending newer AMS commands to stock P1S firmware.

---

## 5. Snapmaker U1 filament / RFID (all unimplemented)

Architecture (`research-notes.md:179-183`): 4 toolheads, **2 feeder modules (left/right) × 2 channels**; left → channels 0,1 → T0,T1; right → channels 2,3 → T2,T3; motorized DC feeders with **FM175XX RFID reader**; Klipper sections `[filament_feed left]` / `[filament_feed right]`.

**Load** (`:185-190`, via Moonraker `POST /printer/gcode/script`):
```
FEED_AUTO MODULE=left CHANNEL=0 LOAD=1     ← load on left feeder, channel 0
FEED_AUTO MODULE=right CHANNEL=1 LOAD=1    ← load on right feeder, channel 1
```
Internally: prepare → home X/Y → pick toolhead (T\<n\>) → feed to nozzle → heat → extrude/purge → flush → finish.

**Unload, staged or all-at-once** (`:192-198`):
```
FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1                    ← full auto unload
FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1 STAGE=prepare      ← stage 1: home+heat
FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1 STAGE=doing        ← stage 2: retract
FEED_AUTO MODULE=left CHANNEL=0 UNLOAD=1 STAGE=cancel       ← cancel mid-unload
```

**Status queries** (`:200-212`): `GET /printer/objects/query?filament_feed` → per-extruder `channel_state`, `channel_error`, `filament_detected`, `disable_auto`. `GET /printer/objects/query?filament_detect` → per-channel `MAIN_TYPE` (PLA/PETG/ABS/…), `SUB_TYPE`, `ARGB_COLOR`, `HOTEND_MIN_TEMP`, `HOTEND_MAX_TEMP`, `BED_TEMP`, `VENDOR`, `OFFICIAL`. Runout detection via `filament_motion_sensor` objects (`:100`). RFID management GCode: `FILAMENT_DT_QUERY/UPDATE/CLEAR` (`:102`).

**`channel_state` values** (`:214-218`):
`wait_insert → preload_prepare → preload_feeding → preload_finish`;
`load_prepare → load_homing → load_picking → load_feeding → load_heating → load_extruding → load_flushing → load_finish`;
`unload_prepare → unload_finish` (or `unload_fail`).

**Error codes** (`:220-222`): `ok`, `no_filament`, `residual_filament`, `motor_speed`, `wheel_speed`, `timeout`, `move`, `move_home`, `move_switch`, `move_extrude`, `custom_gcode`, `heat`.

**Temp defaults per material — load temp / clean temp** (`:224-226`, "from firmware defaults"):

| Material | Load | Clean | Material | Load | Clean |
|---|---|---|---|---|---|
| PLA | 250 °C | 170 °C | PC | 300 °C | 220 °C |
| PETG | 270 °C | 205 °C | TPU | 250 °C | 190 °C |
| ABS | 280 °C | 220 °C | PA-CF | 300 °C | 240 °C |

U1 gaps vs Bambu: **no remaining length/weight at all** ("Not reported (no RFID remaining calc)", `:93`), no AMS unit so no humidity/temp/drying, and filament properties are RFID-auto-detected with no manual-set equivalent (`:101`).

---

## 6. Bambu profile IDs & colour encoding

Profile IDs (`tray_info_idx`), verbatim from `research-notes.md:170-175`:

```
PLA: GFA00(Basic), GFA01(Matte), GFA02(Metal), GFA05(Silk)
ABS: GFB00; ASA: GFB01; PC: GFC00
PETG: GFG00(Basic), GFG02(HF), GFG50(CF)
PA: GFN03(CF); TPU: GFU01(95A)
Generic: GFL99(PLA), GFG99(PETG), GFB99(ABS), GFU99(TPU)
```

Colour: `tray_color` is **RRGGBBAA hex with no `#`**, e.g. `"FF0000FF"` = opaque red (`K-033-spec:142-146`, `interface.go:81`). Rendering prepends `#` and relies on native browser `#RRGGBBAA` support (`onboarding.go:1642`: `'#' + tray.color.replace(/^#/, '')`). Empty colour → dashed `repeating-conic-gradient` placeholder instead of a broken swatch (`onboarding.go:1643-1645`, CSS `.ams-color.empty`).

**Remaining-% is a guess, not a measurement.** The frontend computes `Math.round((tray.remain / 1000) * 100) + '%'` (`onboarding.go:1648`) — i.e. it assumes a **1000 mm** full spool and ignores `tray_weight` entirely, so a 250 g spool and a 1 kg spool read identically. `remain: -1` suppresses the label. Snapmaker has no equivalent value at all.

---

## 7. Known unknowns / deferred AMS work

| Item | State | Citation |
|---|---|---|
| Periodic `pushall` on P1S | research says "must call periodically"; code sends it **once at connect** — no timer anywhere | `research-notes.md:141` vs `client.go:375-376` |
| Real P1S MQTT capture | never done; delta behaviour was inferred then patched defensively ("Verification needed with real P1S MQTT capture") | `K-033-spec:386` |
| `ams_exist_bits` / `tray_exist_bits` | parsed, never consumed; bit meaning "may vary by firmware" | `parser.go:168-169`, `spec:304-308` |
| AMS-HT unit IDs 128-135 | documented, unmodelled; breaks `(id*4)+i` global-ID math | `research-notes.md:111` |
| `k` (linear-advance K-value) field | documented on wire, not in structs | `research-notes.md:125` |
| `ams_status` bits (runout) | referenced as the runout mechanism, never parsed | `research-notes.md:100` |
| `ams_filament_drying` JSON | named + 65 °C cap only, no payload | `research-notes.md:96` |
| `ams_user_setting` / `ams_get_rfid` JSON | named only | `research-notes.md:102` |
| K-082 load/unload | backlog, empty plan; gated on K-032 | `backlog/K-082:17` |
| K-032 per-tool filament status (Bambu **and** Snapmaker `filament_feed`/`filament_detect`) | backlog, empty plan | `backlog/K-032:17` |
| K-034 humidity indicator | in `done/` with **all plan boxes unchecked**; no decision-log entry | `done/K-034:36-49` |
| K-073 HMS/vendor-neutral decoupling | backlog-only, no `done/` file; revisit when Snapmaker gains multi-fault reporting | `backlog/K-073:17` |
| X1C + AMS 1 in LAN mode | blocked — cloud MQTT only, K-106 | `done/K-033:81` |
| Snapmaker filament code | **zero** lines exist | grep of `internal/printers/snapmaker/*.go` |
| Humidity display for index-only case | shipped frontend has a `humidity + '/5'` branch for "P1S AMS 1: humidity index only" — contradicts every doc saying P1S AMS 1 has **no** humidity sensor; likely dead code, untested | `onboarding.go:1607-1610` vs `K-034:24`, `research-notes.md:94` |
| Playwright cannot exercise Bambu printers | so AMS UI has no E2E coverage | `backlog/K-070-*` |

---

## 8. Repo conventions worth preserving (`AGENTS-bak.md`)

No live `AGENTS.md` at repo root — `AGENTS-bak.md` is the surviving copy (removed by F-019). Content is **docker-first build/run verification**, written for a multi-agent fleet where concurrent workers share one host (`AGENTS-bak.md:8-13`):

- Derive `WORKTREE` from the worktree basename under `.fleet/worktrees/<pet-name>/`, empty otherwise; image/container name is `printer-dashboard${WORKTREE:+-$WORKTREE}` (`:15-19`, `:33-40`).
- Always `docker rm -f "$NAME" || true` immediately before `docker run` (`:20-21`).
- Publish a **random** host port `-p 0:8080`, read it back with `docker port "$NAME" 8080` — never a fixed `8080:8080` (`:22-25`).
- Mount the token cache and `config.yaml` from **one shared** `${HOME}/.printer-dashboard/`, not per-worktree: the Bambu token cache is small, written only at login, and tied to the cloud account, so sharing lets workers reuse one authenticated session; `config.yaml` describes the host's printers so it is one canonical file per machine (`:26-31`).
- No required env vars; reads `/app/config.yaml`; listens on `:8080` in-container (`:52-55`). Single container by design — **no docker-compose** (multi-service orchestration tracked separately) (`:57-58`).

**Conflict to note:** `docs/fix-p1s-ams-loading-spec.md:337-375` violates these conventions — it uses a fixed `-p 8081:8081`, a hard-coded image name `printer-dashboard-k033:test`, and another user's home path `/home/chris/.printer-dashboard`. Treat `AGENTS-bak.md` as authoritative for any re-verification.

Other durable conventions from `.fleet/notes.md`: `internal/server/onboarding.go` is one large file with no CSS/JS build step or linter, and `renderCard()`/`updateCard()` are **two independently-maintained JS paths that must be hand-synced** on any markup change (`notes.md` "Codebase gotchas") — the `amsHtml()` shared-helper pattern exists specifically to defuse this for AMS (`K-033-spec:128-138`, "prevents the K-053-class drift"). Testing philosophy is TDD, table-driven, `go test ./... -race -count=1`, 100 % pure-logic coverage target. Bambu-first is a standing user priority: "Snapmaker is the exception, Bambu is the norm" (`notes-archive.md:1298-1300`).

---

## 9. Implementation map (what exists today)

| Layer | File:line | State |
|---|---|---|
| Wire structs | `bambu/parser.go:45,164-193` | done |
| Public model | `printers/interface.go:56-89` | done |
| Parse | `bambu/client.go:801-860` (`parseAMSData`) | done |
| Delta merge | `bambu/client.go:602-620` + helpers `887-972` | done (PR #28) |
| `pushall` | `bambu/client.go:403-413`, called `:376` | done, **connect-only** |
| UI | `server/onboarding.go:1587-1673` (`amsHtml`), `:1266-1280` (updateCard), `:1844` (renderCard, inside `.bottom-sections`) | done |
| Tests | `bambu/ams_delta_test.go` (10 cases), `bambu/merge_test.go` (13 cases) | done |
| AMS commands | `bambu/commands.go` | **absent** (K-082) |
| Snapmaker filament | `internal/printers/snapmaker/*.go` | **absent** (K-032) |
| Humidity indicator | — | **absent** (K-034 nominal only) |
