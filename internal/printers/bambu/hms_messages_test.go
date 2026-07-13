package bambu

import "testing"

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
