package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

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
		if platformAssetMatch(asset.Name) && !isCudaCrtAsset(asset.Name) {
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

// DownloadLlamaServerRelease starts downloading a llama.cpp release in background.
// The frontend polls GetLlamaServerDownloadProgress for progress updates.
func (a *App) DownloadLlamaServerRelease(releaseTag string, assetName string) error {
	if releaseTag == "" || assetName == "" {
		return fmt.Errorf("版本和加速类型不能为空")
	}

	a.mu.Lock()
	if a.llamaDlProgress.Downloading {
		a.mu.Unlock()
		return fmt.Errorf("已有下载任务进行中")
	}

	// Initialize progress
	startedAt := time.Now()
	a.llamaDlProgress = LlamaServerDownloadProgress{
		Downloading:       true,
		ReleaseTag:        releaseTag,
		AssetName:         assetName,
		DownloadStartedAt: startedAt.UTC().Format(time.RFC3339),
	}
	a.mu.Unlock()

	// Launch in background
	go a.downloadLlamaServerRelease(releaseTag, assetName)

	log.Infof("llama: started background download of release %s, asset %s", releaseTag, assetName)
	return nil
}

// downloadLlamaServerRelease performs the actual download, extraction, and installation.
func (a *App) downloadLlamaServerRelease(releaseTag string, assetName string) {
	log.Infof("llama: downloading release %s, asset %s", releaseTag, assetName)

	// Fetch the specific release from GitHub API
	apiURL := fmt.Sprintf("https://api.github.com/repos/ggml-org/llama.cpp/releases/tags/%s", url.PathEscape(releaseTag))

	req, err := http.NewRequestWithContext(a.ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		a.failLlamaDownload(fmt.Sprintf("创建请求失败: %v", err))
		return
	}

	req.Header.Set("User-Agent", "llamacontrol/1.0")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.failLlamaDownload(fmt.Sprintf("请求 GitHub API 失败: %v", err))
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		a.failLlamaDownload(fmt.Sprintf("读取响应失败: %v", err))
		return
	}

	if resp.StatusCode != http.StatusOK {
		a.failLlamaDownload(fmt.Sprintf("GitHub API 返回 %d", resp.StatusCode))
		return
	}

	var release ghRelease
	if err := json.Unmarshal(body, &release); err != nil {
		a.failLlamaDownload(fmt.Sprintf("解析响应失败: %v", err))
		return
	}

	// Find the specific asset by name
	var bestAsset *ghReleaseAsset
	for i, asset := range release.Assets {
		if asset.Name == assetName {
			bestAsset = &release.Assets[i]
			break
		}
	}

	if bestAsset == nil {
		a.failLlamaDownload(fmt.Sprintf("未找到发布文件: %s (适用于 %s)", assetName, runtime.GOOS))
		return
	}

	log.Infof("llama: downloading asset: %s (%d bytes)", bestAsset.Name, bestAsset.Size)

	// Set total size for progress tracking
	totalSize := bestAsset.Size
	if totalSize <= 0 {
		totalSize = resp.ContentLength
	}
	a.mu.Lock()
	a.llamaDlProgress.TotalBytes = totalSize
	a.mu.Unlock()

	// Download the archive to a temp file
	tempDir, err := os.MkdirTemp("", "llamacontrol-llama-*")
	if err != nil {
		a.failLlamaDownload(fmt.Sprintf("创建临时目录失败: %v", err))
		return
	}
	defer os.RemoveAll(tempDir)

	archivePath := filepath.Join(tempDir, bestAsset.Name)

	startedAt := time.Now()

	a.mu.Lock()
	a.llamaDlProgress.DownloadStartedAt = startedAt.UTC().Format(time.RFC3339)
	a.mu.Unlock()

	result, err := downloadFileAuto(a.ctx, a.httpClient, bestAsset.DownloadURL, map[string]string{
		"Accept": "application/octet-stream",
	}, archivePath, totalSize, func(downloaded, total int64) {
		speed, elapsed, remaining := downloadStats(downloaded, total, startedAt)
		a.mu.Lock()
		a.llamaDlProgress.DownloadedBytes = downloaded
		if total > 0 {
			a.llamaDlProgress.TotalBytes = total
		}
		a.llamaDlProgress.DownloadSpeedBytesPerSecond = speed
		a.llamaDlProgress.DownloadElapsedSeconds = elapsed
		a.llamaDlProgress.DownloadRemainingSeconds = remaining
		a.mu.Unlock()
	})
	if err != nil {
		os.Remove(archivePath)
		a.failLlamaDownload(fmt.Sprintf("下载失败: %v", err))
		return
	}

	// Final progress update
	if result.TotalBytes > 0 {
		totalSize = result.TotalBytes
	}
	downloaded := result.DownloadedBytes
	speed, elapsed, _ := downloadStats(downloaded, totalSize, startedAt)
	a.mu.Lock()
	a.llamaDlProgress.DownloadedBytes = downloaded
	a.llamaDlProgress.DownloadSpeedBytesPerSecond = speed
	a.llamaDlProgress.DownloadElapsedSeconds = elapsed
	a.llamaDlProgress.DownloadRemainingSeconds = 0
	a.mu.Unlock()

	log.Infof("llama: downloaded %d bytes to %s", downloaded, archivePath)

	// Extract the archive
	extractDir := filepath.Join(tempDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		a.failLlamaDownload(fmt.Sprintf("创建解压目录失败: %v", err))
		return
	}

	log.Debugf("llama: extracting %s to %s", archivePath, extractDir)

	if err := extractArchive(archivePath, extractDir); err != nil {
		a.failLlamaDownload(fmt.Sprintf("解压失败: %v", err))
		return
	}

	// Verify llama-server exists in extracted files
	serverName := "llama-server"
	if runtime.GOOS == "windows" {
		serverName = "llama-server.exe"
	}

	var foundServer bool
	filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.EqualFold(filepath.Base(path), serverName) {
			foundServer = true
			return filepath.SkipAll
		}
		return nil
	})

	if !foundServer {
		// Fallback: search for any executable with "server" in name
		filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.Contains(strings.ToLower(filepath.Base(path)), "server") {
				foundServer = true
				return filepath.SkipAll
			}
			return nil
		})
	}

	if !foundServer {
		a.failLlamaDownload("解压后未找到 llama-server 可执行文件")
		return
	}

	// Copy ALL extracted files to binDir (not just the binary, to include DLLs etc.)
	log.Infof("llama: installing all extracted files to %s", a.binDir)
	if err := copyDirContents(extractDir, a.binDir); err != nil {
		a.failLlamaDownload(fmt.Sprintf("安装文件失败: %v", err))
		return
	}

	// Save version
	if err := a.writeInstalledVersion(releaseTag); err != nil {
		log.Warnf("llama: failed to write version file: %v", err)
	}

	// ── CUDA Runtime download (Windows only) ──
	// If the selected asset is a CUDA build, also download and install
	// the matching CUDA CRT DLLs from the same release.
	if runtime.GOOS == "windows" {
		if cudaAsset := findMatchingCudaCrt(release.Assets, assetName); cudaAsset != nil {
			log.Infof("llama: found matching CUDA CRT asset: %s", cudaAsset.Name)
			a.downloadCudaCrt(*cudaAsset, tempDir)
		}
	}

	// Clear the releases cache so next listing reflects installed state
	a.releasesCache = nil

	// Mark as completed
	installedPath, _ := a.locateLlamaServer()
	a.mu.Lock()
	a.llamaDlProgress.Downloading = false
	a.llamaDlProgress.Completed = true
	a.llamaDlProgress.Error = ""
	a.llamaDlProgress.DownloadSpeedBytesPerSecond = 0
	a.llamaDlProgress.DownloadRemainingSeconds = 0
	a.llamaDlProgress.Found = true
	a.llamaDlProgress.Version = a.readInstalledVersion()
	a.llamaDlProgress.Path = installedPath
	a.mu.Unlock()

	log.Infof("llama: successfully installed %s", releaseTag)
}

// failLlamaDownload marks the llama download as failed and cleans up progress state.
func (a *App) failLlamaDownload(errMsg string) {
	log.Errorf("llama: download failed: %s", errMsg)

	a.mu.Lock()
	defer a.mu.Unlock()

	a.llamaDlProgress.Downloading = false
	a.llamaDlProgress.Completed = false
	a.llamaDlProgress.Error = errMsg
	a.llamaDlProgress.DownloadSpeedBytesPerSecond = 0
	a.llamaDlProgress.DownloadRemainingSeconds = 0
}

// GetLlamaServerDownloadProgress returns the current llama.cpp download progress.
func (a *App) GetLlamaServerDownloadProgress() LlamaServerDownloadProgress {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Return a copy
	progress := a.llamaDlProgress

	// Also fill in the current installed state if applicable
	if !progress.Downloading && !progress.Completed && progress.Error == "" {
		// No download in progress and no completed/failed state — check installed
		path, err := a.locateLlamaServer()
		if err == nil {
			progress.Found = true
			progress.Version = a.readInstalledVersion()
			progress.Path = path
		}
	}

	return progress
}

// DeleteLlamaServer removes the installed llama-server binary and version file.
func (a *App) DeleteLlamaServer() error {
	// Remove all contents of binDir (llama-server binary, DLLs, version file, etc.)
	if err := os.RemoveAll(a.binDir); err != nil {
		log.Errorf("delete llama-server: failed to remove binDir: %v", err)
		return fmt.Errorf("删除 llama.cpp 目录失败: %w", err)
	}

	// Recreate empty binDir
	if err := os.MkdirAll(a.binDir, 0755); err != nil {
		log.Errorf("delete llama-server: failed to recreate binDir: %v", err)
		return fmt.Errorf("重建 bin 目录失败: %w", err)
	}

	// Clear the releases cache so listing re-fetches
	a.releasesCache = nil

	// Reset progress state
	a.mu.Lock()
	a.llamaDlProgress = LlamaServerDownloadProgress{}
	a.mu.Unlock()

	log.Infof("delete llama-server: removed all installed files")
	return nil
}
