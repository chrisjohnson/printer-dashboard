package snapmaker

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// --- Types for GET /api/printer ---

type temperatureEntry struct {
	Actual float64 `json:"actual"`
	Target float64 `json:"target"`
	Offset int     `json:"offset"`
}

// temperatureReport captures all temperature entries from /api/printer.
// The JSON object has dynamic keys ("bed", "tool0", "tool1", ...) so we
// parse them into a map and provide helper methods for access.
type temperatureReport struct {
	Entries map[string]*temperatureEntry `json:"-"` // populated by UnmarshalJSON
}

// UnmarshalJSON implements custom JSON unmarshaling to capture all temperature
// entries dynamically, handling any number of toolheads.
func (t *temperatureReport) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	t.Entries = make(map[string]*temperatureEntry, len(raw))
	for key, val := range raw {
		// Explicitly handle JSON null — store nil entry.
		if string(val) == "null" {
			t.Entries[key] = nil
			continue
		}
		var entry temperatureEntry
		if err := json.Unmarshal(val, &entry); err != nil {
			continue // skip malformed entries
		}
		t.Entries[key] = &entry
	}
	return nil
}

// BedEntry returns the bed temperature entry, or nil if not present.
func (t *temperatureReport) BedEntry() *temperatureEntry {
	if t == nil {
		return nil
	}
	return t.Entries["bed"]
}

// toolEntry pairs a tool index with its temperature entry.
type toolEntry struct {
	Index int
	Entry *temperatureEntry
}

// ToolEntries returns all tool temperature entries sorted by tool index.
// Keys like "tool0", "tool1", "tool2" are parsed and sorted numerically.
// Handles any number of toolheads dynamically.
func (t *temperatureReport) ToolEntries() []toolEntry {
	if t == nil {
		return nil
	}
	var tools []toolEntry
	for key, entry := range t.Entries {
		if !strings.HasPrefix(key, "tool") {
			continue
		}
		idx := 0
		if _, err := fmt.Sscanf(key, "tool%d", &idx); err != nil {
			continue
		}
		tools = append(tools, toolEntry{Index: idx, Entry: entry})
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Index < tools[j].Index
	})
	return tools
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
