package bambu

import (
	"encoding/json"
	"fmt"
)

// command represents a command message sent to the printer.
type command struct {
	Print printCommand `json:"print"`
}

type printCommand struct {
	Command string `json:"command"`
	// Optional sequence ID for operations like skip
	SequenceID  string `json:"sequence_id,omitempty"`
	Param       string `json:"param,omitempty"`
	CTTVal      *int   `json:"ctt_val,omitempty"`
	TemperCheck *bool  `json:"temper_check,omitempty"`
}

// pauseCommand returns the JSON payload to pause a print.
func pauseCommand() []byte {
	return mustMarshal(command{Print: printCommand{Command: "pause"}})
}

// resumeCommand returns the JSON payload to resume a print.
func resumeCommand() []byte {
	return mustMarshal(command{Print: printCommand{Command: "resume"}})
}

// stopCommand returns the JSON payload to stop/cancel a print.
func stopCommand() []byte {
	return mustMarshal(command{Print: printCommand{Command: "stop"}})
}

// skipObjectCommand returns the JSON payload to skip the current object.
// Note: Bambu's skip object is handled via the project_file command with specific params.
// This sends a skip-object command which the printer firmware interprets.
func skipObjectCommand() []byte {
	return mustMarshal(command{Print: printCommand{
		Command: "project_file",
		Param:   "skip_object",
	}})
}

// setCTTCommand returns the JSON payload to set the chamber target temperature.
func setCTTCommand(temp int) []byte {
	return mustMarshal(command{Print: printCommand{
		Command:     "set_ctt",
		CTTVal:      &temp,
		TemperCheck: boolPtr(true),
	}})
}

// systemCommand represents a system-level command (e.g. LED control).
type systemCommand struct {
	System systemPayload `json:"system"`
}

type systemPayload struct {
	SequenceID   string `json:"sequence_id"`
	Command      string `json:"command"`
	LEDNode      string `json:"led_node,omitempty"`
	LEDMode      string `json:"led_mode,omitempty"`
	LEDOnTime    int    `json:"led_on_time"`
	LEDOffTime   int    `json:"led_off_time"`
	LoopTimes    int    `json:"loop_times"`
	IntervalTime int    `json:"interval_time"`
}

// setBedTempCommand returns the JSON payload to set the bed target temperature
// via G-code M140.
func setBedTempCommand(temp int) []byte {
	return mustMarshal(command{Print: printCommand{
		Command: "gcode_line",
		Param:   fmt.Sprintf("M140 S%d\n", temp),
	}})
}

// setNozzleTempCommand returns the JSON payload to set the nozzle target
// temperature via G-code M104.
func setNozzleTempCommand(temp int) []byte {
	return mustMarshal(command{Print: printCommand{
		Command: "gcode_line",
		Param:   fmt.Sprintf("M104 S%d\n", temp),
	}})
}

// setLightCommand returns the JSON payload to turn the chamber light on or off.
// The Bambu firmware requires all of these fields; without them the command
// is silently ignored.
func setLightCommand(on bool) []byte {
	mode := "off"
	if on {
		mode = "on"
	}
	return mustMarshal(systemCommand{System: systemPayload{
		SequenceID:   "0",
		Command:      "ledctrl",
		LEDNode:      "chamber_light",
		LEDMode:      mode,
		LEDOnTime:    500,
		LEDOffTime:   500,
		LoopTimes:    0,
		IntervalTime: 0,
	}})
}

func mustMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic("bambu: failed to marshal command: " + err.Error())
	}
	return data
}

func boolPtr(v bool) *bool { return &v }
