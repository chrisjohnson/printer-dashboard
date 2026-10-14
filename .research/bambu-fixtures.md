# Bambu package — captured payloads, wire formats, and semantics proven by the tests

Scope (read completely): `internal/printers/bambu/client_test.go` (3258 l), `parser_test.go` (747 l),
`merge_test.go` (298 l), `ams_delta_test.go` (611 l), `commands_test.go` (232 l),
`hms_messages_test.go` (203 l). Interpreted against `client.go` (1009 l), `parser.go` (244 l),
`commands.go` (154 l), `hms_messages.go` (101 l), plus `auth.go`/`auth_test.go` for MQTT credentials.
Fleet context: P1S (stock, pre-Bambu-Connect firmware, **cloud MQTT only**), H2S (Bambu Connect),
Snapmaker U1 (Paxx — different package, out of scope here).

---

## 0. Two framing facts that color everything below

1. **The test client has no model.** `testPrinterClientConfig` (`client_test.go:109-116`) is
   `{ID: "test-id", Name: "Test Printer", Type: "bambu", Serial: "SERIAL001", Host: "10.0.0.1",
   AccessCode: "1234"}` — **no `Model` field**. Every `handleReport` fixture below is therefore
   *model-agnostic at runtime*; the "P1S" / "H2S" labels come **only from comments**, not from any
   model-gated code path. `handleReport` never branches on `c.model` except for HMS message
   lookup (`client.go:630` passes `c.model`). So "H2S sends info.temp" is a documentation claim,
   not an enforced behavior — a P1S sending `info.temp` would be decoded identically.
2. **No test asserts an MQTT topic string, QoS, keepalive, client-id, or TLS config.** The mock
   `publishFn`/`subscribeFn` signatures take topic/qos but every test discards them (`func(_ string,
   _ byte, _ bool, payload interface{})`, e.g. `client_test.go:2730`, `2752`, `2774`, `2796`).
   Section 2 splits "asserted by a test" from "only true in source".

---

## 1. Literal JSON report fixtures (verbatim)

### 1.1 Full status report — the richest fixture in the suite
`parser_test.go:30-56`, test case name `"full valid report"`. Model-agnostic (exercises the whole
`printStatus` struct incl. `wifi_signal`, `stg_cur`, `stg_total`, `lifecycle`, `gcode_file_time`):

```json
{
        "print": {
                "gcode_state": "RUNNING",
                "mc_percent": 50,
                "mc_remaining_time": 3600,
                "bed_temper": 55.5,
                "bed_target_temper": 60.0,
                "nozzle_temper": 210.0,
                "nozzle_target_temper": 220.0,
                "chamber_temper": 30.0,
                "chamber_target_temper": 45.0,
                "layer_num": 5,
                "total_layer_num": 100,
                "wifi_signal": "good",
                "home_flag": 1,
                "stg_cur": 2,
                "stg_total": 10,
                "print_error": 0,
                "gcode_file": "model.gcode",
                "gcode_file_time": "2024-01-15T10:00:00Z",
                "lifecycle": "printing"
        },
        "camera": {
                "ipcam_url": "rtsp://camera",
                "timelapse_url": "http://timelapse"
        }
}
```

Expected struct (`parser_test.go:57-82`): `McPercent=50`, `McRemainingTime=3600`, `BedTemper=55.5`,
`BedTarget=60.0`, `NozzleTemper=210.0`, `NozzleTarget=220.0`, `ChamberTemper=30.0`,
`ChamberTargetTemper=45.0`, `LayerNum=5`, `TotalLayerNum=100`, `WifiSignal="good"`, `HomeFlag=1`,
`StgCur=2`, `StgTotal=10`, `PrintError=0`, `Lifecycle="printing"`, `Camera{IPCamURL:"rtsp://camera",
TimelapseURL:"http://timelapse"}`.

**Surprising:** `gcode_file_time` appears in the fixture but there is **no struct field for it** —
`printStatus` (`parser.go:22-46`) has no `GcodeFileTime`, so it is silently dropped. Likewise
`wifi_signal`, `stg_cur`, `stg_total`, `lifecycle` are parsed into pointers that `handleReport`
**never reads** (grep: `WifiSignal`/`StgCur`/`StgTotal`/`Lifecycle` appear only in `parser.go`).
They are dead payload. `stg_cur`/`stg_total` are never surfaced anywhere in the dashboard.

### 1.2 `null` / absent pointer fields
`parser_test.go:87-94` — proves Go's decoder leaves `*float64` nil for explicit `null`:

```json
{
        "print": {
                "gcode_state": "IDLE",
                "gcode_file": "",
                "bed_temper": null,
                "home_flag": 0
        }
}
```
Expected: `GcodeState:"IDLE"`, `GcodeFile=ptr("")`, `BedTemper=nil`, `HomeFlag=0`, `Camera=nil`.

### 1.3 No `print` section (camera-only report)
`parser_test.go:108-112` and `client_test.go:804-808`:
```json
{
        "camera": {
                "ipcam_url": "rtsp://camera"
        }
}
```
`client_test.go:810-819` asserts `State` and `Online` are **unchanged** — `handleReport` returns
early at `client.go:468-470` after capturing the camera URL.

### 1.4 `info.temp` — the H2S packed-integer chamber fixture family
All four are labeled H2S in comments. Wire encoding asserted (`client.go:566-587`):
`low 16 bits = current °C`, `high 16 bits = target °C`.

* `client_test.go:618-630` — `TestHandleReport_ChamberTempFallback`, comment at `:616-617`:
  *"Report without chamber_temper but with info.temp (H2S-style packed integer). 3932188 =
  (60 << 16) | 28 → current = 28°C, target = 60°C."*
```json
{
        "print": {
                "gcode_state": "RUNNING",
                "gcode_file": "test.gcode",
                "mc_percent": 25,
                "mc_remaining_time": 7200,
                "nozzle_temper": 200.0,
                "home_flag": 0,
                "info": {
                        "temp": 3932188
                }
        }
}
```
* `client_test.go:647-660` — `TestHandleReport_ChamberTempDirectPreferred`: same shape plus
  `"chamber_temper": 30.0` and `"info": {"temp": 28.5}` → asserts `ChamberTemp == 30.0`
  (direct field wins over `info.temp`).
* `client_test.go:1783-1792` — `TestHandleReport_InfoTempPackedInteger`, comment `:1782`:
  *"3932220: low 16 bits = 60 (current), high 16 bits = 60 (target)."*
```json
{
        "print": {
                "gcode_state": "RUNNING",
                "gcode_file": "test.gcode",
                "nozzle_temper": 200.0,
                "info": {
                        "temp": 3932220
                }
        }
}
```
* `client_test.go:1814-1821` — `..._Asymmetric`, comment `:1813`: `"(60 << 16) | 45 = 3932205:
  current = 45, target = 60."`
```json
{
        "print": {
                "gcode_state": "RUNNING",
                "info": {
                        "temp": 3932205
                }
        }
}
```
* `client_test.go:1844-1851` and `1873-1880` — `..._OutOfRangeIgnored` /
  `..._PreservesPreviousWhenOutOfRange`, comment `:1842`:
  *"50000000: low 16 bits = 61568 (out of range), high 16 bits = 762 (out of range)."*
```json
{
        "print": {
                "gcode_state": "RUNNING",
                "info": {
                        "temp": 50000000
                }
        }
}
```
* `parser_test.go:121-131` — `"H2S chamber temp fallback from info.temp"` uses a **fractional**
  `"temp": 28.5` and asserts only that `Info.Temp == 28.5` and `ChamberTemper == nil`
  (`// info.temp is separate from chamber_temper`). At runtime this value hits the truncation
  warning path in `client.go:575-577` (`info.temp has fractional part …, truncating to %d`).

### 1.5 `home_flag` bitmask fixtures
`client_test.go:523-566`, `TestHandleReport_HomeFlag`, four subtests. Comment at `:544-547`:
*"Exercises that only bit 0 (value 1) is treated as the 'homed' signal — other bits in the mask
(e.g. value 2) must not be mistaken for it."*

| fixture (verbatim, one line each) | line | `Homed` |
|---|---|---|
| `{"print": {"gcode_state": "IDLE", "home_flag": 1}}` | 526 | `true` |
| `{"print": {"gcode_state": "IDLE", "home_flag": 0}}` | 536 | `false` |
| `{"print": {"gcode_state": "IDLE", "home_flag": 2}}` | 549 | `false` |
| `{"print": {"gcode_state": "IDLE", "home_flag": 3}}` | 559 | `true` |

`client.go:672-681` documents `home_flag` as a bitmask where bit 0 = `AXIS_HOMED` and notes it is
**not a pointer** on Bambu reports, so `Homed` is re-set on *every* report (never preserved).

### 1.6 `print_error` / `FAILED` fixtures
* `client_test.go:700-706` → `State="error"`, `ErrorMsg="print_error=503"`:
```json
{
        "print": {
                "gcode_state": "FAILED",
                "gcode_file": "failed.gcode",
                "print_error": 503
        }
}
```
* `client_test.go:722-727` — `FAILED` with **no** `print_error` key → `State="error"`,
  `ErrorMsg=""` (asserted empty, `:736-738`).
* `client_test.go:745-753` — `RUNNING` + `print_error: 123` → `error` / `print_error=123`
  (comment `:744`: *"Non-zero print_error with RUNNING state should also trigger error."*).
* `client_test.go:770-778` — `print_error: 0` → stays `printing`, `ErrorMsg=""`.
* `client_test.go:946-951` — `{"print":{"gcode_state":"RUNNING","print_error":999}}` →
  `error` / `print_error=999`.

### 1.7 HMS blocks (all `print.hms` arrays)
Every HMS fixture is a two-field `{"attr": <uint32>, "code": <uint32>}` array. The three distinct
pairs used:

| fixture | lines | decoded |
|---|---|---|
| `"hms": [{"attr": 201327360, "code": 196615}]` | 977 | `HMS_0C00-0300-0003-0007`, xcam/common |
| `"hms": [{"attr": 83886080, "code": 65536}]` | 1013, 1088, 1115, 1141, 1178, 1230, 1287, 1335 | `HMS_0500-0000-0001-0000`, mainboard/fatal |
| `"hms": [{"attr": 134217728, "code": 131072}]` | 1042 | `HMS_0800-0000-0002-0000`, toolhead/serious |
| `"hms": []` | 1195, 1349 | explicit clear |

Full context of the warning-only case (`client_test.go:972-979`), comment `:970-971`:
*"severity 'common' (code>>16 == 3) — module byte 0x0C (xcam), matches the pybambu oracle sample
used in parser_test.go."*
```json
{
        "print": {
                "gcode_state": "RUNNING",
                "gcode_file": "print.gcode",
                "print_error": 0,
                "hms": [{"attr": 201327360, "code": 196615}]
        }
}
```
The "cover-off" scenario (`client_test.go:1082-1090`), the second of a two-report sequence:
```json
{
        "print": {
                "gcode_state": "RUNNING",
                "gcode_file": "benchy.gcode",
                "print_error": 0,
                "mc_percent": 41,
                "hms": [{"attr": 83886080, "code": 65536}]
        }
}
```
Heartbeat-style reports with the `hms` key **entirely absent** (`client_test.go:1153-1158`,
`1241-1247`, `1292-1297`, `1303-1307`):
```json
{
        "print": {
                "gcode_state": "RUNNING",
                "mc_percent": 42
        }
}
```
```json
{
        "print": {
                "gcode_state": "RUNNING",
                "print_error": 0,
                "mc_percent": 55
        }
}
```
```json
{
        "print": {
                "mc_percent": 60
        }
}
```
Explicit-clear-with-absent-gcode_state (`client_test.go:1346-1351`):
```json
{
        "print": {
                "mc_percent": 61,
                "hms": []
        }
}
```

### 1.8 `lights_report` and `system.ledctrl`
`lights_report` lives **inside `print`**, not `system` (`parser.go:44`, `parser_test.go:212-268`):
```json
{"print": {"gcode_state": "RUNNING", "lights_report": [{"node": "chamber_light", "mode": "on"}]}}
```
(`client_test.go:2442-2449`, mode `"off"` at `2464-2471`). Multi-entry fixture
(`client_test.go:2487-2495`, `parser_test.go:233-241`):
```json
{
        "print": {
                "gcode_state": "RUNNING",
                "lights_report": [
                        {"node": "status_light", "mode": "on"},
                        {"node": "chamber_light", "mode": "on"}
                ]
        }
}
```
Empty array (`client_test.go:2531-2536`, `parser_test.go:255-260`):
`{"print": {"gcode_state": "RUNNING", "lights_report": []}}` → `LightOn` stays nil.
Only `chamber_light` matters; a `lights_report` with just `status_light`
(`client_test.go:2511-2518`) leaves `LightOn == nil`.

`system.ledctrl` is still **parsed** (`parser_test.go:171-185`, `187-210`) but explicitly no longer
drives state — `TestHandleReport_LightState_SystemLedCtrlNoLongerSetsLight`
(`client_test.go:2545-2563`), comment `:2548`: *"The old system.ledctrl path should no longer set
LightOn."*
```json
{
        "system": {
                "ledctrl": {
                        "node": "chamber_light",
                        "mode": "on"
                }
        }
}
```
`client.go:465-466` comment: *"System reports can carry other fields (e.g. command ACKs) but light
state is reported via print.lights_report"*; `client.go:477-479`: *"The old system.ledctrl path
only carried command ACKs, not the live state."*

### 1.9 AMS fixtures (`ams_delta_test.go`) — the P1S/H2S differential
`fullAMSReport` (`ams_delta_test.go:16-49`), comment `:13-15`: *"a complete **H2S-style** AMS
report JSON (all units, all 4 trays per unit populated). Used as the baseline 'full update' that
seeds the cache before sending **P1S-style** delta reports."*
```json
{
  "print": {
    "gcode_state": "RUNNING",
    "gcode_file": "benchy.gcode",
    "ams": {
      "ams": [
        {
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
        }
      ],
      "tray_now": "0",
      "ams_exist_bits": "1",
      "tray_exist_bits": "f"
    }
  }
}
```
Assertions (`:181-234`): `Humidity="3"`, `HumidityRaw="23"`, `Temp="25.0"`, 4 trays;
tray0 `PLA/FF0000FF/Loaded=true/remain=50000/min=190/max=230`; tray1 `type=""`,
`Loaded=false`, `remain=-1`; `ActiveTrayID=0`.

`amsEmptyAmsArrayReport` (`:53-63`) — *"a P1S delta where the `ams` key is present but the inner
`ams` array is absent — only tray_now changed"*:
```json
{
  "print": {
    "gcode_state": "RUNNING",
    "ams": {
      "tray_now": "2",
      "ams_exist_bits": "1",
      "tray_exist_bits": "f"
    }
  }
}
```
`amsTrayNowOnlyReport` (`:67-78`) — *"the ams array is explicitly empty (`[]`)"*:
```json
{
  "print": {
    "gcode_state": "RUNNING",
    "ams": {
      "tray_now": "3",
      "ams": [],
      "ams_exist_bits": "1",
      "tray_exist_bits": "f"
    }
  }
}
```
`amsPartialTrayReport` (`:82-102`) — *"one unit but only tray index 0 is present (the other 3 trays
changed nothing)"*:
```json
{
  "print": {
    "gcode_state": "RUNNING",
    "ams": {
      "ams": [
        {
          "id": "0",
          "tray": [
            {"id": "0", "state": 3, "tray_type": "PETG", "tray_color": "FF8800FF",
             "tray_info_idx": "GFA01", "nozzle_temp_min": "220", "nozzle_temp_max": "250",
             "remain": 45000, "tray_weight": "", "tag_uid": "00000000"}
          ]
        }
      ],
      "tray_now": "0",
      "ams_exist_bits": "1",
      "tray_exist_bits": "f"
    }
  }
}
```
`amsNoTrayArrayReport` (`:107-125`) — *"the `tray` field is entirely absent (only unit-level metadata
like humidity changed — **common for H2S AMS 2 Pro with humidity sensors**)"*:
```json
{
  "print": {
    "gcode_state": "RUNNING",
    "ams": {
      "ams": [
        {
          "id": "0",
          "humidity": "4",
          "humidity_raw": "31",
          "temp": "26.5"
        }
      ],
      "tray_now": "0",
      "ams_exist_bits": "1",
      "tray_exist_bits": "f"
    }
  }
}
```
`amsNoTrayNowReport` (`:129-146`) — *"the ams key is present with unit data but `tray_now` is
absent — the active tray must be retained"*:
```json
{
  "print": {
    "gcode_state": "RUNNING",
    "ams": {
      "ams": [
        {
          "id": "0",
          "humidity": "2",
          "humidity_raw": "20",
          "temp": "24.0"
        }
      ],
      "ams_exist_bits": "1",
      "tray_exist_bits": "f"
    }
  }
}
```
`noAmsKeyReport` (`:150-156`) — *"the `ams` key is entirely absent from the JSON (not even the shell
key) — AMS data should be untouched"*:
```json
{
  "print": {
    "gcode_state": "RUNNING",
    "mc_percent": 50
  }
}
```
H2S full-replacement fixture (`ams_delta_test.go:470-490`, inline in
`TestHandleReport_AMS_H2SFullUpdateReplacesCache`):
```json
{
  "print": {
    "gcode_state": "RUNNING",
    "ams": {
      "ams": [{
        "id": "0",
        "humidity": "1",
        "temp": "22.0",
        "tray": [
          {"id": "0", "state": 0, "tray_type": "", "tray_color": "000000FF", "remain": -1},
          {"id": "1", "state": 3, "tray_type": "PETG", "tray_color": "0000FFFF", "remain": 80000},
          {"id": "2", "state": 0, "tray_type": "", "remain": -1},
          {"id": "3", "state": 0, "tray_type": "", "remain": -1}
        ]
      }],
      "tray_now": "1",
      "ams_exist_bits": "1",
      "tray_exist_bits": "2"
    }
  }
}
```
Two-unit fixtures (`:525-538` full, `:547-557` delta touching only unit 1):
```json
{"print": {"gcode_state": "RUNNING", "ams": {"ams": [
  {"id": "0", "humidity": "3", "tray": [{"id": "0", "state": 3, "tray_type": "PLA", "tray_color": "FF0000FF", "remain": 50000}]},
  {"id": "1", "humidity": "4", "tray": [{"id": "0", "state": 3, "tray_type": "PETG", "tray_color": "00FF00FF", "remain": 75000}]}
], "tray_now": "5", "ams_exist_bits": "3", "tray_exist_bits": "f"}}}
```
```json
{"print": {"gcode_state": "RUNNING", "ams": {"ams": [
  {"id": "1", "humidity": "5", "tray": [{"id": "0", "state": 3, "tray_type": "TPU", "tray_color": "0000FFFF", "remain": 90000}]}
], "tray_now": "5"}}}
```
Note `ams_exist_bits` / `tray_exist_bits` appear in every AMS fixture but are **never consumed** —
`AmsExistBits`/`TrayExistBits` (`parser.go:168-169`) are read by nothing.

### 1.10 `mclogs` / `pushing`
**No `mclogs` fixture exists anywhere in the test suite** (grep over all `*_test.go`).
`pushing` appears only as the hard-coded pushall payload in source, never asserted by a test:
`client.go:406` → `{"pushing":{"command":"pushall","version":1,"push_target":1}}`.

### 1.11 Camera-report fixtures
`client_test.go:406-410` → `"rtsp://camera.example.com/stream"`; `:439-443` →
`"rtsp://preferred-stream/feed"`; `:390` → `"rtsp://mqtt-priority/stream"` (set directly on
`c.camIPCamURL`). All assert `Type="internal"`, `Label="Camera"`.

---

## 2. MQTT wire facts

### Asserted by tests
| Fact | Value | Citation |
|---|---|---|
| Broker (non-china / "global" / "europe") | `us.mqtt.bambulab.com:8883` | `auth_test.go:1077-1098` |
| Broker (`region == "china"`) | `cn.mqtt.bambulab.com:8883` | `auth_test.go:1086-1089` |
| Username format | `u_{user_id}` → `"u_12345"` | `auth.go:745-749`, `auth_test.go:1100-1107` |
| Password | the JWT access token (`"jwt-token-abc"`) | `auth.go:751-753`, `auth_test.go:1108-1110` |
| Scheme actually used | `"ssl://" + MQTTBroker(region)` | `client.go:248` |
| Error when not connected | `bambu test-id: not connected to cloud MQTT` | `client_test.go:2615`, `2628` |
| Publish timeout error | `bambu test-id: command publish timeout` | `client_test.go:2662` |
| Audit log line | `bambu test-id: sending command pause` and **payload must not appear in the log** | `client_test.go:2712-2718` |
| Connect retry | initial connect retried indefinitely; test uses `brokerOverride = "tcp://" + 127.0.0.1:port` and a hand-rolled MQTT 3.1.1 CONNACK `{0x20,0x02,0x00,0x00}` | `client_test.go:3088-3114`, `3122-3207` |
| Retry honors ctx cancellation during backoff | `client_test.go:3213-3258` | |

### Only in source — **not asserted by any test**
| Fact | Value | Citation |
|---|---|---|
| Subscribe topic | `device/{serial}/report` | `client.go:365-366` |
| Publish topic (commands **and** pushall) | `device/{serial}/request` | `client.go:734`, `405` |
| Subscribe QoS | `0` | `client.go:366` |
| Publish QoS / retain | `0`, `retained=false` | `client.go:735`, `407` |
| Client ID | `printer-dashboard-{cfg.ID}-{UnixNano}` | `client.go:255` |
| TLS | `&tls.Config{InsecureSkipVerify: true}` — *"Bambu's cloud cert may not be in system store"* | `client.go:258-260` |
| Keepalive / ping timeout | `30s` / `10s` | `client.go:266-267` |
| AutoReconnect + max reconnect interval | `true`, `30s` | `client.go:264-265` |
| Clean session | `true` | `client.go:268` |
| Connect token wait | `15s` per attempt | `client.go:322` |
| Subscribe token wait | `10s` | `client.go:367` |
| Command publish wait | `10s` | `client.go:736` |
| Pushall wait | `5s` | `client.go:408` |
| Initial backoff | 1s base doubling, capped 30s (test seams `connectBackoffBase/Max`) | `client.go:39-45`, `299-352` |
| Report-silence warning | 60s → *"printer may be in LAN Mode … See GitHub issue #22."* | `client.go:47-53`, `389-401` |
| Disconnect quiesce | `1000` ms | `client.go:289` |

Because the mock discards topic/QoS (`client_test.go:2730` etc.), a regression that published
commands to the wrong topic or with QoS 1 would **not** fail any test.

---

## 3. Command JSON asserted byte-for-byte

`commands_test.go:8-59` compares *normalized* JSON (unmarshal→remarshal) for the four basic
commands; the rest compare the `param` string exactly. Exact expected bytes:

| Command | Exact JSON | Citation |
|---|---|---|
| pause | `{"print":{"command":"pause"}}` | `commands_test.go:15-18` |
| resume | `{"print":{"command":"resume"}}` | `:20-23` |
| stop | `{"print":{"command":"stop"}}` | `:25-28` |
| skip_object | `{"print":{"command":"project_file","param":"skip_object"}}` | `:30-33` |
| set_bed_temp(60) | `{"print":{"command":"gcode_line","param":"M140 S60\n"}}` | `:73-89` (asserts `command=="gcode_line"`, `param=="M140 S60\n"`) |
| set_nozzle_temp(210) | `{"print":{"command":"gcode_line","param":"M104 S210\n"}}` | `:91-104` |
| home_all (G28) | `{"print":{"command":"gcode_line","param":"G28\n"}}` | `:162-178` |
| jog(10,-5,0,1500) | `{"print":{"command":"gcode_line","param":"G91\nG1 X10 Y-5 F1500\nG90\n"}}` | `:187-194` |
| jog(0,0,2.5,600) | `{"print":{"command":"gcode_line","param":"G91\nG1 Z2.5 F600\nG90\n"}}` | `:195-202` |
| jog(1,2,3,3000) | `{"print":{"command":"gcode_line","param":"G91\nG1 X1 Y2 Z3 F3000\nG90\n"}}` | `:203-210` |
| ledctrl on | `{"system":{"sequence_id":"0","command":"ledctrl","led_node":"chamber_light","led_mode":"on","led_on_time":500,"led_off_time":500,"loop_times":0,"interval_time":0}}` | `:106-141` (field-by-field; `sequence_id` asserted as the **string** `"0"`) |
| ledctrl off | same with `"led_mode":"off"` | `:143-159` |
| set_ctt(temp) | `{"print":{"command":"set_ctt","ctt_val":<int>,"temper_check":true}}` | **no test** — `setCTTCommand` (`commands.go:47-54`) is untested; `SetChamberTemp` (`client.go:774-777`) likewise |

Jog details locked in by the tests: zero-delta axes are **omitted entirely** (not `Z0`), `%g`
formatting (no trailing zeros), wrapped in `G91 … G90` (`commands.go:118-144`).
`setLightCommand` comment (`commands.go:90-92`): *"The Bambu firmware requires all of these fields;
without them the command is silently ignored."*
`mustMarshal` panics on an unmarshalable value (`commands_test.go:61-71`).
Delegation tests (`client_test.go:2725-2811`) assert `Pause/Resume/Cancel/SkipObject` publish
exactly `pauseCommand()/resumeCommand()/stopCommand()/skipObjectCommand()` bytes.

---

## 4. Merge / delta semantics (PR #28 / K-033)

Commit `9d9a97d "fix: merge P1S AMS delta updates instead of wiping cache (#28)"`. Commit body:
*"P1S sends delta MQTT updates where only changed fields are included. The ams key may be present
with an empty ams array (only tray_now changed) or with unit objects lacking the tray array. The
previous code replaced `s.AMSUnits` wholesale, wiping cached filament slot data on every such
delta."* It also removed the `else` branch that reset `ActiveTrayID = -1` when `tray_now` was
absent. A second commit in the same PR: *"P1S reports `state=0` for all AMS trays, which sets
`Loaded=false` … so all P1S slots showed 'Empty' even though valid tray_type/color/remain data was
being received"* → `loaded` now also true when `tray_type != ""` (`client.go:830-835`).
`docs/fix-p1s-ams-loading-spec.md` adds the mechanism: `parseAMSData` returns `nil` when the inner
`ams` array is empty → `s.AMSUnits = nil` → `omitempty` drops `ams_units` from the serialized
status → frontend renders nothing. It also states plainly: *"The `client_test.go` file has **zero**
AMS test cases"* (pre-fix).

### Merge rules (asserted in `merge_test.go`, implemented `client.go:887-974`)
* `mergeAMSData(nil, cached)` → `cached` verbatim (`merge_test.go:13-24`).
* `mergeAMSData([]AMSUnit{}, cached)` → `cached` (`:26-37`) — **empty and nil are treated alike**.
* `mergeAMSData(new, nil)` → `new` (`:39-50`); `mergeAMSData(nil,nil)` → `nil` (`:170-175`).
* Unit-level scalars `Humidity`/`HumidityRaw`/`Temp` use `firstNonEmpty(new, cached)` — an **empty
  new value retains the cached one** (`:52-103`, `client.go:908-910`).
* Trays merge **per `Index`**: matching index → whole slot object replaced by the new one;
  cached-only indices retained; new-only indices appended (`merge_test.go:222-273`,
  `client.go:934-966`).
* New unit with **no** `tray` array → cached trays kept entirely (`merge_test.go:177-203`,
  `ams_delta_test.go:349-378`).
* New unit whose ID isn't cached → appended as-is (`merge_test.go:105-136`).
* Cached unit absent from the update → retained (`merge_test.go:138-168`,
  `ams_delta_test.go:524-608`).
* **Order is not ID-ordered**: new units come first, retained cached units after —
  `ams_delta_test.go:566-567`: *"mergeAMSData iterates new data first then cached, so array order
  isn't guaranteed to match ID order."* Tests therefore look units up by ID.
* `firstNonEmpty` unit test: `("a","b")→"a"`, `("","b")→"b"`, `("a","")→"a"`, `("","")→""`
  (`merge_test.go:282-295`).

### End-to-end delta behavior (`ams_delta_test.go`)
| Test | Proves |
|---|---|
| `TestHandleReport_AMS_FullUpdate_PopulatesData` (`:175`) | H2S full report populates everything, `ActiveTrayID=0` |
| `..._P1SDeltaEmptyAmsArray_RetainsCache` (`:240`) | `ams` present, inner array absent → 1 unit / 4 trays retained; `ActiveTrayID` **does** move to 2 |
| `..._P1SDeltaTrayNowOnly_RetainsCache` (`:266`) | explicit `"ams": []` → cache retained, `ActiveTrayID=3` |
| `..._P1SDeltaPartialTray_RetainsOtherTrays` (`:290`) | tray0 updated to PETG/FF8800FF/45000/min 220; trays 1-3 retained |
| `..._P1SDeltaNoTrayArray_RetainsCachedTrays` (`:349`) | unit scalars updated (`Humidity="4"`, `Temp="26.5"`), trays kept |
| `..._P1SDeltaNoTrayNow_RetainsActiveTray` (`:382`) | `ActiveTrayID` **not** reset to -1 when `tray_now` absent |
| `..._NoAmsKey_RetainsCache` (`:402`) | no `ams` key → AMS untouched, but `mc_percent` still applied |
| `..._SuccessiveDeltaStacking` (`:426`) | empty-array → partial-tray → no-tray-array deltas stack; final `Humidity="4"`, `Temp="26.5"`, `ActiveTrayID=0` |
| `..._H2SFullUpdateReplacesCache` (`:466`) | full update blanks tray0 (`type=""`, `Loaded=false`), sets tray1 PETG, `ActiveTrayID=1` |
| `..._MultipleUnits_Deltas` (`:524`) | delta on unit 1 leaves unit 0 intact |

**Asymmetry worth flagging:** "H2S full update replaces the cache" is only true because H2S happens
to send every field. A full update that *omits* a unit-level scalar silently keeps the stale value
(`firstNonEmpty`), while an omitted *tray* index also silently keeps its stale slot. Only
tray-level objects are truly replaced. Stale comment: `ams_delta_test.go:451` contains a
duplicated/contradictory Humidity comment (`… -> "4" (no-tray-array delta)."3" -> … -> "2"`).

`ActiveTrayID` rule (`client.go:614-620`): updated only when `tray_now != ""`; non-numeric → `-1`.
`parser.go:167` documents `tray_now` as `(ams_id*4)+tray_id`, `254`=external spool, `255`=none.

---

## 5. Field-preservation semantics (`handleReport`, `client.go:431-719`)

### Preserved (previous value kept) when absent from a report
| Field | Gate | Citation |
|---|---|---|
| `State` | only when `gcode_state != ""` | `client.go:502-514`; `client_test.go:871-890`, `1673-1743` |
| `CurrentFile` | untouched if neither `gcode_file` nor `subtask_name` present/non-empty | `client.go:532-541`; `client_test.go:1972-2032`, `2110-2141` |
| `BedTemp`, `NozzleTemp`, `BedTargetTemp`, `NozzleTargetTemp` | `!= nil` gate | `client.go:545-556`; `client_test.go:568-611`, `1555-1609` |
| `ChamberTemp` | `chamber_temper` present **and** in range, else `info.temp` low bits in range, else preserved | `client.go:557-588`; `client_test.go:670-695`, `1863-1890` |
| `ChamberTargetTemp` | `info.temp` high bits or direct `chamber_target_temper` (see quirk) | `client.go:566-597` |
| `HMSErrors` / `HMSWarnings` | preserved when the `hms` key is **absent** (nil), subject to the decay streak | `client.go:629-657`; `client_test.go:1133-1168` |
| `Progress`, `RemainingTime`, `CurrentLayer`, `TotalLayers` | `!= nil` gates | `client.go:659-670`; `client_test.go:1555-1609` |
| `AMSUnits`, `ActiveTrayID` | preserved when `ams` key absent; merged when present | `client.go:602-621` |
| `LightOn` | only set if a `chamber_light` entry exists in `lights_report` | `client.go:480-486`; `client_test.go:2507-2543` |
| `ErrorMsg` | cleared only in the non-error branch | `client.go:687-716` |

### Cleared / always overwritten
* **`CurrentFile` idle-clear rule** (`client.go:532-533`): cleared when `gcode_state != ""` **and**
  the *raw* mapped state is `"idle"` — i.e. `IDLE` or `STANDBY` (`client_test.go:2038-2108`).
  `SUCCESS` does **not** clear it (`client_test.go:2189-2222`, comment: *"The user may still want to
  see the completed file's name on the dashboard."*).
  Deliberate decoupling (K-007, `client.go:521-531`): the clear keys off raw `newState`, not the
  latched `s.State`, so **CurrentFile clears one report earlier than the COMPLETE badge unlatches**
  — locked in by `TestHandleReport_SuccessIdleIdleSequence_CurrentFileClearsBeforeState`
  (`client_test.go:2239-2298`), whose comment says a change here *"should be a conscious decision,
  not a side effect."*
* **`Homed`** is recomputed on *every* report from `home_flag & 0x1` — never preserved
  (`client.go:672-681`).
* `HMSErrors/HMSWarnings` are **replaced wholesale** whenever `hms` is present (including `[]`), and
  nil-ed out once the healthy streak threshold is reached (`client.go:629-652`).

### `gcode_file` → `subtask_name` fallback chain (`client.go:532-541`)
Order: (1) idle-clear; (2) `gcode_file` if non-nil **and** non-empty; (3) `subtask_name` if non-nil
and non-empty; (4) leave previous value. Tests: `client_test.go:1896-1922` (absent `gcode_file` →
`subtask_name`, comment *"This is the P1S behavior: the printer sends subtask_name during printing
instead of gcode_file"*), `:1924-1944` (explicit `""` also falls back), `:1946-1967`
(`gcode_file` wins when both present), `:1969-2004` (survives a heartbeat omitting both),
`:2006-2032` (neither present → untouched).

### Temperature validation bounds (`validateTemp`, `client.go:55-67`)
* `chamber_temper` (direct wire): **-50 … 100** (`client.go:563`).
* `info.temp` low 16 bits (current): **-50 … 100** (`client.go:582`).
* `info.temp` high 16 bits (target): **0 … 100** (`client.go:585`).
* `chamber_target_temper` (direct wire): **0 … 100** (`client.go:594`).
* Rationale comment (`client.go:57-60`): *"ambient temperatures can dip below zero"* vs *"the
  chamber heater can only raise temperature from ambient, never below it."*
* Out-of-range → `nil` returned + log line `temperature %.1f out of range [%.0f, %.0f], ignoring`;
  the caller only assigns on non-nil, so the previous reading survives
  (`client_test.go:1839-1890`).
* **Bed and nozzle temps get no validation at all** — raw pointers are assigned
  (`client.go:545-556`).
* Quirk, untested: direct `chamber_target_temper` is applied only when `s.ChamberTargetTemp == nil`
  (`client.go:590`). Since `s` is the *cached* status, once any report has set a chamber target, a
  later report's direct `chamber_target_temper` can never change it.
* Quirk, untested: `info.temp` is an `else if` on `chamber_temper != nil` (`client.go:557/566`), so
  an out-of-range `chamber_temper` suppresses the `info.temp` fallback for that report.

---

## 6. HMS

### Decoders (`parser.go:86-145`)
* `decodeHMSCode(attr, code)` = `fmt.Sprintf("HMS_%04X-%04X-%04X-%04X", attr>>16, attr&0xFFFF,
  code>>16, code&0xFFFF)`.
* Module = `(attr>>24)&0xFF`: `0x05 mainboard`, `0x0C xcam`, `0x07 ams`, `0x08 toolhead`,
  `0x03 mc`, anything else `"unknown"` (`parser.go:69-75`; test `parser_test.go:611-633`).
* Severity = `code>>16`: `1 fatal`, `2 serious`, `3 common`, `4 info`, else `"unknown"`
  (`parser.go:79-84`; test `parser_test.go:635-657`).
* Buckets: `fatal`/`serious` → `HMSErrors`; **everything else (common/info/unknown) → warnings**
  (`parser.go:129-145`; `parser_test.go:659-734`).

### Exact (attr, code) pairs used and their decoded strings
| attr | code | hex | `HMS_…` | module | severity | bucket |
|---|---|---|---|---|---|---|
| 201327360 | 196615 | `0x0C000300` / `0x00030007` | `HMS_0C00-0300-0003-0007` | xcam | common | warning |
| 83886080 | 65536 | `0x05000000` / `0x00010000` | `HMS_0500-0000-0001-0000` | mainboard | fatal | error |
| 134217728 | 131072 | `0x08000000` / `0x00020000` | `HMS_0800-0000-0002-0000` | toolhead | serious | error |
| `0x07<<24` | `4<<16` | `0x07000000` / `0x00040000` | `HMS_0700-0000-0004-0000` | ams | info | warning |
| `0xFF<<24` | `1<<16` | `0xFF000000` / `0x00010000` | `HMS_FF00-0000-0001-0000` | unknown | fatal | error |
| `0x03001200` | `0x00020001` | — | `HMS_0300-1200-0002-0001` | mc | serious | error |

The pybambu oracle `{attr: 201327360, code: 196615}` is validated *formulaically*, not against a
hardcoded literal — `TestDecodeHMSCode_PybambuOracle` (`parser_test.go:584-596`) rebuilds the
expected string with its own `hexPart` helper (`:601-609`).

### Vendored message table (`hms_messages.go`)
* Embedded `hms_messages_en.json` via `//go:embed` (`:25-26`); vendored from
  `greghesp/ha-bambulab` → `custom_components/bambu_lab/pybambu/hms_error_text/hms_en.json.gz`,
  fetched 2026-07-12. **Upstream has no LICENSE file** — the licensing decision is documented at
  `hms_messages.go:8-14` (`.agent/STATE.md` K-074).
* Shape: `map[topLevel]map[16-hex-key]map[message][]models` (`:34`). Top-level keys are
  `device_hms` (4998 entries) and `device_error` (946 entries) — **only `device_hms` is ever
  queried** (`:83`); `device_error` is dead in this codebase.
* Key format: `hmsMessageKey(attr, code)` = `fmt.Sprintf("%04X%04X%04X%04X", attr>>16,
  attr&0xFFFF, code>>16, code&0xFFFF)` — the dashed display string minus `HMS_` and dashes
  (`:63-70`). Test `TestHmsMessageKey` (`hms_messages_test.go:9-62`) cross-checks that stripping
  `decodeHMSCode`'s output equals the key.
  * `hmsMessageKey(0x03001200, 0x00020001)` = `"0300120000020001"` — *"The user's real,
    confirmed-correct P1S fault"* (`:17-24`).
  * `hmsMessageKey(201327360, 196615)` = `"0C00030000030007"`.
  * `hmsMessageKey(0,0)` = `"0000000000000000"`.
  * Verified against the real file: `0300120000020001` → *"The front cover of the toolhead fell
    off."* (universal, empty model list); `0C00030000030007` → *"Possible first layer defects have
    been detected. Please check the first layer quality and decide if the job should be stopped."*;
    the format example `0500050000010007` → *"MQTT Command verification failed. Please update
    Studio (including the network plugin) or Handy to the latest version…"* (no test uses it).
    The synthetic fatal/serious codes (`0500000000010000`, `0800000000020000`) are **not** in the
    table → `Message == ""`, which is why `TestSplitHMS` expectations omit `Message` for them.
* Model preference (`:79-100`): a variant whose model list matches (case-insensitive) wins over the
  universal default (empty list); model-only entries with no default return `""` for a
  non-matching model. Exercised with an **inline fixture** by swapping the `loadHMSMessages`
  function variable (`hms_messages_test.go:70-177`, keys `0300000000010000/1/2`) — deliberately
  not the ~5,000-entry real file, *"keeps these tests independent of upstream data changes"*.
* `TestLookupHMSMessage_RealVendoredFile_UserConfirmedCode` (`:189-203`) is the only test against
  the real embedded table: `decodeHMSCode(0x03001200, 0x00020001) == "HMS_0300-1200-0002-0001"`
  and `lookupHMSMessage(..., "P1S") == "The front cover of the toolhead fell off."`
* Parse failure of the embedded JSON logs and yields an empty table rather than panicking
  (`hms_messages.go:47-61`).

### HMS → state policy (`client.go:623-716`)
* Warning-only HMS leaves `State="printing"` and `ErrorMsg=""` (`client_test.go:967-1002`).
* A fatal/serious HMS entry trips `State="error"` **even with `print_error: 0` and
  `gcode_state: "RUNNING"`** — `TestHandleReport_HMS_CoverOffScenario` (`client_test.go:1057-1103`),
  comment: *"reproduces the actual failure mode this card is about … (e.g. a force/vibration-
  disturbance code on a P1S with no door sensor)"*.
* `print_error` message wins over the HMS summary when both present
  (`client_test.go:1105-1131`); HMS summary is `strings.Join(hmsEntrySummary(e), "; ")` where
  summary is `"<message> (<code>)"` or just `"<code>"` (`parser.go:112-121`).
* Absent `hms` key does **not** wipe (`client_test.go:1133-1168`); explicit `"hms": []` does
  (`client_test.go:1170-1213`).
* **Decay streak** `hmsHealthyStreakThreshold = 2` (`client.go:22`): 2 consecutive reports with a
  *present, healthy* `gcode_state` and no `hms` key clear stale HMS
  (`client_test.go:1222-1273`). A report with `gcode_state` absent **or** unhealthy resets the
  streak (`client_test.go:1280-1320`).
* Secondary un-latch: explicit `"hms": []` arriving on a heartbeat that omits `gcode_state` still
  drops `State` off `"error"` → `"idle"` (`client.go:704-716`;
  `client_test.go:1322-1364`).

---

## 7. State mapping (`parser.go:204-244`, `parser_test.go:307-387`)

| `gcode_state` | canonical | note |
|---|---|---|
| `RUNNING` | `printing` | |
| `PAUSE` | `paused` | |
| `SUCCESS` | `complete` | |
| `FINISH` | `complete` | |
| `FAILED` | `error` | |
| `IDLE` | `idle` | |
| `""` (empty) | `idle` | `mapState("")` is `idle`, but `handleReport` never calls it for `""` — state is preserved instead |
| `PREPARE` | `printing` | `parser.go:218`: *"warming up, homing, etc."* |
| `STANDBY` | `idle` | |
| anything else | `unknown` | |

`isHealthyGcodeState` (`parser.go:237-243`): true for everything except `error` and `unknown` —
so `RUNNING, PAUSE, SUCCESS, FINISH, IDLE, PREPARE, STANDBY` are healthy; `FAILED` and any unknown
value are not (`parser_test.go:363-387`, comment on `SOMETHING_ELSE`: *"maps to 'unknown' — not
confidently healthy"*).

`isErrorState` (`parser.go:226-229`) returns true for `FAILED` **and `IDLE`** (*"IDLE can be a
state after a failure too"*), tested at `parser_test.go:338-357` — but it is **dead code**: no
production caller references it (grep confirms only the definition + test).

### Hysteresis / streak thresholds
* `completeIdleStreakThreshold = 2` (`client.go:24-33`): while `State == "complete"`, an incoming
  `"idle"` only wins after 2 consecutive idle reports. Any other new state overrides `complete`
  immediately (`client.go:501-514`). Tests: `TestHandleReport_SuccessThenSingleIdle_DoesNotDropState`
  (`client_test.go:2311`), `..._SuccessThenTwoIdles_DropsStateToIdle` (`:2352`),
  `..._SuccessThenRunning_OverridesImmediately` (`:2397`), plus the boot-sequence and full-lifecycle
  walks (`:1407-1488`, `:1490-1667`) which each step through the extra IDLE report.
* `hmsHealthyStreakThreshold = 2` — see §6.
* Other timing constants: `initialConnectBackoffBase = 1s`, `initialConnectBackoffMax = 30s`,
  `reportSilenceTimeout = 60s` (`client.go:35-53`).

---

## 8. Test comments describing real-hardware behavior

* `client_test.go:616-617` — *"Report without chamber_temper but with info.temp (**H2S-style packed
  integer**). 3932188 = (60 << 16) | 28 → current = 28°C, target = 60°C."*
* `client_test.go:1692` — *"heartbeat with empty gcode_state but temperature data (**typical H2S
  heartbeat during active print**)."*
* `client_test.go:1898-1899` — *"This is the **P1S behavior**: the printer sends subtask_name
  during printing instead of gcode_file."*
* `client_test.go:1947-1948` — *"Some printer models may send both fields; gcode_file is the
  canonical source."*
* `client_test.go:1410` — *"**P1S boot sequence**: empty → SUCCESS → IDLE"*; `:1430` — *"previous
  print's SUCCESS stored in NVRAM"*.
* `client_test.go:2304-2310` — `TestHandleReport_SuccessThenSingleIdle_DoesNotDropState` doc
  comment: *"**Bambu firmware reports SUCCESS briefly right after a print finishes, then settles to
  IDLE on the very next MQTT push** — without a latch this flickers 'complete' -> 'idle' within one
  report, which a connected dashboard client would see as COMPLETE flashing and vanishing."*
  Mirrored in source at `client.go:24-33`.
* `client_test.go:1057-1061` — cover-off: *"print_error stays 0, gcode_state stays RUNNING
  throughout, but an HMS fatal/serious entry shows up mid-stream (e.g. a force/vibration-
  disturbance code on a **P1S with no door sensor**)."*
* `client_test.go:1215-1221` — *"**firmware simply stops sending the "hms" key at all** once a
  condition resolves (no explicit "hms: []" ever arrives)"* (K-072 step 6b).
* `client_test.go:1322-1327` — the opposite case: *"firmware DOES send an explicit "hms: []" to
  clear the condition, but that same report is a heartbeat that also omits gcode_state entirely."*
* `client_test.go:2224-2238` — *"locks in the exact real-world **SUCCESS → IDLE → IDLE firmware
  sequence**"* and the deliberate CurrentFile/State decoupling (K-007).
* `client_test.go:2093` — *"STANDBY also maps to idle — verify it clears too."*
* `client_test.go:237-238` / `:271` / `:350-352` — *"want 1 (BirdsEye only — **H2S has a single
  physical camera**)"*; `client.go:200-205` — *"a second stream was previously guessed at
  `/streaming/live/2`, but that path **404s on real hardware (confirmed against a live H2S)**."*
* `client_test.go:2987-2991` — *"X1-series printers (BL-P001 = X1 Carbon, etc.) must route to
  RTSPS on port 322, NOT to the bambus:// protocol on port 6000 used by P1/A1. This is the fix for
  **issue #21: X1 series cameras were incorrectly sent to port 6000, causing a tight reconnect
  loop**."*
* `client_test.go:2969` — `{"O1S", true}, // Bambu Cloud API internal code for H2S`.
* `client_test.go:3079-3086` — *"mockMQTTClient only fakes the post-construction mqtt.Client, but
  Connect constructs its own real *paho* client internally … so the only way to exercise the retry
  loop faithfully is a real (if minimal) TCP/MQTT endpoint."*
* `client.go:600-601` / `parser.go:175-177` — *"**P1S AMS 1 does not report humidity/temp
  fields**; H2S AMS 2 Pro and X1 series do. P1S sends delta updates (only changed fields); H2S
  sends full updates."*
* `client.go:830-835` — *"**P1S reports state 0 for all trays** but still provides
  tray_type/color/remain data when filament is present; treat non-empty tray_type as loaded …
  H2S/X1 empty trays always have tray_type="" so this does not change their behavior."*
* `client.go:672-679` — *"home_flag is always present (not a pointer) on Bambu reports carrying a
  'print' section."*
* `client.go:571-574` — *"If the wire sends a non-integer (e.g. firmware changes the encoding in
  the future), int64() truncates silently — warn so that drift surfaces during testing."*
* `client.go:47-53` — *"A printer in LAN Mode stops publishing to Bambu's cloud MQTT broker by
  design, so the dashboard shows it as permanently offline with no indication of why"* (K-106,
  GitHub issue #22). Relevant to the P1S fleet: the P1S here is cloud-MQTT-only, so a LAN-Mode
  toggle silently kills all telemetry apart from this 60s log warning.

---

## 9. Gaps a fixture reader should know about

1. No test asserts MQTT topic strings, QoS, retain, client-id, keepalive, or TLS settings (§2).
2. `set_ctt` / `SetChamberTemp` has no test; `setCTTCommand`'s exact bytes are inferred from
   `commands.go:47-54` only.
3. `wifi_signal`, `stg_cur`, `stg_total`, `lifecycle`, `gcode_file_time`, `ams_exist_bits`,
   `tray_exist_bits`, `timelapse_url` (log-only) are parsed but never surfaced.
4. `isErrorState` is tested but unused by production code.
5. `device_error` half of the vendored HMS table (946 entries) is never queried.
6. No test covers: direct `chamber_target_temper` after a target is already cached (permanently
   ignored, `client.go:590`); out-of-range `chamber_temper` suppressing the `info.temp` fallback;
   non-numeric `tray_now` → `-1`; `tray_now` 254/255 (external/none).
7. `ams_delta_test.go:451` carries a duplicated, self-contradicting Humidity comment.
