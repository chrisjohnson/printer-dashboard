# AMS Code Review: Additional Fixes Spec

## Context

The K-033 delta-merge fix (commit `9d9a97d`) is already merged. This spec
covers **remaining** bugs and improvements found during code review of the
AMS subsystem, spanning the Go backend (`internal/printers/bambu/`), the
embedded frontend (`internal/server/onboarding.go`), and the test suite.

---

## Bug 1 — Frontend: `remain` percentage is 100× too large (HIGH, user-visible)

**Location:** `internal/server/onboarding.go`, `amsHtml()` (~line 1648)

**Problem:** The remaining-filament display uses
`Math.round((tray.remain / 1000) * 100)`. Since `remain` is in **millimeters**
(see `FilamentSlot.RemainingMM` comment: "Remaining filament length mm"),
this formula yields values 100× too large:

| `remain` (mm) | Current display | Expected |
|---------------|-----------------|----------|
| 50 000        | `5000%`         | `50.0m`  |
| 100 000       | `10000%`        | `100.0m` |

**Fix:** Replace the percentage calculation with remaining-meters display:
`(tray.remain / 1000).toFixed(1) + 'm'`. The `>= 0` guard is kept so
`remain = -1` (unknown) still hides the text.

---

## Bug 2 — Frontend: Stray `>` in color swatch (LOW, visual artifact)

**Location:** `internal/server/onboarding.go`, `amsHtml()` (~line 1645)

**Problem:** When `tray.color` is empty but `tray.type` is non-empty (a
loaded tray with no RFID color), the ternary produces a stray `>`:

```js
(colorHex ? ' style="background-color:' + colorHex + ';">' : '>')
```

Resulting in `<div class="ams-color empty">></div>` — a literal `>` character
appears as visible text inside the swatch div.

**Fix:** Replace `'>>'` with `''` (empty string) — the closing `>` is already
provided by the `</div>` tag.

---

## Bug 3 — Frontend: AMS section not created when initially absent (MEDIUM, latent)

**Location:** `internal/server/onboarding.go`, `updateCard()` (~line 1270)

**Problem:** `renderCard()` calls `amsHtml(p)`, which returns `''` when
`ams_units` is empty. This means no `<div class="ams-section">` element is
rendered into the DOM. When AMS data later arrives via WebSocket,
`updateCard()` calls `card.querySelector('.ams-section')`, gets `null`, and
**skips the entire AMS block** — the data is silently lost until the next
full `renderCard()` rebuild (triggered only by `loadPrinters()` or a card
not-found reload).

**Fix:** In `updateCard()`, when `amsHtml()` returns non-empty content but
no `.ams-section` element exists, create the element and insert it into
`.bottom-sections` before `.move-section-collapsible`.

This fix is coupled with Bug 4 (nesting), since both require changing what
`amsHtml()` returns.

---

## Bug 4 — Frontend: Double-nested `.ams-section` div on WS update (LOW, code quality)

**Location:** `internal/server/onboarding.go`, `amsHtml()` + `updateCard()`

**Problem:** `amsHtml()` returns a full `<div class="ams-section" data-ams">…</div>`.
In `updateCard()`, this string is set as `amsEl.innerHTML` on an *existing*
`.ams-section` element, creating:

```html
<div class="ams-section" data-ams>   ← original (from renderCard)
  <div class="ams-section" data-ams>  ← nested (from amsHtml)
    …
  </div>
</div>
```

While the CSS (`display: flex; flex-direction: column; gap: 6px`) on both
parent and child doesn't cause a visible break (the parent has exactly one
child, so its `gap` is a no-op), the double-nesting is incorrect and fragile
— future CSS changes could surface visual issues.

**Fix:** Refactor `amsHtml()` to return only the **inner content** (label,
units, external spool — without the wrapping `.ams-section` div). Both
`renderCard()` and `updateCard()` then wrap the result:

- `renderCard()`: `'<div class="ams-section" data-ams">' + amsInner + '</div>'`
- `updateCard()`: `amsEl.innerHTML = amsInner` (no nesting)

---

## Bug 5 — Backend: Dead code `isErrorState()` (LOW, code quality)

**Location:** `internal/printers/bambu/parser.go` (function) +
`internal/printers/bambu/parser_test.go` (`TestIsErrorState`)

**Problem:** `isErrorState()` is defined but **never called** from any
production code path. It has also been superseded by `mapState()` +
`isHealthyGcodeState()`, which are the actively-used functions in
`handleReport()`'s HMS staleness logic. Additionally, `isErrorState()`
contains a logic error: it treats `"IDLE"` as an error state, but IDLE is a
normal, healthy state (printer is powered on but not printing).

**Fix:** Remove `isErrorState()` from `parser.go` and remove its test
`TestIsErrorState` from `parser_test.go`. This eliminates both dead code
and the misleading IDLE-as-error logic.

---

## Bug 6 — Backend: `mergeAMSData`/`mergeTrays` return aliased cached slice (LOW, latent)

**Location:** `internal/printers/bambu/client.go`, `mergeAMSData()` (~line 887)
and `mergeTrays()` (~line 934)

**Problem:** When `new`/`newAMS` is empty, both functions return the `cached`
slice **directly** (same backing array). If the caller or any downstream code
later mutates the returned slice, the cached data is silently corrupted. This
is not currently exploitable (no mutation occurs after merge), but it's a
latent aliasing hazard that violates defensive-programming expectations.

**Fix:** Return a defensive copy (`append([]T(nil), cached...)`) when
returning the cached slice. Preserve `nil` semantics: `nil` input → `nil`
output (existing tests `TestMergeAMSData_BothNil_ReturnsNil` and
`TestMergeTrays_BothEmpty_ReturnsEmpty` must still pass).

---

## Test Gap 1 — No direct unit tests for `parseAMSData` (MEDIUM, coverage)

**Location:** New tests in `internal/printers/bambu/parser_test.go`

**Problem:** `parseAMSData` has **zero** direct unit tests. Coverage is
indirect through `handleReport` integration tests, meaning a regression in
the parsing logic (non-numeric IDs, empty tray arrays, `remain = -1`)
might not be caught by a focused test.

**Fix:** Add `TestParseAMSData` with table-driven test cases:

| Case | Input | Expected behavior |
|------|-------|-------------------|
| nil `*amsData` | `nil` | returns `nil` |
| Empty `ams` array | `&amsData{}` | returns `nil` |
| Single unit, full trays | 4 trays, state 3, `remain=50000` | 4 `FilamentSlot`s, `Loaded=true`, `RemainingMM=50000` |
| Non-numeric unit/tray IDs | `"abc"` | falls back to `0` |
| Missing tray array | unit with `Tray: nil` | `Trays` empty, unit still returned |
| `remain = -1` | one tray with `remain: -1` | `RemainingMM = -1` (preserved) |
| Empty `tray_type` with `state=0` | `"tray_type":""`, `state:0` | `Loaded = false` |
| Empty `tray_type` with `state=3` | `"tray_type":""`, `state:3` | `Loaded = true` (P1S-style) |

---

## Test Gap 2 — `mergeTrays` ordering not asserted (LOW, coverage)

**Location:** `internal/printers/bambu/merge_test.go`

**Problem:** Existing `TestMergeTrays_*` tests verify element values but not
the **ordering** of the result. `mergeTrays` preserves cached order first,
then appends new-only trays. A regression that shuffles order wouldn't be
caught.

**Fix:** Add ordering assertions (e.g., `got[0].Index == 0, got[1].Index == 1`)
to `TestMergeTrays_MatchingUpdated_NonMatchingRetained` and
`TestMergeTrays_NewNotInCached_Added`.

---

## Comment Cleanup — Garbled comment in `ams_delta_test.go` (TRIVIAL)

**Location:** `internal/printers/bambu/ams_delta_test.go` (~line 454)

**Problem:** Garbled comment:
```
// Humidity: "3" -> (retained) -> (retained) -> "4" (no-tray-array delta)."3" -> (retained) -> (retained) -> "2" (no-tray-array delta).
```
This appears to be a concatenation of two separate comment lines (one for
unit 0 humidity going to `"4"`, one for unit 1 humidity going to `"2"`)
that lost its newline.

**Fix:** Split into two clean comment lines describing the humidity
retention path for each unit.

---

## Summary of Files Changed

| File | Changes |
|------|---------|
| `internal/server/onboarding.go` | Bugs 1, 2, 3, 4 (frontend JS) |
| `internal/printers/bambu/parser.go` | Bug 5 (remove `isErrorState`) |
| `internal/printers/bambu/parser_test.go` | Bug 5 (remove `TestIsErrorState`), Test Gap 1 (add `TestParseAMSData`) |
| `internal/printers/bambu/client.go` | Bug 6 (defensive copies) |
| `internal/printers/bambu/ams_delta_test.go` | Comment cleanup |
| `internal/printers/bambu/merge_test.go` | Test Gap 2 (ordering assertions) |
