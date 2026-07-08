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
