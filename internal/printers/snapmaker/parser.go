package snapmaker

import "encoding/json"

// --- Types for GET /api/printer ---

type temperatureEntry struct {
	Actual float64 `json:"actual"`
	Target float64 `json:"target"`
	Offset int     `json:"offset"`
}

type temperatureReport struct {
	Bed   *temperatureEntry `json:"bed,omitempty"`
	Tool0 *temperatureEntry `json:"tool0,omitempty"`
	Tool1 *temperatureEntry `json:"tool1,omitempty"`
	Tool2 *temperatureEntry `json:"tool2,omitempty"`
	Tool3 *temperatureEntry `json:"tool3,omitempty"`
}

type stateFlags struct {
	Operational   bool `json:"operational"`
	Paused        bool `json:"paused"`
	Printing      bool `json:"printing"`
	Cancelling    bool `json:"cancelling"`
	Pausing       bool `json:"pausing"`
	Error         bool `json:"error"`
	Ready         bool `json:"ready"`
	ClosedOrError bool `json:"closedOrError"`
}

type stateReport struct {
	Text  string      `json:"text"`
	Flags *stateFlags `json:"flags,omitempty"`
}

type apiPrinterResponse struct {
	Temperature *temperatureReport `json:"temperature,omitempty"`
	State       *stateReport       `json:"state,omitempty"`
}

// --- Types for GET /printer/objects/query ---

type printStatsInfo struct {
	CurrentLayer int `json:"current_layer"`
	TotalLayer   int `json:"total_layer"`
}

type printStatsReport struct {
	Filename      string          `json:"filename"`
	PrintDuration float64         `json:"print_duration"`
	State         string          `json:"state"`
	Message       string          `json:"message"`
	Info          *printStatsInfo `json:"info,omitempty"`
}

type virtualSDCardReport struct {
	Progress float64 `json:"progress"`
}

type queryStatus struct {
	PrintStats    *printStatsReport    `json:"print_stats,omitempty"`
	VirtualSDCard *virtualSDCardReport `json:"virtual_sdcard,omitempty"`
}

type moonrakerQueryResponse struct {
	Result struct {
		Status *queryStatus `json:"status"`
	} `json:"result"`
}

// --- Functions ---

// parseAPIReport parses a response from GET /api/printer.
func parseAPIReport(data []byte) (*apiPrinterResponse, error) {
	var r apiPrinterResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// parseQueryReport parses a response from GET /printer/objects/query.
func parseQueryReport(data []byte) (*moonrakerQueryResponse, error) {
	var r moonrakerQueryResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// mapMoonrakerState converts Moonraker state.text + flags into canonical state.
func mapMoonrakerState(stateText string, flags *stateFlags) string {
	// Priority order: flags first (more reliable), then text.
	if flags != nil {
		switch {
		case flags.Printing:
			return "printing"
		case flags.Paused:
			return "paused"
		case flags.Error:
			return "error"
		}
	}
	switch stateText {
	case "Operational":
		return "idle"
	case "Printing":
		return "printing"
	case "Paused":
		return "paused"
	case "Error":
		return "error"
	case "Complete", "Cancelled":
		return "complete"
	default:
		return "unknown"
	}
}
