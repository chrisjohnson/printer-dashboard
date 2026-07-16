package server

import "sync"

// HMSDismissTracker records HMS (Health Management System) codes the user
// has dismissed per printer. It is in-memory only and not persisted across
// server restarts — a dismissal only needs to suppress re-notification while
// the underlying condition remains active, so this is acceptable.
type HMSDismissTracker struct {
	mu        sync.RWMutex
	dismissed map[string]map[string]struct{}
}

// NewHMSDismissTracker creates a new, empty HMSDismissTracker.
func NewHMSDismissTracker() *HMSDismissTracker {
	return &HMSDismissTracker{dismissed: make(map[string]map[string]struct{})}
}

// Dismiss marks code as dismissed for printerID.
func (t *HMSDismissTracker) Dismiss(printerID, code string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	codes := t.dismissed[printerID]
	if codes == nil {
		codes = make(map[string]struct{})
		t.dismissed[printerID] = codes
	}
	codes[code] = struct{}{}
}

// IsDismissed reports whether code has been dismissed for printerID.
func (t *HMSDismissTracker) IsDismissed(printerID, code string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.dismissed[printerID][code]
	return ok
}

// Reconcile clears dismissals for printerID that are no longer present in
// activeCodes. This ensures a code that clears and later re-fires is not
// permanently suppressed — dismissal only applies to the occurrence the user
// actually saw.
func (t *HMSDismissTracker) Reconcile(printerID string, activeCodes []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	codes := t.dismissed[printerID]
	if len(codes) == 0 {
		return
	}
	active := make(map[string]struct{}, len(activeCodes))
	for _, c := range activeCodes {
		active[c] = struct{}{}
	}
	for c := range codes {
		if _, ok := active[c]; !ok {
			delete(codes, c)
		}
	}
	if len(codes) == 0 {
		delete(t.dismissed, printerID)
	}
}
