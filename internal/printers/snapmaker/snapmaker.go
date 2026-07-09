// Package snapmaker provides a stub implementation of printers.Printer for
// Snapmaker U1 (Paxx firmware) printers.
//
// This is a placeholder that shows a printer tile on the dashboard but does
// not yet implement real connectivity (REST API + WebSocket). The Connect
// method blocks until the context is cancelled so the goroutine lifecycle
// matches what the server expects. Control methods (Pause, Resume, Cancel,
// SkipObject) all return a "not yet implemented" error.
//
// Real implementation will be added in a follow-up session.
package snapmaker

import (
	"context"
	"fmt"
	"sync"

	"github.com/chrisjohnson/printer-dashboard/internal/config"
	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// Printer implements the printers.Printer interface as a stub for Snapmaker
// U1 printers running Paxx firmware.
type Printer struct {
	cfg config.PrinterDef
	mu  sync.Mutex

	status printers.PrinterStatus
}

// New creates a new stub Snapmaker printer from the given config.
func New(cfg config.PrinterDef) *Printer {
	return &Printer{
		cfg: cfg,
		status: printers.PrinterStatus{
			ID:   cfg.ID,
			Name: cfg.Name,
			Type: "snapmaker",
		},
	}
}

// ID returns the printer's unique identifier.
func (p *Printer) ID() string { return p.cfg.ID }

// Name returns the printer's human-readable name.
func (p *Printer) Name() string { return p.cfg.Name }

// Connect blocks until the context is cancelled. No actual connection is
// established — this is a stub for the real Snapmaker Paxx client.
func (p *Printer) Connect(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// Status returns the current cached status. The stub always reports offline
// with an "idle" state and a message indicating Snapmaker support is not
// yet implemented.
func (p *Printer) Status() printers.PrinterStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Pause returns an error indicating Snapmaker control is not yet implemented.
func (p *Printer) Pause(_ context.Context) error {
	return fmt.Errorf("snapmaker %s: pause not yet implemented", p.cfg.ID)
}

// Resume returns an error indicating Snapmaker control is not yet implemented.
func (p *Printer) Resume(_ context.Context) error {
	return fmt.Errorf("snapmaker %s: resume not yet implemented", p.cfg.ID)
}

// Cancel returns an error indicating Snapmaker control is not yet implemented.
func (p *Printer) Cancel(_ context.Context) error {
	return fmt.Errorf("snapmaker %s: cancel not yet implemented", p.cfg.ID)
}

// SkipObject returns an error indicating Snapmaker control is not yet implemented.
func (p *Printer) SkipObject(_ context.Context) error {
	return fmt.Errorf("snapmaker %s: skip not yet implemented", p.cfg.ID)
}

// CameraURLs returns nil. Camera support will be added with the real client.
func (p *Printer) CameraURLs() []string { return nil }
