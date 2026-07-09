package snapmaker

import (
	"math"
	"testing"
)

const epsilon = 1e-9

func float64Ptr(v float64) *float64 { return &v }
func intPtr(v int) *int             { return &v }
func stringPtr(v string) *string    { return &v }

// tempPair is a helper for constructing temperatureReport test data.
type tempPair struct {
	key   string
	entry *temperatureEntry
}

// makeTempReport creates a temperatureReport from key-entry pairs.
func makeTempReport(pairs ...tempPair) *temperatureReport {
	tr := &temperatureReport{Entries: make(map[string]*temperatureEntry, len(pairs))}
	for _, p := range pairs {
		tr.Entries[p.key] = p.entry
	}
	return tr
}

// ---------------------------------------------------------------------------
// parseAPIReport tests
// ---------------------------------------------------------------------------

func TestParseAPIReport(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *apiPrinterResponse
		wantErr bool
	}{
		{
			name: "full status with all temps + state",
			input: `{
				"temperature": {
					"bed": {"actual": 24.0, "offset": 0, "target": 0.0},
					"tool0": {"actual": 47.0, "offset": 0, "target": 0.0},
					"tool1": {"actual": 26.0, "offset": 0, "target": 0.0},
					"tool2": {"actual": 26.0, "offset": 0, "target": 0.0},
					"tool3": {"actual": 26.0, "offset": 0, "target": 0.0}
				},
				"state": {
					"text": "Operational",
					"flags": {
						"operational": true, "paused": false, "printing": false,
						"cancelling": false, "pausing": false, "error": false,
						"ready": true, "closedOrError": false
					}
				}
			}`,
			want: &apiPrinterResponse{
				Temperature: makeTempReport(
					tempPair{"bed", &temperatureEntry{Actual: 24.0, Target: 0.0, Offset: 0}},
					tempPair{"tool0", &temperatureEntry{Actual: 47.0, Target: 0.0, Offset: 0}},
					tempPair{"tool1", &temperatureEntry{Actual: 26.0, Target: 0.0, Offset: 0}},
					tempPair{"tool2", &temperatureEntry{Actual: 26.0, Target: 0.0, Offset: 0}},
					tempPair{"tool3", &temperatureEntry{Actual: 26.0, Target: 0.0, Offset: 0}},
				),
				State: &stateReport{
					Text: "Operational",
					Flags: &stateFlags{
						Operational: true,
						Ready:       true,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "minimal status (state only)",
			input: `{
				"state": {
					"text": "Operational",
					"flags": {"ready": true}
				}
			}`,
			want: &apiPrinterResponse{
				State: &stateReport{
					Text:  "Operational",
					Flags: &stateFlags{Ready: true},
				},
			},
			wantErr: false,
		},
		{
			name: "partial temps (bed + tool0 only)",
			input: `{
				"temperature": {
					"bed": {"actual": 55.0, "offset": 0, "target": 60.0},
					"tool0": {"actual": 210.0, "offset": 0, "target": 220.0}
				},
				"state": {
					"text": "Printing",
					"flags": {"printing": true}
				}
			}`,
			want: &apiPrinterResponse{
				Temperature: makeTempReport(
					tempPair{"bed", &temperatureEntry{Actual: 55.0, Target: 60.0, Offset: 0}},
					tempPair{"tool0", &temperatureEntry{Actual: 210.0, Target: 220.0, Offset: 0}},
				),
				State: &stateReport{
					Text:  "Printing",
					Flags: &stateFlags{Printing: true},
				},
			},
			wantErr: false,
		},
		{
			name: "null temps",
			input: `{
				"temperature": {
					"bed": {"actual": 55.0, "offset": 0, "target": 60.0},
					"tool0": null,
					"tool1": null,
					"tool2": {"actual": 30.0, "offset": 0, "target": 0.0},
					"tool3": null
				},
				"state": {"text": "Operational"}
			}`,
			want: &apiPrinterResponse{
				Temperature: makeTempReport(
					tempPair{"bed", &temperatureEntry{Actual: 55.0, Target: 60.0, Offset: 0}},
					tempPair{"tool0", nil},
					tempPair{"tool1", nil},
					tempPair{"tool2", &temperatureEntry{Actual: 30.0, Target: 0.0, Offset: 0}},
					tempPair{"tool3", nil},
				),
				State: &stateReport{Text: "Operational"},
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
			got, err := parseAPIReport([]byte(tt.input))

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
					t.Fatal("expected nil response")
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil response")
			}

			compareAPIResponse(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// parseQueryReport tests
// ---------------------------------------------------------------------------

func TestParseQueryReport(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *moonrakerQueryResponse
		wantErr bool
	}{
		{
			name: "full query with print_stats + virtual_sdcard",
			input: `{
				"result": {
					"eventtime": 12345678,
					"status": {
						"print_stats": {
							"filename": "test.gcode",
							"total_duration": 28185.7,
							"print_duration": 28125.6,
							"filament_used": 88827.7,
							"state": "printing",
							"info": {"total_layer": 65, "current_layer": 42}
						},
						"virtual_sdcard": {"progress": 0.5}
					}
				}
			}`,
			want: &moonrakerQueryResponse{
				Result: struct {
					Status *queryStatus `json:"status"`
				}{
					Status: &queryStatus{
						PrintStats: &printStatsReport{
							Filename:      "test.gcode",
							PrintDuration: 28125.6,
							State:         "printing",
							Info: &printStatsInfo{
								CurrentLayer: 42,
								TotalLayer:   65,
							},
						},
						VirtualSDCard: &virtualSDCardReport{
							Progress: 0.5,
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "query with no layers (no info)",
			input: `{
				"result": {
					"status": {
						"print_stats": {
							"filename": "simple.gcode",
							"print_duration": 100.0,
							"state": "printing"
						},
						"virtual_sdcard": {"progress": 0.25}
					}
				}
			}`,
			want: &moonrakerQueryResponse{
				Result: struct {
					Status *queryStatus `json:"status"`
				}{
					Status: &queryStatus{
						PrintStats: &printStatsReport{
							Filename:      "simple.gcode",
							PrintDuration: 100.0,
							State:         "printing",
						},
						VirtualSDCard: &virtualSDCardReport{
							Progress: 0.25,
						},
					},
				},
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
			got, err := parseQueryReport([]byte(tt.input))

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
					t.Fatal("expected nil response")
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil response")
			}

			compareQueryResponse(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// mapMoonrakerState tests
// ---------------------------------------------------------------------------

func TestMapMoonrakerState(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		flags *stateFlags
		want  string
	}{
		{name: "operational idle", text: "Operational", flags: &stateFlags{Operational: true, Ready: true}, want: "idle"},
		{name: "printing via text", text: "Printing", flags: &stateFlags{Printing: true}, want: "printing"},
		{name: "paused via text", text: "Paused", flags: &stateFlags{Paused: true}, want: "paused"},
		{name: "error via text", text: "Error", want: "error"},
		{name: "error via flags", text: "Operational", flags: &stateFlags{Error: true}, want: "error"},
		{name: "flags override text", text: "Operational", flags: &stateFlags{Printing: true}, want: "printing"},
		{name: "complete", text: "Complete", want: "complete"},
		{name: "cancelled", text: "Cancelled", want: "complete"},
		{name: "no flags", text: "Operational", want: "idle"},
		{name: "nil flags", text: "Operational", flags: nil, want: "idle"},
		{name: "unknown state text", text: "SomethingBizarre", want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapMoonrakerState(tt.text, tt.flags)
			if got != tt.want {
				t.Errorf("mapMoonrakerState(%q, %+v) = %q; want %q", tt.text, tt.flags, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func compareAPIResponse(t *testing.T, want, got *apiPrinterResponse) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Fatal("expected nil apiPrinterResponse")
		return
	}
	if got == nil {
		t.Fatal("expected non-nil apiPrinterResponse")
		return
	}

	// Compare State
	if want.State == nil && got.State != nil {
		t.Error("State = non-nil; want nil")
		return
	}
	if want.State != nil && got.State == nil {
		t.Error("State = nil; want non-nil")
		return
	}
	if want.State != nil && got.State != nil {
		if got.State.Text != want.State.Text {
			t.Errorf("State.Text = %q; want %q", got.State.Text, want.State.Text)
		}
		compareStateFlags(t, want.State.Flags, got.State.Flags)
	}

	// Compare Temperature
	if want.Temperature == nil && got.Temperature != nil {
		t.Error("Temperature = non-nil; want nil")
		return
	}
	if want.Temperature != nil && got.Temperature == nil {
		t.Error("Temperature = nil; want non-nil")
		return
	}
	if want.Temperature != nil && got.Temperature != nil {
		// Compare entry counts.
		if len(want.Temperature.Entries) != len(got.Temperature.Entries) {
			t.Errorf("Temperature.Entries length = %d; want %d", len(got.Temperature.Entries), len(want.Temperature.Entries))
		}
		// Compare each expected entry.
		for key, wantEntry := range want.Temperature.Entries {
			gotEntry, ok := got.Temperature.Entries[key]
			if !ok {
				t.Errorf("Temperature.Entries[%q] missing", key)
				continue
			}
			compareTemperatureEntry(t, key, wantEntry, gotEntry)
		}
	}
}

func compareStateFlags(t *testing.T, want, got *stateFlags) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Errorf("Flags = %+v; want nil", *got)
		return
	}
	if got == nil {
		t.Errorf("Flags = nil; want %+v", *want)
		return
	}
	if *got != *want {
		t.Errorf("Flags = %+v; want %+v", *got, *want)
	}
}

func compareTemperatureEntry(t *testing.T, name string, want, got *temperatureEntry) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Errorf("%s = %+v; want nil", name, *got)
		return
	}
	if got == nil {
		t.Errorf("%s = nil; want %+v", name, *want)
		return
	}
	if math.Abs(got.Actual-want.Actual) > epsilon {
		t.Errorf("%s.Actual = %f; want %f", name, got.Actual, want.Actual)
	}
	if math.Abs(got.Target-want.Target) > epsilon {
		t.Errorf("%s.Target = %f; want %f", name, got.Target, want.Target)
	}
	if got.Offset != want.Offset {
		t.Errorf("%s.Offset = %d; want %d", name, got.Offset, want.Offset)
	}
}

func compareQueryResponse(t *testing.T, want, got *moonrakerQueryResponse) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Fatal("expected nil moonrakerQueryResponse")
		return
	}
	if got == nil {
		t.Fatal("expected non-nil moonrakerQueryResponse")
		return
	}

	// Compare Result.Status
	if want.Result.Status == nil && got.Result.Status != nil {
		t.Error("Result.Status = non-nil; want nil")
		return
	}
	if want.Result.Status != nil && got.Result.Status == nil {
		t.Error("Result.Status = nil; want non-nil")
		return
	}
	if want.Result.Status != nil && got.Result.Status != nil {
		comparePrintStats(t, want.Result.Status.PrintStats, got.Result.Status.PrintStats)
		compareVirtualSDCard(t, want.Result.Status.VirtualSDCard, got.Result.Status.VirtualSDCard)
	}
}

func comparePrintStats(t *testing.T, want, got *printStatsReport) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Errorf("PrintStats = %+v; want nil", *got)
		return
	}
	if got == nil {
		t.Errorf("PrintStats = nil; want %+v", *want)
		return
	}
	if got.Filename != want.Filename {
		t.Errorf("PrintStats.Filename = %q; want %q", got.Filename, want.Filename)
	}
	if math.Abs(got.PrintDuration-want.PrintDuration) > epsilon {
		t.Errorf("PrintStats.PrintDuration = %f; want %f", got.PrintDuration, want.PrintDuration)
	}
	if got.State != want.State {
		t.Errorf("PrintStats.State = %q; want %q", got.State, want.State)
	}
	if got.Message != want.Message {
		t.Errorf("PrintStats.Message = %q; want %q", got.Message, want.Message)
	}

	// Compare Info
	if want.Info == nil && got.Info != nil {
		t.Error("PrintStats.Info = non-nil; want nil")
		return
	}
	if want.Info != nil && got.Info == nil {
		t.Error("PrintStats.Info = nil; want non-nil")
		return
	}
	if want.Info != nil && got.Info != nil {
		if got.Info.CurrentLayer != want.Info.CurrentLayer {
			t.Errorf("PrintStats.Info.CurrentLayer = %d; want %d", got.Info.CurrentLayer, want.Info.CurrentLayer)
		}
		if got.Info.TotalLayer != want.Info.TotalLayer {
			t.Errorf("PrintStats.Info.TotalLayer = %d; want %d", got.Info.TotalLayer, want.Info.TotalLayer)
		}
	}
}

func compareVirtualSDCard(t *testing.T, want, got *virtualSDCardReport) {
	t.Helper()

	if want == nil && got == nil {
		return
	}
	if want == nil {
		t.Errorf("VirtualSDCard = %+v; want nil", *got)
		return
	}
	if got == nil {
		t.Errorf("VirtualSDCard = nil; want %+v", *want)
		return
	}
	if math.Abs(got.Progress-want.Progress) > epsilon {
		t.Errorf("VirtualSDCard.Progress = %f; want %f", got.Progress, want.Progress)
	}
}
