package bambu

import (
	"encoding/json"
	"testing"
)

func TestCommand_BasicCommands(t *testing.T) {
	tests := []struct {
		name string
		fn   func() []byte
		want string
	}{
		{
			name: "pause",
			fn:   pauseCommand,
			want: `{"print":{"command":"pause"}}`,
		},
		{
			name: "resume",
			fn:   resumeCommand,
			want: `{"print":{"command":"resume"}}`,
		},
		{
			name: "stop",
			fn:   stopCommand,
			want: `{"print":{"command":"stop"}}`,
		},
		{
			name: "skip_object",
			fn:   skipObjectCommand,
			want: `{"print":{"command":"project_file","param":"skip_object"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()

			// Verify output is valid JSON.
			var gotMap map[string]any
			if err := json.Unmarshal(got, &gotMap); err != nil {
				t.Fatalf("invalid JSON: %v\nraw: %s", err, string(got))
			}

			// Normalise expected JSON for consistent comparison.
			var wantMap map[string]any
			if err := json.Unmarshal([]byte(tt.want), &wantMap); err != nil {
				t.Fatalf("bug: bad expected JSON: %v", err)
			}

			gotNorm, _ := json.Marshal(gotMap)
			wantNorm, _ := json.Marshal(wantMap)
			if string(gotNorm) != string(wantNorm) {
				t.Errorf("command mismatch:\ngot:  %s\nwant: %s", string(got), tt.want)
			}
		})
	}
}

func TestMustMarshal_PanicsOnBadValue(t *testing.T) {
	t.Run("panics on channel value", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected mustMarshal to panic, but it did not")
			}
		}()

		mustMarshal(make(chan int))
	})
}

func TestSetBedTempCommand(t *testing.T) {
	got := setBedTempCommand(60)
	var gotMap map[string]any
	if err := json.Unmarshal(got, &gotMap); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	printSection, ok := gotMap["print"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'print' key: %v", gotMap)
	}
	if printSection["command"] != "gcode_line" {
		t.Errorf("command = %v; want gcode_line", printSection["command"])
	}
	if printSection["param"] != "M140 S60\n" {
		t.Errorf("param = %v; want \"M140 S60\\n\"", printSection["param"])
	}
}

func TestSetNozzleTempCommand(t *testing.T) {
	got := setNozzleTempCommand(210)
	var gotMap map[string]any
	if err := json.Unmarshal(got, &gotMap); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	printSection := gotMap["print"].(map[string]any)
	if printSection["command"] != "gcode_line" {
		t.Errorf("command = %v; want gcode_line", printSection["command"])
	}
	if printSection["param"] != "M104 S210\n" {
		t.Errorf("param = %v; want \"M104 S210\\n\"", printSection["param"])
	}
}

func TestSetLightCommand(t *testing.T) {
	t.Run("on", func(t *testing.T) {
		got := setLightCommand(true)
		var gotMap map[string]any
		if err := json.Unmarshal(got, &gotMap); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		sys, ok := gotMap["system"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'system' key: %v", gotMap)
		}
		if sys["sequence_id"] != "0" {
			t.Errorf("sequence_id = %v; want 0", sys["sequence_id"])
		}
		if sys["command"] != "ledctrl" {
			t.Errorf("command = %v; want ledctrl", sys["command"])
		}
		if sys["led_node"] != "chamber_light" {
			t.Errorf("led_node = %v; want chamber_light", sys["led_node"])
		}
		if sys["led_mode"] != "on" {
			t.Errorf("led_mode = %v; want on", sys["led_mode"])
		}
		if sys["led_on_time"] != float64(500) {
			t.Errorf("led_on_time = %v; want 500", sys["led_on_time"])
		}
		if sys["led_off_time"] != float64(500) {
			t.Errorf("led_off_time = %v; want 500", sys["led_off_time"])
		}
		if sys["loop_times"] != float64(0) {
			t.Errorf("loop_times = %v; want 0", sys["loop_times"])
		}
		if sys["interval_time"] != float64(0) {
			t.Errorf("interval_time = %v; want 0", sys["interval_time"])
		}
	})

	t.Run("off", func(t *testing.T) {
		got := setLightCommand(false)
		var gotMap map[string]any
		if err := json.Unmarshal(got, &gotMap); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		sys := gotMap["system"].(map[string]any)
		if sys["led_mode"] != "off" {
			t.Errorf("led_mode = %v; want off", sys["led_mode"])
		}
		if sys["sequence_id"] != "0" {
			t.Errorf("sequence_id = %v; want 0", sys["sequence_id"])
		}
		if sys["led_on_time"] != float64(500) {
			t.Errorf("led_on_time = %v; want 500", sys["led_on_time"])
		}
	})
}

func TestHomeAllCommand(t *testing.T) {
	got := homeAllCommand()
	var gotMap map[string]any
	if err := json.Unmarshal(got, &gotMap); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	printSection, ok := gotMap["print"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'print' key: %v", gotMap)
	}
	if printSection["command"] != "gcode_line" {
		t.Errorf("command = %v; want gcode_line", printSection["command"])
	}
	if printSection["param"] != "G28\n" {
		t.Errorf("param = %v; want \"G28\\n\"", printSection["param"])
	}
}

func TestJogCommand(t *testing.T) {
	tests := []struct {
		name      string
		x, y, z   float64
		speed     int
		wantParam string
	}{
		{
			name:      "x and y move",
			x:         10,
			y:         -5,
			z:         0,
			speed:     1500,
			wantParam: "G91\nG1 X10 Y-5 F1500\nG90\n",
		},
		{
			name:      "z only move",
			x:         0,
			y:         0,
			z:         2.5,
			speed:     600,
			wantParam: "G91\nG1 Z2.5 F600\nG90\n",
		},
		{
			name:      "all three axes",
			x:         1,
			y:         2,
			z:         3,
			speed:     3000,
			wantParam: "G91\nG1 X1 Y2 Z3 F3000\nG90\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jogCommand(tt.x, tt.y, tt.z, tt.speed)
			var gotMap map[string]any
			if err := json.Unmarshal(got, &gotMap); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			printSection, ok := gotMap["print"].(map[string]any)
			if !ok {
				t.Fatalf("expected 'print' key: %v", gotMap)
			}
			if printSection["command"] != "gcode_line" {
				t.Errorf("command = %v; want gcode_line", printSection["command"])
			}
			if printSection["param"] != tt.wantParam {
				t.Errorf("param = %q; want %q", printSection["param"], tt.wantParam)
			}
		})
	}
}
