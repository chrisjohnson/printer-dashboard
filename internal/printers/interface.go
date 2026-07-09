package printers

import "context"

// PrinterStatus represents the current state of a printer.
type PrinterStatus struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	Type             string           `json:"type"` // "bambu" or "snapmaker"
	Online           bool             `json:"online"`
	State            string           `json:"state"` // "idle", "printing", "paused", "error", "complete"
	Progress         float64          `json:"progress"`
	RemainingTime    int              `json:"remaining_time"` // seconds
	CurrentFile      string           `json:"current_file"`
	BedTemp          float64          `json:"bed_temp"`
	BedTargetTemp    float64          `json:"bed_target_temp"`
	NozzleTemp       float64          `json:"nozzle_temp"`
	NozzleTargetTemp float64          `json:"nozzle_target_temp"`
	ChamberTemp      float64          `json:"chamber_temp"`
	CurrentLayer     int              `json:"current_layer"`
	TotalLayers      int              `json:"total_layers"`
	ErrorMsg         string           `json:"error_msg,omitempty"`
	NozzleTemps      []NozzleTempEntry `json:"nozzle_temps,omitempty"`
}

// NozzleTempEntry captures one toolhead's temperature data.
type NozzleTempEntry struct {
	Index  int     `json:"index"`
	Actual float64 `json:"actual"`
	Target float64 `json:"target"`
}

// Printer defines the interface that all printer drivers must implement.
type Printer interface {
	// ID returns the unique identifier for this printer.
	ID() string

	// Name returns the human-readable name.
	Name() string

	// Connect establishes the connection to the printer and starts listening
	// for status updates. It blocks until the context is cancelled.
	Connect(ctx context.Context) error

	// Status returns the current cached status of the printer.
	Status() PrinterStatus

	// Pause pauses the current print job.
	Pause(ctx context.Context) error

	// Resume resumes a paused print job.
	Resume(ctx context.Context) error

	// Cancel stops and cancels the current print job.
	Cancel(ctx context.Context) error

	// SkipObject skips the current object being printed and moves to the next.
	SkipObject(ctx context.Context) error

	// CameraURL returns the URL(s) for the printer's camera feed(s).
	CameraURLs() []string
}
