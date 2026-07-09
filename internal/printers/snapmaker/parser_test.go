package snapmaker

import (
	"math"
	"testing"
)

const epsilon = 1e-9

func float64Ptr(v float64) *float64 { return &v }
func intPtr(v int) *int             { return &v }

// ---------------------------------------------------------------------------
// parseReport tests
// ---------------------------------------------------------------------------

func TestParseReport(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *paxxStatus
		wantErr bool
	}{
		{
			name: "full valid report",
			input: `{
				"status": "running",
				"progress": 0.75,
				"file": "benchy.gcode",
				"bed_temp": 55.5,
				"bed_target_temp": 60.0,
				"nozzle_temp": 210.0,
				"nozzle_target_temp": 220.0,
				"chamber_temp": 30.0,
				"print_duration": 1800,
				"remaining_time": 1200,
				"current_layer": 42,
				"total_layers": 100,
				"error": ""
			}`,
			want: &paxxStatus{
				Status:        "running",
				Progress:      0.75,
				File:          "benchy.gcode",
				BedTemp:       float64Ptr(55.5),
				BedTarget:     float64Ptr(60.0),
				NozzleTemp:    float64Ptr(210.0),
				NozzleTarget:  float64Ptr(220.0),
				ChamberTemp:   float64Ptr(30.0),
				PrintDuration: intPtr(1800),
				RemainingTime: intPtr(1200),
				CurrentLayer:  intPtr(42),
				TotalLayers:   intPtr(100),
				Error:         "",
			},
			wantErr: false,
		},
		{
			name: "minimal report",
			input: `{
				"status": "idle"
			}`,
			want: &paxxStatus{
				Status:  "idle",
				Error:   "",
			},
			wantErr: false,
		},
		{
			name: "null and missing pointer fields",
			input: `{
				"status": "idle",
				"bed_temp": null,
				"nozzle_temp": null
			}`,
			want: &paxxStatus{
				Status:   "idle",
				BedTemp:  nil,
				NozzleTemp: nil,
				Error:    "",
			},
			wantErr: false,
		},
		{
			name: "error state with message",
			input: `{
				"status": "error",
				"error": "Heater timeout"
			}`,
			want: &paxxStatus{
				Status: "error",
				Error:  "Heater timeout",
			},
			wantErr: false,
		},
		{
			name: "printing with progress zero",
			input: `{
				"status": "running",
				"progress": 0,
				"file": "model.gcode"
			}`,
			want: &paxxStatus{
				Status:   "running",
				Progress: 0,
				File:     "model.gcode",
				Error:    "",
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

			comparePaxxStatus(t, tt.want, got)
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
		{input: "idle", want: "idle"},
		{input: "running", want: "printing"},
		{input: "printing", want: "printing"},
		{input: "paused", want: "paused"},
		{input: "error", want: "error"},
		{input: "failed", want: "error"},
		{input: "complete", want: "complete"},
		{input: "finished", want: "complete"},
		{input: "success", want: "complete"},
		{input: "", want: "idle"},
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
// helpers
// ---------------------------------------------------------------------------

func comparePaxxStatus(t *testing.T, want, got *paxxStatus) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Fatal("expected nil paxxStatus")
		return
	}
	if got == nil {
		t.Fatal("expected non-nil paxxStatus")
		return
	}

	// String fields.
	if got.Status != want.Status {
		t.Errorf("Status = %q; want %q", got.Status, want.Status)
	}
	if got.File != want.File {
		t.Errorf("File = %q; want %q", got.File, want.File)
	}
	if got.Error != want.Error {
		t.Errorf("Error = %q; want %q", got.Error, want.Error)
	}

	// Float64 fields.
	if math.Abs(got.Progress-want.Progress) > epsilon {
		t.Errorf("Progress = %f; want %f", got.Progress, want.Progress)
	}

	// Float64 pointer fields.
	compareFloat64Ptr(t, "BedTemp", want.BedTemp, got.BedTemp)
	compareFloat64Ptr(t, "BedTarget", want.BedTarget, got.BedTarget)
	compareFloat64Ptr(t, "NozzleTemp", want.NozzleTemp, got.NozzleTemp)
	compareFloat64Ptr(t, "NozzleTarget", want.NozzleTarget, got.NozzleTarget)
	compareFloat64Ptr(t, "ChamberTemp", want.ChamberTemp, got.ChamberTemp)

	// Int pointer fields.
	compareIntPtr(t, "PrintDuration", want.PrintDuration, got.PrintDuration)
	compareIntPtr(t, "RemainingTime", want.RemainingTime, got.RemainingTime)
	compareIntPtr(t, "CurrentLayer", want.CurrentLayer, got.CurrentLayer)
	compareIntPtr(t, "TotalLayers", want.TotalLayers, got.TotalLayers)
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
