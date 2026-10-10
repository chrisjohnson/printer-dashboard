package server

import (
	"strings"
	"testing"

	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// The movement gate must allow "complete" as well as "idle". A finished print
// has already parked the toolhead, so it is physically as safe as idle — and
// because Bambu P1-series delta reports routinely omit gcode_state, State stays
// latched at "complete" for as long as such a printer sits between prints. When
// "complete" was rejected, the jog pad and Home All were disabled permanently on
// any P1S that had ever printed, which is what users reported as "the move
// buttons are all greyed out".
func TestRequireMovementSafe(t *testing.T) {
	tests := []struct {
		name      string
		online    bool
		state     string
		wantError bool
	}{
		{"idle is safe", true, "idle", false},
		{"complete is safe", true, "complete", false},
		{"offline rejected", false, "idle", true},
		{"offline rejected even when complete", false, "complete", true},
		{"printing rejected", true, "printing", true},
		{"paused rejected", true, "paused", true},
		{"error rejected", true, "error", true},
		{"unknown rejected", true, "unknown", true},
		{"empty state rejected", true, "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := requireMovementSafe(printers.PrinterStatus{Online: tc.online, State: tc.state})
			if tc.wantError && err == nil {
				t.Errorf("requireMovementSafe(online=%v, state=%q) = nil; want error", tc.online, tc.state)
			}
			if !tc.wantError && err != nil {
				t.Errorf("requireMovementSafe(online=%v, state=%q) = %v; want nil", tc.online, tc.state, err)
			}
		})
	}
}

// The rejection text is what the UI surfaces verbatim, so it has to name the
// state the printer was actually in.
func TestRequireMovementSafe_ErrorText(t *testing.T) {
	err := requireMovementSafe(printers.PrinterStatus{Online: true, State: "printing"})
	if err == nil || !strings.Contains(err.Error(), "printing") {
		t.Errorf("error = %v; want it to name the offending state", err)
	}
}

// The dashboard's client-side mirror of this gate must allow "complete" too, or
// the buttons stay greyed out on a finished printer even though the backend now
// accepts the command. Asserted against the template source (the same style used
// elsewhere in this package) because the Playwright suite needs a browser and a
// live server and so can't cover this in a plain `go test` run.
func TestDashboardTemplate_MovementSafeStatesMatchBackend(t *testing.T) {
	for _, want := range []string{
		`const MOVEMENT_SAFE_STATES = ['idle', 'complete'];`,
		`function canMove(p) {`,
		`return !!p.online && MOVEMENT_SAFE_STATES.indexOf(p.state || 'unknown') !== -1;`,
	} {
		if !strings.Contains(indexDashboardTemplate, want) {
			t.Errorf("indexDashboardTemplate missing %q", want)
		}
	}

	// The old single-state condition must be gone from both places that used to
	// duplicate it (moveSectionHtml and updateCard's step 9b) — if either is
	// still comparing against 'idle' alone, that copy has drifted from the
	// backend again.
	if strings.Contains(indexDashboardTemplate, "st !== 'idle'") {
		t.Error("indexDashboardTemplate still contains the old `st !== 'idle'` move gate; " +
			"both moveSectionHtml() and updateCard() must go through canMove()")
	}
}
