package bambu

import (
	"testing"

	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// TestHandleReport_FaultPrintErrorIsNotAlsoAnAdvisory pins the dedupe between
// the error banner and the advisory row.
//
// print_error arrives on two channels at once when it is the actual fault: it
// sets State="error" and owns ErrorMsg, and it was also being appended to
// Advisories. The popover renders both, so a failed P1S printed the same
// "SD card performance has degraded…" sentence twice in a row — once red, once
// amber — which reads as two problems rather than one. HMS already had this
// dedupe on the banner side (see showErrorBanner in the frontend); this is the
// same rule applied where the duplication actually originates.
func TestHandleReport_FaultPrintErrorIsNotAlsoAnAdvisory(t *testing.T) {
	c := newTestPrinterClient(nil)
	c.setStatus(printers.PrinterStatus{ID: "test-id", Name: "Test Printer", Type: "bambu", Online: true, State: "idle"})

	// FAILED + print_error: the printer's own state machine says the job died,
	// so print_error is the fault, not a side note.
	c.handleReport(nil, newMockMessage([]byte(
		`{"print": {"gcode_state": "FAILED", "print_error": 83935249, "subtask_name": "cube.gcode"}}`)))

	s := c.Status()
	if s.State != "error" {
		t.Errorf("State = %q; want %q — a FAILED print must still trip the error state", s.State, "error")
	}
	if s.ErrorMsg == "" {
		t.Error("ErrorMsg is empty; the fault must still be explained")
	}
	if len(s.Advisories) != 0 {
		t.Errorf("Advisories = %+v; want none — the same condition is already the error banner", s.Advisories)
	}
}

// TestHandleReport_HealthyPrintKeepsPrintErrorAsAdvisory is the other half of
// the rule, and the reason the fix cannot simply stop emitting advisories: a
// non-zero print_error alongside a healthy gcode_state is a warning the
// printer raises *while printing normally*, and it must stay visible without
// hijacking the card into an error state.
func TestHandleReport_HealthyPrintKeepsPrintErrorAsAdvisory(t *testing.T) {
	c := newTestPrinterClient(nil)
	c.setStatus(printers.PrinterStatus{ID: "test-id", Name: "Test Printer", Type: "bambu", Online: true, State: "idle"})

	c.handleReport(nil, newMockMessage([]byte(
		`{"print": {"gcode_state": "RUNNING", "print_error": 83935249, "mc_percent": 40, "subtask_name": "cube.gcode"}}`)))

	s := c.Status()
	if s.State == "error" {
		t.Errorf("State = %q; a print_error during a RUNNING job must not trip error (that was the stuck-card bug)", s.State)
	}
	if len(s.Advisories) != 1 {
		t.Fatalf("Advisories = %+v; want the SD-card condition surfaced exactly once", s.Advisories)
	}
	if s.Advisories[0].Code != "0500-C011" {
		t.Errorf("Advisories[0].Code = %q; want 0500-C011", s.Advisories[0].Code)
	}
}
