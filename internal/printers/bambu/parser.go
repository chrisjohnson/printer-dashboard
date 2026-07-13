package bambu

import (
	"encoding/json"
	"fmt"

	"github.com/chrisjohnson/printer-dashboard/internal/printers"
)

// report is the top-level JSON structure pushed by Bambu printers.
type report struct {
	Print  *printStatus  `json:"print"`
	Camera *cameraStatus `json:"camera,omitempty"`
	System *systemStatus `json:"system,omitempty"`
}

// infoData captures the H2S-style ambient/chamber temperature at print.info.temp.
type infoData struct {
	Temp *float64 `json:"temp"`
}

type printStatus struct {
	GcodeState          string             `json:"gcode_state"`
	GcodeFile           *string            `json:"gcode_file"`
	SubtaskName         *string            `json:"subtask_name"`
	McPercent           *int               `json:"mc_percent"`
	McRemainingTime     *int               `json:"mc_remaining_time"`
	BedTemper           *float64           `json:"bed_temper"`
	BedTarget           *float64           `json:"bed_target_temper"`
	NozzleTemper        *float64           `json:"nozzle_temper"`
	NozzleTarget        *float64           `json:"nozzle_target_temper"`
	ChamberTemper       *float64           `json:"chamber_temper"`
	ChamberTargetTemper *float64           `json:"chamber_target_temper"`
	Info                *infoData          `json:"info"`
	LayerNum            *int               `json:"layer_num"`
	TotalLayerNum       *int               `json:"total_layer_num"`
	WifiSignal          *string            `json:"wifi_signal"`
	HomeFlag            int                `json:"home_flag"`
	StgCur              *int               `json:"stg_cur"` // current stage
	StgTotal            *int               `json:"stg_total"`
	PrintError          *int               `json:"print_error"`
	Lifecycle           *string            `json:"lifecycle,omitempty"`
	HMS                 []hmsItem          `json:"hms,omitempty"`
	LightsReport        []lightReportEntry `json:"lights_report,omitempty"`
}

// lightReportEntry is a single entry from the lights_report array in a
// print report. The chamber light's state is reported as
// {"node": "chamber_light", "mode": "on"} or "off".
type lightReportEntry struct {
	Node string `json:"node"`
	Mode string `json:"mode"`
}

// hmsItem is one raw Bambu HMS (Health Management System) wire entry.
// Wire shape confirmed against greghesp/ha-bambulab (pybambu): a plain
// {"attr": <uint32>, "code": <uint32>} pair, no severity field on the wire —
// severity is derived from code, see decodeHMSSeverity.
type hmsItem struct {
	Attr uint32 `json:"attr"`
	Code uint32 `json:"code"`
}

// hmsModules maps the module byte ((attr>>24)&0xFF) to a human-readable
// module name. This table is deliberately non-exhaustive — it mirrors
// pybambu's known set, not the full space of possible module IDs. Unknown
// bytes fall back to "unknown", which is correct behavior, not a bug.
var hmsModules = map[uint8]string{
	0x05: "mainboard",
	0x0C: "xcam",
	0x07: "ams",
	0x08: "toolhead",
	0x03: "mc",
}

// hmsSeverities maps the severity nibble (code>>16) to a human-readable
// severity name, per pybambu's HMS severity levels.
var hmsSeverities = map[uint32]string{
	1: "fatal",
	2: "serious",
	3: "common",
	4: "info",
}

// decodeHMSCode formats a raw HMS (attr, code) pair into Bambu's
// human-readable HMS_XXXX-XXXX-XXXX-XXXX code string.
func decodeHMSCode(attr, code uint32) string {
	return fmt.Sprintf("HMS_%04X-%04X-%04X-%04X", attr>>16, attr&0xFFFF, code>>16, code&0xFFFF)
}

// decodeHMSModule extracts the module byte from attr and looks it up in
// hmsModules, defaulting to "unknown" for unrecognized modules.
func decodeHMSModule(attr uint32) string {
	b := uint8((attr >> 24) & 0xFF)
	if m, ok := hmsModules[b]; ok {
		return m
	}
	return "unknown"
}

// decodeHMSSeverity extracts the severity nibble from code and looks it up
// in hmsSeverities, defaulting to "unknown" for unrecognized severities.
func decodeHMSSeverity(code uint32) string {
	s := code >> 16
	if sev, ok := hmsSeverities[s]; ok {
		return sev
	}
	return "unknown"
}

// hmsEntrySummary formats one decoded HMS entry for display: "<message>
// (<code>)" when a human-readable message was found, or just "<code>" when
// it wasn't (message not in the vendored table — expected for uncovered
// codes, not an error).
func hmsEntrySummary(e printers.HMSEntry) string {
	if e.Message != "" {
		return fmt.Sprintf("%s (%s)", e.Message, e.Code)
	}
	return e.Code
}

// splitHMS decodes each raw HMS wire entry and buckets it into errors
// (severity fatal/serious) or warnings (everything else — common/info/
// unknown). A nil or empty items slice yields nil/empty output slices, no
// error. model is the printer model (e.g. "H2S", "P1S") used to prefer a
// model-specific human-readable message over the universal default; pass ""
// if the model isn't known yet.
func splitHMS(items []hmsItem, model string) (errors, warnings []printers.HMSEntry) {
	for _, item := range items {
		entry := printers.HMSEntry{
			Code:     decodeHMSCode(item.Attr, item.Code),
			Module:   decodeHMSModule(item.Attr),
			Severity: decodeHMSSeverity(item.Code),
			Message:  lookupHMSMessage(item.Attr, item.Code, model),
		}
		switch entry.Severity {
		case "fatal", "serious":
			errors = append(errors, entry)
		default:
			warnings = append(warnings, entry)
		}
	}
	return errors, warnings
}

type cameraStatus struct {
	IPCamURL     string `json:"ipcam_url"`
	TimelapseURL string `json:"timelapse_url"`
}

// systemStatus captures the "system" section of a Bambu report, which carries
// LED state and other system-level info.
type systemStatus struct {
	LEDCtrl *ledStatus `json:"ledctrl,omitempty"`
}

// ledStatus captures the state of an LED node (e.g. chamber_light).
type ledStatus struct {
	Node string `json:"node"`
	Mode string `json:"mode"`
}

// parseReport unmarshals a raw Bambu report JSON into the report struct.
func parseReport(data []byte) (*report, error) {
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// mapState converts Bambu's gcode_state strings into our canonical state.
func mapState(bambuState string) string {
	switch bambuState {
	case "RUNNING":
		return "printing"
	case "PAUSE":
		return "paused"
	case "SUCCESS", "FINISH":
		return "complete"
	case "FAILED":
		return "error"
	case "IDLE", "":
		return "idle"
	case "PREPARE":
		return "printing" // warming up, homing, etc.
	case "STANDBY":
		return "idle"
	default:
		return "unknown"
	}
}

// isErrorState returns true if the state indicates a problem.
func isErrorState(bambuState string) bool {
	return bambuState == "FAILED" || bambuState == "IDLE" // IDLE can be a state after a failure too
}

// isHealthyGcodeState reports whether a present gcode_state value maps to a
// non-error, non-FAILED canonical state — i.e. the printer's own state
// machine is reporting itself healthy this cycle. Used by handleReport's HMS
// staleness decay: a gcode_state of "unknown" (an unrecognized value) is
// deliberately NOT treated as healthy here, since we can't be confident the
// printer is actually fine.
func isHealthyGcodeState(bambuState string) bool {
	switch mapState(bambuState) {
	case "error", "unknown":
		return false
	default:
		return true
	}
}
