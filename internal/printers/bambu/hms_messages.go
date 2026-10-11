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

// deviceErrorKey builds the 8-hex-digit lookup key used by the table's
// "device_error" section from a raw print_error integer, e.g. 83935249
// (0x0500C011) -> "0500C011". The device_error section is keyed
// differently from device_hms: one 32-bit code instead of the HMS
// (attr, code) pair, so it gets its own key builder.
func deviceErrorKey(code uint32) string {
	return fmt.Sprintf("%08X", code)
}

// modelAliases maps Bambu's internal product codes to the friendly model
// names the vendored message table uses ("P1S", not "C12"). Config and the
// cloud API both hand us internal codes — this repo's own config.yaml
// declares the P1S as model "C12" and the H2S as "O1S" — while the table
// only ever names printers "P1S"/"H2S"/…, so without this step every
// model-specific message variant misses and only the universal default is
// ever reachable.
//
// Deliberately non-exhaustive, mirroring hmsModules: codes not listed here
// still resolve to a universal (empty model list) message when one exists,
// they just can't win a model-specific variant. Only codes whose friendly
// name actually appears in the vendored table are listed.
var modelAliases = map[string]string{
	"C11": "P1P",
	"C12": "P1S",
	"C13": "X1E",
	"N1S": "A1",
	"N1A": "A1MINI",
	"O1S": "H2S",
	"H3A": "H2D",
	"H3C": "H2C",
}

// normalizeModel canonicalizes a printer model string for comparison
// against the vendored table's model lists: uppercased, with spaces and
// hyphens stripped ("A1 mini" -> "A1MINI"), then resolved through
// modelAliases when it is an internal product code. Returns "" for an
// empty input so callers can pass an unknown model through harmlessly.
func normalizeModel(model string) string {
	if model == "" {
		return ""
	}
	m := strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToUpper(model))
	if alias, ok := modelAliases[m]; ok {
		return alias
	}
	return m
}

// pickMessage selects the message for model out of one table entry's
// message-to-model-lists map. A variant whose model list names model wins;
// otherwise the universal default (the entry whose model list is empty) is
// returned. That default is frequently the empty string — the table uses
// {"": []} as a placeholder for "this code has no model-independent text" —
// so "" here means "no usable text for this model", which callers render as
// the bare code. That is expected for uncovered codes, not an error.
func pickMessage(entries map[string][]string, model string) string {
	want := normalizeModel(model)
	var fallback string
	for msg, models := range entries {
		if len(models) == 0 {
			fallback = msg
			continue
		}
		if want == "" {
			continue
		}
		for _, m := range models {
			if normalizeModel(m) == want {
				return msg
			}
		}
	}
	return fallback
}

// lookupHMSMessage looks up a human-readable message for a raw HMS (attr,
// code) pair against the vendored code-to-message table's "device_hms"
// section, preferring a variant whose model list names the given model over
// the universal default (empty model list). Returns "" if the code isn't in
// the table at all, or the table has no default and no match for model —
// this is expected for codes not covered by the vendored dataset, not an
// error.
func lookupHMSMessage(attr, code uint32, model string) string {
	entries := loadHMSMessages()["device_hms"][hmsMessageKey(attr, code)]
	if len(entries) == 0 {
		return ""
	}
	return pickMessage(entries, model)
}

// lookupDeviceError looks up a human-readable message for a raw print_error
// integer against the vendored table's "device_error" section.
//
// print_error and HMS are two different code spaces that happen to share one
// vendored dataset: HMS codes arrive as an (attr, code) pair and live under
// "device_hms" keyed by 16 hex digits, while print_error arrives as a single
// 32-bit value and lives under "device_error" keyed by 8 hex digits. The
// same code can therefore be described in one section and absent from the
// other, so the two lookups are kept separate rather than tried in sequence.
//
// Returns "" for codes the table doesn't cover — notably the widely-reported
// undocumented codes (see bambulab/BambuStudio#4495 on 0500C011, which Bambu's
// own e.bambulab.com/query.php catalog also lacks).
func lookupDeviceError(code uint32, model string) string {
	entries := loadHMSMessages()["device_error"][deviceErrorKey(code)]
	if len(entries) == 0 {
		return ""
	}
	return pickMessage(entries, model)
}
