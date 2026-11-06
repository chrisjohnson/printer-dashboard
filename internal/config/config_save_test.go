package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSave_PermissionDeniedIsActionable pins the message a failed config write
// produces.
//
// The raw Go error is "open config.yaml: permission denied", and that string
// sends people to the wrong file: under Docker or rootless Podman the config is
// a bind mount carrying *host* ownership through a uid mapping, so the file can
// be perfectly writable for the human who owns it while the container's user is
// a different uid entirely. The message has to name the file, say nothing was
// saved, and point at the container fix.
func TestSave_PermissionDeniedIsActionable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; a 0444 file would still be writable")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("listen: \":8080\"\nprinters: []\n"), 0444); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	err = cfg.Save()
	if err == nil {
		t.Fatal("Save() = nil, want a permission error for a read-only file")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Save() error = %v; want it to still wrap fs.ErrPermission", err)
	}

	msg := err.Error()
	for _, want := range []string{
		path,             // which file — "config.yaml" alone is a guess
		"nothing was saved", // the caller has already mutated in-memory state
		"container",        // the actual usual cause
		"--user",          // a concrete thing to try
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Save() error = %q; want it to mention %q", msg, want)
		}
	}
}

// TestSave_SuccessRoundTrips guards the happy path the permission test orbits:
// a writable config still saves and reloads with its printers intact.
func TestSave_SuccessRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("listen: \":9000\"\nprinters: []\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Printers = []PrinterDef{{ID: "u1", Name: "U1", Type: "snapmaker", Host: "10.0.0.5"}}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Printers) != 1 || reloaded.Printers[0].Name != "U1" {
		t.Errorf("after Save, printers = %+v; want the U1 we just added", reloaded.Printers)
	}
}
