package bambu

import (
	"testing"

	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// The dashboard sorts by whether an error demands attention *now*, and the only
// honest way to tell those apart is the transition, not the code. A P1S reports
// its degraded SD card (print_error 0x0500C011) in every report forever while
// printing perfectly well on request; a printer whose job just died reports the
// same shape of error once, having been RUNNING a moment ago. Both read as
// State="error". Treating them alike meant an unactionable maintenance notice
// permanently outranked every active print on the fleet.

func seedState(c *Client, state string) {
	c.setStatus(printers.PrinterStatus{ID: "test-id", Name: "Test Printer", Type: "bambu", Online: true, State: state})
}

// TestPrintFailed_FailureDuringAnActivePrintIsUrgent: we were watching it print,
// then it errored. Filament is in the nozzle and something is waiting on a human.
func TestPrintFailed_FailureDuringAnActivePrintIsUrgent(t *testing.T) {
	c := newTestPrinterClient(nil)
	seedState(c, "printing")

	c.handleReport(nil, newMockMessage([]byte(
		`{"print": {"gcode_state": "FAILED", "print_error": 3, "subtask_name": "cube.gcode"}}`)))

	s := c.Status()
	if s.State != "error" {
		t.Fatalf("State = %q, want error", s.State)
	}
	if !s.PrintFailed {
		t.Error("PrintFailed = false; a print that failed while we were watching it is the urgent case")
	}
}

// TestPrintFailed_StandingFaultAtConnectIsNotUrgent is the reported bug. The
// first report after a connect already carries the error, so there was no
// transition — the printer was in this condition before we looked, and will be
// after we look away.
func TestPrintFailed_StandingFaultAtConnectIsNotUrgent(t *testing.T) {
	c := newTestPrinterClient(nil)
	seedState(c, "idle")

	// The real P1S payload: SD card degraded, FAILED gcode state left over from
	// some print days ago, layer 0 of 1083.
	c.handleReport(nil, newMockMessage([]byte(
		`{"print": {"gcode_state": "FAILED", "print_error": 83935249, "subtask_name": "Part Studio 1 - Part 1(10).stl"}}`)))

	s := c.Status()
	if s.State != "error" {
		t.Fatalf("State = %q, want error — the fault is still real and still shown", s.State)
	}
	if s.PrintFailed {
		t.Error("PrintFailed = true; a fault that predates our connection is maintenance, not an emergency")
	}
}

// TestPrintFailed_PausedThenFails is the same urgent case from the other active
// state — a paused job that goes wrong still needs someone.
func TestPrintFailed_PausedThenFails(t *testing.T) {
	c := newTestPrinterClient(nil)
	seedState(c, "paused")

	c.handleReport(nil, newMockMessage([]byte(
		`{"print": {"gcode_state": "FAILED", "print_error": 3}}`)))

	if !c.Status().PrintFailed {
		t.Error("PrintFailed = false; failing out of a paused print is still a dead job")
	}
}

// TestPrintFailed_ClearsWhenThePrinterRecovers guards the latch direction: a
// cleared error must not leave PrintFailed set, or the card keeps its urgent
// tier after the problem is gone.
func TestPrintFailed_ClearsWhenThePrinterRecovers(t *testing.T) {
	c := newTestPrinterClient(nil)
	seedState(c, "printing")
	c.handleReport(nil, newMockMessage([]byte(`{"print": {"gcode_state": "FAILED", "print_error": 3}}`)))
	if !c.Status().PrintFailed {
		t.Fatal("setup: expected the urgent classification first")
	}

	c.handleReport(nil, newMockMessage([]byte(`{"print": {"gcode_state": "RUNNING", "print_error": 0}}`)))

	s := c.Status()
	if s.PrintFailed {
		t.Error("PrintFailed still set after the printer returned to a healthy state")
	}
}
