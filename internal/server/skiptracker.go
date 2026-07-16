package server

import (
	"strconv"
	"sync"
	"time"
)

// SkippedObject records one object the user chose to skip during a print.
type SkippedObject struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SkippedAt time.Time `json:"skipped_at"`
}

// SkipTracker records skipped-object history per printer for the current
// print session. It is in-memory only and not persisted across server
// restarts — a print session is inherently ephemeral, so this is acceptable.
type SkipTracker struct {
	mu      sync.RWMutex
	objects map[string][]SkippedObject
	nextID  uint64
}

// NewSkipTracker creates a new, empty SkipTracker.
func NewSkipTracker() *SkipTracker {
	return &SkipTracker{objects: make(map[string][]SkippedObject)}
}

// RecordSkip appends a new skipped-object entry for printerID and returns it.
// name is best-effort display text and may be empty.
func (t *SkipTracker) RecordSkip(printerID, name string) SkippedObject {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	obj := SkippedObject{
		ID:        strconv.FormatUint(t.nextID, 10),
		Name:      name,
		SkippedAt: time.Now(),
	}
	t.objects[printerID] = append(t.objects[printerID], obj)
	return obj
}

// GetSkipped returns a copy of the skipped-object list for printerID (empty
// if none have been skipped this session).
func (t *SkipTracker) GetSkipped(printerID string) []SkippedObject {
	t.mu.RLock()
	defer t.mu.RUnlock()
	objs := t.objects[printerID]
	out := make([]SkippedObject, len(objs))
	copy(out, objs)
	return out
}

// Clear resets the skipped-object list for printerID, e.g. when the print
// session ends (state transitions away from "printing").
func (t *SkipTracker) Clear(printerID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.objects, printerID)
}
