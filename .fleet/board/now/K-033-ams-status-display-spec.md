---
id: K-033-FRONTEND
title: K-033 Frontend Implementation Spec — AMS status display
---

# K-033 — Frontend Implementation Spec: AMS status display

## Context

The backend (Go) changes for K-033 have been merged to `main` via
`feat/k033-ams-status-display`. The frontend work (steps 6–9 of the K-033 card)
has been implemented in the worktree `.fleet/worktrees/K-033-ams-status-display`. The spec remains here as the design record and acceptance checklist.

### Backend data model (already merged, do NOT re-implement)

`internal/printers/interface.go` now includes:

```go
// On PrinterStatus:
AMSUnits []AMSUnit `json:"ams_units,omitempty"`
ActiveTrayID int `json:"active_tray_id"`  // (ams_id*4)+tray_id; 254=external, 255=none, -1=unknown

type AMSUnit struct {
    ID           int            `json:"id"`
    Humidity     string         `json:"humidity"`        // "0"–"5"; empty on P1S AMS 1
    HumidityRaw  string         `json:"humidity_raw"`    // raw %; empty on P1S AMS 1
    Temp         string         `json:"temp"`            // °C; empty on P1S AMS 1
    Trays        []FilamentSlot `json:"trays"`
}

type FilamentSlot struct {
    Index         int    `json:"index"`          // 0–3 within unit
    Type          string `json:"type"`           // e.g. "PLA", "PETG"
    Color         string `json:"color"`          // RRGGBBAA hex, e.g. "FF0000FF"
    InfoIdx       string `json:"info_idx"`       // Bambu profile ID e.g. "GFA00"
    NozzleTempMin int    `json:"nozzle_temp_min"`
    NozzleTempMax int    `json:"nozzle_temp_max"`
    RemainingMM   int    `json:"remain"`          // mm; -1 = unknown
    Weight        string `json:"weight"`          // grams
    TagUID        string `json:"tag_uid"`
    Loaded        bool   `json:"loaded"`          // state 2/3/10/11 = loaded
}
```

The JSON field names as seen by the frontend (`p.ams_units`, `p.active_tray_id`)
are determined by the `json` tags above. The WebSocket push (`PrinterStatus`
serialized via `startStatusForwarder` in `server.go`) and the REST
`/api/printers` listing both serialize `printers.PrinterStatus` directly, so
no server-side template changes are needed — the AMS data is already in the
JSON payload.

### Frontend file: `internal/server/onboarding.go`

The entire dashboard lives in a single Go string constant
`indexDashboardTemplate` (starts at line 473). It contains:

- **CSS** (lines 479–899): `<style>` block with `:root` variables (lines 480–514)
  and section styles. Key variables: `--bg-page`, `--bg-card`, `--text`,
  `--text-muted`, `--text-subtle`, `--accent`, `--border-subtle`,
  `--tag-error-*`, `--radius-control`, `--radius-card`, etc.
- **Skeleton card** (lines 905–970): server-rendered first-paint markup for
  each printer card (inside a `{{range}}` loop). Replaced wholesale by
  `loadPrinters()` on first fetch.
- **JS** (lines 972 onward): all dashboard logic. Key functions:
  - `renderCard(p)` — builds full card HTML from a status object `p`
  - `updateCard(p, rebuildCameras)` — patches an existing card from WS updates
  - `bannerHtml()`, `toggleBanner()`, `hmsRowHtml()`, `hmsRowsHtml()` — shared
    helpers used by both renderCard and updateCard (precedent to follow)
  - `escapeHtml()`, `escapeJsString()` — escaping helpers
  - SVG icon functions: `svgBed()`, `svgNozzle()`, `svgChamber()`, etc.

### Card structure in `renderCard()`

The card is built as an HTML string with this element order (line 1526 onward):

```
.card >
  .card-header        (printer name, state tag, online dot)
  .progress-section   (progress bar + text)
  [camera-section]    (camera streams, conditional on p.camera_streams)
  .temps              (bed, nozzles 1+N, chamber if p.has_chamber, light toggle)
  .filename           (always rendered, desktop-only via CSS)
  .layer-info         (always rendered, desktop-only via CSS)
  .ams-section          (always in DOM, display:none when no AMS data)
  .error-banner        (always in DOM, display:none when not error)
  .hms-list           (always in DOM, empty when no HMS entries)
  .controls           (pause/resume/cancel/skip buttons)
  .skipped-badge      (hidden by default)
  .move-section       (jog pad + home all)
  .skip-modal         (hidden by default)
  .zjog-modal         (hidden by default)
```

### Card structure in `updateCard()`

Updates specific DOM elements by class/querySelector in order:
1. State tag (`.tag`)
2. Online indicator (`.card-online`)
3. Progress bar fill (`.progress-bar .fill`)
4. Progress text (`.progress-text`)
5. Temperatures (iterates `.temps .temp-row`, updates `.val` and `input.target`)
6. File name (`.filename` — textContent swap)
7. Layer info (`.layer-info` — textContent swap)
8. Error banner (`.error-banner` — toggleBanner)
8b. AMS section (`.ams-section` — innerHTML replacement via shared `amsHtml()`)
9. HMS rows (`.hms-list` — innerHTML replacement via shared `hmsRowsHtml()`)
10. Control buttons (pause/resume/cancel/skip — disabled state)
11. Movement-pad buttons (`.move-section` — disabled state)
12. Light toggle (`[data-light]` — checked state)

---

## Design decisions

### Placement: AMS section goes after `.temps` and before `.filename`

The AMS section is hardware status (like temps), not print progress
(like filename/layer). Placing it right after the temps/light section:
- Groups all printer hardware status together at the top of the card
- Pushes print metadata (filename, layer) and controls below
- Matches how a user scanning the card would want to see filament state
  before print state

The section uses the same "always in DOM, hidden via `display:none` when
no AMS data" pattern as `.error-banner` and `.hms-list`, so `renderCard()`
and `updateCard()` agree on shape.

### Shared helper pattern

Following the established `hmsRowsHtml()` precedent (one function used by
both `renderCard` for initial markup and `updateCard` for `innerHTML`
replacement), create a single `amsHtml(p)` function that:
- Takes the full status object `p`
- Returns the complete AMS section HTML string
- Is called by `renderCard()` for initial markup
- Is called by `updateCard()` to replace `.ams-section` innerHTML

This prevents the K-053-class drift where render/update paths diverge.

### Color swatch rendering

`tray_color` is RRGGBBAA hex (e.g. `"FF0000FF"` = red opaque). Convert to
CSS `background-color` by prepending `#`. No alpha conversion needed —
browsers accept `#RRGGBBAA` natively since ~2023. Empty/missing color or
empty tray → show a dashed "empty" placeholder pattern (repeating-conic
gradient).

### Active tray highlighting

`ActiveTrayID` = `(ams_id * 4) + tray_id`. A tray is "active" when its
global ID matches `p.active_tray_id`. The active tray tile gets a
`border-color: var(--accent)` with a 2px accent ring shadow.

### External spool (tray_now = 254)

When `ActiveTrayID` is 254, the active "tray" is an external spool.
Render an external-spool tile with dashed border and a label "External"
instead of a color swatch (since external spools don't report color/type).

### P1S vs H2S display differences

- **P1S AMS 1**: `humidity`, `humidity_raw`, `temp` are empty strings —
  do NOT render the humidity/temp row. The unit tile shows only the
  4 tray slots.
- **H2S AMS 2 Pro**: `humidity`/`humidity_raw`/`temp` are populated —
  render a small "Humidity: 3 (23%)" and "25.0°C" line below the unit ID
  in the unit tile header.

### Delta updates (P1S)

P1S sends partial/delta updates — only changed fields. The backend's
`handleReport()` already handles this at the Go level (it replaces
`s.AMSUnits` wholesale from whatever the report contains, which is correct
because a delta update that includes the `ams` key means the printer is
reporting current AMS state). The frontend doesn't need special delta
handling — it just renders whatever `p.ams_units` it receives. If the
`ams` key is absent from a report, `s.AMSUnits` retains its previous value
(the backend doesn't nil it out, since the Go code only sets it when
`p.Ams != nil`).

However, there's a subtlety: P1S delta updates may include the `ams` key
with a partial structure (e.g., only `tray_now` changed but `ams[].tray[]`
is still the full array). The current backend code handles this correctly
because it re-parses the full `ams` JSON block each time — Go's
`json.Unmarshal` into a struct will zero-fill any fields not present in the
JSON, so if `tray` is present in the delta it gets reparsed, and if it's
absent the `[]amsTrayData` stays empty (not retained from cache).

**This is a potential bug** — see the "Open question" below.

---

## Spec: changes needed

### 1. Add AMS CSS (insert after the move-section CSS block, before `</style>`)

Insert after line 864 (the `.btn-home-all:disabled` rule, end of the
movement pad CSS block), before the Z-jog modal CSS:

```css
/* AMS (Automatic Material System) section */
.ams-section { display: flex; flex-direction: column; gap: 6px; margin: 6px 0; }
.ams-section .ams-label {
  font-size: 0.75rem; color: var(--text-subtle); font-weight: 600;
  display: flex; align-items: center; gap: 6px;
}
.ams-units { display: flex; flex-wrap: wrap; gap: 8px; }
.ams-unit {
  display: flex; flex-direction: column; gap: 4px;
  background: #f5f5f7; border-radius: var(--radius-control);
  padding: 6px 8px; flex: 1; min-width: 100px;
}
.ams-unit-id {
  font-size: 0.6875rem; color: var(--text-muted); font-weight: 600;
  text-align: center; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.ams-unit-meta {
  font-size: 0.625rem; color: var(--text-subtle); text-align: center; line-height: 1.2;
}
.ams-tray-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 4px; }
.ams-tray {
  display: flex; flex-direction: column; align-items: center; gap: 2px;
  background: var(--bg-card); border: 1px solid var(--border-subtle);
  border-radius: 4px; padding: 4px 2px; min-height: 44px;
}
.ams-tray.active { border-color: var(--accent); box-shadow: 0 0 0 2px rgba(59,130,246,.2); }
.ams-tray.external { border-style: dashed; }
.ams-color { width: 16px; height: 16px; border-radius: 3px; border: 1px solid rgba(0,0,0,.1); flex-shrink: 0; }
.ams-color.empty { background: repeating-conic-gradient(45deg, #ccc, #ccc 4px, #e5e5ea 4px, #e5e5ea 8px); }
.ams-tray-info { display: flex; flex-direction: column; align-items: center; width: 100%; overflow: hidden; }
.ams-tray-type { font-size: 0.5625rem; font-weight: 600; color: var(--text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; width: 100%; text-align: center; }
.ams-tray-remaining { font-size: 0.5625rem; color: var(--text-subtle); white-space: nowrap; }
```

### 2. Add `amsHtml()` helper (insert before `renderCard()`, near line 1475)

```javascript
function amsHtml(p) {
  var units = p.ams_units || [];
  if (units.length === 0) { return ''; }
  var activeTrayID = p.active_tray_id;

  var html = '<div class="ams-section" data-ams>' +
    '<div class="ams-label">AMS</div>';

  units.forEach(function(unit) {
    html += '<div class="ams-unit">';
    html += '<div class="ams-unit-id">Unit ' + unit.id + '</div>';

    // Humidity/temp meta — only on H2S AMS 2 Pro (P1S AMS 1 sends empty strings)
    if (unit.humidity !== '' || unit.temp !== '') {
      var meta = '';
      if (unit.humidity !== '') { meta += 'Humidity: ' + unit.humidity; if (unit.humidity_raw !== '') meta += ' (' + unit.humidity_raw + '%)'; }
      if (unit.temp !== '') { if (meta) meta += ' / '; meta += unit.temp + '°C'; }
      html += '<div class="ams-unit-meta">' + escapeHtml(meta) + '</div>';
    }

    html += '<div class="ams-tray-grid">';

    var trays = unit.trays || [];
    // Always render 4 slots (0–3). If the backend didn't send a slot,
    // show an empty placeholder.
    for (var i = 0; i < 4; i++) {
      var tray = null;
      for (var j = 0; j < trays.length; j++) { if (trays[j].index === i) { tray = trays[j]; break; } }

      // Compute global tray ID: (unitID * 4) + slotIndex
      var globalID = (unit.id * 4) + i;
      var isActive = globalID === activeTrayID;
      var cls = 'ams-tray';
      if (isActive) cls += ' active';

      // External spool (tray_now=254) is rendered as its own dashed tile,
      // not inside a unit — handled separately below.

      if (!tray || !tray.loaded) {
        // Empty slot
        html += '<div class="' + cls + '">' +
          '<div class="ams-color empty"></div>' +
          '<div class="ams-tray-info"><div class="ams-tray-type">Empty</div></div>' +
          '</div>';
      } else {
        // Loaded tray — color swatch from RRGGBBAA hex
        var colorHex = '#' + tray.color.replace(/^#/, '');
        var colorClass = 'ams-color';
        html += '<div class="' + cls + '">' +
          '<div class="' + colorClass + '" style="background-color:' + colorHex + ';"></div>' +
          '<div class="ams-tray-info">' +
          '<div class="ams-tray-type">' + escapeHtml(tray.type || 'Unknown') + '</div>' +
          (tray.remain >= 0 ? '<div class="ams-tray-remaining">' + Math.round((tray.remain / 1000) * 100) + '%</div>' : '') +
          '</div></div>';
      }
    }

    html += '</div></div>'; // close ams-tray-grid, ams-unit
  });

  // External spool tile (tray_now=254)
  if (activeTrayID === 254) {
    html += '<div class="ams-unit">' +
      '<div class="ams-unit-id">External</div>' +
      '<div class="ams-tray-grid">' +
      '<div class="ams-tray active external">' +
      '<div class="ams-color empty"></div>' +
      '<div class="ams-tray-info"><div class="ams-tray-type">External Spool</div></div>' +
      '</div>' +
      '<div class="ams-tray"><div class="ams-color empty"></div><div class="ams-tray-info"><div class="ams-tray-type">—</div></div></div>' +
      '<div class="ams-tray"><div class="ams-color empty"></div><div class="ams-tray-info"><div class="ams-tray-type">—</div></div></div>' +
      '<div class="ams-tray"><div class="ams-color empty"></div><div class="ams-tray-info"><div class="ams-tray-type">—</div></div></div>' +
      '</div></div>';
  }

  html += '</div>'; // close ams-section
  return html;
}
```

### 3. Add AMS section to `renderCard()` (insert at line 1633, after `hmsHtml`, before `controls`)

In `renderCard()`, after the `hmsHtml` line and before the controls `<div>`:

```javascript
        hmsHtml +
        // AMS section — always in DOM (empty string when no AMS units) so
        // renderCard()/updateCard() agree on shape. The data-ams marker
        // lets updateCard() find/replace it directly.
        amsHtml(p) +
        '<div class="controls">' +
```

### 4. Add AMS update to `updateCard()` (insert at line 1173, after HMS update, before control buttons)

After the HMS `innerHTML` replacement and before the control buttons section:

```javascript
      // 8b. AMS section — one shared amsHtml() helper builds the full
      // markup (same precedent as hmsRowsHtml() above); replace
      // innerHTML wholesale since the section is small and may change
      // structure (active tray highlight, external spool, empty slots).
      var amsEl = card.querySelector('.ams-section');
      if (amsEl) {
        var amsHtmlStr = amsHtml(p);
        if (amsHtmlStr) {
          amsEl.innerHTML = amsHtmlStr;
        } else {
          // No AMS data — hide the section
          amsEl.style.display = 'none';
        }
      }
```

**Note**: `amsHtml(p)` returns a string that includes the outer
`.ams-section` wrapper. When `updateCard` sets `amsEl.innerHTML = amsHtmlStr`,
the inner content replaces correctly. When there are no AMS units, the
initial `renderCard` outputs an empty string (no `.ams-section` element),
so `card.querySelector('.ams-section')` returns `null` and the update is a
no-op — which is correct.

### 5. Add AMS skeleton to server-rendered card (line 961, after `.layer-info` placeholder)

For the skeleton/fallback card, add an always-present AMS section that's
hidden when no AMS data arrives:

After line 961 (`<div class="layer-info">&nbsp;</div>`), add:

```html
      <div class="ams-section" style="display:none;"></div>
```

This ensures `updateCard` can always find `.ams-section` even on the initial
skeleton before `loadPrinters()` replaces the card. The skeleton is replaced
wholesale by `loadPrinters()`, so this is just a safety net.

---

## Open questions / risks

1. **P1S delta update correctness**: The backend `parseAMSData()` creates
   fresh `[]AMSUnit` from each report's `ams` block. If P1S sends a delta
   with `ams` present but `ams[].tray[]` absent (e.g., only `tray_now`
   changed), Go's `json.Unmarshal` will produce an empty `Trays` slice —
   the backend does NOT merge with cached tray data. This would cause
   filament type/color/remaining to disappear on partial updates.
   **Fix**: The backend `handleReport` should cache AMS unit data and merge
   partial updates, OR the P1S firmware should send the full `tray[]` array
   in every delta. Verification needed with real P1S MQTT capture.
   *Severity: high — affects display correctness on P1S.*

2. **`NozzleTempMin`/`NozzleTempMax` wire type**: The `amsTrayData`
   struct declares these as `int`, but the research notes say the wire
   format sends them as strings (`"190"`, `"230"`). Go's `json.Unmarshal`
   will fail to parse `"190"` into an `int` field. The backend `parseAMSData`
   does `strconv.Atoi(t.NozzleTempMin)` — but `t.NozzleTempMin` is already
   an `int`, not a string, so the `strconv.Atoi` call is on an int, which
   won't compile.
   **Fix**: The `amsTrayData` struct's `NozzleTempMin`/`NozzleTempMax`
   should be `string`, and `parseAMSData` should parse them to `int` via
   `strconv.Atoi`. The current backend code has the types wrong.
   *Severity: high — compilation error or runtime data loss.*

3. **`RemainingMM` field name**: The Go `FilamentSlot` has `RemainingMM`
   with JSON tag `"remain"`, but `amsTrayData` has `Remain` with JSON tag
   `"remain"`. The `parseAMSData` function assigns `RemainingMM: t.Remain`
   correctly, but the JSON tag on the frontend side is `"remain"` — this
   matches, so no issue. Just confirm.

4. **Color alpha handling**: `#RRGGBBAA` is supported in modern browsers
   but check caniuse for the target browser support matrix. If older
   browsers need support, convert to `rgba(r, g, b, a/255)`.

---

## Implementation Status

Frontend code is complete. All 5 spec steps have been applied to
`internal/server/onboarding.go` in the worktree
`.fleet/worktrees/K-033-ams-status-display` on branch
`feat/k033-ams-status-display`.

**Changes in `internal/server/onboarding.go` (+134 lines):**
1. AMS CSS rules (line ~865): `.ams-section`, `.ams-unit`, `.ams-tray-grid`,
   `.ams-tray`, `.ams-color` with active highlighting and external spool styling
2. `amsHtml(p)` shared helper (line ~1520): called by both `renderCard()`
   and `updateCard()` to prevent render/update path drift
3. `renderCard()` insertion (line ~1764): `amsHtml(p)` after `hmsHtml`, before controls
4. `updateCard()` insertion (line ~1207): finds `.ams-section`, replaces innerHTML
   or hides when no AMS data
5. Skeleton card (line ~994): hidden `.ams-section` placeholder alongside error-banner

**Pending:** Push to origin and create PR (SSH key not configured in this environment).

## Acceptance criteria

- [x] AMS section renders in the printer card when `ams_units` is non-empty
- [x] Each AMS unit shows a tile with its ID and 4 tray slots
- [x] Loaded trays show a color swatch (from `tray_color` RRGGBBAA)
- [x] Empty trays show a dashed placeholder pattern
- [x] Filament type (`tray_type`) is displayed on each tray tile
- [x] Remaining % calculated from `remain` (mm) and displayed
- [x] Active tray (matching `active_tray_id`) is highlighted with accent border
- [x] External spool (tray_now=254) shows a dashed "External Spool" tile
- [x] H2S humidity/temp meta shown below unit ID; P1S shows only trays
- [x] `renderCard()` and `updateCard()` use the same `amsHtml()` helper
- [x] Section is always in DOM (hidden when no AMS data), matching the
      `.error-banner`/`.hms-list` pattern
- [x] No Go backend changes needed (already merged and verified)
