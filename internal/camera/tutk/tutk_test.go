package tutk

import (
	"net/url"
	"strings"
	"testing"
)

func TestBuildURL(t *testing.T) {
	creds := Credentials{
		TTCode:  "01234567890ABCDEF012",
		AuthKey: "01234567",
		Passwd:  "012345",
		Region:  "us",
		Serial:  "00M00A2C0604476",
	}

	result := BuildURL(creds)

	// Must start with bambu:///tutk?
	if !strings.HasPrefix(result, "bambu:///tutk?") {
		t.Errorf("expected prefix bambu:///tutk?, got %q", result)
	}

	// Parse query to verify params
	qidx := strings.Index(result, "?")
	q, err := url.ParseQuery(result[qidx+1:])
	if err != nil {
		t.Fatalf("failed to parse query: %v", err)
	}

	tests := []struct {
		key, want string
	}{
		{"uid", "01234567890ABCDEF012"},
		{"authkey", "01234567"},
		{"passwd", "012345"},
		{"region", "us"},
		{"device", "00M00A2C0604476"},
		{"net_ver", defaultNetVer},
		{"refresh_url", "1"},
	}

	for _, tt := range tests {
		if got := q.Get(tt.key); got != tt.want {
			t.Errorf("query param %q = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestBuildURLDefaults(t *testing.T) {
	// Minimal creds - should use defaults for optional fields
	creds := Credentials{
		TTCode:  "ABC",
		AuthKey: "DEF",
		Passwd:  "123",
		Region:  "cn",
		Serial:  "SERIAL01",
	}

	result := BuildURL(creds)
	qidx := strings.Index(result, "?")
	q, err := url.ParseQuery(result[qidx+1:])
	if err != nil {
		t.Fatalf("failed to parse query: %v", err)
	}

	if got := q.Get("net_ver"); got != defaultNetVer {
		t.Errorf("net_ver default = %q, want %q", got, defaultNetVer)
	}
	if got := q.Get("cli_ver"); got != defaultCliVer {
		t.Errorf("cli_ver default = %q, want %q", got, defaultCliVer)
	}
	if got := q.Get("cli_id"); got == "" {
		t.Error("cli_id should not be empty")
	}
	if got := q.Get("dev_ver"); got == "" {
		t.Error("dev_ver should not be empty (defaulted)")
	}
}

func TestBuildURLEscapes(t *testing.T) {
	creds := Credentials{
		TTCode:  "abc def", // space in ttcode
		AuthKey: "key&val", // ampersand in authkey
		Passwd:  "12=45",
		Region:  "us",
		Serial:  "serial-01",
	}

	result := BuildURL(creds)
	qidx := strings.Index(result, "?")
	q, err := url.ParseQuery(result[qidx+1:])
	if err != nil {
		t.Fatalf("failed to parse query: %v", err)
	}

	if got := q.Get("uid"); got != "abc def" {
		t.Errorf("uid = %q, want %q", got, "abc def")
	}
	if got := q.Get("authkey"); got != "key&val" {
		t.Errorf("authkey = %q, want %q", got, "key&val")
	}
	if got := q.Get("passwd"); got != "12=45" {
		t.Errorf("passwd = %q, want %q", got, "12=45")
	}
}

func TestStubNewSession(t *testing.T) {
	s, err := NewSession(Credentials{})
	if err == nil {
		t.Error("expected error from stub NewSession")
	}
	if s != nil {
		t.Error("expected nil session from stub NewSession")
	}
}
