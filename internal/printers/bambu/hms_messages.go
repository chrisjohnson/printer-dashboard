package bambu

// hms_messages_en.json is vendored from greghesp/ha-bambulab (Home Assistant
// Bambu Lab integration), path:
//   custom_components/bambu_lab/pybambu/hms_error_text/hms_en.json.gz
// Source repo: https://github.com/greghesp/ha-bambulab
// Fetched: 2026-07-12
// NOTE ON LICENSING: as of the fetch date, the upstream repository has no
// LICENSE file (GitHub reports `license: None`). This was a deliberate,
// explicitly user-authorized decision to vendor the full dataset anyway for
// this personal, non-commercial project, having been presented with the
// licensing gray area — not a default or blanket policy for vendoring
// unlicensed code elsewhere. See .agent/STATE.md (K-074) for the full
// authorization record.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
)

//go:embed hms_messages_en.json
var hmsMessagesRaw []byte

// hmsMessageTable mirrors the vendored JSON shape directly:
// top-level key ("device_hms" or "device_error") -> 16-hex-digit code
// (no dashes, no "HMS_" prefix) -> {message text -> [applicable model
// strings]}. An empty model list means the message is a universal default
// (applies regardless of model); a non-empty list means the message is
// specific to those models.
type hmsMessageTable map[string]map[string]map[string][]string

var (
	hmsMessagesOnce  sync.Once
	hmsMessagesTable hmsMessageTable
)

// loadHMSMessages is the function that lookupHMSMessage calls to obtain the
// parsed table. It is a package-level variable so that tests can swap it to
// return a fixture, avoiding the sync.Once one-shot that would otherwise
// overwrite a test fixture with real embedded data.
var loadHMSMessages = loadHMSMessagesImpl

// loadHMSMessagesImpl parses the embedded HMS message table once, lazily.
// Parse errors are logged (this table is a "best effort" human-readability
// aid, not load-bearing for core functionality) and result in an empty table
// rather than a panic.
func loadHMSMessagesImpl() hmsMessageTable {
	hmsMessagesOnce.Do(func() {
		var t hmsMessageTable
		if err := json.Unmarshal(hmsMessagesRaw, &t); err != nil {
			log.Printf("bambu: failed to parse embedded HMS message table: %v", err)
			t = hmsMessageTable{}
		}
		hmsMessagesTable = t
	})
	return hmsMessagesTable
}

// hmsMessageKey builds the 16-hex-digit lookup key used by the vendored
// message table, from the same (attr, code) pair decodeHMSCode formats into
// the dashed "HMS_XXXX-XXXX-XXXX-XXXX" display string — this is that same
// string with the "HMS_" prefix and dashes stripped, e.g. attr=0x03001200,
// code=0x00020001 -> "0300120000020001".
func hmsMessageKey(attr, code uint32) string {
	return fmt.Sprintf("%04X%04X%04X%04X", attr>>16, attr&0xFFFF, code>>16, code&0xFFFF)
}

// lookupHMSMessage looks up a human-readable message for a raw HMS (attr,
// code) pair against the vendored code-to-message table, preferring a
// variant whose model list names the given model (case-insensitive) over
// the universal default (empty model list). Returns "" if the code isn't in
// the table at all, or the table has no default and no match for model —
// this is expected for codes not covered by the vendored dataset, not an
// error.
func lookupHMSMessage(attr, code uint32, model string) string {
	key := hmsMessageKey(attr, code)
	table := loadHMSMessages()

	entries := table["device_hms"][key]
	if len(entries) == 0 {
		return ""
	}

	var fallback string
	for msg, models := range entries {
		if len(models) == 0 {
			fallback = msg
			continue
		}
		for _, m := range models {
			if strings.EqualFold(m, model) {
				return msg
			}
		}
	}
	return fallback
}
