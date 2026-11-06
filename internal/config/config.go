package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the application configuration.
type Config struct {
	Listen       string         `yaml:"listen"`        // e.g., ":8080"
	Auth         AuthConfig     `yaml:"auth"`          // dashboard web auth
	BambuAccount *BambuAccount  `yaml:"bambu_account,omitempty"` // Bambu cloud account
	Printers     []PrinterDef   `yaml:"printers"`
	Cameras      []CameraDef    `yaml:"cameras"`
	configPath   string         // path to the YAML file (set by Load, used by Save)
}

// AuthConfig holds dashboard authentication settings.
type AuthConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"` // bcrypt hash recommended
	Secret   string `yaml:"secret"`   // session secret
}

// BambuAccount holds Bambu Lab cloud account credentials.
// Provide email+password for automatic login, or pre-obtained token+user_id.
type BambuAccount struct {
	Email    string `yaml:"email"`     // Bambu account email
	Password string `yaml:"password"`  // Bambu account password
	Token    string `yaml:"token"`     // JWT access token (alternative to email/password)
	UserID   string `yaml:"user_id"`   // Numeric user ID (required if using token directly)
	Region   string `yaml:"region"`    // "global" or "china" (default: "global")
}

// PrinterDef describes a printer to connect to.
type PrinterDef struct {
	ID     string `yaml:"id"`
	Name   string `yaml:"name"`
	Type   string `yaml:"type"` // "bambu" or "snapmaker"
	// Host and AccessCode do double duty for Bambu printers: they are the
	// credentials for the printer's own local MQTT broker (preferred when
	// reachable) as well as for the local camera stream. Leaving them off
	// falls the printer back to Bambu's cloud MQTT, which needs no local
	// network access but does depend on an account token that can expire.
	Host         string `yaml:"host,omitempty"`         // Printer LAN IP
	Port         int    `yaml:"port,omitempty"`         // Not used for Bambu MQTT
	AccessCode   string `yaml:"access_code,omitempty"`  // Bambu device access code
	Serial       string `yaml:"serial"`                 // Bambu device serial number (dev_id)
	Model        string `yaml:"model,omitempty"`        // e.g., "H2S", "P1S", "X1C" — used for camera URL format
	MQTTTransport string `yaml:"mqtt_transport,omitempty"` // "auto" (default), "lan", or "cloud"
}

// CameraDef describes an external camera.
type CameraDef struct {
	ID        string `yaml:"id"`
	Name      string `yaml:"name"`
	URL       string `yaml:"url"`
	PrinterID string `yaml:"printer_id"` // optional, link to a printer
}

// Load reads and parses a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{
		Listen:     ":8080",
		configPath: path,
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

// Save writes the config back to its YAML file, preserving existing content
// by doing a round-trip through the struct.
func (c *Config) Save() error {
	if c.configPath == "" {
		return fmt.Errorf("config path not set")
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(c.configPath, data, 0644); err != nil {
		// Worth special-casing, because the raw error ("open config.yaml:
		// permission denied") reliably sends people to the wrong file. Under
		// Docker or rootless Podman the config is a bind mount, so it keeps its
		// *host* ownership through a uid mapping: the file can be perfectly
		// writable for the human who owns it while the container's own user is
		// not that uid and cannot touch it. Naming the file and both fixes beats
		// a bare "permission denied", and saying nothing was saved matters
		// because the caller has already mutated the in-memory config by now.
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf(
				"permission denied writing %s — nothing was saved. If the dashboard "+
					"runs in a container, the bind-mounted config file belongs to a host "+
					"user the container's user cannot write as: run the container as a "+
					"user that can write it (e.g. --user 0:0 under rootless podman) or "+
					"make the file writable by it: %w", c.configPath, err)
		}
		return fmt.Errorf("writing config file: %w", err)
	}
	return nil
}

func (c *Config) validate() error {
	hasBambu := false
	for i, p := range c.Printers {
		if p.ID == "" {
			return fmt.Errorf("printer[%d] missing 'id'", i)
		}
		if p.Type != "bambu" && p.Type != "snapmaker" {
			return fmt.Errorf("printer[%d] type must be 'bambu' or 'snapmaker', got %q", i, p.Type)
		}
		if p.Type == "bambu" {
			hasBambu = true
			if p.Serial == "" {
				return fmt.Errorf("printer[%d] (bambu) requires 'serial'", i)
			}
		}
	}

	// If any Bambu printers are configured, we need BambuAccount creds
	if hasBambu {
		if c.BambuAccount == nil {
			return fmt.Errorf("bambu printers configured but 'bambu_account' section is missing")
		}
		if c.BambuAccount.Token == "" && c.BambuAccount.UserID == "" &&
			(c.BambuAccount.Email == "" || c.BambuAccount.Password == "") {
			return fmt.Errorf("bambu_account requires one of: 'token', 'user_id' (with token on disk), or 'email' + 'password'")
		}
	}

	return nil
}
