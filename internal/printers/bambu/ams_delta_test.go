package bambu

import (
	"testing"

	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// ---------------------------------------------------------------------------
// AMS delta update merge tests (constants + fixtures)
// ---------------------------------------------------------------------------

// fullAMSReport is a complete H2S-style AMS report JSON (all units, all 4
// trays per unit populated). Used as the baseline "full update" that seeds
// the cache before sending P1S-style delta reports.
const fullAMSReport = `
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
}`

// amsEmptyAmsArrayReport simulates a P1S delta where the "ams" key is present
// but the inner "ams" array is absent — only tray_now changed.
const amsEmptyAmsArrayReport = `
{
  "print": {
    "gcode_state": "RUNNING",
    "ams": {
      "tray_now": "2",
      "ams_exist_bits": "1",
      "tray_exist_bits": "f"
    }
  }
}`

// amsTrayNowOnlyReport simulates a P1S delta where the ams array is explicitly
// empty ([]) and only tray_now metadata is present.
const amsTrayNowOnlyReport = `
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
}`

// amsPartialTrayReport simulates a P1S delta where the ams array has one unit
// but only tray index 0 is present (the other 3 trays changed nothing).
const amsPartialTrayReport = `
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
}`

// amsNoTrayArrayReport simulates a P1S delta where the ams array has a unit
// but the "tray" field is entirely absent (only unit-level metadata like
// humidity changed — common for H2S AMS 2 Pro with humidity sensors).
const amsNoTrayArrayReport = `
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
}`

// amsNoTrayNowReport simulates a P1S delta where the ams key is present with
// unit data but tray_now is absent — the active tray must be retained.
const amsNoTrayNowReport = `
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
}`

// noAmsKeyReport simulates a delta where the "ams" key is entirely absent
// from the JSON (not even the shell key) — AMS data should be untouched.
const noAmsKeyReport = `
{
  "print": {
    "gcode_state": "RUNNING",
    "mc_percent": 50
  }
}`

// loadFullAMSReport seeds the test client's cache with a complete AMS
// update, then returns the client so subsequent delta reports can be
// applied on top.
func loadFullAMSReport(t *testing.T, c *Client) {
	t.Helper()
	c.handleReport(nil, newMockMessage([]byte(fullAMSReport)))
	s := c.Status()
	if len(s.AMSUnits) != 1 {
		t.Fatalf("after full AMS report: AMSUnits len = %d; want 1", len(s.AMSUnits))
	}
	if len(s.AMSUnits[0].Trays) != 4 {
		t.Fatalf("after full AMS report: Trays len = %d; want 4", len(s.AMSUnits[0].Trays))
	}
}

// TestHandleReport_AMS_FullUpdate_PopulatesData verifies a complete H2S-style
// AMS report populates all units, trays, type/color/remaining, and ActiveTrayID.
func TestHandleReport_AMS_FullUpdate_PopulatesData(t *testing.T) {
	c := newTestPrinterClient(nil)

	c.handleReport(nil, newMockMessage([]byte(fullAMSReport)))
	s := c.Status()

	if len(s.AMSUnits) != 1 {
		t.Fatalf("AMSUnits len = %d; want 1", len(s.AMSUnits))
	}
	unit := s.AMSUnits[0]
	if unit.ID != 0 {
		t.Errorf("unit[0].ID = %d; want 0", unit.ID)
	}
	if unit.Humidity != "3" {
		t.Errorf("unit[0].Humidity = %q; want %q", unit.Humidity, "3")
	}
	if unit.HumidityRaw != "23" {
		t.Errorf("unit[0].HumidityRaw = %q; want %q", unit.HumidityRaw, "23")
	}
	if unit.Temp != "25.0" {
		t.Errorf("unit[0].Temp = %q; want %q", unit.Temp, "25.0")
	}
	if len(unit.Trays) != 4 {
		t.Fatalf("Trays len = %d; want 4", len(unit.Trays))
	}

	// Slot 0 (ready PLA, red)
	if unit.Trays[0].Type != "PLA" {
		t.Errorf("tray[0].Type = %q; want %q", unit.Trays[0].Type, "PLA")
	}
	if unit.Trays[0].Color != "FF0000FF" {
		t.Errorf("tray[0].Color = %q; want %q", unit.Trays[0].Color, "FF0000FF")
	}
	if !unit.Trays[0].Loaded {
		t.Errorf("tray[0].Loaded = false; want true (state 3 = ready, filament present)")
	}
	if unit.Trays[0].RemainingMM != 50000 {
		t.Errorf("tray[0].RemainingMM = %d; want 50000", unit.Trays[0].RemainingMM)
	}
	if unit.Trays[0].NozzleTempMin != 190 {
		t.Errorf("tray[0].NozzleTempMin = %d; want 190", unit.Trays[0].NozzleTempMin)
	}
	if unit.Trays[0].NozzleTempMax != 230 {
		t.Errorf("tray[0].NozzleTempMax = %d; want 230", unit.Trays[0].NozzleTempMax)
	}

	// Slot 1 (empty)
	if unit.Trays[1].Type != "" {
		t.Errorf("tray[1].Type = %q; want %q", unit.Trays[1].Type, "")
	}
	if unit.Trays[1].Loaded {
		t.Errorf("tray[1].Loaded = true; want false (state 0 = empty)")
	}
	if unit.Trays[1].RemainingMM != -1 {
		t.Errorf("tray[1].RemainingMM = %d; want -1", unit.Trays[1].RemainingMM)
	}

	if s.ActiveTrayID != 0 {
		t.Errorf("ActiveTrayID = %d; want 0 (tray_now=0)", s.ActiveTrayID)
	}
}

// TestHandleReport_AMS_P1SDeltaEmptyAmsArray_RetainsCache verifies that a P1S
// delta with the ams key present but the inner ams array absent retains
// cached AMS data instead of wiping it.
func TestHandleReport_AMS_P1SDeltaEmptyAmsArray_RetainsCache(t *testing.T) {
	c := newTestPrinterClient(nil)
	loadFullAMSReport(t, c)

	c.handleReport(nil, newMockMessage([]byte(amsEmptyAmsArrayReport)))
	s := c.Status()

	if len(s.AMSUnits) != 1 {
		t.Fatalf("AMSUnits len = %d; want 1 (should be retained, not wiped to nil)", len(s.AMSUnits))
	}
	if len(s.AMSUnits[0].Trays) != 4 {
		t.Errorf("Trays len = %d; want 4 (should be retained)", len(s.AMSUnits[0].Trays))
	}
	if got := s.AMSUnits[0].Trays[0].Type; got != "PLA" {
		t.Errorf("tray[0].Type = %q; want %q (retained from cache)", got, "PLA")
	}
	if got := s.AMSUnits[0].Trays[0].Color; got != "FF0000FF" {
		t.Errorf("tray[0].Color = %q; want %q (retained from cache)", got, "FF0000FF")
	}
	if s.ActiveTrayID != 2 {
		t.Errorf("ActiveTrayID = %d; want 2 (updated from tray_now=2)", s.ActiveTrayID)
	}
}

// TestHandleReport_AMS_P1SDeltaTrayNowOnly_RetainsCache verifies that a P1S
// delta with an explicitly empty ams array ([]) retains cached AMS data.
func TestHandleReport_AMS_P1SDeltaTrayNowOnly_RetainsCache(t *testing.T) {
	c := newTestPrinterClient(nil)
	loadFullAMSReport(t, c)

	c.handleReport(nil, newMockMessage([]byte(amsTrayNowOnlyReport)))
	s := c.Status()

	if len(s.AMSUnits) != 1 {
		t.Fatalf("AMSUnits len = %d; want 1 (should be retained from cache)", len(s.AMSUnits))
	}
	if len(s.AMSUnits[0].Trays) != 4 {
		t.Errorf("Trays len = %d; want 4 (should be retained from cache)", len(s.AMSUnits[0].Trays))
	}
	if got := s.AMSUnits[0].Trays[2].Color; got != "00FF00FF" {
		t.Errorf("tray[2].Color = %q; want %q (retained from cache)", got, "00FF00FF")
	}
	if s.ActiveTrayID != 3 {
		t.Errorf("ActiveTrayID = %d; want 3 (updated from tray_now=3)", s.ActiveTrayID)
	}
}

// TestHandleReport_AMS_P1SDeltaPartialTray_RetainsOtherTrays verifies that a
// P1S delta with only one tray present updates just that tray while retaining
// the others from cache.
func TestHandleReport_AMS_P1SDeltaPartialTray_RetainsOtherTrays(t *testing.T) {
	c := newTestPrinterClient(nil)
	loadFullAMSReport(t, c)

	c.handleReport(nil, newMockMessage([]byte(amsPartialTrayReport)))
	s := c.Status()

	if len(s.AMSUnits) != 1 {
		t.Fatalf("AMSUnits len = %d; want 1", len(s.AMSUnits))
	}
	if len(s.AMSUnits[0].Trays) != 4 {
		t.Fatalf("Trays len = %d; want 4 (3 retained + 1 updated)", len(s.AMSUnits[0].Trays))
	}
	unit := s.AMSUnits[0]

	// Tray 0: updated to PETG, orange.
	if unit.Trays[0].Type != "PETG" {
		t.Errorf("tray[0].Type = %q; want %q (updated from delta)", unit.Trays[0].Type, "PETG")
	}
	if unit.Trays[0].Color != "FF8800FF" {
		t.Errorf("tray[0].Color = %q; want %q (updated from delta)", unit.Trays[0].Color, "FF8800FF")
	}
	if unit.Trays[0].RemainingMM != 45000 {
		t.Errorf("tray[0].RemainingMM = %d; want 45000 (updated from delta)", unit.Trays[0].RemainingMM)
	}
	if unit.Trays[0].NozzleTempMin != 220 {
		t.Errorf("tray[0].NozzleTempMin = %d; want 220 (updated from delta)", unit.Trays[0].NozzleTempMin)
	}

	// Tray 1: empty, retained from cache.
	if unit.Trays[1].Type != "" {
		t.Errorf("tray[1].Type = %q; want %q (retained from cache)", unit.Trays[1].Type, "")
	}
	if unit.Trays[1].Loaded {
		t.Errorf("tray[1].Loaded = true; want false (retained from cache)")
	}

	// Tray 2: PLA green, retained from cache.
	if unit.Trays[2].Type != "PLA" {
		t.Errorf("tray[2].Type = %q; want %q (retained from cache)", unit.Trays[2].Type, "PLA")
	}
	if unit.Trays[2].Color != "00FF00FF" {
		t.Errorf("tray[2].Color = %q; want %q (retained from cache)", unit.Trays[2].Color, "00FF00FF")
	}
	if unit.Trays[2].RemainingMM != 75000 {
		t.Errorf("tray[2].RemainingMM = %d; want 75000 (retained from cache)", unit.Trays[2].RemainingMM)
	}

	// Tray 3: PLA blue, retained from cache.
	if unit.Trays[3].Type != "PLA" {
		t.Errorf("tray[3].Type = %q; want %q (retained from cache)", unit.Trays[3].Type, "PLA")
	}
	if unit.Trays[3].Color != "0000FFFF" {
		t.Errorf("tray[3].Color = %q; want %q (retained from cache)", unit.Trays[3].Color, "0000FFFF")
	}
}

// TestHandleReport_AMS_P1SDeltaNoTrayArray_RetainsCachedTrays verifies that a
// P1S delta with a unit but no tray field retains all cached trays.
func TestHandleReport_AMS_P1SDeltaNoTrayArray_RetainsCachedTrays(t *testing.T) {
	c := newTestPrinterClient(nil)
	loadFullAMSReport(t, c)

	c.handleReport(nil, newMockMessage([]byte(amsNoTrayArrayReport)))
	s := c.Status()

	if len(s.AMSUnits) != 1 {
		t.Fatalf("AMSUnits len = %d; want 1", len(s.AMSUnits))
	}
	unit := s.AMSUnits[0]
	if len(unit.Trays) != 4 {
		t.Errorf("Trays len = %d; want 4 (cached trays retained when tray array absent)", len(unit.Trays))
	}
	if unit.Humidity != "4" {
		t.Errorf("unit.Humidity = %q; want %q (updated from delta)", unit.Humidity, "4")
	}
	if unit.Temp != "26.5" {
		t.Errorf("unit.Temp = %q; want %q (updated from delta)", unit.Temp, "26.5")
	}
	if got := unit.Trays[0].Type; got != "PLA" {
		t.Errorf("tray[0].Type = %q; want %q (retained from cache)", got, "PLA")
	}
	if got := unit.Trays[3].Color; got != "0000FFFF" {
		t.Errorf("tray[3].Color = %q; want %q (retained from cache)", got, "0000FFFF")
	}
	if s.ActiveTrayID != 0 {
		t.Errorf("ActiveTrayID = %d; want 0 (updated from tray_now=0)", s.ActiveTrayID)
	}
}

// TestHandleReport_AMS_P1SDeltaNoTrayNow_RetainsActiveTray verifies that
// ActiveTrayID is not reset to -1 when a P1S delta omits tray_now.
func TestHandleReport_AMS_P1SDeltaNoTrayNow_RetainsActiveTray(t *testing.T) {
	c := newTestPrinterClient(nil)
	loadFullAMSReport(t, c)

	c.handleReport(nil, newMockMessage([]byte(amsNoTrayNowReport)))
	s := c.Status()

	if s.ActiveTrayID != 0 {
		t.Errorf("ActiveTrayID = %d; want 0 (should be retained when tray_now absent, not reset to -1)", s.ActiveTrayID)
	}
	if len(s.AMSUnits) != 1 {
		t.Fatalf("AMSUnits len = %d; want 1", len(s.AMSUnits))
	}
	if s.AMSUnits[0].Humidity != "2" {
		t.Errorf("Humidity = %q; want %q (updated from delta)", s.AMSUnits[0].Humidity, "2")
	}
}

// TestHandleReport_AMS_NoAmsKey_RetainsCache verifies that a delta without
// the ams key at all leaves AMSUnits and ActiveTrayID untouched.
func TestHandleReport_AMS_NoAmsKey_RetainsCache(t *testing.T) {
	c := newTestPrinterClient(nil)
	loadFullAMSReport(t, c)

	c.handleReport(nil, newMockMessage([]byte(noAmsKeyReport)))
	s := c.Status()

	if len(s.AMSUnits) != 1 {
		t.Fatalf("AMSUnits len = %d; want 1 (untouched by ams-absent delta)", len(s.AMSUnits))
	}
	if len(s.AMSUnits[0].Trays) != 4 {
		t.Errorf("Trays len = %d; want 4 (untouched)", len(s.AMSUnits[0].Trays))
	}
	if s.ActiveTrayID != 0 {
		t.Errorf("ActiveTrayID = %d; want 0 (untouched by ams-absent delta)", s.ActiveTrayID)
	}
	if s.Progress != 0.50 {
		t.Errorf("Progress = %f; want 0.50 (updated from delta)", s.Progress)
	}
}

// TestHandleReport_AMS_SuccessiveDeltaStacking verifies that multiple P1S
// deltas applied in sequence each merge on top of the previous without
// wiping the cache.
func TestHandleReport_AMS_SuccessiveDeltaStacking(t *testing.T) {
	c := newTestPrinterClient(nil)
	loadFullAMSReport(t, c)

	c.handleReport(nil, newMockMessage([]byte(amsEmptyAmsArrayReport)))
	c.handleReport(nil, newMockMessage([]byte(amsPartialTrayReport)))
	c.handleReport(nil, newMockMessage([]byte(amsNoTrayArrayReport)))
	s := c.Status()

	if len(s.AMSUnits) != 1 {
		t.Fatalf("AMSUnits len = %d; want 1", len(s.AMSUnits))
	}
	unit := s.AMSUnits[0]
	if len(unit.Trays) != 4 {
		t.Fatalf("Trays len = %d; want 4 (all retained through stacking)", len(unit.Trays))
	}

	// Tray 0 was updated to PETG in the partial-tray delta.
	if unit.Trays[0].Type != "PETG" {
		t.Errorf("tray[0].Type = %q; want %q (from partial-tray delta, persisted)", unit.Trays[0].Type, "PETG")
	}
	// Tray 2 was never touched by any delta — retained from original full report.
	if unit.Trays[2].Color != "00FF00FF" {
		t.Errorf("tray[2].Color = %q; want %q (retained from original full report)", unit.Trays[2].Color, "00FF00FF")
	}
	// Humidity: "3" -> (retained) -> (retained) -> "4" (no-tray-array delta)."3" -> (retained) -> (retained) -> "2" (no-tray-array delta).
	if unit.Humidity != "4" {
		t.Errorf("Humidity = %q; want %q (updated by no-tray-array delta)", unit.Humidity, "4")
	}
	if unit.Temp != "26.5" {
		t.Errorf("Temp = %q; want %q (updated by no-tray-array delta)", unit.Temp, "26.5")
	}
	// ActiveTrayID: 0 -> 2 -> 0 -> 0 (no-tray-array retained).
	if s.ActiveTrayID != 0 {
		t.Errorf("ActiveTrayID = %d; want 0 (last delta with tray_now=0)", s.ActiveTrayID)
	}
}

// TestHandleReport_AMS_H2SFullUpdateReplacesCache verifies that H2S full
// updates replace the cache entirely (no stale trays bleeding through).
func TestHandleReport_AMS_H2SFullUpdateReplacesCache(t *testing.T) {
	c := newTestPrinterClient(nil)
	loadFullAMSReport(t, c)

	fullUpdate := []byte(`{
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
}`)

	c.handleReport(nil, newMockMessage([]byte(fullUpdate)))
	s := c.Status()

	if len(s.AMSUnits) != 1 {
		t.Fatalf("AMSUnits len = %d; want 1", len(s.AMSUnits))
	}
	unit := s.AMSUnits[0]
	if unit.Humidity != "1" {
		t.Errorf("Humidity = %q; want %q (replaced from new full update)", unit.Humidity, "1")
	}
	if len(unit.Trays) != 4 {
		t.Fatalf("Trays len = %d; want 4", len(unit.Trays))
	}
	if unit.Trays[0].Type != "" {
		t.Errorf("tray[0].Type = %q; want %q (replaced from new full update)", unit.Trays[0].Type, "")
	}
	if unit.Trays[0].Loaded {
		t.Errorf("tray[0].Loaded = true; want false (replaced from new full update)")
	}
	if unit.Trays[1].Type != "PETG" {
		t.Errorf("tray[1].Type = %q; want %q (replaced from new full update)", unit.Trays[1].Type, "PETG")
	}
	if !unit.Trays[1].Loaded {
		t.Errorf("tray[1].Loaded = false; want true (replaced from new full update)")
	}
	if s.ActiveTrayID != 1 {
		t.Errorf("ActiveTrayID = %d; want 1 (from new full update tray_now=1)", s.ActiveTrayID)
	}
}

// TestHandleReport_AMS_MultipleUnits_Deltas verifies that a delta touching
// only one AMS unit retains other units from cache.
func TestHandleReport_AMS_MultipleUnits_Deltas(t *testing.T) {
	fullTwoUnits := []byte(`{
  "print": {
    "gcode_state": "RUNNING",
    "ams": {
      "ams": [
        {"id": "0", "humidity": "3", "tray": [{"id": "0", "state": 3, "tray_type": "PLA", "tray_color": "FF0000FF", "remain": 50000}]},
        {"id": "1", "humidity": "4", "tray": [{"id": "0", "state": 3, "tray_type": "PETG", "tray_color": "00FF00FF", "remain": 75000}]}
      ],
      "tray_now": "5",
      "ams_exist_bits": "3",
      "tray_exist_bits": "f"
    }
  }
}`)

	c := newTestPrinterClient(nil)
	c.handleReport(nil, newMockMessage([]byte(fullTwoUnits)))
	s := c.Status()
	if len(s.AMSUnits) != 2 {
		t.Fatalf("after full update: AMSUnits len = %d; want 2", len(s.AMSUnits))
	}

	deltaOneUnit := []byte(`{
  "print": {
    "gcode_state": "RUNNING",
    "ams": {
      "ams": [
        {"id": "1", "humidity": "5", "tray": [{"id": "0", "state": 3, "tray_type": "TPU", "tray_color": "0000FFFF", "remain": 90000}]}
      ],
      "tray_now": "5"
    }
  }
}`)

	c.handleReport(nil, newMockMessage([]byte(deltaOneUnit)))
	s = c.Status()

	if len(s.AMSUnits) != 2 {
		t.Fatalf("after delta: AMSUnits len = %d; want 2 (unit 0 retained)", len(s.AMSUnits))
	}

	// Find units by ID — mergeAMSData iterates new data first then cached,
	// so array order isn't guaranteed to match ID order.
	var u0, u1 printers.AMSUnit
	for _, u := range s.AMSUnits {
		if u.ID == 0 {
			u0 = u
		}
		if u.ID == 1 {
			u1 = u
		}
	}

	// Unit 0 untouched.
	if u0.ID != 0 {
		t.Errorf("unit[0].ID = %d; want 0", u0.ID)
	}
	if u0.Humidity != "3" {
		t.Errorf("unit[0].Humidity = %q; want %q (retained, delta didn't touch unit 0)", u0.Humidity, "3")
	}
	if len(u0.Trays) != 1 {
		t.Fatalf("unit[0].Trays len = %d; want 1 (retained)", len(u0.Trays))
	}
	if u0.Trays[0].Type != "PLA" {
		t.Errorf("unit[0].tray[0].Type = %q; want %q (retained)", u0.Trays[0].Type, "PLA")
	}

	// Unit 1 updated.
	if u1.ID != 1 {
		t.Errorf("unit[1].ID = %d; want 1", u1.ID)
	}
	if u1.Humidity != "5" {
		t.Errorf("unit[1].Humidity = %q; want %q (updated from delta)", u1.Humidity, "5")
	}
	if u1.Trays[0].Type != "TPU" {
		t.Errorf("unit[1].tray[0].Type = %q; want %q (updated from delta)", u1.Trays[0].Type, "TPU")
	}
	if u1.Trays[0].Color != "0000FFFF" {
		t.Errorf("unit[1].tray[0].Color = %q; want %q (updated from delta)", u1.Trays[0].Color, "0000FFFF")
	}
	if u1.Trays[0].RemainingMM != 90000 {
		t.Errorf("unit[1].tray[0].RemainingMM = %d; want 90000 (updated from delta)", u1.Trays[0].RemainingMM)
	}
}

// Ensure the printers import is used.
var _ = printers.AMSUnit{}
