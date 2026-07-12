package bambu

import (
	"math"
	"testing"
)

// epsilon is used for floating-point comparisons.
const epsilon = 1e-9

func float64Ptr(v float64) *float64  { return &v }
func intPtr(v int) *int              { return &v }
func stringPtr(v string) *string     { return &v }

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
