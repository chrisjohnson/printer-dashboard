package bambu

import "encoding/json"

// command represents a command message sent to the printer.
type command struct {
	Print printCommand `json:"print"`
}

type printCommand struct {
	Command string `json:"command"`
	// Optional sequence ID for operations like skip
	SequenceID string `json:"sequence_id,omitempty"`
	Param      string `json:"param,omitempty"`
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

func mustMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic("bambu: failed to marshal command: " + err.Error())
	}
	return data
}
