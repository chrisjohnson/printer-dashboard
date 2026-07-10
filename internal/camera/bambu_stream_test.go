package camera

import (
	"encoding/binary"
	"testing"
)

func TestBuildAuthPacket_Size(t *testing.T) {
	pkt := BuildAuthPacket("1234")
	if len(pkt) != 80 {
		t.Errorf("BuildAuthPacket() returned %d bytes; want 80", len(pkt))
	}
}

func TestBuildAuthPacket_HeaderFields(t *testing.T) {
	pkt := BuildAuthPacket("1234")

	// Bytes 0-3: uint32 LE 0x40 (64)
	if got := binary.LittleEndian.Uint32(pkt[0:4]); got != 0x40 {
		t.Errorf("header[0:4] = 0x%x; want 0x40", got)
	}

	// Bytes 4-7: uint32 LE 0x3000 (12288)
	if got := binary.LittleEndian.Uint32(pkt[4:8]); got != 0x3000 {
		t.Errorf("header[4:8] = 0x%x; want 0x3000", got)
	}

	// Bytes 8-11: uint32 LE 0
	if got := binary.LittleEndian.Uint32(pkt[8:12]); got != 0 {
		t.Errorf("header[8:12] = 0x%x; want 0", got)
	}

	// Bytes 12-15: uint32 LE 0
	if got := binary.LittleEndian.Uint32(pkt[12:16]); got != 0 {
		t.Errorf("header[12:16] = 0x%x; want 0", got)
	}
}

func TestBuildAuthPacket_Username(t *testing.T) {
	pkt := BuildAuthPacket("1234")

	// Bytes 16-19: username "bblp"
	if string(pkt[16:20]) != "bblp" {
		t.Errorf("username = %q; want %q", string(pkt[16:20]), "bblp")
	}

	// Bytes 20-47: null padding to fill 32 bytes total.
	for i := 20; i < 48; i++ {
		if pkt[i] != 0 {
			t.Errorf("username padding byte at offset %d = 0x%x; want 0x00", i, pkt[i])
		}
	}
}

func TestBuildAuthPacket_AccessCode(t *testing.T) {
	accessCode := "ABCD1234"
	pkt := BuildAuthPacket(accessCode)

	// Bytes 48-79: access code padded with nulls to 32 chars
	gotCode := string(pkt[48:56])
	if gotCode != accessCode {
		t.Errorf("access code = %q; want %q", gotCode, accessCode)
	}

	// Verify trailing nulls.
	for i := 56; i < 80; i++ {
		if pkt[i] != 0 {
			t.Errorf("access code padding byte at offset %d = 0x%x; want 0x00", i, pkt[i])
		}
	}
}

func TestBuildAuthPacket_AccessCodeTruncation(t *testing.T) {
	// Access code longer than 32 chars should be truncated.
	longCode := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789" // 36 chars
	pkt := BuildAuthPacket(longCode)

	gotCode := string(pkt[48:80])
	if len(gotCode) != 32 {
		t.Errorf("access code field = %d bytes; want 32", len(gotCode))
	}

	// First 32 chars should match.
	if gotCode != longCode[:32] {
		t.Errorf("access code = %q; want %q", gotCode, longCode[:32])
	}
}

func TestBuildAuthPacket_EmptyAccessCode(t *testing.T) {
	pkt := BuildAuthPacket("")

	// All 32 bytes should be nulls.
	for i := 48; i < 80; i++ {
		if pkt[i] != 0 {
			t.Errorf("empty access code byte at offset %d = 0x%x; want 0x00", i, pkt[i])
		}
	}
}

func TestBuildAuthPacket_Deterministic(t *testing.T) {
	pkt1 := BuildAuthPacket("test-code")
	pkt2 := BuildAuthPacket("test-code")

	for i := 0; i < 80; i++ {
		if pkt1[i] != pkt2[i] {
			t.Errorf("byte %d differs: 0x%x vs 0x%x", i, pkt1[i], pkt2[i])
		}
	}
}

func TestBuildAuthPacket_AllFields(t *testing.T) {
	// Verify the entire packet structure for a known access code.
	pkt := BuildAuthPacket("SECRET")

	// Header
	if binary.LittleEndian.Uint32(pkt[0:4]) != 0x40 {
		t.Error("field 0-3 wrong")
	}
	if binary.LittleEndian.Uint32(pkt[4:8]) != 0x3000 {
		t.Error("field 4-7 wrong")
	}
	if binary.LittleEndian.Uint32(pkt[8:12]) != 0 {
		t.Error("field 8-11 wrong")
	}
	if binary.LittleEndian.Uint32(pkt[12:16]) != 0 {
		t.Error("field 12-15 wrong")
	}

	// Username
	if string(pkt[16:20]) != "bblp" {
		t.Error("username wrong")
	}
	for i := 20; i < 48; i++ {
		if pkt[i] != 0 {
			t.Errorf("username padding at %d non-zero", i)
		}
	}

	// Access code
	if string(pkt[48:54]) != "SECRET" {
		t.Error("access code wrong")
	}
	for i := 54; i < 80; i++ {
		if pkt[i] != 0 {
			t.Errorf("access code padding at %d non-zero", i)
		}
	}
}
