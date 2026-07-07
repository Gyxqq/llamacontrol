package main

import (
	"context"
	"os"
	"path/filepath"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// NewApp creates a new App application struct
func NewApp() *App {
	log.Debug("app: instance created")
	return &App{
		httpClient:      newHTTPClient(),
		activeDownloads: make(map[string]*downloadTask),
		serverState:     &serverState{},
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.startTray()

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

// HideToTray hides the main window while keeping the app running.
func (a *App) HideToTray() {
	if a.ctx == nil {
		return
	}
	wailsruntime.WindowHide(a.ctx)
}

// ShowMainWindow restores the app from the tray.
func (a *App) ShowMainWindow() {
	if a.ctx == nil {
		return
	}
	wailsruntime.WindowShow(a.ctx)
	wailsruntime.WindowUnminimise(a.ctx)
}

// QuitApp exits the app from the tray menu.
func (a *App) QuitApp() {
	if a.ctx == nil {
		return
	}
	a.allowQuit = true
	wailsruntime.Quit(a.ctx)
}

func (a *App) beforeClose(ctx context.Context) bool {
	if a.allowQuit {
		return false
	}
	wailsruntime.WindowHide(ctx)
	return true
}
