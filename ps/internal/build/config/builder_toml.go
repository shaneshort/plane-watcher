package config

import (
	"errors"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
)

type trackedTOMLBody struct {
	Services map[string]string `toml:"services"`
	Defaults Defaults          `toml:"defaults"`
}

type localTOMLBody struct {
	LastRun LastRun `toml:"last_run"`
}

// LoadBuilderTOML loads tracked policy (services + defaults) and overlays
// per-checkout LastRun state. Defaults are tracked-only by design: project
// policy lives in the tracked file; the local file only persists
// last-selection state. Missing files are silently treated as empty.
func LoadBuilderTOML(trackedPath, localPath string) (*Config, error) {
	cfg := &Config{
		Services: map[string]string{},
	}
	if err := mergeTrackedTOMLFile(trackedPath, cfg); err != nil {
		return nil, err
	}
	if err := mergeLocalTOMLFile(localPath, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func mergeTrackedTOMLFile(path string, cfg *Config) error {
	if path == "" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var parsed trackedTOMLBody
	if _, err := toml.Decode(string(body), &parsed); err != nil {
		return err
	}
	for k, v := range parsed.Services {
		cfg.Services[k] = v
	}
	// Single source of truth for booleans: the tracked file's values are
	// assigned verbatim, including false. The local file cannot override.
	cfg.Defaults = parsed.Defaults
	return nil
}

func mergeLocalTOMLFile(path string, cfg *Config) error {
	if path == "" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var parsed localTOMLBody
	if _, err := toml.Decode(string(body), &parsed); err != nil {
		return err
	}
	if len(parsed.LastRun.Stages) > 0 {
		cfg.LastRun.Stages = parsed.LastRun.Stages
	}
	if parsed.LastRun.Deploy != "" {
		cfg.LastRun.Deploy = parsed.LastRun.Deploy
	}
	return nil
}

// SaveLastRun writes a minimal builder.local.toml capturing the last run
// selections; called after a successful (non-cancelled) execution.
func SaveLastRun(localPath string, lr LastRun) error {
	body := struct {
		LastRun LastRun `toml:"last_run"`
	}{LastRun: lr}
	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(body)
}
