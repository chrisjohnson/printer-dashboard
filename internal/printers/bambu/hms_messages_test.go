package bambu

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Key-building tests
// ---------------------------------------------------------------------------

func TestHmsMessageKey(t *testing.T) {
	tests := []struct {
		name string
		attr uint32
		code uint32
		want string
	}{
		{
			// The user's real, confirmed-correct P1S fault: decodeHMSCode
			// formats this as "HMS_0300-1200-0002-0001". The lookup key is
			// the same digits with the "HMS_" prefix and dashes stripped.
			name: "confirmed real P1S fault code",
			attr: 0x03001200,
			code: 0x00020001,
			want: "0300120000020001",
		},
		{
			name: "pybambu oracle sample",
			attr: 201327360, // 0x0C000300
			code: 196615,    // 0x00030007
			want: "0C00030000030007",
		},
		{
			name: "zero",
			attr: 0,
			code: 0,
			want: "0000000000000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hmsMessageKey(tt.attr, tt.code)
			if got != tt.want {
				t.Errorf("hmsMessageKey(%#x, %#x) = %q; want %q", tt.attr, tt.code, got, tt.want)
			}

			// Cross-check against decodeHMSCode: stripping "HMS_" and "-"
			// from the dashed display string must produce the same key.
			dashed := decodeHMSCode(tt.attr, tt.code)
			stripped := ""
			for _, r := range dashed {
				if r == '-' {
					continue
				}
				stripped += string(r)
			}
			stripped = stripped[len("HMS_"):] // decodeHMSCode always prefixes "HMS_"
			if stripped != got {
				t.Errorf("hmsMessageKey(%#x, %#x) = %q; disagrees with stripped decodeHMSCode() = %q", tt.attr, tt.code, got, stripped)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// lookupHMSMessage tests against a small, inline fixture (NOT the real
// ~5,000-entry vendored file) — keeps these tests independent of upstream
// data changes.
// ---------------------------------------------------------------------------

func TestLookupHMSMessage_SmallFixture(t *testing.T) {
	// Swap the loader so lookupHMSMessage returns our fixture instead of
	// triggering the real sync.Once-based parser (which would overwrite the
	// fixture with the full embedded JSON).
	fixture := hmsMessageTable{
		"device_hms": {
			// Universal default only.
			"0300000000010000": {
				"A universal fault message.": {},
			},
			// Model-specific variants plus a universal default.
			"0300000000010001": {
				"A universal fallback message.":  {},
				"An H2S-specific fault message.": {"H2S"},
				"A P1S-specific fault message.":  {"P1S"},
			},
			// Model-specific only, no universal default.
			"0300000000010002": {
				"An H2S-only fault message, no default.": {"H2S"},
			},
		},
	}

	origLoad := loadHMSMessages
	loadHMSMessages = func() hmsMessageTable { return fixture }
	defer func() { loadHMSMessages = origLoad }()

	tests := []struct {
		name  string
		attr  uint32
		code  uint32
		model string
		want  string
	}{
		{
			name:  "exact match, universal default",
			attr:  0x03000000,
			code:  0x00010000,
			model: "P1S",
			want:  "A universal fault message.",
		},
		{
			name:  "model-specific preferred over universal default",
			attr:  0x03000000,
			code:  0x00010001,
			model: "H2S",
			want:  "An H2S-specific fault message.",
		},
		{
			name:  "different model gets its own variant, not H2S's",
			attr:  0x03000000,
			code:  0x00010001,
			model: "P1S",
			want:  "A P1S-specific fault message.",
		},
		{
			name:  "unrecognized model falls back to universal default",
			attr:  0x03000000,
			code:  0x00010001,
			model: "X1C",
			want:  "A universal fallback message.",
		},
		{
			name:  "model match is case-insensitive",
			attr:  0x03000000,
			code:  0x00010001,
			model: "h2s",
			want:  "An H2S-specific fault message.",
		},
		{
			name:  "model-only entry with no default: no match falls back to empty",
			attr:  0x03000000,
			code:  0x00010002,
			model: "P1S",
			want:  "",
		},
		{
			name:  "model-only entry with no default: matching model still resolves",
			attr:  0x03000000,
			code:  0x00010002,
			model: "H2S",
			want:  "An H2S-only fault message, no default.",
		},
		{
			name:  "code not found at all -> empty, no error",
			attr:  0xDEAD0000,
			code:  0xBEEF0000,
			model: "P1S",
			want:  "",
		},
		{
			name:  "empty model still resolves the universal default",
			attr:  0x03000000,
			code:  0x00010000,
			model: "",
			want:  "A universal fault message.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lookupHMSMessage(tt.attr, tt.code, tt.model)
			if got != tt.want {
				t.Errorf("lookupHMSMessage(%#x, %#x, %q) = %q; want %q", tt.attr, tt.code, tt.model, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Integration-style test against the REAL vendored hms_messages_en.json.
// ---------------------------------------------------------------------------

// TestLookupHMSMessage_RealVendoredFile_UserConfirmedCode is a regression
// guard tied to a real, confirmed-correct case: the user's live P1S fault
// that originally motivated K-072/K-074. decodeHMSCode formats attr=0x03001200,
// code=0x00020001 as "HMS_0300-1200-0002-0001", which the user manually
// looked up and confirmed decodes to "The front cover of the toolhead fell
// off." in the vendored table (universal default, empty model list).
func TestLookupHMSMessage_RealVendoredFile_UserConfirmedCode(t *testing.T) {
	const attr = 0x03001200
	const code = 0x00020001

	wantCode := "HMS_0300-1200-0002-0001"
	if gotCode := decodeHMSCode(attr, code); gotCode != wantCode {
		t.Fatalf("decodeHMSCode(%#x, %#x) = %q; want %q (test setup is wrong, not the lookup itself)", attr, code, gotCode, wantCode)
	}

	got := lookupHMSMessage(attr, code, "P1S")
	want := "The front cover of the toolhead fell off."
	if got != want {
		t.Errorf("lookupHMSMessage(%#x, %#x, %q) = %q; want %q (real vendored file, user-confirmed code)", attr, code, "P1S", got, want)
	}
}

// ---------------------------------------------------------------------------
// device_error (print_error) lookup tests
// ---------------------------------------------------------------------------

func TestDeviceErrorKey(t *testing.T) {
	tests := []struct {
		name string
		code uint32
		want string
	}{
		{
			// The live P1S value that motivated this lookup: the wire sends
			// the decimal 83935249, the table keys it as "0500C011".
			name: "P1S SD-card advisory",
			code: 83935249,
			want: "0500C011",
		},
		{name: "zero pads to 8 digits", code: 0, want: "00000000"},
		{name: "small value pads", code: 503, want: "000001F7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deviceErrorKey(tt.code); got != tt.want {
				t.Errorf("deviceErrorKey(%#x) = %q; want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestNormalizeModel(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		// The whole point: config.yaml declares these internal product
		// codes, while the vendored table only names friendly models.
		{name: "C12 aliases to P1S", model: "C12", want: "P1S"},
		{name: "O1S aliases to H2S", model: "O1S", want: "H2S"},
		{name: "alias lookup is case-insensitive", model: "c12", want: "P1S"},
		{name: "friendly name passes through", model: "P1S", want: "P1S"},
		{name: "lowercase friendly name uppercases", model: "p1s", want: "P1S"},
		{name: "spaces stripped", model: "A1 mini", want: "A1MINI"},
		{name: "hyphens stripped", model: "A1-MINI", want: "A1MINI"},
		{name: "unknown code passes through uppercased", model: "060", want: "060"},
		{name: "empty stays empty", model: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeModel(tt.model); got != tt.want {
				t.Errorf("normalizeModel(%q) = %q; want %q", tt.model, got, tt.want)
			}
		})
	}
}

// TestLookupDeviceError_InternalCodeResolvesModelSpecificMessage is the
// regression for the "bare unsearchable integer" symptom. The table entry for
// 0500C011 has NO model-independent text — its universal slot is the empty
// string placeholder — so the message is only reachable by resolving the
// printer's internal product code (C12) to the table's model name (P1S).
// Before normalizeModel existed, every P1S/H2S lookup landed on that empty
// placeholder and the UI had nothing to show.
func TestLookupDeviceError_InternalCodeResolvesModelSpecificMessage(t *testing.T) {
	fixture := hmsMessageTable{
		"device_error": {
			"0500C011": {
				"":                         {},
				"Model-specific advisory.": {"P1P", "P1S"},
			},
			"03004000": {
				"A universal advisory.": {},
			},
		},
	}

	origLoad := loadHMSMessages
	loadHMSMessages = func() hmsMessageTable { return fixture }
	defer func() { loadHMSMessages = origLoad }()

	tests := []struct {
		name  string
		code  uint32
		model string
		want  string
	}{
		{name: "internal code C12 reaches the P1S text", code: 0x0500C011, model: "C12", want: "Model-specific advisory."},
		{name: "friendly name reaches the same text", code: 0x0500C011, model: "P1S", want: "Model-specific advisory."},
		{name: "other model gets the empty placeholder, not P1S text", code: 0x0500C011, model: "H2S", want: ""},
		{name: "unknown model gets the placeholder", code: 0x0500C011, model: "X1C", want: ""},
		{name: "universal entry works for any model", code: 0x03004000, model: "C12", want: "A universal advisory."},
		{name: "uncoded value returns empty", code: 0xDEADBEEF, model: "C12", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lookupDeviceError(tt.code, tt.model); got != tt.want {
				t.Errorf("lookupDeviceError(%#x, %q) = %q; want %q", tt.code, tt.model, got, tt.want)
			}
		})
	}
}

// TestLookupDeviceError_RealVendoredFile_P1SSDCardCode pins the exact value
// from the user's live P1S against the real vendored dataset. 83935249 is
// documented nowhere in Bambu's own e.bambulab.com catalog (see
// bambulab/BambuStudio#4495), so this table is the only place the dashboard
// can learn what the number means — if the vendored file ever loses the
// entry, the code should fail here rather than silently regress to a bare
// integer in the UI.
func TestLookupDeviceError_RealVendoredFile_P1SSDCardCode(t *testing.T) {
	got := lookupDeviceError(83935249, "C12")
	if !strings.Contains(got, "SD card performance has degraded") {
		t.Errorf("lookupDeviceError(83935249, %q) = %q; want the SD-card advisory text", "C12", got)
	}

	// describePrintError must combine the message with Bambu's own dashed
	// code form so a user can look it up verbatim.
	desc := describePrintError(83935249, "C12")
	if !strings.HasPrefix(desc, "SD card performance has degraded") || !strings.HasSuffix(desc, "(0500-C011)") {
		t.Errorf("describePrintError(83935249, %q) = %q; want message + (0500-C011)", "C12", desc)
	}
}

func TestDescribePrintError_UndocumentedCode(t *testing.T) {
	// 503 is not in the table: keep the decimal the wire actually carries,
	// and add the dashed-hex form Bambu's catalog/community references use.
	got := describePrintError(503, "C12")
	want := "print_error=503 (0000-01F7)"
	if got != want {
		t.Errorf("describePrintError(503, %q) = %q; want %q", "C12", got, want)
	}
}

func TestPrintErrorCodeString(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{83935249, "0500-C011"},
		{503, "0000-01F7"},
		{0, "0000-0000"},
		{0x0502C011, "0502-C011"},
	}
	for _, tt := range tests {
		if got := printErrorCodeString(tt.code); got != tt.want {
			t.Errorf("printErrorCodeString(%d) = %q; want %q", tt.code, got, tt.want)
		}
	}
}
