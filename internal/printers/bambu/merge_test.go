package bambu

import (
	"testing"

	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// ---------------------------------------------------------------------------
// mergeAMSData / mergeTrays / firstNonEmpty unit tests
// ---------------------------------------------------------------------------

func TestMergeAMSData_NilNew_ReturnsCached(t *testing.T) {
	cached := []printers.AMSUnit{
		{ID: 0, Humidity: "3", Temp: "25.0"},
	}
	got := mergeAMSData(nil, cached)
	if len(got) != 1 {
		t.Fatalf("got len = %d; want 1", len(got))
	}
	if got[0].Humidity != "3" {
		t.Errorf("got[0].Humidity = %q; want %q", got[0].Humidity, "3")
	}
}

func TestMergeAMSData_EmptyNew_ReturnsCached(t *testing.T) {
	cached := []printers.AMSUnit{
		{ID: 0, Humidity: "3"},
	}
	got := mergeAMSData([]printers.AMSUnit{}, cached)
	if len(got) != 1 {
		t.Fatalf("got len = %d; want 1", len(got))
	}
	if got[0].Humidity != "3" {
		t.Errorf("got[0].Humidity = %q; want %q", got[0].Humidity, "3")
	}
}

func TestMergeAMSData_NilCachedWithNew_ReturnsNew(t *testing.T) {
	newUnits := []printers.AMSUnit{
		{ID: 0, Humidity: "4"},
	}
	got := mergeAMSData(newUnits, nil)
	if len(got) != 1 {
		t.Fatalf("got len = %d; want 1", len(got))
	}
	if got[0].Humidity != "4" {
		t.Errorf("got[0].Humidity = %q; want %q", got[0].Humidity, "4")
	}
}

func TestMergeAMSData_MatchingUnit_MergesTraysAndScalars(t *testing.T) {
	cached := []printers.AMSUnit{{
		ID:          0,
		Humidity:    "3",
		Temp:        "25.0",
		HumidityRaw: "23",
		Trays: []printers.FilamentSlot{
			{Index: 0, Type: "PLA", Color: "FF0000FF", Loaded: true, RemainingMM: 50000},
			{Index: 1, Type: "PETG", Color: "00FF00FF", Loaded: true, RemainingMM: 75000},
		},
	}}
	newUnits := []printers.AMSUnit{{
		ID:       0,
		Humidity: "4",
		// Temp is empty -> retained from cache.
		Trays: []printers.FilamentSlot{
			{Index: 0, Type: "TPU", Color: "0000FFFF", Loaded: true, RemainingMM: 90000},
		},
	}}
	got := mergeAMSData(newUnits, cached)
	if len(got) != 1 {
		t.Fatalf("got len = %d; want 1", len(got))
	}
	u := got[0]

	// Updated values from new.
	if u.Humidity != "4" {
		t.Errorf("Humidity = %q; want %q (updated from new)", u.Humidity, "4")
	}
	if u.Temp != "25.0" {
		t.Errorf("Temp = %q; want %q (retained from cache, empty in new)", u.Temp, "25.0")
	}

	// Tray 0 updated.
	if u.Trays[0].Type != "TPU" {
		t.Errorf("tray[0].Type = %q; want %q (updated from new)", u.Trays[0].Type, "TPU")
	}
	if u.Trays[0].RemainingMM != 90000 {
		t.Errorf("tray[0].RemainingMM = %d; want 90000 (updated from new)", u.Trays[0].RemainingMM)
	}

	// Tray 1 retained from cache.
	if len(u.Trays) != 2 {
		t.Fatalf("len(Trays) = %d; want 2 (1 updated + 1 retained)", len(u.Trays))
	}
	if u.Trays[1].Type != "PETG" {
		t.Errorf("tray[1].Type = %q; want %q (retained from cache)", u.Trays[1].Type, "PETG")
	}
	if u.Trays[1].RemainingMM != 75000 {
		t.Errorf("tray[1].RemainingMM = %d; want 75000 (retained from cache)", u.Trays[1].RemainingMM)
	}
}

func TestMergeAMSData_NewUnitNotInCache_Added(t *testing.T) {
	cached := []printers.AMSUnit{
		{ID: 0, Humidity: "3"},
	}
	newUnits := []printers.AMSUnit{
		{ID: 1, Humidity: "5"},
	}
	got := mergeAMSData(newUnits, cached)

	if len(got) != 2 {
		t.Fatalf("got len = %d; want 2 (1 cached + 1 new)", len(got))
	}

	// Find unit 1 — should be the new one with Humidity="5".
	var foundNew bool
	for _, u := range got {
		if u.ID == 1 {
			foundNew = true
			if u.Humidity != "5" {
				t.Errorf("unit[1].Humidity = %q; want %q", u.Humidity, "5")
			}
		}
		if u.ID == 0 {
			if u.Humidity != "3" {
				t.Errorf("unit[0].Humidity = %q; want %q (retained)", u.Humidity, "3")
			}
		}
	}
	if !foundNew {
		t.Error("unit 1 not found in merged result")
	}
}

func TestMergeAMSData_CachedUnitNotInNew_Retained(t *testing.T) {
	cached := []printers.AMSUnit{
		{ID: 0, Humidity: "3"},
		{ID: 1, Humidity: "4"},
	}
	newUnits := []printers.AMSUnit{
		{ID: 0, Humidity: "9"},
	}
	got := mergeAMSData(newUnits, cached)

	if len(got) != 2 {
		t.Fatalf("got len = %d; want 2 (unit 1 retained from cache)", len(got))
	}

	// Unit 0 updated.
	var u0, u1 printers.AMSUnit
	for _, u := range got {
		if u.ID == 0 {
			u0 = u
		}
		if u.ID == 1 {
			u1 = u
		}
	}
	if u0.Humidity != "9" {
		t.Errorf("unit[0].Humidity = %q; want %q (updated from new)", u0.Humidity, "9")
	}
	if u1.Humidity != "4" {
		t.Errorf("unit[1].Humidity = %q; want %q (retained from cache, not in new)", u1.Humidity, "4")
	}
}

func TestMergeAMSData_BothNil_ReturnsNil(t *testing.T) {
	got := mergeAMSData(nil, nil)
	if got != nil {
		t.Errorf("got = %v; want nil", got)
	}
}

func TestMergeAMSData_EmptyTraysInNew_RetainsCachedTrays(t *testing.T) {
	cached := []printers.AMSUnit{{
		ID: 0,
		Trays: []printers.FilamentSlot{
			{Index: 0, Type: "PLA"},
			{Index: 1, Type: "PETG"},
		},
	}}
	newUnits := []printers.AMSUnit{{
		// ID 0, but Trays is nil/empty (delta omitted tray array).
		ID: 0,
	}}
	got := mergeAMSData(newUnits, cached)

	if len(got) != 1 {
		t.Fatalf("got len = %d; want 1", len(got))
	}
	if len(got[0].Trays) != 2 {
		t.Fatalf("len(Trays) = %d; want 2 (cached trays retained)", len(got[0].Trays))
	}
	if got[0].Trays[0].Type != "PLA" {
		t.Errorf("tray[0].Type = %q; want %q (retained from cache)", got[0].Trays[0].Type, "PLA")
	}
	if got[0].Trays[1].Type != "PETG" {
		t.Errorf("tray[1].Type = %q; want %q (retained from cache)", got[0].Trays[1].Type, "PETG")
	}
}

func TestMergeTrays_EmptyNew_ReturnsCached(t *testing.T) {
	cached := []printers.FilamentSlot{
		{Index: 0, Type: "PLA"},
		{Index: 1, Type: "PETG"},
	}
	got := mergeTrays(nil, cached)
	if len(got) != 2 {
		t.Fatalf("got len = %d; want 2 (cached retained)", len(got))
	}
	if got[0].Type != "PLA" {
		t.Errorf("got[0].Type = %q; want %q", got[0].Type, "PLA")
	}
	if got[1].Type != "PETG" {
		t.Errorf("got[1].Type = %q; want %q", got[1].Type, "PETG")
	}
}

func TestMergeTrays_MatchingUpdated_NonMatchingRetained(t *testing.T) {
	cached := []printers.FilamentSlot{
		{Index: 0, Type: "PLA", RemainingMM: 50000},
		{Index: 1, Type: "PETG", RemainingMM: 75000},
	}
	newTrays := []printers.FilamentSlot{
		{Index: 0, Type: "TPU", RemainingMM: 90000},
	}
	got := mergeTrays(newTrays, cached)

	if len(got) != 2 {
		t.Fatalf("got len = %d; want 2 (1 updated + 1 retained)", len(got))
	}
	if got[0].Type != "TPU" {
		t.Errorf("got[0].Type = %q; want %q (updated from new)", got[0].Type, "TPU")
	}
	if got[0].RemainingMM != 90000 {
		t.Errorf("got[0].RemainingMM = %d; want 90000 (updated from new)", got[0].RemainingMM)
	}
	if got[1].Type != "PETG" {
		t.Errorf("got[1].Type = %q; want %q (retained from cache)", got[1].Type, "PETG")
	}
	if got[1].RemainingMM != 75000 {
		t.Errorf("got[1].RemainingMM = %d; want 75000 (retained from cache)", got[1].RemainingMM)
	}
}

func TestMergeTrays_NewNotInCached_Added(t *testing.T) {
	cached := []printers.FilamentSlot{
		{Index: 0, Type: "PLA"},
	}
	newTrays := []printers.FilamentSlot{
		{Index: 1, Type: "PETG"},
	}
	got := mergeTrays(newTrays, cached)

	if len(got) != 2 {
		t.Fatalf("got len = %d; want 2", len(got))
	}
	var foundNew bool
	for _, tray := range got {
		if tray.Index == 1 {
			foundNew = true
			if tray.Type != "PETG" {
				t.Errorf("tray[1].Type = %q; want %q", tray.Type, "PETG")
			}
		}
	}
	if !foundNew {
		t.Error("tray index 1 not found in merged result")
	}
}

func TestMergeTrays_BothEmpty_ReturnsEmpty(t *testing.T) {
	got := mergeTrays(nil, nil)
	if got != nil {
		t.Errorf("got = %v; want nil (both empty)", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("firstNonEmpty(\"a\",\"b\") = %q; want %q", got, "a")
	}
	if got := firstNonEmpty("", "b"); got != "b" {
		t.Errorf("firstNonEmpty(\"\",\"b\") = %q; want %q", got, "b")
	}
	if got := firstNonEmpty("a", ""); got != "a" {
		t.Errorf("firstNonEmpty(\"a\",\"\") = %q; want %q", got, "a")
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty(\"\",\"\") = %q; want %q", got, "")
	}
}

// Ensure the printers import is used.
var _ = printers.FilamentSlot{}
