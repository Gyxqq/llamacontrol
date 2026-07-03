package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ──────────────────────────────────────────────
// Helpers: metadata persistence
// ──────────────────────────────────────────────

// appDataDir returns the directory where the app's data lives.
// In production it uses the executable's directory; in wails dev mode
// (binary in a temp directory) it falls back to the working directory.
func (a *App) appDataDir() (string, error) {
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		// wails dev places the binary in a temp directory — detect that
		if !strings.HasPrefix(strings.ToLower(execDir), strings.ToLower(os.TempDir())) {
			return execDir, nil
		}
	}
	return os.Getwd()
}

// loadMetadata reads metadata.json from disk into a.models.
func (a *App) loadMetadata() {
	log.Debugf("metadata: loading from %s", a.metadataPath)

	data, err := os.ReadFile(a.metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debug("metadata: file does not exist, starting fresh")
			a.models = []ModelRecord{}
			return
		}
		log.Warnf("metadata: failed to read: %v", err)
		a.models = []ModelRecord{}
		return
	}

	var models []ModelRecord
	if err := json.Unmarshal(data, &models); err != nil {
		log.Warnf("metadata: failed to parse: %v", err)
		a.models = []ModelRecord{}
		return
	}

	a.models = models
	log.Infof("metadata: loaded %d model record(s)", len(a.models))
}

// saveMetadata atomically writes a.models to metadata.json.
func (a *App) saveMetadata() error {
	log.Debugf("metadata: saving %d record(s)", len(a.models))

	data, err := json.MarshalIndent(a.models, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Write to tmp file, then rename for atomicity
	tmpPath := a.metadataPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata tmp: %w", err)
	}
	if err := os.Rename(tmpPath, a.metadataPath); err != nil {
		return fmt.Errorf("failed to rename metadata: %w", err)
	}

	return nil
}

// ──────────────────────────────────────────────
// Helpers: model operations
// ──────────────────────────────────────────────

// findModel returns the index of a model by ID, or -1 if not found.
func (a *App) findModel(id string) int {
	for i, m := range a.models {
		if m.ID == id {
			return i
		}
	}
	return -1
}

// modelID generates a safe, unique model identifier from a repo ID and filename.
func modelID(repoID, filename string) string {
	s := repoID + "/" + filename
	// Replace path separators and common unsafe chars with underscore
	s = strings.NewReplacer(
		"/", "_",
		"\\", "_",
		" ", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	).Replace(s)
	return strings.ToLower(s)
}

// modelFilePath returns the full path to a model file given its ID.
func (a *App) modelFilePath(modelID string) string {
	return filepath.Join(a.modelsDir, modelID)
}

// validateFiles checks each model record's local file and updates state accordingly.
// Must be called with a.mu held (write lock).
func (a *App) validateFiles() {
	log.Debug("validateFiles: starting file validation")
	changed := false

	for i, m := range a.models {
		if m.State == "downloading" {
			// A downloading state at startup means the process was killed.
			// Mark as failed and clean up partial file.
			a.models[i].State = "failed"
			a.models[i].Error = "下载被中断（应用退出）"
			a.models[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			changed = true

			// Clean up partial file
			partialPath := a.modelFilePath(m.ID) + ".part"
			os.Remove(partialPath)
			continue
		}

		fullPath := a.modelFilePath(m.ID)
		info, err := os.Stat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				// File is missing
				if m.State != "missing" && m.State != "failed" {
					a.models[i].State = "missing"
					a.models[i].LocalPath = ""
					a.models[i].SizeBytes = 0
					a.models[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
					changed = true
				}
			}
			continue
		}

		// File exists — update size and path
		a.models[i].LocalPath = fullPath
		a.models[i].SizeBytes = info.Size()

		if m.State == "ready" {
			continue
		}

		// If the file exists but wasn't marked ready, mark it ready
		a.models[i].State = "ready"
		a.models[i].DownloadedBytes = info.Size()
		a.models[i].Error = ""
		a.models[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		changed = true
	}

	if changed {
		if err := a.saveMetadata(); err != nil {
			log.Errorf("validateFiles: failed to save metadata: %v", err)
		}
	}
	log.Debugf("validateFiles: completed, %d model(s) validated (changed=%v)", len(a.models), changed)
}

// ──────────────────────────────────────────────
// ListModels
// ──────────────────────────────────────────────

// ListModels returns all known models.
func (a *App) ListModels() []ModelRecord {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Return a copy to avoid data races
	result := make([]ModelRecord, len(a.models))
	copy(result, a.models)

	// Sort: ready first, then downloading, then failed, then missing
	sort.SliceStable(result, func(i, j int) bool {
		order := map[string]int{
			"ready":       0,
			"downloading": 1,
			"failed":      2,
			"missing":     3,
		}
		oi := order[result[i].State]
		oj := order[result[j].State]
		if oi != oj {
			return oi < oj
		}
		// Within same state, sort by display name
		return result[i].DisplayName < result[j].DisplayName
	})

	log.Debugf("ListModels: returning %d model(s)", len(result))

	return result
}

// inferModelName generates a human-readable display name from a repo and filename.
func inferModelName(repoID, filename string) string {
	parts := strings.Split(repoID, "/")
	repoName := parts[len(parts)-1]
	fileName := strings.TrimSuffix(filename, ".gguf")
	fileName = strings.TrimSuffix(fileName, ".GGUF")
	return repoName + " / " + fileName
}