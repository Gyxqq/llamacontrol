package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// ──────────────────────────────────────────────
// Server config persistence
// ──────────────────────────────────────────────

// loadServerConfig reads server-config.json from disk into a.serverConfig.
// When no file exists, it populates default values.
func (a *App) loadServerConfig() {
	log.Debugf("server-config: loading from %s", a.serverConfigPath)

	defaults := ServerConfig{
		Host:                  "127.0.0.1",
		Port:                  8080,
		CtxSize:               4096,
		GPULayers:             999,
		Threads:               8,
		BatchSize:             512,
		UbatchSize:            512,
		Parallel:              1,
		FlashAttention:        true,
		Background:            true,
		HostEnabled:           true,
		PortEnabled:           true,
		CtxSizeEnabled:        true,
		GPULayersEnabled:      true,
		ThreadsEnabled:        true,
		BatchSizeEnabled:      true,
		UbatchSizeEnabled:     true,
		ParallelEnabled:       true,
		FlashAttentionEnabled: true,
		ExtraArgsEnabled:      true,
	}

	data, err := os.ReadFile(a.serverConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debug("server-config: file does not exist, using defaults")
			a.serverConfig = defaults
			return
		}
		log.Warnf("server-config: failed to read: %v", err)
		a.serverConfig = defaults
		return
	}

	var config ServerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		log.Warnf("server-config: failed to parse: %v", err)
		a.serverConfig = defaults
		return
	}

	a.serverConfig = config
	log.Infof("server-config: loaded saved config")
}

// saveServerConfigFile atomically writes a.serverConfig to server-config.json.
func (a *App) saveServerConfigFile() error {
	log.Debugf("server-config: saving")

	data, err := json.MarshalIndent(a.serverConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal server config: %w", err)
	}

	tmpPath := a.serverConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write server config tmp: %w", err)
	}
	if err := os.Rename(tmpPath, a.serverConfigPath); err != nil {
		return fmt.Errorf("failed to rename server config: %w", err)
	}

	return nil
}

// GetServerConfig returns the saved server configuration.
func (a *App) GetServerConfig() ServerConfig {
	return a.serverConfig
}

// SaveServerConfig persists the given server configuration and returns the saved config.
func (a *App) SaveServerConfig(config ServerConfig) error {
	log.Infof("server-config: saving config (modelId=%s)", config.ModelID)
	a.serverConfig = config
	return a.saveServerConfigFile()
}