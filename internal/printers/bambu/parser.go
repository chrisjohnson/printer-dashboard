package bambu

import "encoding/json"

// report is the top-level JSON structure pushed by Bambu printers.
type report struct {
	Print  *printStatus  `json:"print"`
	Camera *cameraStatus `json:"camera,omitempty"`
}

type printStatus struct {
	GcodeState       string  `json:"gcode_state"`
	GcodeFile        string  `json:"gcode_file"`
	McPercent        *int    `json:"mc_percent"`
	McRemainingTime  *int    `json:"mc_remaining_time"`
	BedTemper        float64 `json:"bed_temper"`
	BedTarget        float64 `json:"bed_target"`
	NozzleTemper     float64 `json:"nozzle_temper"`
	NozzleTarget     float64 `json:"nozzle_target"`
	LayerNum         *int    `json:"layer_num"`
	TotalLayerNum    *int    `json:"total_layer_num"`
	WifiSignal       *int    `json:"wifi_signal"`
	HomeFlag         int     `json:"home_flag"`
	StgCur           *int    `json:"stg_cur"` // current stage
	StgTotal         *int    `json:"stg_total"`
	Lifecycle         *string `json:"lifecycle,omitempty"`
}

type cameraStatus struct {
	IPCamURL string `json:"ipcam_url"`
	TimelapseURL string `json:"timelapse_url"`
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
	case "IDLE":
		return "idle"
	case "PREPARE":
		return "printing" // warming up, homing, etc.
	default:
		return "unknown"
	}
}

// isErrorState returns true if the state indicates a problem.
func isErrorState(bambuState string) bool {
	return bambuState == "FAILED" || bambuState == "IDLE" // IDLE can be a state after a failure too
}
