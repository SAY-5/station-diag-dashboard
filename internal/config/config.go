// Package config loads the dashboard service configuration from YAML.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the dashboard service configuration. Zero values are filled
// with sane defaults by Defaults, so an empty file is valid.
type Config struct {
	// HTTPAddr is the address for the web dashboard and REST API.
	HTTPAddr string `yaml:"http_addr"`

	// TCPAddr is the address the TCP log ingest listener binds to.
	TCPAddr string `yaml:"tcp_addr"`

	// TailFile, when set, additionally tails this log file.
	TailFile string `yaml:"tail_file"`

	// RulesFile points at the YAML actuator signature document.
	RulesFile string `yaml:"rules_file"`

	// DBPath is the SQLite database path.
	DBPath string `yaml:"db_path"`

	// BacklogSize is how many recent events the hub retains for backfill.
	BacklogSize int `yaml:"backlog_size"`
}

// Defaults returns a Config populated with development-friendly values.
func Defaults() Config {
	return Config{
		HTTPAddr:    ":8080",
		TCPAddr:     ":7000",
		RulesFile:   "rules/actuator_signatures.yaml",
		DBPath:      "station-diag.db",
		BacklogSize: 500,
	}
}

// Load reads a YAML config file, applying defaults for any unset field.
// A missing path is not an error: defaults are returned.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("config: read: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse: %w", err)
	}
	return applyDefaults(cfg), nil
}

func applyDefaults(c Config) Config {
	d := Defaults()
	if c.HTTPAddr == "" {
		c.HTTPAddr = d.HTTPAddr
	}
	if c.TCPAddr == "" {
		c.TCPAddr = d.TCPAddr
	}
	if c.RulesFile == "" {
		c.RulesFile = d.RulesFile
	}
	if c.DBPath == "" {
		c.DBPath = d.DBPath
	}
	if c.BacklogSize == 0 {
		c.BacklogSize = d.BacklogSize
	}
	return c
}
