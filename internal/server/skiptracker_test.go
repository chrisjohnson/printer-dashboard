package server

import "testing"

func TestSkipTracker_RecordAndGet(t *testing.T) {
	tr := NewSkipTracker()

	if got := tr.GetSkipped("printer-1"); len(got) != 0 {
		t.Fatalf("expected no skipped objects initially, got %d", len(got))
	}

	first := tr.RecordSkip("printer-1", "Part A")
	second := tr.RecordSkip("printer-1", "Part B")

	if first.ID == "" || second.ID == "" {
		t.Fatal("expected non-empty IDs")
	}
	if first.ID == second.ID {
		t.Fatalf("expected distinct IDs, got %q twice", first.ID)
	}

	got := tr.GetSkipped("printer-1")
	if len(got) != 2 {
		t.Fatalf("expected 2 skipped objects, got %d", len(got))
	}
	if got[0].Name != "Part A" || got[1].Name != "Part B" {
		t.Errorf("unexpected names: %+v", got)
	}
}

func TestSkipTracker_PerPrinterIsolation(t *testing.T) {
	tr := NewSkipTracker()
	tr.RecordSkip("printer-1", "A")
	tr.RecordSkip("printer-2", "B")

	if got := tr.GetSkipped("printer-1"); len(got) != 1 {
		t.Fatalf("printer-1: expected 1, got %d", len(got))
	}
	if got := tr.GetSkipped("printer-2"); len(got) != 1 {
		t.Fatalf("printer-2: expected 1, got %d", len(got))
	}
}

func TestSkipTracker_GetSkippedReturnsCopy(t *testing.T) {
	tr := NewSkipTracker()
	tr.RecordSkip("printer-1", "A")

	got := tr.GetSkipped("printer-1")
	got[0].Name = "mutated"

	got2 := tr.GetSkipped("printer-1")
	if got2[0].Name != "A" {
		t.Errorf("expected internal state unaffected by caller mutation, got %q", got2[0].Name)
	}
}

func TestSkipTracker_Clear(t *testing.T) {
	tr := NewSkipTracker()
	tr.RecordSkip("printer-1", "A")
	tr.RecordSkip("printer-2", "B")

	tr.Clear("printer-1")

	if got := tr.GetSkipped("printer-1"); len(got) != 0 {
		t.Errorf("printer-1: expected cleared, got %d entries", len(got))
	}
	if got := tr.GetSkipped("printer-2"); len(got) != 1 {
		t.Errorf("printer-2: expected untouched, got %d entries", len(got))
	}
}
