package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// TestPrinterSortPriority pins the tier each state lands in.
//
// The regression this guards against: the switch used to be
// error→0 / idle→2 / everything-else→1, so "complete" was classed as *active*.
// A P1S that had finished hours earlier therefore tied with a U1 that was
// printing right now and beat it on the A-Z tiebreak — the finished job sat on
// top of the dashboard and the live one underneath it. Every state in the
// driver vocabulary is listed explicitly so a new one cannot silently fall into
// a tier by accident.
func TestPrinterSortPriority(t *testing.T) {
	tests := []struct {
		name string
		st   printers.PrinterStatus
		want int
		why  string
	}{
		{
			"failed print",
			printers.PrinterStatus{State: "error", PrintFailed: true},
			0,
			"a job died: filament in the nozzle, something waiting on a human",
		},
		{
			"standing fault on an idle printer",
			printers.PrinterStatus{State: "error", PrintFailed: false},
			2,
			"a degraded SD card has been true for weeks and must not outrank a live print",
		},
		{"printing", printers.PrinterStatus{State: "printing"}, 1, "a live job is the reason the dashboard is open"},
		{"paused", printers.PrinterStatus{State: "paused"}, 1, "a paused job still needs a decision"},
		{"complete", printers.PrinterStatus{State: "complete"}, 3, "finished is finished — it must not outrank a running job"},
		{"idle", printers.PrinterStatus{State: "idle"}, 3, "nothing running"},
		{"unknown", printers.PrinterStatus{State: "unknown"}, 3, "nothing known is not the same as something running"},
		{"blank", printers.PrinterStatus{State: ""}, 3, "blank state is not an active print"},
		{"unrecognised", printers.PrinterStatus{State: "bogus_future_state"}, 3, "an unrecognised state must not be promoted to active"},
		{
			"locked out",
			printers.PrinterStatus{State: "error", AuthInvalid: true},
			3,
			"an expired sign-in is the dashboard being blind, not the printer being broken",
		},
		{
			"locked out beats nothing but ties idle",
			printers.PrinterStatus{State: "error", AuthInvalid: true, PrintFailed: true},
			3,
			"AuthInvalid wins: we cannot see the printer, so its reported failure is not ours to act on",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := printerSortPriority(tc.st); got != tc.want {
				t.Errorf("printerSortPriority(%+v) = %d, want %d (%s)", tc.st, got, tc.want, tc.why)
			}
		})
	}
}

// TestPrinterSortPriority_ActiveBeatsComplete is the exact shape of the
// reported bug, stated as a direct comparison rather than a tier number.
func TestPrinterSortPriority_ActiveBeatsComplete(t *testing.T) {
	if printerSortPriority(printers.PrinterStatus{State: "complete"}) <=
		printerSortPriority(printers.PrinterStatus{State: "printing"}) {
		t.Error("a completed print sorts at or above a printing one; a finished job must never outrank a live one")
	}
}

// TestPrinterSortPriority_LockedOutNeverOutranksRunning guards the specific
// regression the auth work could otherwise introduce: a dead token shows up as
// State="error", and if that earned tier 0 then two printers the dashboard
// cannot see at all would sit above a print running right now — permanently,
// because only a human can clear it.
func TestPrinterSortPriority_LockedOutNeverOutranksRunning(t *testing.T) {
	locked := printers.PrinterStatus{State: "error", AuthInvalid: true}
	running := printers.PrinterStatus{State: "printing"}
	if printerSortPriority(locked) <= printerSortPriority(running) {
		t.Error("a printer the dashboard is locked out of outranks one that is printing")
	}
}

// newSortTestServer builds a Server whose printer list is the given statuses,
// bypassing config/driver construction so the ordering logic is what is under
// test and nothing else.
func newSortTestServer(statuses ...printers.PrinterStatus) *Server {
	s := &Server{
		cfg:      &config.Config{Listen: ":0"},
		printers: make(map[string]printers.Printer),
		mux:      http.NewServeMux(),
	}
	for _, st := range statuses {
		s.printers[st.ID] = &MockPrinter{id: st.ID, name: st.Name, stat: st}
	}
	return s
}

// listPrinterNames calls the real endpoint and returns the names in the order
// the API actually put them.
func listPrinterNames(t *testing.T, s *Server) []string {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/printers", nil)
	rec := httptest.NewRecorder()
	s.handleListPrinters(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Printers []printers.PrinterStatus `json:"printers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := make([]string, 0, len(body.Printers))
	for _, p := range body.Printers {
		names = append(names, p.Name)
	}
	return names
}

// TestHandleListPrinters_ActivePrintsComeFirst reproduces the reported layout:
// a completed P1S alphabetically ahead of a printing U1, plus an idle H2S.
// The running printer must be first.
func TestHandleListPrinters_ActivePrintsComeFirst(t *testing.T) {
	s := newSortTestServer(
		printers.PrinterStatus{ID: "p1s", Name: "P1S-001", State: "complete"},
		printers.PrinterStatus{ID: "u1", Name: "U1", State: "printing"},
		printers.PrinterStatus{ID: "h2s", Name: "H2S-001", State: "idle"},
	)

	got := listPrinterNames(t, s)
	want := []string{"U1", "H2S-001", "P1S-001"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v (printing first, then idle/complete A-Z)", got, want)
		}
	}
}

// TestHandleListPrinters_FailedPrintBeatsActiveBeatsStandingFault pins the full
// four-tier contract in one pass, including that A-Z still orders within a tier.
func TestHandleListPrinters_FailedPrintBeatsActiveBeatsStandingFault(t *testing.T) {
	s := newSortTestServer(
		printers.PrinterStatus{ID: "c", Name: "Bravo", State: "complete"},
		printers.PrinterStatus{ID: "a", Name: "Alpha", State: "error", PrintFailed: true},
		printers.PrinterStatus{ID: "d", Name: "Delta", State: "idle"},
		printers.PrinterStatus{ID: "b", Name: "Charlie", State: "printing"},
		printers.PrinterStatus{ID: "e", Name: "Echo", State: "paused"},
		printers.PrinterStatus{ID: "f", Name: "Foxtrot", State: "error"}, // idle + maintenance fault
	)

	got := listPrinterNames(t, s)
	// Alpha's job died, so it leads. Charlie and Echo are running. Foxtrot's
	// fault is chronic, so it waits behind the live work.
	want := []string{"Alpha", "Charlie", "Echo", "Foxtrot", "Bravo", "Delta"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestHandleListPrinters_LockedOutSitsBelowRunning is the end-to-end shape of
// the real outage: two Bambu printers whose cloud sign-in died (so they read
// State="error" with nothing behind it) plus one printer actually printing.
// The live print must be the first card on the dashboard.
func TestHandleListPrinters_LockedOutSitsBelowRunning(t *testing.T) {
	s := newSortTestServer(
		printers.PrinterStatus{ID: "b1", Name: "AAA-Bambu", State: "error", AuthInvalid: true},
		printers.PrinterStatus{ID: "b2", Name: "AAB-Bambu", State: "error", AuthInvalid: true},
		printers.PrinterStatus{ID: "u1", Name: "ZZZ-Printing", State: "printing"},
	)

	got := listPrinterNames(t, s)
	want := []string{"ZZZ-Printing", "AAA-Bambu", "AAB-Bambu"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v (a live print must not be pushed down by printers we cannot see)", got, want)
		}
	}
}

// TestHandleListPrinters_LiveDataBeatsFrozenWithinTier covers the tiebreak
// inside a tier: a printer that went blind mid-print keeps State="printing",
// but the one still reporting real progress is the more urgent card.
func TestHandleListPrinters_LiveDataBeatsFrozenWithinTier(t *testing.T) {
	s := newSortTestServer(
		printers.PrinterStatus{ID: "gone", Name: "AAA-Gone", State: "printing", Stale: true},
		printers.PrinterStatus{ID: "live", Name: "ZZZ-Live", State: "printing"},
	)

	got := listPrinterNames(t, s)
	want := []string{"ZZZ-Live", "AAA-Gone"}

	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("order = %v, want %v (a live reading outranks a frozen one in the same tier)", got, want)
	}
}
