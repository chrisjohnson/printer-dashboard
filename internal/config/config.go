package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the application configuration.
type Config struct {
	Listen   string         `yaml:"listen"`   // e.g., ":8080"
	Auth     AuthConfig     `yaml:"auth"`
	Printers []PrinterDef   `yaml:"printers"`
	Cameras  []CameraDef    `yaml:"cameras"`
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"` // bcrypt hash recommended
	Secret   string   `yaml:"secret"`   // session secret
}

// PrinterDef describes a printer to connect to.
type PrinterDef struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	Type       string `yaml:"type"` // "bambu" or "snapmaker"
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	AccessCode string `yaml:"access_code"` // Bambu access code / API key
	Serial     string `yaml:"serial"`      // Bambu serial number
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
		Listen: ":8080",
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

func (c *Config) validate() error {
	for i, p := range c.Printers {
		if p.ID == "" {
			return fmt.Errorf("printer[%d] missing 'id'", i)
		}
		if p.Type != "bambu" && p.Type != "snapmaker" {
			return fmt.Errorf("printer[%d] type must be 'bambu' or 'snapmaker', got %q", i, p.Type)
		}
	}
	return nil
}
