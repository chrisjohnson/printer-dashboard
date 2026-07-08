package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempYAML creates a temporary YAML file with the given content
// and returns its path. The file is cleaned up automatically by t.TempDir().
func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	return path
}

func TestLoad(t *testing.T) {
	t.Run("file not found", func(t *testing.T) {
		_, err := Load("/nonexistent/path/config.yaml")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "reading config file") {
			t.Errorf("expected error to contain 'reading config file', got: %v", err)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("expected error to wrap os.ErrNotExist, got: %v", err)
		}
	})

	t.Run("invalid yaml", func(t *testing.T) {
		path := writeTempYAML(t, ":: invalid yaml ::")
		_, err := Load(path)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "parsing config file") {
			t.Errorf("expected error to contain 'parsing config file', got: %v", err)
		}
		if !strings.Contains(err.Error(), "yaml") {
			t.Errorf("expected error to mention yaml parsing, got: %v", err)
		}
	})

	t.Run("valid config with defaults", func(t *testing.T) {
		path := writeTempYAML(t, "listen: \":9090\"\n")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Listen != ":9090" {
			t.Errorf("expected Listen to be ':9090', got: %q", cfg.Listen)
		}
		if cfg.Auth.Enabled {
			t.Errorf("expected Auth.Enabled to be false, got true")
		}
		if cfg.Printers != nil {
			t.Errorf("expected Printers to be nil, got: %v", cfg.Printers)
		}
		if cfg.Cameras != nil {
			t.Errorf("expected Cameras to be nil, got: %v", cfg.Cameras)
		}
		if cfg.BambuAccount != nil {
			t.Errorf("expected BambuAccount to be nil, got: %v", cfg.BambuAccount)
		}
	})

	t.Run("default listen port", func(t *testing.T) {
		path := writeTempYAML(t, "# no listen field\n")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Listen != ":8080" {
			t.Errorf("expected default Listen to be ':8080', got: %q", cfg.Listen)
		}
	})

	t.Run("full valid config", func(t *testing.T) {
		yaml := `
listen: ":9090"
auth:
  enabled: true
  username: admin
  password: bcrypt_hash
  secret: session_secret
bambu_account:
  email: user@example.com
  password: bambu_pass
  region: global
printers:
  - id: snap1
    name: Snapmaker
    type: snapmaker
    host: 192.168.1.10
    port: 8080
  - id: bambu1
    name: Bambu X1C
    type: bambu
    host: 192.168.1.20
    access_code: ABC123
    serial: SERIAL001
cameras:
  - id: cam1
    name: Top Cam
    url: http://192.168.1.100/video
    printer_id: bambu1
`
		path := writeTempYAML(t, yaml)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// top-level
		if cfg.Listen != ":9090" {
			t.Errorf("Listen = %q, want %q", cfg.Listen, ":9090")
		}

		// auth
		if cfg.Auth.Enabled != true {
			t.Errorf("Auth.Enabled = %v, want true", cfg.Auth.Enabled)
		}
		if cfg.Auth.Username != "admin" {
			t.Errorf("Auth.Username = %q, want %q", cfg.Auth.Username, "admin")
		}
		if cfg.Auth.Password != "bcrypt_hash" {
			t.Errorf("Auth.Password = %q, want %q", cfg.Auth.Password, "bcrypt_hash")
		}
		if cfg.Auth.Secret != "session_secret" {
			t.Errorf("Auth.Secret = %q, want %q", cfg.Auth.Secret, "session_secret")
		}

		// bambu_account
		if cfg.BambuAccount == nil {
			t.Fatal("BambuAccount is nil")
		}
		if cfg.BambuAccount.Email != "user@example.com" {
			t.Errorf("BambuAccount.Email = %q, want %q", cfg.BambuAccount.Email, "user@example.com")
		}
		if cfg.BambuAccount.Password != "bambu_pass" {
			t.Errorf("BambuAccount.Password = %q, want %q", cfg.BambuAccount.Password, "bambu_pass")
		}
		if cfg.BambuAccount.Region != "global" {
			t.Errorf("BambuAccount.Region = %q, want %q", cfg.BambuAccount.Region, "global")
		}

		// printers
		if len(cfg.Printers) != 2 {
			t.Fatalf("expected 2 printers, got %d", len(cfg.Printers))
		}

		snap := cfg.Printers[0]
		if snap.ID != "snap1" || snap.Name != "Snapmaker" || snap.Type != "snapmaker" {
			t.Errorf("snapmaker printer = %+v", snap)
		}
		if snap.Host != "192.168.1.10" {
			t.Errorf("snapmaker Host = %q, want %q", snap.Host, "192.168.1.10")
		}
		if snap.Port != 8080 {
			t.Errorf("snapmaker Port = %d, want %d", snap.Port, 8080)
		}

		bambu := cfg.Printers[1]
		if bambu.ID != "bambu1" || bambu.Name != "Bambu X1C" || bambu.Type != "bambu" {
			t.Errorf("bambu printer = %+v", bambu)
		}
		if bambu.Host != "192.168.1.20" {
			t.Errorf("bambu Host = %q, want %q", bambu.Host, "192.168.1.20")
		}
		if bambu.AccessCode != "ABC123" {
			t.Errorf("bambu AccessCode = %q, want %q", bambu.AccessCode, "ABC123")
		}
		if bambu.Serial != "SERIAL001" {
			t.Errorf("bambu Serial = %q, want %q", bambu.Serial, "SERIAL001")
		}
		if bambu.Port != 0 {
			t.Errorf("bambu Port = %d, want 0 (unset)", bambu.Port)
		}

		// cameras
		if len(cfg.Cameras) != 1 {
			t.Fatalf("expected 1 camera, got %d", len(cfg.Cameras))
		}
		cam := cfg.Cameras[0]
		if cam.ID != "cam1" {
			t.Errorf("Camera ID = %q, want %q", cam.ID, "cam1")
		}
		if cam.Name != "Top Cam" {
			t.Errorf("Camera Name = %q, want %q", cam.Name, "Top Cam")
		}
		if cam.URL != "http://192.168.1.100/video" {
			t.Errorf("Camera URL = %q, want %q", cam.URL, "http://192.168.1.100/video")
		}
		if cam.PrinterID != "bambu1" {
			t.Errorf("Camera PrinterID = %q, want %q", cam.PrinterID, "bambu1")
		}
	})

	t.Run("missing printer id", func(t *testing.T) {
		yaml := `
printers:
  - name: NoID
    type: snapmaker
`
		path := writeTempYAML(t, yaml)
		_, err := Load(path)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "missing 'id'") {
			t.Errorf("expected error containing \"missing 'id'\", got: %v", err)
		}
	})

	t.Run("invalid printer type", func(t *testing.T) {
		yaml := `
printers:
  - id: p1
    type: foo
`
		path := writeTempYAML(t, yaml)
		_, err := Load(path)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "type must be") {
			t.Errorf("expected error about valid types, got: %v", err)
		}
	})

	t.Run("bambu printer without serial", func(t *testing.T) {
		yaml := `
printers:
  - id: p1
    type: bambu
`
		path := writeTempYAML(t, yaml)
		_, err := Load(path)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "requires 'serial'") {
			t.Errorf("expected error about missing serial, got: %v", err)
		}
	})

	t.Run("bambu printer without bambu_account", func(t *testing.T) {
		yaml := `
printers:
  - id: p1
    type: bambu
    serial: SERIAL001
`
		path := writeTempYAML(t, yaml)
		_, err := Load(path)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "bambu_account") {
			t.Errorf("expected error about missing bambu_account, got: %v", err)
		}
	})

	t.Run("bambu_account with missing auth", func(t *testing.T) {
		yaml := `
bambu_account:
  region: global
printers:
  - id: p1
    type: bambu
    serial: SERIAL001
`
		path := writeTempYAML(t, yaml)
		_, err := Load(path)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "bambu_account requires") {
			t.Errorf("expected error about bambu_account credentials, got: %v", err)
		}
	})

	t.Run("valid via email+password", func(t *testing.T) {
		yaml := `
bambu_account:
  email: user@example.com
  password: secret
printers:
  - id: p1
    type: bambu
    serial: SERIAL001
`
		path := writeTempYAML(t, yaml)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.BambuAccount == nil {
			t.Fatal("BambuAccount is nil")
		}
		if cfg.BambuAccount.Email != "user@example.com" {
			t.Errorf("Email = %q, want %q", cfg.BambuAccount.Email, "user@example.com")
		}
		if cfg.BambuAccount.Password != "secret" {
			t.Errorf("Password = %q, want %q", cfg.BambuAccount.Password, "secret")
		}
	})

	t.Run("valid via token", func(t *testing.T) {
		yaml := `
bambu_account:
  token: my.jwt.token
printers:
  - id: p1
    type: bambu
    serial: SERIAL001
`
		path := writeTempYAML(t, yaml)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.BambuAccount == nil {
			t.Fatal("BambuAccount is nil")
		}
		if cfg.BambuAccount.Token != "my.jwt.token" {
			t.Errorf("Token = %q, want %q", cfg.BambuAccount.Token, "my.jwt.token")
		}
	})
}

func TestSave(t *testing.T) {
	t.Run("no configPath", func(t *testing.T) {
		cfg := &Config{}
		err := cfg.Save()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "config path not set") {
			t.Errorf("expected 'config path not set', got: %v", err)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		orig := &Config{
			Listen: ":9090",
			Auth: AuthConfig{
				Enabled:  true,
				Username: "admin",
				Password: "bcrypt_hash",
				Secret:   "session_secret",
			},
			BambuAccount: &BambuAccount{
				Email:    "user@example.com",
				Password: "bambu_pass",
				Region:   "global",
			},
			Printers: []PrinterDef{
				{
					ID:   "snap1",
					Name: "Snapmaker",
					Type: "snapmaker",
					Host: "192.168.1.10",
					Port: 8080,
				},
				{
					ID:         "bambu1",
					Name:       "Bambu X1C",
					Type:       "bambu",
					Host:       "192.168.1.20",
					AccessCode: "ABC123",
					Serial:     "SERIAL001",
				},
			},
			Cameras: []CameraDef{
				{
					ID:        "cam1",
					Name:      "Top Cam",
					URL:       "http://192.168.1.100/video",
					PrinterID: "bambu1",
				},
			},
			configPath: filepath.Join(t.TempDir(), "roundtrip.yaml"),
		}

		if err := orig.Save(); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		loaded, err := Load(orig.configPath)
		if err != nil {
			t.Fatalf("Load after save failed: %v", err)
		}

		// Compare top-level fields
		if loaded.Listen != orig.Listen {
			t.Errorf("Listen = %q, want %q", loaded.Listen, orig.Listen)
		}

		// Compare Auth
		if loaded.Auth.Enabled != orig.Auth.Enabled {
			t.Errorf("Auth.Enabled = %v, want %v", loaded.Auth.Enabled, orig.Auth.Enabled)
		}
		if loaded.Auth.Username != orig.Auth.Username {
			t.Errorf("Auth.Username = %q, want %q", loaded.Auth.Username, orig.Auth.Username)
		}
		if loaded.Auth.Password != orig.Auth.Password {
			t.Errorf("Auth.Password = %q, want %q", loaded.Auth.Password, orig.Auth.Password)
		}
		if loaded.Auth.Secret != orig.Auth.Secret {
			t.Errorf("Auth.Secret = %q, want %q", loaded.Auth.Secret, orig.Auth.Secret)
		}

		// Compare BambuAccount
		if loaded.BambuAccount == nil {
			t.Fatal("BambuAccount is nil after round-trip")
		}
		if loaded.BambuAccount.Email != orig.BambuAccount.Email {
			t.Errorf("BambuAccount.Email = %q, want %q", loaded.BambuAccount.Email, orig.BambuAccount.Email)
		}
		if loaded.BambuAccount.Password != orig.BambuAccount.Password {
			t.Errorf("BambuAccount.Password = %q, want %q", loaded.BambuAccount.Password, orig.BambuAccount.Password)
		}
		if loaded.BambuAccount.Region != orig.BambuAccount.Region {
			t.Errorf("BambuAccount.Region = %q, want %q", loaded.BambuAccount.Region, orig.BambuAccount.Region)
		}
		if loaded.BambuAccount.Token != orig.BambuAccount.Token {
			t.Errorf("BambuAccount.Token = %q, want %q", loaded.BambuAccount.Token, orig.BambuAccount.Token)
		}
		if loaded.BambuAccount.UserID != orig.BambuAccount.UserID {
			t.Errorf("BambuAccount.UserID = %q, want %q", loaded.BambuAccount.UserID, orig.BambuAccount.UserID)
		}

		// Compare Printers
		if len(loaded.Printers) != len(orig.Printers) {
			t.Fatalf("len(Printers) = %d, want %d", len(loaded.Printers), len(orig.Printers))
		}
		for i := range orig.Printers {
			if loaded.Printers[i] != orig.Printers[i] {
				t.Errorf("Printers[%d] = %+v, want %+v", i, loaded.Printers[i], orig.Printers[i])
			}
		}

		// Compare Cameras
		if len(loaded.Cameras) != len(orig.Cameras) {
			t.Fatalf("len(Cameras) = %d, want %d", len(loaded.Cameras), len(orig.Cameras))
		}
		for i := range orig.Cameras {
			if loaded.Cameras[i] != orig.Cameras[i] {
				t.Errorf("Cameras[%d] = %+v, want %+v", i, loaded.Cameras[i], orig.Cameras[i])
			}
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("no printers is valid", func(t *testing.T) {
		cfg := &Config{Listen: ":8080"}
		if err := cfg.validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("snapmaker printer is valid", func(t *testing.T) {
		cfg := &Config{
			Listen: ":8080",
			Printers: []PrinterDef{
				{ID: "p1", Name: "Snap", Type: "snapmaker"},
			},
		}
		if err := cfg.validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("bambu printer valid with email+password", func(t *testing.T) {
		cfg := &Config{
			Listen: ":8080",
			BambuAccount: &BambuAccount{
				Email:    "user@example.com",
				Password: "pass",
			},
			Printers: []PrinterDef{
				{ID: "p1", Name: "Bambu", Type: "bambu", Serial: "SERIAL001"},
			},
		}
		if err := cfg.validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("bambu printer valid with token", func(t *testing.T) {
		cfg := &Config{
			Listen: ":8080",
			BambuAccount: &BambuAccount{
				Token: "my.jwt.token",
			},
			Printers: []PrinterDef{
				{ID: "p1", Name: "Bambu", Type: "bambu", Serial: "SERIAL001"},
			},
		}
		if err := cfg.validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("bambu printer valid with user_id", func(t *testing.T) {
		cfg := &Config{
			Listen: ":8080",
			BambuAccount: &BambuAccount{
				UserID: "12345",
			},
			Printers: []PrinterDef{
				{ID: "p1", Name: "Bambu", Type: "bambu", Serial: "SERIAL001"},
			},
		}
		if err := cfg.validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing printer id", func(t *testing.T) {
		cfg := &Config{
			Printers: []PrinterDef{
				{Name: "NoID", Type: "snapmaker"},
			},
		}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "missing 'id'") {
			t.Errorf("expected 'missing id', got: %v", err)
		}
	})

	t.Run("invalid printer type", func(t *testing.T) {
		cfg := &Config{
			Printers: []PrinterDef{
				{ID: "p1", Type: "foo"},
			},
		}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "type must be") {
			t.Errorf("expected 'type must be', got: %v", err)
		}
	})

	t.Run("bambu printer without serial", func(t *testing.T) {
		cfg := &Config{
			Printers: []PrinterDef{
				{ID: "p1", Type: "bambu"},
			},
		}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "requires 'serial'") {
			t.Errorf("expected 'requires serial', got: %v", err)
		}
	})

	t.Run("bambu printer without bambu_account", func(t *testing.T) {
		cfg := &Config{
			Printers: []PrinterDef{
				{ID: "p1", Type: "bambu", Serial: "SERIAL001"},
			},
		}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "bambu_account") {
			t.Errorf("expected 'bambu_account', got: %v", err)
		}
	})

	t.Run("bambu_account with empty credentials", func(t *testing.T) {
		cfg := &Config{
			BambuAccount: &BambuAccount{
				Region: "global",
			},
			Printers: []PrinterDef{
				{ID: "p1", Type: "bambu", Serial: "SERIAL001"},
			},
		}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "bambu_account requires") {
			t.Errorf("expected 'bambu_account requires', got: %v", err)
		}
	})

	t.Run("bambu_account email without password fails", func(t *testing.T) {
		cfg := &Config{
			BambuAccount: &BambuAccount{
				Email: "user@example.com",
				// no Password
			},
			Printers: []PrinterDef{
				{ID: "p1", Type: "bambu", Serial: "SERIAL001"},
			},
		}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "bambu_account requires") {
			t.Errorf("expected 'bambu_account requires', got: %v", err)
		}
	})

	t.Run("bambu_account password without email fails", func(t *testing.T) {
		cfg := &Config{
			BambuAccount: &BambuAccount{
				Password: "secret",
				// no Email
			},
			Printers: []PrinterDef{
				{ID: "p1", Type: "bambu", Serial: "SERIAL001"},
			},
		}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "bambu_account requires") {
			t.Errorf("expected 'bambu_account requires', got: %v", err)
		}
	})
}
