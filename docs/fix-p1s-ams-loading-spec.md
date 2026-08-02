# Fix: P1S AMS values not loading (delta update merge bug)

## Summary

P1S AMS filament data (slot type, color, remaining) doesn't appear in the
dashboard, while H2S AMS data loads fine. Root cause: the backend's
`handleReport` replaces `s.AMSUnits` **wholesale** on every MQTT report that
contains an `ams` key, with no delta-merge logic. P1S sends **delta updates**
(only changed fields); H2S sends **full updates**. When a P1S delta includes
the `ams` key with only metadata (e.g. `tray_now`) and an empty/absent
`ams` array or empty `tray` arrays within units, `parseAMSData` returns
`nil` or incomplete units, wiping the previously-cached AMS data. The
`omitempty` JSON tag on `AMSUnits` then causes `ams_units` to be absent
from the serialized status, so the frontend renders nothing.

## Root cause analysis

### Data flow

1. MQTT report arrives on `device/{serial}/report`.
2. `handleReport()` parses it via `parseReport()`.
3. If `p.Ams != nil` (the `ams` key is present in JSON), the code calls:
   ```go
   s.AMSUnits = parseAMSData(p.Ams)
   ```
4. `parseAMSData` returns `nil` when `len(ams.AMS) == 0` — i.e. the `ams`
   array inside the `ams` object is absent or empty.
5. `s.AMSUnits = nil` wipes the cache. `Status()` now returns `nil` for
   `AMSUnits`. JSON serialization omits the `ams_units` field (`omitempty`).
6. The frontend receives no `ams_units` → no AMS section rendered.

### P1S delta vs H2S full — the differential

- **H2S**: Every report includes the complete `ams` block (all units, all
  4 trays per unit, with type/color/remaining). `parseAMSData` always
  returns full data. Works fine.
- **P1S**: Reports are delta — only changed fields. A delta might include
  `ams` with only `tray_now` (no `ams` array at all), or `ams` with the
  array present but `tray` arrays absent within units. In both cases the
  cached AMS data is wiped.

Additionally, `handleReport` resets `s.ActiveTrayID = -1` whenever
`p.Ams.TrayNow` is empty (the `else` branch), which also clobbers the
active tray on P1S delta updates that don't include `tray_now`.

### Why there were no tests catching this

The `client_test.go` file has **zero** AMS test cases. `parseAMSData` is
completely untested. The K-033 card's verification step ("Test with
mocked MQTT data") was never completed.

## Fix

### File: `internal/printers/bambu/client.go`

#### 1. Modify `handleReport` AMS block (line ~602)

**Before:**
```go
if p.Ams != nil {
    s.AMSUnits = parseAMSData(p.Ams)
    if p.Ams.TrayNow != "" {
        if trayNow, err := strconv.Atoi(p.Ams.TrayNow); err == nil {
            s.ActiveTrayID = trayNow
        } else {
            s.ActiveTrayID = -1
        }
    } else {
        s.ActiveTrayID = -1
    }
}
```

**After:**
```go
if p.Ams != nil {
    // Merge new AMS data with cached units. P1S sends delta updates
    // (only changed fields); the ams key may be present with an empty
    // ams array or empty tray arrays within units — in those cases we
    // must retain the previously-cached data, not wipe it. H2S sends
    // full updates, so the merge is a no-op (new data replaces cache).
    s.AMSUnits = mergeAMSData(parseAMSData(p.Ams), s.AMSUnits)
    // Only update ActiveTrayID when tray_now is actually present in the
    // report. P1S delta updates may omit tray_now (only reporting other
    // AMS metadata) — resetting to -1 here would lose the active tray.
    if p.Ams.TrayNow != "" {
        if trayNow, err := strconv.Atoi(p.Ams.TrayNow); err == nil {
            s.ActiveTrayID = trayNow
        } else {
            s.ActiveTrayID = -1
        }
    }
}
```

Key changes:
- `parseAMSData(p.Ams)` result is passed through `mergeAMSData` with the
  cached `s.AMSUnits` to merge partial deltas instead of replacing.
- The `else { s.ActiveTrayID = -1 }` branch is **removed** — `tray_now`
  is only updated when present, not reset when absent.

#### 2. Add `mergeAMSData` and `mergeTrays` helper functions

Insert after `parseAMSData` (before `var _ printers.Printer = (*Client)(nil)`).

```go
// mergeAMSData merges newly-parsed AMS units with previously-cached units
// to handle P1S delta updates. The P1S sends delta updates where only
// changed fields are included — the "ams" key may be present with an empty
// ams array (only tray_now changed) or with unit objects that lack the
// "tray" array (only unit-level metadata like humidity changed). Without
// merging, these partial updates would wipe the cached filament slot data
// (type, color, remaining) that the frontend depends on.
//
// H2S sends full updates (complete ams + tray arrays every time), so the
// merge here is effectively a no-op — each new unit fully replaces its
// cached counterpart, and all trays are present.
//
// Merge rules:
//   - If newAMS is nil (empty ams array in delta): return cached as-is.
//   - For each unit in newAMS:
//     - If a cached unit with the same ID exists:
//       - Trays: new trays replace matching cached trays (by Index);
//         cached trays not in new data are retained; new trays not in
//         cache are added. If new unit has NO trays (empty Tray array),
//         the cached unit's trays are kept entirely.
//       - Humidity/HumidityRaw/Temp: use non-empty new value, else
//         retain cached value (P1S omits these on delta updates).
//     - If no cached unit matches: append the new unit as-is.
//   - Cached units not present in newAMS are retained.
func mergeAMSData(newAMS, cached []printers.AMSUnit) []printers.AMSUnit {
    if len(newAMS) == 0 {
        // Delta without AMS unit data — retain cached units entirely.
        return cached
    }

    // Index cached units by ID for O(1) lookup.
    cachedByID := make(map[int]printers.AMSUnit, len(cached))
    for _, u := range cached {
        cachedByID[u.ID] = u
    }

    seenIDs := make(map[int]bool, len(newAMS))
    merged := make([]printers.AMSUnit, 0, len(newAMS)+len(cached))

    for _, nu := range newAMS {
        seenIDs[nu.ID] = true
        if cu, ok := cachedByID[nu.ID]; ok {
            // Cached unit exists — merge.
            merged = append(merged, printers.AMSUnit{
                ID:          nu.ID,
                Humidity:    firstNonEmpty(nu.Humidity, cu.Humidity),
                HumidityRaw: firstNonEmpty(nu.HumidityRaw, cu.HumidityRaw),
                Temp:        firstNonEmpty(nu.Temp, cu.Temp),
                Trays:       mergeTrays(nu.Trays, cu.Trays),
            })
        } else {
            // No cached unit — use new unit as-is.
            merged = append(merged, nu)
        }
    }

    // Retain cached units not present in this update.
    for _, cu := range cached {
        if !seenIDs[cu.ID] {
            merged = append(merged, cu)
        }
    }

    return merged
}

// mergeTrays merges newly-parsed filament slots with cached slots. New
// slots (those in `new`) replace matching cached slots by Index; cached
// slots not in `new` are retained; new slots not in cache are added.
// If `new` is empty (delta didn't include tray array), cached trays are
// kept entirely.
func mergeTrays(new, cached []printers.FilamentSlot) []printers.FilamentSlot {
    if len(new) == 0 {
        // Delta didn't include tray data — retain cached trays.
        return cached
    }

    newByIDx := make(map[int]printers.FilamentSlot, len(new))
    for _, t := range new {
        newByIDx[t.Index] = t
    }

    seen := make(map[int]bool, len(cached))
    merged := make([]printers.FilamentSlot, 0, len(new)+len(cached))

    // Start with cached trays, updating those that appear in new data.
    for _, ct := range cached {
        seen[ct.Index] = true
        if nt, ok := newByIDx[ct.Index]; ok {
            merged = append(merged, nt)
        } else {
            merged = append(merged, ct)
        }
    }

    // Append new trays not in cache.
    for _, nt := range new {
        if !seen[nt.Index] {
            merged = append(merged, nt)
        }
    }

    return merged
}

// firstNonEmpty returns a if non-empty, otherwise b.
func firstNonEmpty(a, b string) string {
    if a != "" {
        return a
    }
    return b
}
```

### File: `internal/printers/bambu/client_test.go`

Add a new test section for AMS delta update handling. All tests use the
existing `mockMQTTClient` / `newTestPrinterClient` / `newMockMessage` test
helpers already in the file.

#### Test cases

1. **`TestHandleReport_AMS_FullUpdate_PopulatesData`** — A report with
   complete `ams` data (matching the H2S protocol) populates `s.AMSUnits`
   with the correct units, trays, types, colors, and `ActiveTrayID`.

2. **`TestHandleReport_AMS_P1SDeltaEmptyAmsArray_RetainsCache`** — After
   a full update sets AMS data, a subsequent P1S-style delta report with
   `ams` present but `ams` array empty (`"ams": {"tray_now": "0"}`)
   **retains** the cached `AMSUnits` and does not set them to nil.

3. **`TestHandleReport_AMS_P1SDeltaTrayNowOnly_RetainsCache`** — After
   a full update, a delta with `"ams": {"tray_now": "3", "ams": []}`
   updates `ActiveTrayID` to 3 but retains the cached `AMSUnits`.

4. **`TestHandleReport_AMS_P1SDeltaPartialTray_RetainsOtherTrays`** —
   After a full update with 4 loaded trays, a delta that includes one
   unit with only tray index 0 (changed) retains trays 1, 2, 3 from the
   cache and updates tray 0 with the new data.

5. **`TestHandleReport_AMS_P1SDeltaNoTrayArray_RetainsCachedTrays`** —
   After a full update, a delta includes the `ams` array with one unit
   but the `tray` field is absent. The cached trays for that unit are
   retained (not wiped to empty).

6. **`TestHandleReport_AMS_P1SDeltaNoTrayNow_RetainsActiveTray`** —
   After a full update sets `ActiveTrayID = 0`, a delta that includes
   `ams` but omits `tray_now` does **not** reset `ActiveTrayID` to -1.

7. **`TestMergeAMSData`** — Direct unit test of `mergeAMSData`: new
   data replaces matching cached units, cached-only units are retained,
   nil new data returns cached.

8. **`TestMergeTrays`** — Direct unit test of `mergeTrays`: matching
   trays are updated, non-matching cached trays retained, new trays added,
   empty new returns cached.

9. **`TestHandleReport_AMS_NoAmsKey_RetainsCache`** — A report
   without the `ams` key at all (delta) does not touch `AMSUnits` (this
   already works, but lock it in with a test).

#### Sample test JSON (full AMS update)

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

Note: This JSON matches the user's report — PLA in slots 1, 3, 4 (0-indexed:
0, 2, 3) with slot 1 (index 1) empty (state 0). `tray_exist_bits` = "e"
(0x0E = bits 1,2,3 = slots 1,2,3 = 1-indexed slots 2,3,4, which is close to
the user's "slots 1, 3, 4" — the exact bits may vary by firmware but the
parsing logic is the same).

## Validation instructions

### 1. Unit tests

```bash
cd /work
go test ./internal/printers/bambu/ -run "TestHandleReport_AMS|TestMergeAMS|TestMergeTrays" -v -count=1
```

All new tests should pass.

### 2. Full test suite (no regressions)

```bash
cd /work
go test ./... -race -count=1
```

All existing tests must still pass.

### 3. Build check

```bash
cd /work
go build ./...
```

### 4. Docker container validation

After building the Docker image (see step 5 below), run:

```bash
docker rm -f printer-dashboard 2>/dev/null
docker run --rm -d --name printer-dashboard -p 8081:8081 \
  -v "/home/chris/.printer-dashboard:/home/app/.printer-dashboard:rw" \
  -v "/home/chris/.printer-dashboard/config.yaml:/app/config.yaml:rw" \
  printer-dashboard-k033:test
```

Then verify the dashboard at `http://localhost:8081`:
- The P1S printer card should show an **AMS** section with filament slot
  tiles (color swatches + type labels) after the printer connects and
  receives its first MQTT report.
- Slots with filament should show the color swatch and type (e.g. "PLA").
- Empty slots should show a dashed "Empty" placeholder.
- The active tray should have an accent border highlight.

Check the container logs for the P1S printer to confirm AMS data is being
received and parsed without errors:

```bash
docker logs printer-dashboard 2>&1 | grep -i "ams\|tray\|filament" | tail -20
```

Look for lines like:
```
bambu <id>: MQTT camera report: ipcam_url=...
```
(The AMS parse is silent on success — it only logs if there's a parse error.)

### 5. Docker build

```bash
cd /work
docker build -t printer-dashboard-k033:test .
```

## Git branch

Create a branch, commit, and push:

```bash
cd /work
git checkout -b fix/p1s-ams-delta-merge
git add internal/printers/bambu/client.go internal/printers/bambu/client_test.go docs/fix-p1s-ams-loading-spec.md
git commit -m "fix: merge P1S AMS delta updates instead of wiping cache

P1S sends delta MQTT updates where only changed fields are included.
The ams key may be present with an empty ams array (only tray_now
changed) or with unit objects lacking the tray array. The previous
code replaced s.AMSUnits wholesale, wiping cached filament slot data
on every such delta. This added mergeAMSData/mergeTrays helpers
that retain cached data for absent fields, and stops resetting
ActiveTrayID to -1 when tray_now is omitted from a delta."
git push -u origin fix/p1s-ams-delta-merge
```

## Risk assessment

- **Low risk**: The merge logic is a superset of the old behavior. For H2S
  (full updates), every field is present in every report, so the merge
  produces identical results to the old wholesale replacement.
- **Non-breaking**: The `parseAMSData` function signature changes from
  `func parseAMSData(ams *amsData)` to
  `func parseAMSData(ams *amsData) []printers.AMSUnit` — it stays the same
  (takes only `*amsData`, returns `[]printers.AMSUnit`). The merge is done
  in `handleReport` via `mergeAMSData(parseAMSData(p.Ams), s.AMSUnits)`.
  `parseAMSData` itself is unchanged, preserving its existing behavior.
- **Backward compatible**: The `PrinterStatus` JSON shape is unchanged
  (`ams_units`, `active_tray_id`). Only the values are more correct.

## Additional fix: P1S state=0 and Loaded flag

### Problem
Even after the delta-merge fix, P1S AMS trays appeared empty in the
frontend. Investigation revealed two issues:

1. **Backend — `Loaded` flag**: `parseAMSData` sets `Loaded` based solely on
tray `state` (2=loaded, 3=ready, 10=reading, 11=loaded+data). All P1S
trays report `state: 0`, so `Loaded` was `false` for every tray.

2. **Frontend — display condition**: The frontend's `amsHtml()` function
only renders filament type/color/remaining when `tray.loaded` is `true`.
With `Loaded: false` on all P1S trays, every slot showed "Empty".

### Fix

1. **Backend**: Updated the `loaded` expression in `parseAMSData` to also
treat trays with a non-empty `tray_type` as loaded:
   ```go
   loaded := t.State == 2 || t.State == 3 || t.State == 10 || t.State == 11 || t.TrayType != ""
   ```
   This is safe because H2S/X1 *empty* trays always have `tray_type: ""` —
   so their behavior is unchanged. P1S trays with filament have
tray_type like "PLA" even at state 0.

2. **Frontend**: Updated the display condition in `internal/server/onboarding.go`
   `amsHtml()` from `!tray.loaded` to `(!tray.loaded && !tray.type)` so that
trays with non-empty type are rendered with their filament info regardless
   of the `loaded` flag. Also added a guard for empty `tray.color` to avoid
   a broken color swatch.

### Validation
Confirmed with a live Docker run: P1S AMS data now shows correct
filament types (PLA), colors, and remaining percentages. H2S AMS
behavior is unchanged.
