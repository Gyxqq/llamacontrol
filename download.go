package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// ──────────────────────────────────────────────
// Hugging Face model downloads
// ──────────────────────────────────────────────

// StartModelDownload begins downloading a model from Hugging Face.
func (a *App) StartModelDownload(req DownloadRequest) error {
	// Validate required fields
	if req.RepoID == "" {
		return fmt.Errorf("repoId 不能为空")
	}
	if req.Filename == "" {
		return fmt.Errorf("filename 不能为空")
	}
	if req.Revision == "" {
		req.Revision = "main"
	}
	if req.DisplayName == "" {
		req.DisplayName = inferModelName(req.RepoID, req.Filename)
	}

	id := modelID(req.RepoID, req.Filename)

	a.mu.Lock()

	// Check for duplicate download
	if existing := a.findModel(id); existing >= 0 {
		m := a.models[existing]
		if m.State == "downloading" {
			a.mu.Unlock()
			return fmt.Errorf("该模型正在下载中: %s", id)
		}
	}

	// Create a cancel context for this download
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// Store download task
	a.activeDownloads[id] = &downloadTask{
		cancel:  cancel,
		modelID: id,
		done:    done,
	}

	// Create or update model record
	now := time.Now().UTC().Format(time.RFC3339)
	record := ModelRecord{
		ID:                id,
		DisplayName:       req.DisplayName,
		RepoID:            req.RepoID,
		Filename:          req.Filename,
		Revision:          req.Revision,
		LocalPath:         a.modelFilePath(id),
		SizeBytes:         0,
		DownloadedBytes:   0,
		DownloadStartedAt: now,
		State:             "downloading",
		Error:             "",
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if idx := a.findModel(id); idx >= 0 {
		// Update existing record
		record.CreatedAt = a.models[idx].CreatedAt
		a.models[idx] = record
	} else {
		a.models = append(a.models, record)
	}

	if err := a.saveMetadata(); err != nil {
		a.mu.Unlock()
		cancel()
		delete(a.activeDownloads, id)
		close(done)
		return fmt.Errorf("保存元数据失败: %w", err)
	}
	a.mu.Unlock()

	// Launch download in background
	go a.downloadModel(ctx, id, req, done)

	log.Infof("download: started %s/%s (revision=%s, id=%s)", req.RepoID, req.Filename, req.Revision, id)

	return nil
}

// downloadModel performs the actual HTTP download in a goroutine.
func (a *App) downloadModel(ctx context.Context, id string, req DownloadRequest, taskDone chan struct{}) {
	defer close(taskDone)

	// Build HF resolve URL
	downloadURL := fmt.Sprintf(
		"https://huggingface.co/%s/resolve/%s/%s",
		req.RepoID, url.QueryEscape(req.Revision), url.QueryEscape(req.Filename),
	)

	log.Infof("download: starting %s from %s", id, downloadURL)

	headers := map[string]string{}
	if req.HfToken != "" {
		headers["Authorization"] = "Bearer " + req.HfToken
	}

	// Write to .part file
	partialPath := a.modelFilePath(id) + ".part"
	finalPath := a.modelFilePath(id)

	// Download with progress tracking
	var downloaded atomic.Int64
	var totalBytes atomic.Int64
	totalBytes.Store(-1)
	var lastSave int64 // last time we saved metadata (bytes written)
	startedAt := time.Now()
	startedAtText := startedAt.UTC().Format(time.RFC3339)
	progressTicker := time.NewTicker(500 * time.Millisecond)
	defer progressTicker.Stop()

	a.mu.Lock()
	if idx := a.findModel(id); idx >= 0 {
		a.models[idx].DownloadStartedAt = startedAtText
		a.models[idx].UpdatedAt = startedAtText
		a.saveMetadata()
	}
	a.mu.Unlock()

	// Channel to signal completion
	done := make(chan error, 1)
	var result downloadResult

	go func() {
		var err error
		result, err = downloadFileAuto(ctx, a.httpClient, downloadURL, headers, partialPath, -1, func(current, total int64) {
			downloaded.Store(current)
			if total > 0 {
				totalBytes.Store(total)
			}
		})
		done <- err
	}()

	// Progress update loop
	for {
		select {
		case err := <-done:
			if err != nil {
				// Check if cancelled
				if errors.Is(err, context.Canceled) {
					os.Remove(partialPath)
					a.failDownload(id, "下载已取消")
					return
				}
				a.failDownload(id, fmt.Sprintf("下载失败: %v", err))
				return
			}

			// Download complete — atomically rename
			if err := os.Rename(partialPath, finalPath); err != nil {
				a.failDownload(id, fmt.Sprintf("重命名文件失败: %v", err))
				return
			}

			// Get actual file size
			info, _ := os.Stat(finalPath)
			currentDownloaded := downloaded.Load()
			var actualSize int64
			if info != nil {
				actualSize = info.Size()
			} else if result.DownloadedBytes > 0 {
				actualSize = result.DownloadedBytes
			} else {
				actualSize = currentDownloaded
			}

			// Update metadata
			a.mu.Lock()
			if idx := a.findModel(id); idx >= 0 {
				a.models[idx].State = "ready"
				a.models[idx].SizeBytes = actualSize
				a.models[idx].DownloadedBytes = actualSize
				a.models[idx].LocalPath = finalPath
				a.models[idx].DownloadSpeedBytesPerSecond = 0
				a.models[idx].DownloadElapsedSeconds = int64(time.Since(startedAt).Seconds())
				a.models[idx].DownloadRemainingSeconds = 0
				a.models[idx].Error = ""
				a.models[idx].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
				a.saveMetadata()
			}
			delete(a.activeDownloads, id)
			a.mu.Unlock()

			if result.Parallel {
				log.Infof("download: completed %s (%d bytes, %d workers)", id, actualSize, result.Workers)
			} else {
				log.Infof("download: completed %s (%d bytes, single stream)", id, actualSize)
			}
			return

		case <-progressTicker.C:
			currentDownloaded := downloaded.Load()
			totalSize := totalBytes.Load()
			speed, elapsed, remaining := downloadStats(currentDownloaded, totalSize, startedAt)

			// Update progress in metadata
			a.mu.Lock()
			if idx := a.findModel(id); idx >= 0 {
				a.models[idx].DownloadedBytes = currentDownloaded
				a.models[idx].DownloadSpeedBytesPerSecond = speed
				a.models[idx].DownloadElapsedSeconds = elapsed
				a.models[idx].DownloadRemainingSeconds = remaining
				if totalSize > 0 {
					a.models[idx].SizeBytes = totalSize
				}
				a.models[idx].UpdatedAt = time.Now().UTC().Format(time.RFC3339)

				if currentDownloaded-lastSave > 100*1024 { // Save every 100KB
					a.saveMetadata()
					lastSave = currentDownloaded
				}
			}
			a.mu.Unlock()
		}
	}
}

// failDownload marks a download as failed and cleans up.
func (a *App) failDownload(id string, errMsg string) {
	log.Errorf("download: failed %s: %s", id, errMsg)

	// Clean up partial file
	partialPath := filepath.Join(a.modelsDir, id+".part")
	os.Remove(partialPath)

	a.mu.Lock()
	defer a.mu.Unlock()

	if idx := a.findModel(id); idx >= 0 {
		a.models[idx].State = "failed"
		a.models[idx].Error = errMsg
		a.models[idx].DownloadSpeedBytesPerSecond = 0
		a.models[idx].DownloadRemainingSeconds = 0
		a.models[idx].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		a.saveMetadata()
	}

	delete(a.activeDownloads, id)
}

// CancelModelDownload cancels an ongoing download.
func (a *App) CancelModelDownload(modelId string) error {
	a.mu.Lock()
	task, exists := a.activeDownloads[modelId]
	if !exists {
		// Check if it's already in a non-downloading state
		if idx := a.findModel(modelId); idx >= 0 {
			if a.models[idx].State != "downloading" {
				a.mu.Unlock()
				return fmt.Errorf("该模型当前没有进行中的下载")
			}
		}
		a.mu.Unlock()
		return fmt.Errorf("未找到下载任务: %s", modelId)
	}
	a.mu.Unlock()

	// Cancel the context
	task.cancel()
	<-task.done
	log.Infof("download: cancelled %s", modelId)

	return nil
}
