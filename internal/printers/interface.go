package printers

import "context"

// CameraStream represents a camera or display stream for a printer.
type CameraStream struct {
	URL   string `json:"url"`
	Type  string `json:"type"`  // "internal", "external", or "touchscreen"
	Label string `json:"label"` // Human-readable label e.g. "Camera", "Front Camera", "Touchscreen"
}

// PrinterStatus represents the current state of a printer.
type PrinterStatus struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Type              string   `json:"type"` // "bambu" or "snapmaker"
	Online            bool     `json:"online"`
	State             string   `json:"state"` // "idle", "printing", "paused", "error", "complete"
	Progress          float64  `json:"progress"`
	RemainingTime     int      `json:"remaining_time"` // seconds
	CurrentFile       string   `json:"current_file"`
	BedTemp           *float64 `json:"bed_temp"`
	BedTargetTemp     *float64 `json:"bed_target_temp"`
	NozzleTemp        *float64 `json:"nozzle_temp"`
	NozzleTargetTemp  *float64 `json:"nozzle_target_temp"`
	ChamberTemp       *float64 `json:"chamber_temp"`
	ChamberTargetTemp *float64 `json:"chamber_target_temp"`
	// HasChamber is a capability flag: true only for printer models that
	// physically have a chamber heater. It is set unconditionally by the
	// driver at construction (and, for Bambu, re-derived whenever the model
	// becomes known/changes) and is NOT inferred from ChamberTemp — a nil
	// ChamberTemp only means "not reported this cycle", not "no hardware".
	HasChamber    bool              `json:"has_chamber"`
	CurrentLayer  int               `json:"current_layer"`
	TotalLayers   int               `json:"total_layers"`
	ErrorMsg      string            `json:"error_msg,omitempty"`
	NozzleTemps   []NozzleTempEntry `json:"nozzle_temps,omitempty"`
	CameraStreams []CameraStream    `json:"camera_streams,omitempty"`
	// HMSErrors holds decoded Bambu HMS (Health Management System) events of
	// fatal/serious severity. These independently trip State="error" — see
	// bambu/client.go's handleReport, which folds these into ErrorMsg when
	// print_error itself is 0/nil.
	HMSErrors []HMSEntry `json:"hms_errors,omitempty"`
	// HMSWarnings holds decoded Bambu HMS events of common/info/unknown
	// severity — non-blocking, surfaced in the UI but does not affect State.
	HMSWarnings []HMSEntry `json:"hms_warnings,omitempty"`
}

// HMSEntry is one decoded Bambu HMS (Health Management System) event.
type HMSEntry struct {
	Code     string `json:"code"`
	Module   string `json:"module"`
	Severity string `json:"severity"`
	// Message is a human-readable description of the HMS code, looked up
	// from a vendored code-to-message table (see bambu/hms_messages.go).
	// Empty if the code isn't found in the table — this is expected for
	// unrecognized/new codes, not an error condition.
	Message string `json:"message,omitempty"`
}

// NozzleTempEntry captures one toolhead's temperature data.
type NozzleTempEntry struct {
	Index  int      `json:"index"`
	Actual *float64 `json:"actual"`
	Target *float64 `json:"target"`
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

	// CameraStreams returns the available camera/display streams for this printer.
	CameraStreams() []CameraStream
}
