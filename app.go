package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"archive/zip"

	"github.com/bodgit/sevenzip"
)

// ──────────────────────────────────────────────
// App struct
// ──────────────────────────────────────────────

type App struct {
	ctx          context.Context
	httpClient   *http.Client
	modelsDir    string
	binDir       string
	metadataPath string
	mu           sync.RWMutex

	// Model metadata (persisted to metadata.json)
	models []ModelRecord

	// Active downloads (in-memory only)
	activeDownloads map[string]*downloadTask

	// llama-server process state (in-memory only)
	serverState *serverState

	// GitHub releases cache
	releasesCache     []LlamaServerRelease
	releasesCachedAt  time.Time
}

// downloadTask tracks an in-flight download for cancellation
type downloadTask struct {
	cancel  context.CancelFunc
	modelID string
}

// serverState tracks the llama-server subprocess
type serverState struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	status ServerStatus
}

// ──────────────────────────────────────────────
// Types matching frontend TypeScript definitions
// ──────────────────────────────────────────────

// ModelRecord represents a downloaded or downloading model
type ModelRecord struct {
	ID              string `json:"id"`
	DisplayName     string `json:"displayName"`
	RepoID          string `json:"repoId"`
	Filename        string `json:"filename"`
	Revision        string `json:"revision"`
	LocalPath       string `json:"localPath"`
	SizeBytes       int64  `json:"sizeBytes"`
	DownloadedBytes int64  `json:"downloadedBytes"`
	State           string `json:"state"` // "missing" | "downloading" | "ready" | "failed"
	Error           string `json:"error"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// DownloadRequest is sent by the frontend to start a model download
type DownloadRequest struct {
	RepoID      string `json:"repoId"`
	Filename    string `json:"filename"`
	Revision    string `json:"revision"`
	DisplayName string `json:"displayName"`
	HfToken     string `json:"hfToken"`
}

// ServerConfig configures the llama-server process
type ServerConfig struct {
	ModelID        string `json:"modelId"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	CtxSize        int    `json:"ctxSize"`
	GPULayers      int    `json:"gpuLayers"`
	Threads        int    `json:"threads"`
	BatchSize      int    `json:"batchSize"`
	UbatchSize     int    `json:"ubatchSize"`
	Parallel       int    `json:"parallel"`
	FlashAttention bool   `json:"flashAttention"`
	Background     bool   `json:"background"`
	ExtraArgs      string `json:"extraArgs"`
}

// ServerStatus reports the current llama-server state
type ServerStatus struct {
	Running     bool     `json:"running"`
	PID         int      `json:"pid"`
	Endpoint    string   `json:"endpoint"`
	ModelID     string   `json:"modelId"`
	ModelName   string   `json:"modelName"`
	StartedAt   string   `json:"startedAt"`
	CommandLine string   `json:"commandLine"`
	LogTail     []string `json:"logTail"`
}

// ──────────────────────────────────────────────
// llama.cpp release management types
// ──────────────────────────────────────────────

// LlamaServerInfo reports whether llama-server is available.
type LlamaServerInfo struct {
	Found   bool   `json:"found"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

// LlamaServerRelease represents a llama.cpp GitHub release for the frontend.
type LlamaServerRelease struct {
	TagName     string `json:"tagName"`
	Name        string `json:"name"`
	PublishedAt string `json:"publishedAt"`
}

// LlamaReleaseAsset represents a downloadable file in a llama.cpp release.
type LlamaReleaseAsset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"downloadUrl"`
}

// ghReleaseAsset is a single asset in a GitHub release.
type ghReleaseAsset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`
}

// ghRelease is the raw GitHub API response shape.
type ghRelease struct {
	TagName     string           `json:"tag_name"`
	Name        string           `json:"name"`
	PublishedAt string           `json:"published_at"`
	Assets      []ghReleaseAsset `json:"assets"`
}

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

	log.Infof("startup: ready, %d model(s) in metadata", len(a.models))
}

// ──────────────────────────────────────────────
// Helpers: metadata persistence
// ──────────────────────────────────────────────

// appDataDir returns a data directory relative to the current working directory,
// so the app is portable and can be moved freely.
func (a *App) appDataDir() (string, error) {
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

	// Store download task
	a.activeDownloads[id] = &downloadTask{
		cancel:  cancel,
		modelID: id,
	}

	// Create or update model record
	now := time.Now().UTC().Format(time.RFC3339)
	record := ModelRecord{
		ID:              id,
		DisplayName:     req.DisplayName,
		RepoID:          req.RepoID,
		Filename:        req.Filename,
		Revision:        req.Revision,
		LocalPath:       a.modelFilePath(id),
		SizeBytes:       0,
		DownloadedBytes: 0,
		State:           "downloading",
		Error:           "",
		CreatedAt:       now,
		UpdatedAt:       now,
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
		return fmt.Errorf("保存元数据失败: %w", err)
	}
	a.mu.Unlock()

	// Launch download in background
	go a.downloadModel(ctx, id, req)

	log.Infof("download: started %s/%s (revision=%s, id=%s)", req.RepoID, req.Filename, req.Revision, id)

	return nil
}

// downloadModel performs the actual HTTP download in a goroutine.
func (a *App) downloadModel(ctx context.Context, id string, req DownloadRequest) {
	// Build HF resolve URL
	downloadURL := fmt.Sprintf(
		"https://huggingface.co/%s/resolve/%s/%s",
		req.RepoID, url.QueryEscape(req.Revision), url.QueryEscape(req.Filename),
	)

	log.Infof("download: starting %s from %s", id, downloadURL)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		a.failDownload(id, fmt.Sprintf("创建请求失败: %v", err))
		return
	}

	httpReq.Header.Set("User-Agent", "llamacontrol/1.0")

	// Add auth token for private models
	if req.HfToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.HfToken)
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			a.failDownload(id, "下载已取消")
			return
		}
		a.failDownload(id, fmt.Sprintf("HTTP 请求失败: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		a.failDownload(id, fmt.Sprintf("Hugging Face 返回 %d: %s", resp.StatusCode, string(body)))
		return
	}

	// Determine total size from Content-Length
	var totalSize int64 = -1
	if resp.ContentLength > 0 {
		totalSize = resp.ContentLength
	}

	// Write to .part file
	partialPath := a.modelFilePath(id) + ".part"
	finalPath := a.modelFilePath(id)

	outFile, err := os.Create(partialPath)
	if err != nil {
		a.failDownload(id, fmt.Sprintf("创建临时文件失败: %v", err))
		return
	}
	defer outFile.Close()

	// Download with progress tracking
	buf := make([]byte, 32*1024) // 32KB buffer
	var downloaded int64
	var lastSave int64 // last time we saved metadata (bytes written)
	progressTicker := time.NewTicker(500 * time.Millisecond)
	defer progressTicker.Stop()

	// Channel to signal completion
	done := make(chan error, 1)

	go func() {
		for {
			select {
			case <-ctx.Done():
				// Cancelled — close and clean up
				outFile.Close()
				os.Remove(partialPath)
				done <- ctx.Err()
				return
			default:
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					if _, writeErr := outFile.Write(buf[:n]); writeErr != nil {
						done <- fmt.Errorf("写入文件失败: %w", writeErr)
						return
					}
					downloaded += int64(n)
				}
				if readErr != nil {
					if readErr == io.EOF {
						done <- nil
					} else {
						done <- readErr
					}
					return
				}
			}
		}
	}()

	// Progress update loop
	for {
		select {
		case err := <-done:
			if err != nil {
				// Check if cancelled
				if errors.Is(err, context.Canceled) {
					a.failDownload(id, "下载已取消")
					return
				}
				a.failDownload(id, fmt.Sprintf("下载失败: %v", err))
				return
			}

			// Download complete — atomically rename
			outFile.Close()

			if err := os.Rename(partialPath, finalPath); err != nil {
				a.failDownload(id, fmt.Sprintf("重命名文件失败: %v", err))
				return
			}

			// Get actual file size
			info, _ := os.Stat(finalPath)
			var actualSize int64
			if info != nil {
				actualSize = info.Size()
			} else {
				actualSize = downloaded
			}

			// Update metadata
			a.mu.Lock()
			if idx := a.findModel(id); idx >= 0 {
				a.models[idx].State = "ready"
				a.models[idx].SizeBytes = actualSize
				a.models[idx].DownloadedBytes = actualSize
				a.models[idx].LocalPath = finalPath
				a.models[idx].Error = ""
				a.models[idx].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
				a.saveMetadata()
			}
			delete(a.activeDownloads, id)
			a.mu.Unlock()

			log.Infof("download: completed %s (%d bytes)", id, actualSize)
			return

		case <-progressTicker.C:
			// Update progress in metadata
			if downloaded-lastSave > 100*1024 { // Save every 100KB
				a.mu.Lock()
				if idx := a.findModel(id); idx >= 0 {
					a.models[idx].DownloadedBytes = downloaded
					if totalSize > 0 {
						a.models[idx].SizeBytes = totalSize
					}
					a.models[idx].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
					a.saveMetadata()
				}
				a.mu.Unlock()
				lastSave = downloaded
			}
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
	log.Infof("download: cancelled %s", modelId)

	return nil
}

// inferModelName generates a human-readable display name from a repo and filename.
func inferModelName(repoID, filename string) string {
	parts := strings.Split(repoID, "/")
	repoName := parts[len(parts)-1]
	fileName := strings.TrimSuffix(filename, ".gguf")
	fileName = strings.TrimSuffix(fileName, ".GGUF")
	return repoName + " / " + fileName
}

// ──────────────────────────────────────────────
// Model file operations
// ──────────────────────────────────────────────

// DeleteModel removes a local model file and its metadata.
func (a *App) DeleteModel(modelId string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	idx := a.findModel(modelId)
	if idx < 0 {
		return fmt.Errorf("未找到模型: %s", modelId)
	}

	// Check if model is currently being served
	a.serverState.mu.Lock()
	if a.serverState.status.Running && a.serverState.status.ModelID == modelId {
		a.serverState.mu.Unlock()
		return fmt.Errorf("该模型正在被 llama-server 使用，请先停止服务")
	}
	a.serverState.mu.Unlock()

	// Cancel active download if any
	if task, exists := a.activeDownloads[modelId]; exists {
		task.cancel()
		delete(a.activeDownloads, modelId)
	}

	// Delete the local file
	modelPath := a.modelFilePath(modelId)
	if err := os.Remove(modelPath); err != nil && !os.IsNotExist(err) {
		log.Warnf("delete: failed to remove file %s: %v", modelPath, err)
	}

	// Also clean up any .part file
	partialPath := modelPath + ".part"
	os.Remove(partialPath)

	// Remove from metadata
	a.models = append(a.models[:idx], a.models[idx+1:]...)

	if err := a.saveMetadata(); err != nil {
		return fmt.Errorf("保存元数据失败: %w", err)
	}

	log.Infof("delete: removed model %s", modelId)
	return nil
}

// OpenModelsDir opens the models directory in the system file manager.
func (a *App) OpenModelsDir() error {
	log.Debugf("OpenModelsDir: opening %s", a.modelsDir)
	if a.modelsDir == "" {
		return fmt.Errorf("模型目录未初始化")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", a.modelsDir)
	case "darwin":
		cmd = exec.Command("open", a.modelsDir)
	default:
		cmd = exec.Command("xdg-open", a.modelsDir)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("打开目录失败: %w", err)
	}

	// Don't wait — just fire and forget
	go func() {
		cmd.Wait()
	}()

	return nil
}

// ──────────────────────────────────────────────
// llama-server process management
// ──────────────────────────────────────────────

// findLlamaServer locates the llama-server binary.
func findLlamaServer() (string, error) {
	log.Debug("server: searching for llama-server binary")

	// Check env override first
	if path := os.Getenv("LLAMACONTROL_LLAMA_SERVER_PATH"); path != "" {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
		return "", fmt.Errorf("LLAMACONTROL_LLAMA_SERVER_PATH 指向的文件不存在: %s", path)
	}

	// Search PATH
	path, err := exec.LookPath("llama-server")
	if err != nil {
		// Try common locations
		commonPaths := []string{
			"llama-server.exe",
			"llama-server",
		}
		for _, p := range commonPaths {
			if found, err := exec.LookPath(p); err == nil {
				return found, nil
			}
		}
		return "", fmt.Errorf("未找到 llama-server，请确保它在 PATH 中，或设置 LLAMACONTROL_LLAMA_SERVER_PATH 环境变量")
	}

	return path, nil
}

// locateLlamaServer checks binDir first, then falls back to PATH / env var.
func (a *App) locateLlamaServer() (string, error) {
	// 1. Check binDir first (auto-downloaded binary)
	for _, name := range []string{"llama-server", "llama-server.exe"} {
		binPath := filepath.Join(a.binDir, name)
		if info, err := os.Stat(binPath); err == nil && !info.IsDir() {
			log.Debugf("server: found in binary directory: %s", binPath)
			return binPath, nil
		}
	}

	// 2. Fall back to PATH / env var
	return findLlamaServer()
}

// readInstalledVersion reads the version file from binDir.
func (a *App) readInstalledVersion() string {
	versionPath := filepath.Join(a.binDir, "version.txt")
	data, err := os.ReadFile(versionPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writeInstalledVersion writes the version file to binDir.
func (a *App) writeInstalledVersion(version string) error {
	versionPath := filepath.Join(a.binDir, "version.txt")
	return os.WriteFile(versionPath, []byte(version), 0644)
}

// GetLlamaServerInfo returns whether llama-server is available.
func (a *App) GetLlamaServerInfo() LlamaServerInfo {
	path, err := a.locateLlamaServer()
	if err != nil {
		return LlamaServerInfo{
			Found:   false,
			Version: a.readInstalledVersion(),
		}
	}
	return LlamaServerInfo{
		Found:   true,
		Path:    path,
		Version: a.readInstalledVersion(),
	}
}

// validateServerConfig checks that the server config is reasonable.
func validateServerConfig(config ServerConfig) error {
	if config.Host == "" {
		return fmt.Errorf("host 不能为空")
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("端口号必须在 1-65535 之间")
	}
	if config.CtxSize < 128 {
		return fmt.Errorf("上下文大小不能小于 128")
	}
	if config.GPULayers < 0 {
		return fmt.Errorf("GPU 层数不能为负数")
	}
	if config.Threads < 1 {
		return fmt.Errorf("线程数不能小于 1")
	}
	if config.BatchSize < 1 {
		return fmt.Errorf("batch size 不能小于 1")
	}
	if config.UbatchSize < 1 {
		return fmt.Errorf("ubatch size 不能小于 1")
	}
	if config.Parallel < 1 {
		return fmt.Errorf("parallel 不能小于 1")
	}

	// Check extra args for shell injection characters
	if config.ExtraArgs != "" {
		disallowed := []string{"|", ";", "&", "$", "`", "(", ")", "{", "}", "<", ">"}
		for _, ch := range disallowed {
			if strings.Contains(config.ExtraArgs, ch) {
				return fmt.Errorf("额外参数中包含非法字符: %s", ch)
			}
		}
	}

	return nil
}

// buildServerArgs constructs the argument slice for llama-server.
func buildServerArgs(modelPath string, config ServerConfig) []string {
	args := []string{
		"-m", modelPath,
		"--host", config.Host,
		"--port", strconv.Itoa(config.Port),
		"-c", strconv.Itoa(config.CtxSize),
		"-ngl", strconv.Itoa(config.GPULayers),
		"-t", strconv.Itoa(config.Threads),
		"-b", strconv.Itoa(config.BatchSize),
		"-ub", strconv.Itoa(config.UbatchSize),
		"-np", strconv.Itoa(config.Parallel),
	}

	if config.FlashAttention {
		args = append(args, "-fa")
	}

	// Append extra args (split by whitespace)
	if config.ExtraArgs != "" {
		extra := strings.Fields(config.ExtraArgs)
		args = append(args, extra...)
	}

	return args
}

// GetServerStatus returns the llama-server process status.
func (a *App) GetServerStatus() ServerStatus {
	a.serverState.mu.Lock()
	defer a.serverState.mu.Unlock()

	// Return a copy
	status := a.serverState.status
	if status.LogTail == nil {
		status.LogTail = []string{}
	} else {
		logTail := make([]string, len(status.LogTail))
		copy(logTail, status.LogTail)
		status.LogTail = logTail
	}

	log.Debugf("GetServerStatus: running=%v pid=%d", status.Running, status.PID)
	return status
}

// StartLlamaServer starts the llama-server subprocess.
func (a *App) StartLlamaServer(config ServerConfig) error {
	// Validate config
	if err := validateServerConfig(config); err != nil {
		return err
	}

	// Find the llama-server binary
		serverPath, err := a.locateLlamaServer()
	if err != nil {
		return err
	}

	a.mu.RLock()
	// Find the model
	idx := a.findModel(config.ModelID)
	if idx < 0 {
		a.mu.RUnlock()
		return fmt.Errorf("未找到模型: %s", config.ModelID)
	}

	model := a.models[idx]
	if model.State != "ready" {
		a.mu.RUnlock()
		return fmt.Errorf("模型尚未就绪 (状态: %s)", model.State)
	}

	modelPath := model.LocalPath
	modelName := model.DisplayName
	a.mu.RUnlock()

	// Check that model file exists
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("模型文件不存在: %s", modelPath)
	}

	a.serverState.mu.Lock()
	defer a.serverState.mu.Unlock()

	// Check if server is already running
	if a.serverState.status.Running {
		return fmt.Errorf("llama-server 已在运行中 (PID: %d)", a.serverState.status.PID)
	}

	// Build args
	args := buildServerArgs(modelPath, config)

	cmdStr := serverPath + " " + strings.Join(args, " ")
	log.Infof("server: starting: %s", cmdStr)

	// Create the command
	cmd := exec.Command(serverPath, args...)

	// Capture stdout/stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建 stdout pipe 失败: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("创建 stderr pipe 失败: %w", err)
	}

	// Bounded log ring buffer (max 100 lines)
	logTail := make([]string, 0, 100)
	var logMu sync.Mutex
	addLog := func(line string) {
		logMu.Lock()
		defer logMu.Unlock()
		if len(logTail) >= 100 {
			logTail = logTail[1:]
		}
		logTail = append(logTail, line)
	}

	// Read stdout in background
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			log.Infoln("[llama-server] stdout:", line)
			addLog(line)
		}
	}()

	// Read stderr in background
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			log.Infoln("[llama-server] stderr:", line)
			addLog(line)
		}
	}()

	// Start the process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 llama-server 失败: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	host := config.Host
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	endpoint := fmt.Sprintf("http://%s:%d", host, config.Port)

	// Update status
	a.serverState.cmd = cmd
	a.serverState.status = ServerStatus{
		Running:     true,
		PID:         cmd.Process.Pid,
		Endpoint:    endpoint,
		ModelID:     config.ModelID,
		ModelName:   modelName,
		StartedAt:   now,
		CommandLine: cmdStr,
		LogTail:     logTail,
	}

	// Monitor process exit in background
	go func() {
		err := cmd.Wait()
		log.Infof("server: process exited: %v", err)

		a.serverState.mu.Lock()
		defer a.serverState.mu.Unlock()

		// Capture remaining logs
		logMu.Lock()
		a.serverState.status.LogTail = make([]string, len(logTail))
		copy(a.serverState.status.LogTail, logTail)
		logMu.Unlock()

		a.serverState.status.Running = false
		a.serverState.cmd = nil
	}()

	return nil
}

// StopLlamaServer stops the llama-server subprocess gracefully.
func (a *App) StopLlamaServer() error {
	a.serverState.mu.Lock()
	defer a.serverState.mu.Unlock()

	if !a.serverState.status.Running || a.serverState.cmd == nil {
		return fmt.Errorf("llama-server 未在运行")
	}

	pid := a.serverState.status.PID
	log.Infof("server: stopping llama-server (PID: %d)", pid)

	// Try graceful shutdown with SIGTERM
	if err := a.serverState.cmd.Process.Signal(os.Interrupt); err != nil {
		log.Warnf("server: interrupt failed: %v, trying kill", err)
		if err := a.serverState.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("终止进程失败: %w", err)
		}
	}

	// Wait up to 5 seconds for graceful exit
	done := make(chan struct{})
	go func() {
		a.serverState.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Infof("server: stopped gracefully (PID: %d)", pid)
	case <-time.After(5 * time.Second):
		log.Warnf("server: graceful shutdown timeout, killing (PID: %d)", pid)
		if err := a.serverState.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("强制终止进程失败: %w", err)
		}
		a.serverState.cmd.Wait()
	}

	// Update status
	a.serverState.status.Running = false
	a.serverState.status.PID = 0
	a.serverState.cmd = nil

	return nil
}

// ──────────────────────────────────────────────
// Hugging Face API (existing, kept as-is)
// ──────────────────────────────────────────────

// HuggingFaceModel represents a model returned by the HF API search
type HuggingFaceModel struct {
	ID          string `json:"id"`
	Downloads   int    `json:"downloads"`
	Description string `json:"description"`
}

// hfModelRaw is the raw response shape from the Hugging Face API
type hfModelRaw struct {
	ID          string `json:"id"`
	Downloads   int    `json:"downloads"`
	Description string `json:"description"`
	PipelineTag string `json:"pipeline_tag"`
	LibraryName string `json:"library_name"`
}

// SearchHuggingFaceModels searches Hugging Face for GGUF model repos.
// The HF API search parameter does server-side full-text matching; we
// embed "gguf" in the query so only GGUF-related models are returned.
func (a *App) SearchHuggingFaceModels(query string) ([]HuggingFaceModel, error) {
	log.Debugf("hf: searching models with query=%q", query)
	params := url.Values{}
	params.Set("search", query+" gguf")
	params.Set("sort", "downloads")
	params.Set("direction", "-1")
	params.Set("limit", "30")

	apiURL := fmt.Sprintf("https://huggingface.co/api/models?%s", params.Encode())

	req, err := http.NewRequestWithContext(a.ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "llamacontrol/1.0")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from Hugging Face: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Hugging Face API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw []hfModelRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Deduplicate by model ID
	seen := make(map[string]bool)
	models := make([]HuggingFaceModel, 0, len(raw))

	for _, m := range raw {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true

		models = append(models, HuggingFaceModel{
			ID:          m.ID,
			Downloads:   m.Downloads,
			Description: truncateString(m.Description, 200),
		})
	}

	return models, nil
}

// fileTreeEntry is a single entry from the HF API file tree
type fileTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int    `json:"size"`
}

// ListModelGguFiles lists all .gguf files in a Hugging Face model repo
func (a *App) ListModelGguFiles(repoId string) ([]string, error) {
	log.Debugf("hf: listing GGUF files for repo=%s", repoId)
	apiURL := fmt.Sprintf("https://huggingface.co/api/models/%s/tree/main?recursive=1", repoId)

	req, err := http.NewRequestWithContext(a.ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "llamacontrol/1.0")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch file list: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Hugging Face API returned %d: %s", resp.StatusCode, string(body))
	}

	var entries []fileTreeEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse file tree: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if entry.Type == "file" && strings.HasSuffix(strings.ToLower(entry.Path), ".gguf") {
			files = append(files, entry.Path)
		}
	}

	return files, nil
}

// ──────────────────────────────────────────────
// llama.cpp release management
// ──────────────────────────────────────────────

// platformAssetMatch returns true if the asset name matches the current OS.
func platformAssetMatch(name string) bool {
	name = strings.ToLower(name)
	switch runtime.GOOS {
	case "windows":
		return strings.Contains(name, "win") &&
			(strings.HasSuffix(name, ".7z") || strings.HasSuffix(name, ".zip"))
	case "darwin":
		return (strings.Contains(name, "macos") || strings.Contains(name, "apple")) &&
			(strings.HasSuffix(name, ".7z") || strings.HasSuffix(name, ".zip"))
	default: // linux
		return (strings.Contains(name, "ubuntu") || strings.Contains(name, "linux")) &&
			(strings.HasSuffix(name, ".7z") || strings.HasSuffix(name, ".zip"))
	}
}

// ListLlamaServerReleases fetches available llama.cpp releases from GitHub.
// Results are filtered to only include releases with assets for the current platform.
// Results are cached in memory for 5 minutes.
func (a *App) ListLlamaServerReleases() ([]LlamaServerRelease, error) {
	log.Debug("llama: listing releases from GitHub")

	// Check cache (5 min TTL)
	if a.releasesCache != nil && time.Since(a.releasesCachedAt) < 5*time.Minute {
		log.Debug("llama: returning cached releases")
		return a.releasesCache, nil
	}

	apiURL := "https://api.github.com/repos/ggml-org/llama.cpp/releases?per_page=20"

	req, err := http.NewRequestWithContext(a.ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("User-Agent", "llamacontrol/1.0")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 返回 %d", resp.StatusCode)
	}

	var raw []ghRelease
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	var releases []LlamaServerRelease
	for _, r := range raw {
		// Skip if no platform-matching asset
		hasMatch := false
		for _, asset := range r.Assets {
			if platformAssetMatch(asset.Name) {
				hasMatch = true
				break
			}
		}
		if !hasMatch {
			continue
		}

		releases = append(releases, LlamaServerRelease{
			TagName:     r.TagName,
			Name:        strings.TrimSpace(r.Name),
			PublishedAt: r.PublishedAt,
		})
	}

	// Update cache
	a.releasesCache = releases
	a.releasesCachedAt = time.Now()

	log.Infof("llama: found %d compatible release(s)", len(releases))
	return releases, nil
}

// ListLlamaReleaseAssets fetches all platform-compatible assets for a specific release tag.
func (a *App) ListLlamaReleaseAssets(releaseTag string) ([]LlamaReleaseAsset, error) {
	log.Debugf("llama: listing assets for release %s", releaseTag)

	apiURL := fmt.Sprintf("https://api.github.com/repos/ggml-org/llama.cpp/releases/tags/%s", url.PathEscape(releaseTag))

	req, err := http.NewRequestWithContext(a.ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("User-Agent", "llamacontrol/1.0")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 返回 %d", resp.StatusCode)
	}

	var release ghRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	var assets []LlamaReleaseAsset
	for _, asset := range release.Assets {
		if platformAssetMatch(asset.Name) {
			assets = append(assets, LlamaReleaseAsset{
				Name:        asset.Name,
				Size:        asset.Size,
				DownloadURL: asset.DownloadURL,
			})
		}
	}

	if len(assets) == 0 {
		return nil, fmt.Errorf("未找到适用于当前平台 (%s) 的发布文件", runtime.GOOS)
	}

	log.Infof("llama: found %d asset(s) for release %s", len(assets), releaseTag)
	return assets, nil
}

// DownloadLlamaServerRelease downloads a llama.cpp release, extracts the
// llama-server binary, and places it in the bin directory.
func (a *App) DownloadLlamaServerRelease(releaseTag string, assetName string) error {
	log.Infof("llama: downloading release %s, asset %s", releaseTag, assetName)

	// Fetch the specific release
	apiURL := fmt.Sprintf("https://api.github.com/repos/ggml-org/llama.cpp/releases/tags/%s", url.PathEscape(releaseTag))

	req, err := http.NewRequestWithContext(a.ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("User-Agent", "llamacontrol/1.0")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API 返回 %d", resp.StatusCode)
	}

	var release ghRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	// Find the specific asset by name (user-selected)
	var bestAsset *ghReleaseAsset
	for i, asset := range release.Assets {
		if asset.Name == assetName {
			bestAsset = &release.Assets[i]
			break
		}
	}

	if bestAsset == nil {
		return fmt.Errorf("未找到发布文件: %s (适用于 %s)", assetName, runtime.GOOS)
	}

	log.Infof("llama: downloading asset: %s (%d bytes)", bestAsset.Name, bestAsset.Size)

	// Download the archive to a temp file
	tempDir, err := os.MkdirTemp("", "llamacontrol-llama-*")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tempDir)

	archivePath := filepath.Join(tempDir, bestAsset.Name)

	dlReq, err := http.NewRequestWithContext(a.ctx, http.MethodGet, bestAsset.DownloadURL, nil)
	if err != nil {
		return fmt.Errorf("创建下载请求失败: %w", err)
	}
	dlReq.Header.Set("User-Agent", "llamacontrol/1.0")
	dlReq.Header.Set("Accept", "application/octet-stream")

	dlResp, err := a.httpClient.Do(dlReq)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK && dlResp.StatusCode != http.StatusFound {
		return fmt.Errorf("下载返回 %d", dlResp.StatusCode)
	}

	outFile, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}

	written, err := io.Copy(outFile, dlResp.Body)
	outFile.Close()
	if err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}
	log.Infof("llama: downloaded %d bytes to %s", written, archivePath)

	// Extract the archive
	extractDir := filepath.Join(tempDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("创建解压目录失败: %w", err)
	}

	log.Debugf("llama: extracting %s to %s", archivePath, extractDir)

	if err := extractArchive(archivePath, extractDir); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}

	// Find llama-server binary in extracted files
	serverName := "llama-server"
	if runtime.GOOS == "windows" {
		serverName = "llama-server.exe"
	}

	var serverPath string
	filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.EqualFold(filepath.Base(path), serverName) {
			serverPath = path
			return filepath.SkipAll
		}
		return nil
	})

	if serverPath == "" {
		// Fallback: search for any executable with "server" in name
		filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.Contains(strings.ToLower(filepath.Base(path)), "server") {
				serverPath = path
				return filepath.SkipAll
			}
			return nil
		})
	}

	if serverPath == "" {
		return fmt.Errorf("解压后未找到 llama-server 可执行文件")
	}

	// Copy to binDir
	destPath := filepath.Join(a.binDir, serverName)
	log.Infof("llama: installing %s -> %s", serverPath, destPath)

	srcFile, err := os.Open(serverPath)
	if err != nil {
		return fmt.Errorf("打开源文件失败: %w", err)
	}
	defer srcFile.Close()

	destFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		return fmt.Errorf("复制文件失败: %w", err)
	}

	// Make executable on Unix
	if runtime.GOOS != "windows" {
		os.Chmod(destPath, 0755)
	}

	// Save version
	if err := a.writeInstalledVersion(releaseTag); err != nil {
		log.Warnf("llama: failed to write version file: %v", err)
	}

	// Clear the releases cache so next listing reflects installed state
	a.releasesCache = nil

	log.Infof("llama: successfully installed %s", releaseTag)
	return nil
}

// extractArchive extracts a .7z or .zip archive to the destination directory.
func extractArchive(archivePath, destDir string) error {
	ext := strings.ToLower(filepath.Ext(archivePath))
	switch ext {
	case ".7z":
		r, err := sevenzip.OpenReader(archivePath)
		if err != nil {
			return fmt.Errorf("打开 7z 文件失败: %w", err)
		}
		defer r.Close()

		for _, f := range r.File {
			fpath := filepath.Join(destDir, f.Name)

			if f.FileInfo().IsDir() {
				if err := os.MkdirAll(fpath, 0755); err != nil {
					return err
				}
				continue
			}

			// Create parent directories
			if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
				return err
			}

			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("打开 7z 内文件 %s 失败: %w", f.Name, err)
			}

			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				rc.Close()
				return fmt.Errorf("创建文件 %s 失败: %w", fpath, err)
			}

			_, err = io.Copy(outFile, rc)
			rc.Close()
			outFile.Close()
			if err != nil {
				return fmt.Errorf("写入文件 %s 失败: %w", fpath, err)
			}
		}
		return nil

	case ".zip":
		r, err := zip.OpenReader(archivePath)
		if err != nil {
			return fmt.Errorf("打开 zip 文件失败: %w", err)
		}
		defer r.Close()

		for _, f := range r.File {
			fpath := filepath.Join(destDir, f.Name)

			if f.FileInfo().IsDir() {
				if err := os.MkdirAll(fpath, 0755); err != nil {
					return err
				}
				continue
			}

			if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
				return err
			}

			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("打开 zip 内文件 %s 失败: %w", f.Name, err)
			}

			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				rc.Close()
				return fmt.Errorf("创建文件 %s 失败: %w", fpath, err)
			}

			_, err = io.Copy(outFile, rc)
			rc.Close()
			outFile.Close()
			if err != nil {
				return fmt.Errorf("写入文件 %s 失败: %w", fpath, err)
			}
		}
		return nil

	default:
		return fmt.Errorf("不支持的压缩格式: %s (仅支持 .7z 和 .zip)", ext)
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}