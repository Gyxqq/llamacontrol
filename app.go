package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
)

// NewApp creates a new App application struct
func NewApp() *App {
	log.Debug("app: instance created")
	return &App{
		httpClient: &http.Client{
			Timeout: 0, // no timeout — downloads can be very long
		},
		activeDownloads: make(map[string]*downloadTask),
		serverState:     &serverState{},
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Initialize app log buffer and hook into logrus
	a.appLogs = &appLogBuffer{max: 200}
	log.AddHook(&appLogHook{buffer: a.appLogs})
	log.Infof("startup: app logger hook installed")

	// Resolve app data directory
	appDataDir, err := a.appDataDir()
	if err != nil {
		log.Errorf("startup: failed to resolve app data dir: %v", err)
		return
	}
	log.Infof("startup: app data directory: %s", appDataDir)

	a.modelsDir = filepath.Join(appDataDir, "models")
	a.binDir = filepath.Join(appDataDir, "bin")
	a.metadataPath = filepath.Join(appDataDir, "metadata.json")
	a.serverConfigPath = filepath.Join(appDataDir, "server-config.json")

	// Ensure directories exist
	if err := os.MkdirAll(a.modelsDir, 0755); err != nil {
		log.Errorf("startup: failed to create models dir: %v", err)
		return
	}
	if err := os.MkdirAll(a.binDir, 0755); err != nil {
		log.Errorf("startup: failed to create bin dir: %v", err)
		return
	}

	// Load existing metadata
	a.loadMetadata()

	// Validate local files against metadata
	a.validateFiles()

	// Load saved server config
	a.loadServerConfig()

	log.Infof("startup: ready, %d model(s) in metadata", len(a.models))
}