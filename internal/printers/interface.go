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
	HasChamber   bool `json:"has_chamber"`
	CurrentLayer int  `json:"current_layer"`
	TotalLayers  int  `json:"total_layers"`
	// ErrorMsg describes why State is "error". It is only meaningful while
	// State == "error" — a non-empty ErrorMsg alongside a healthy State means
	// the driver failed to clear it, not that something is wrong.
	ErrorMsg string `json:"error_msg,omitempty"`
	// Advisories holds printer-reported faults that arrive on a channel
	// outside the printer's own state machine, so they are deliberately NOT
	// errors: the printer keeps printing (and finishes successfully) while
	// asserting them. See bambu/client.go's handleReport for which Bambu
	// fields land here and why they must not trip State="error".
	//
	// Vendor-neutral by design (see K-073): the shape carries no Bambu MQTT
	// vocabulary, only a Source tag naming the driver-specific channel it
	// came from.
	Advisories    []Advisory        `json:"advisories,omitempty"`
	PositionZ     float64           `json:"position_z,omitempty"`
	NozzleTemps   []NozzleTempEntry `json:"nozzle_temps,omitempty"`
	CameraStreams []CameraStream    `json:"camera_streams,omitempty"`
	// HMSErrors holds decoded Bambu HMS (Health Management System) events of
	// fatal/serious severity. These independently trip State="error" — see
	// bambu/client.go's handleReport, which folds these into ErrorMsg when
	// nothing else produced an error message.
	HMSErrors []HMSEntry `json:"hms_errors,omitempty"`
	// HMSWarnings holds decoded Bambu HMS events of common/info/unknown
	// severity — non-blocking, surfaced in the UI but does not affect State.
	HMSWarnings []HMSEntry `json:"hms_warnings,omitempty"`
	// LightOn reports the chamber light state when known. nil means unknown
	// (not yet reported), true = on, false = off.
	LightOn *bool `json:"light_on,omitempty"`
	// Homed reports whether the printer's axes are homed, when known. nil
	// means unknown (not yet reported, or not supported by this driver —
	// e.g. Snapmaker has no homed-state query wiring today), true = homed,
	// false = not homed.
	Homed *bool `json:"homed,omitempty"`
	// AMSUnits holds per-AMS-unit data for Bambu printers with AMS (Automatic
	// Material System). Each unit contains up to 4 filament slots. Nil for
	// printers without AMS or when no AMS data has been received.
	AMSUnits []AMSUnit `json:"ams_units,omitempty"`
	// ActiveTrayID is the globally-indexed active tray ID for Bambu printers
	// with AMS. Encoded as (ams_id * 4) + tray_id. 254 = external spool,
	// 255 = none. -1 when unknown or not applicable.
	ActiveTrayID int `json:"active_tray_id"`
}

// AMSUnit represents one AMS (Automatic Material System) unit on a Bambu printer.
// An AMS unit contains up to 4 filament trays/slots. P1S AMS 1 does not report
// humidity or temperature; H2S AMS 2 Pro and X1 series do.
type AMSUnit struct {
	ID           int          `json:"id"`              // AMS unit index (0-3)
	Humidity     string       `json:"humidity"`        // Humidity index 0-5 (lower=drier); empty if not supported
	HumidityRaw  string       `json:"humidity_raw"`    // Raw humidity percentage; empty if not supported
	Temp         string       `json:"temp"`            // AMS internal temperature °C; empty if not supported
	Trays        []FilamentSlot `json:"trays"`         // Up to 4 filament slots
}

// FilamentSlot represents one tray/slot in an AMS unit.
type FilamentSlot struct {
	Index        int     `json:"index"`         // Tray slot index (0-3 within the AMS unit)
	Type         string  `json:"type"`          // Filament material type (e.g., "PLA", "PETG")
	Color        string  `json:"color"`         // RGBA hex color code (8 chars, e.g., "FF0000FF")
	InfoIdx      string  `json:"info_idx"`      // Bambu filament profile ID (e.g., "GFA00")
	NozzleTempMin int    `json:"nozzle_temp_min"` // Min nozzle temp °C
	NozzleTempMax int    `json:"nozzle_temp_max"` // Max nozzle temp °C
	RemainingMM  int     `json:"remain"`        // Remaining filament length mm; -1=unknown
	Weight       string  `json:"weight"`        // Spool weight grams
	TagUID       string  `json:"tag_uid"`       // RFID tag UID (zeros if no RFID)
	Loaded       bool    `json:"loaded"`        // true if tray has filament (state 2=loaded, 3=ready, 11=loaded+data)
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

// Advisory is one non-blocking fault the printer is asserting about itself
// rather than about the current job. Unlike HMSErrors it never trips
// State="error"; unlike HMSWarnings it did not come from a health-check
// array, so it has no severity and is not dismissible (the printer re-asserts
// it on every full status push until the underlying condition is fixed).
//
// Code is the printer's own human-readable code form (e.g. "0500-C011"),
// already normalized by the driver rather than a raw integer, so the UI can
// render it without knowing any vendor's encoding. Message is empty when the
// driver's catalog has no text for the code — expected for undocumented
// codes, and rendered as the bare code.
type Advisory struct {
	// Source names the driver-specific channel the advisory came from, for
	// debugging and for the UI to label the row (e.g. "print_error").
	Source  string `json:"source"`
	Code    string `json:"code"`
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

	// SetBedTemp sets the bed heater target temperature in °C.
	SetBedTemp(ctx context.Context, temp int) error

	// SetNozzleTemp sets the primary nozzle target temperature in °C.
	SetNozzleTemp(ctx context.Context, temp int) error

	// SetChamberTemp sets the chamber heater target temperature in °C.
	SetChamberTemp(ctx context.Context, temp int) error

	// SetLight turns the chamber light on or off.
	SetLight(ctx context.Context, on bool) error

	// HomeAll homes all axes (equivalent to G28 with no axis arguments).
	HomeAll(ctx context.Context) error

	// Jog moves the toolhead by the given relative deltas (in mm) on each
	// axis at the given feedrate (in mm/min). A zero delta on an axis means
	// no movement on that axis.
	Jog(ctx context.Context, x, y, z float64, speedMMPerMin int) error

	// SetHomed marks the printer as homed (or not). Used when firmware
	// doesn't expose homed_axes (e.g. Paxx/Snapmaker U1). nil clears the
	// cached homed state.
	SetHomed(homed *bool)
}
