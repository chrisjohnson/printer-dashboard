package snapmaker

import "encoding/json"

// paxxStatus is the JSON report structure pushed by Snapmaker U1 printers
// running Paxx firmware. Fields are sent via WebSocket push or returned from
// the REST API (GET /api/printer, GET /api/print).
//
// Pointer fields (BedTemp, NozzleTemp, etc.) are optional — they may be
// absent in partial reports. nil means the field was not present in the JSON.
type paxxStatus struct {
	Status  string  `json:"status"`            // "idle", "running", "paused", etc.
	Progress float64 `json:"progress,omitempty"` // 0.0 to 1.0
	File     string  `json:"file,omitempty"`    // current print file

	// Temperatures
	BedTemp      *float64 `json:"bed_temp,omitempty"`
	BedTarget    *float64 `json:"bed_target_temp,omitempty"`
	NozzleTemp   *float64 `json:"nozzle_temp,omitempty"`
	NozzleTarget *float64 `json:"nozzle_target_temp,omitempty"`
	ChamberTemp  *float64 `json:"chamber_temp,omitempty"`

	// Print job
	PrintDuration *int `json:"print_duration,omitempty"` // seconds elapsed
	RemainingTime *int `json:"remaining_time,omitempty"` // seconds remaining
	CurrentLayer  *int `json:"current_layer,omitempty"`
	TotalLayers   *int `json:"total_layers,omitempty"`

	// Errors
	Error string `json:"error,omitempty"` // error message if status is "error"
}

// parseReport unmarshals a raw Paxx JSON report into a paxxStatus struct.
func parseReport(data []byte) (*paxxStatus, error) {
	var s paxxStatus
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// mapState converts a Paxx status string into the canonical printer state
// used by PrinterStatus.State.
func mapState(paxxState string) string {
	switch paxxState {
	case "running", "printing":
		return "printing"
	case "paused":
		return "paused"
	case "error", "failed":
		return "error"
	case "complete", "finished", "success":
		return "complete"
	case "idle", "":
		return "idle"
	default:
		return "unknown"
	}
}
