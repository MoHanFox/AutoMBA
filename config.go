package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type appConfig struct {
	Width               int    `json:"width"`
	Language            string `json:"language"`
	Verify              bool   `json:"verify"`
	PreserveSideEffects bool   `json:"preserveSideEffects"`
	AutoSimplify        bool   `json:"autoSimplify"`
	DeadCode            bool   `json:"deadCode"`
	ExtendedC           bool   `json:"extendedC"`
	Signed              bool   `json:"signed"`
	CSE                 bool   `json:"cse"`
	ImpureCalls         string `json:"impureCalls"`
}

func defaultAppConfig() appConfig {
	return appConfig{Width: 32, Language: "en", ExtendedC: true, Signed: true}
}

func normalizeConfig(c appConfig) appConfig {
	switch c.Width {
	case 8, 16, 32, 64:
	default:
		c.Width = 32
	}
	if c.Language != "en" && c.Language != "zh" {
		c.Language = "en"
	}
	return c
}

func configFilePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "config.json"), nil
}

// loadAppConfig returns the persisted settings and creates a default config on
// first run. The file lives next to the executable as requested.
func loadAppConfig() appConfig {
	cfg := defaultAppConfig()
	path, err := configFilePath()
	if err != nil {
		return cfg
	}
	data, err := os.ReadFile(path)
	if err != nil {
		_ = writeAppConfig(cfg)
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		cfg = defaultAppConfig()
		_ = writeAppConfig(cfg)
		return cfg
	}
	cfg = normalizeConfig(cfg)
	return cfg
}

func writeAppConfig(cfg appConfig) error {
	cfg = normalizeConfig(cfg)
	path, err := configFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func saveAppConfig(cfg appConfig) error {
	return writeAppConfig(normalizeConfig(cfg))
}
