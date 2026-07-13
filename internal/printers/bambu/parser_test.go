package bambu

import (
	"math"
	"testing"

	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// epsilon is used for floating-point comparisons.
const epsilon = 1e-9

func float64Ptr(v float64) *float64 { return &v }
func intPtr(v int) *int             { return &v }
func stringPtr(v string) *string    { return &v }

// ---------------------------------------------------------------------------
// parseReport tests
// ---------------------------------------------------------------------------

func TestParseReport(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *report
		wantErr bool
	}{
		{
			name: "full valid report",
			input: `{
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
			}`,
			want: &report{
				Print: &printStatus{
					GcodeState:          "RUNNING",
					GcodeFile:           stringPtr("model.gcode"),
					McPercent:           intPtr(50),
					McRemainingTime:     intPtr(3600),
					BedTemper:           float64Ptr(55.5),
					BedTarget:           float64Ptr(60.0),
					NozzleTemper:        float64Ptr(210.0),
					NozzleTarget:        float64Ptr(220.0),
					ChamberTemper:       float64Ptr(30.0),
					ChamberTargetTemper: float64Ptr(45.0),
					LayerNum:            intPtr(5),
					TotalLayerNum:       intPtr(100),
					WifiSignal:          stringPtr("good"),
					HomeFlag:            1,
					StgCur:              intPtr(2),
					StgTotal:            intPtr(10),
					PrintError:          intPtr(0),
					Lifecycle:           stringPtr("printing"),
				},
				Camera: &cameraStatus{
					IPCamURL:     "rtsp://camera",
					TimelapseURL: "http://timelapse",
				},
			},
			wantErr: false,
		},
		{
			name: "null and missing pointer fields",
			input: `{
				"print": {
					"gcode_state": "IDLE",
					"gcode_file": "",
					"bed_temper": null,
					"home_flag": 0
				}
			}`,
			want: &report{
				Print: &printStatus{
					GcodeState: "IDLE",
					GcodeFile:  stringPtr(""),
					BedTemper:  nil,
					HomeFlag:   0,
				},
				Camera: nil,
			},
			wantErr: false,
		},
		{
			name: "no print section",
			input: `{
				"camera": {
					"ipcam_url": "rtsp://camera"
				}
			}`,
			want: &report{
				Print:  nil,
				Camera: &cameraStatus{IPCamURL: "rtsp://camera"},
			},
			wantErr: false,
		},
		{
			name: "H2S chamber temp fallback from info.temp",
			input: `{
				"print": {
					"gcode_state": "RUNNING",
					"gcode_file": "test.gcode",
					"home_flag": 0,
					"nozzle_temper": 200.0,
					"info": {
						"temp": 28.5
					}
				}
			}`,
			want: &report{
				Print: &printStatus{
					GcodeState:    "RUNNING",
					GcodeFile:     stringPtr("test.gcode"),
					NozzleTemper:  float64Ptr(200.0),
					Info:          &infoData{Temp: float64Ptr(28.5)},
					ChamberTemper: nil, // info.temp is separate from chamber_temper
				},
				Camera: nil,
			},
			wantErr: false,
		},
		{
			name: "subtask_name field parsed (P1S-style report)",
			input: `{
				"print": {
					"gcode_state": "RUNNING",
					"subtask_name": "benchy.gcode",
					"mc_percent": 42
				}
			}`,
			want: &report{
				Print: &printStatus{
					GcodeState:  "RUNNING",
					SubtaskName: stringPtr("benchy.gcode"),
					McPercent:   intPtr(42),
				},
				Camera: nil,
			},
			wantErr: false,
		},
		{
			name:    "malformed JSON",
			input:   `{bad`,
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseReport([]byte(tt.input))

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.want == nil {
				if got != nil {
					t.Fatal("expected nil report")
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil report")
			}

			// Compare top-level fields.
			comparePrintStatus(t, tt.want.Print, got.Print)
			compareCameraStatus(t, tt.want.Camera, got.Camera)
		})
	}
}

// ---------------------------------------------------------------------------
// mapState tests
// ---------------------------------------------------------------------------

func TestMapState(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "RUNNING", want: "printing"},
		{input: "PAUSE", want: "paused"},
		{input: "SUCCESS", want: "complete"},
		{input: "FINISH", want: "complete"},
		{input: "FAILED", want: "error"},
		{input: "IDLE", want: "idle"},
		{input: "", want: "idle"},
		{input: "PREPARE", want: "printing"},
		{input: "STANDBY", want: "idle"},
		{input: "SOMETHING_ELSE", want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapState(tt.input)
			if got != tt.want {
				t.Errorf("mapState(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isErrorState tests
// ---------------------------------------------------------------------------

func TestIsErrorState(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{input: "FAILED", want: true},
		{input: "IDLE", want: true},
		{input: "RUNNING", want: false},
		{input: "SOMETHING", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isErrorState(tt.input)
			if got != tt.want {
				t.Errorf("isErrorState(%q) = %v; want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isHealthyGcodeState tests
// ---------------------------------------------------------------------------

func TestIsHealthyGcodeState(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{input: "RUNNING", want: true},
		{input: "PAUSE", want: true},
		{input: "SUCCESS", want: true},
		{input: "FINISH", want: true},
		{input: "IDLE", want: true},
		{input: "PREPARE", want: true},
		{input: "STANDBY", want: true},
		{input: "FAILED", want: false},
		{input: "SOMETHING_ELSE", want: false}, // maps to "unknown" — not confidently healthy
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isHealthyGcodeState(tt.input)
			if got != tt.want {
				t.Errorf("isHealthyGcodeState(%q) = %v; want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func comparePrintStatus(t *testing.T, want, got *printStatus) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Fatal("expected nil Print")
		return
	}
	if got == nil {
		t.Fatal("expected non-nil Print")
		return
	}

	// String fields.
	if got.GcodeState != want.GcodeState {
		t.Errorf("GcodeState = %q; want %q", got.GcodeState, want.GcodeState)
	}

	// Int pointer fields.
	compareIntPtr(t, "McPercent", want.McPercent, got.McPercent)
	compareIntPtr(t, "McRemainingTime", want.McRemainingTime, got.McRemainingTime)
	compareIntPtr(t, "LayerNum", want.LayerNum, got.LayerNum)
	compareIntPtr(t, "TotalLayerNum", want.TotalLayerNum, got.TotalLayerNum)
	compareIntPtr(t, "StgCur", want.StgCur, got.StgCur)
	compareIntPtr(t, "StgTotal", want.StgTotal, got.StgTotal)
	compareIntPtr(t, "PrintError", want.PrintError, got.PrintError)

	// Float64 pointer fields.
	compareFloat64Ptr(t, "BedTemper", want.BedTemper, got.BedTemper)
	compareFloat64Ptr(t, "BedTarget", want.BedTarget, got.BedTarget)
	compareFloat64Ptr(t, "NozzleTemper", want.NozzleTemper, got.NozzleTemper)
	compareFloat64Ptr(t, "NozzleTarget", want.NozzleTarget, got.NozzleTarget)
	compareFloat64Ptr(t, "ChamberTemper", want.ChamberTemper, got.ChamberTemper)
	compareFloat64Ptr(t, "ChamberTargetTemper", want.ChamberTargetTemper, got.ChamberTargetTemper)

	// String pointer fields.
	compareStringPtr(t, "WifiSignal", want.WifiSignal, got.WifiSignal)
	compareStringPtr(t, "GcodeFile", want.GcodeFile, got.GcodeFile)
	compareStringPtr(t, "SubtaskName", want.SubtaskName, got.SubtaskName)
	compareStringPtr(t, "Lifecycle", want.Lifecycle, got.Lifecycle)

	// Plain int fields.
	if got.HomeFlag != want.HomeFlag {
		t.Errorf("HomeFlag = %d; want %d", got.HomeFlag, want.HomeFlag)
	}

	// Info field.
	if want.Info == nil && got.Info != nil {
		t.Errorf("Info = %+v; want nil", got.Info)
	} else if want.Info != nil && got.Info == nil {
		t.Errorf("Info = nil; want %+v", want.Info)
	} else if want.Info != nil && got.Info != nil {
		compareFloat64Ptr(t, "Info.Temp", want.Info.Temp, got.Info.Temp)
	}
}

func compareCameraStatus(t *testing.T, want, got *cameraStatus) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Fatal("expected nil Camera")
		return
	}
	if got == nil {
		t.Fatal("expected non-nil Camera")
		return
	}

	if got.IPCamURL != want.IPCamURL {
		t.Errorf("IPCamURL = %q; want %q", got.IPCamURL, want.IPCamURL)
	}
	if got.TimelapseURL != want.TimelapseURL {
		t.Errorf("TimelapseURL = %q; want %q", got.TimelapseURL, want.TimelapseURL)
	}
}

func compareIntPtr(t *testing.T, name string, want, got *int) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Errorf("%s = %d; want nil", name, *got)
		return
	}
	if got == nil {
		t.Errorf("%s = nil; want %d", name, *want)
		return
	}
	if *got != *want {
		t.Errorf("%s = %d; want %d", name, *got, *want)
	}
}

func compareFloat64Ptr(t *testing.T, name string, want, got *float64) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Errorf("%s = %f; want nil", name, *got)
		return
	}
	if got == nil {
		t.Errorf("%s = nil; want %f", name, *want)
		return
	}
	if math.Abs(*got-*want) > epsilon {
		t.Errorf("%s = %f; want %f", name, *got, *want)
	}
}

func compareStringPtr(t *testing.T, name string, want, got *string) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Errorf("%s = %q; want nil", name, *got)
		return
	}
	if got == nil {
		t.Errorf("%s = nil; want %q", name, *want)
		return
	}
	if *got != *want {
		t.Errorf("%s = %q; want %q", name, *got, *want)
	}
}

// ---------------------------------------------------------------------------
// HMS decode tests
// ---------------------------------------------------------------------------

// TestDecodeHMSCode_PybambuOracle validates the bit-math against the real
// pybambu sample entry {attr: 201327360, code: 196615}, deriving the expected
// string from the documented formula rather than a guessed literal.
func TestDecodeHMSCode_PybambuOracle(t *testing.T) {
	var attr uint32 = 201327360
	var code uint32 = 196615

	// Derive expected value directly from the spec formula.
	want := hexPart(attr>>16) + "-" + hexPart(attr&0xFFFF) + "-" + hexPart(code>>16) + "-" + hexPart(code&0xFFFF)
	want = "HMS_" + want

	got := decodeHMSCode(attr, code)
	if got != want {
		t.Errorf("decodeHMSCode(%d, %d) = %q; want %q", attr, code, got, want)
	}
}

// hexPart formats a uint32 sub-part as 4-digit uppercase hex, mirroring the
// %04X verb used by decodeHMSCode, so the oracle test derives its expectation
// independently rather than hardcoding the formatted string.
func hexPart(v uint32) string {
	const hexDigits = "0123456789ABCDEF"
	b := make([]byte, 4)
	for i := 3; i >= 0; i-- {
		b[i] = hexDigits[v&0xF]
		v >>= 4
	}
	return string(b)
}

func TestDecodeHMSModule(t *testing.T) {
	tests := []struct {
		name string
		attr uint32
		want string
	}{
		{name: "pybambu oracle sample -> xcam", attr: 201327360, want: "xcam"},
		{name: "mainboard", attr: 0x05 << 24, want: "mainboard"},
		{name: "ams", attr: 0x07 << 24, want: "ams"},
		{name: "toolhead", attr: 0x08 << 24, want: "toolhead"},
		{name: "mc", attr: 0x03 << 24, want: "mc"},
		{name: "unknown module byte falls back", attr: 0xFF << 24, want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeHMSModule(tt.attr)
			if got != tt.want {
				t.Errorf("decodeHMSModule(%d) = %q; want %q", tt.attr, got, tt.want)
			}
		})
	}
}

func TestDecodeHMSSeverity(t *testing.T) {
	tests := []struct {
		name string
		code uint32
		want string
	}{
		{name: "pybambu oracle sample -> common", code: 196615, want: "common"},
		{name: "fatal", code: 1 << 16, want: "fatal"},
		{name: "serious", code: 2 << 16, want: "serious"},
		{name: "common", code: 3 << 16, want: "common"},
		{name: "info", code: 4 << 16, want: "info"},
		{name: "unknown severity falls back", code: 99 << 16, want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeHMSSeverity(tt.code)
			if got != tt.want {
				t.Errorf("decodeHMSSeverity(%d) = %q; want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestSplitHMS(t *testing.T) {
	tests := []struct {
		name       string
		items      []hmsItem
		wantErrors []printers.HMSEntry
		wantWarn   []printers.HMSEntry
	}{
		{
			name:       "nil input -> nil/empty output, no error",
			items:      nil,
			wantErrors: nil,
			wantWarn:   nil,
		},
		{
			name:       "empty slice input -> nil/empty output, no error",
			items:      []hmsItem{},
			wantErrors: nil,
			wantWarn:   nil,
		},
		{
			name: "fatal buckets into errors",
			items: []hmsItem{
				{Attr: 0x05 << 24, Code: 1 << 16},
			},
			wantErrors: []printers.HMSEntry{
				{Code: decodeHMSCode(0x05<<24, 1<<16), Module: "mainboard", Severity: "fatal"},
			},
			wantWarn: nil,
		},
		{
			name: "serious buckets into errors",
			items: []hmsItem{
				{Attr: 0x08 << 24, Code: 2 << 16},
			},
			wantErrors: []printers.HMSEntry{
				{Code: decodeHMSCode(0x08<<24, 2<<16), Module: "toolhead", Severity: "serious"},
			},
			wantWarn: nil,
		},
		{
			name: "common and info bucket into warnings",
			items: []hmsItem{
				{Attr: 201327360, Code: 196615}, // pybambu oracle sample, xcam/common
				{Attr: 0x07 << 24, Code: 4 << 16},
			},
			wantErrors: nil,
			wantWarn: []printers.HMSEntry{
				// The pybambu oracle sample code (201327360, 196615) happens
				// to be a real entry in the vendored HMS message table (a
				// first-layer-defect warning) — Message is derived via
				// lookupHMSMessage rather than hardcoded, so this test
				// doesn't silently drift if upstream data changes.
				{Code: decodeHMSCode(201327360, 196615), Module: "xcam", Severity: "common", Message: lookupHMSMessage(201327360, 196615, "")},
				{Code: decodeHMSCode(0x07<<24, 4<<16), Module: "ams", Severity: "info"},
			},
		},
		{
			name: "unknown module falls back to unknown, still buckets by severity",
			items: []hmsItem{
				{Attr: 0xFF << 24, Code: 1 << 16},
			},
			wantErrors: []printers.HMSEntry{
				{Code: decodeHMSCode(0xFF<<24, 1<<16), Module: "unknown", Severity: "fatal"},
			},
			wantWarn: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotErrors, gotWarn := splitHMS(tt.items, "")
			compareHMSEntries(t, "errors", tt.wantErrors, gotErrors)
			compareHMSEntries(t, "warnings", tt.wantWarn, gotWarn)
		})
	}
}

func compareHMSEntries(t *testing.T, label string, want, got []printers.HMSEntry) {
	t.Helper()

	if len(want) != len(got) {
		t.Fatalf("%s: len = %d; want %d (got=%+v, want=%+v)", label, len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %+v; want %+v", label, i, got[i], want[i])
		}
	}
}
