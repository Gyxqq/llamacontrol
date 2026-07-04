package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// isCudaCrtAsset returns true if the asset is a CUDA CRT zip
// (e.g., "cudart-llama-bin-win-cuda-12.4-x64.zip").
func isCudaCrtAsset(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "cudart-")
}

// cudaVersionFromAsset extracts the CUDA version from a CUDA llama-server asset name.
// For "llama-server-bin-win-cuda-cu12.4-x64.7z" or
// "llama-server-bin-win-cuda-12.4-x64.zip", returns "12.4".
// Returns empty string if the asset is not a CUDA build.
func cudaVersionFromAsset(assetName string) string {
	name := strings.ToLower(assetName)
	for _, marker := range []string{"cuda-cu", "cuda-"} {
		idx := strings.Index(name, marker)
		if idx < 0 {
			continue
		}
		rest := name[idx+len(marker):]
		parts := strings.SplitN(rest, "-", 2)
		if parts[0] != "" {
			return parts[0]
		}
	}
	return ""
}

// findMatchingCudaCrt finds the CUDA CRT asset matching the given main asset's CUDA version.
func findMatchingCudaCrt(assets []ghReleaseAsset, mainAssetName string) *ghReleaseAsset {
	version := cudaVersionFromAsset(mainAssetName)
	if version == "" {
		return nil
	}
	for i := range assets {
		if isCudaCrtAsset(assets[i].Name) && strings.Contains(strings.ToLower(assets[i].Name), "cuda-"+version) {
			return &assets[i]
		}
	}
	return nil
}

// setCudaCrtError sets the CUDA CRT error message.
func (a *App) setCudaCrtError(errMsg string) {
	log.Warnf("llama: CUDA CRT error: %s", errMsg)
	a.mu.Lock()
	a.llamaDlProgress.CudaError = errMsg
	a.mu.Unlock()
}

// downloadCudaCrt downloads and extracts the CUDA CRT asset to binDir.
// This runs after the main llama-server asset has been installed.
// Errors are non-fatal: the main installation is already complete.
func (a *App) downloadCudaCrt(cudaAsset ghReleaseAsset, tempDir string) {
	log.Infof("llama: downloading CUDA CRT asset: %s (%d bytes)", cudaAsset.Name, cudaAsset.Size)

	startedAt := time.Now()
	a.mu.Lock()
	a.llamaDlProgress.CudaDownloading = true
	a.llamaDlProgress.CudaAssetName = cudaAsset.Name
	a.llamaDlProgress.CudaTotalBytes = cudaAsset.Size
	a.llamaDlProgress.CudaDownloadedBytes = 0
	a.llamaDlProgress.CudaDownloadStartedAt = startedAt.UTC().Format(time.RFC3339)
	a.llamaDlProgress.CudaDownloadSpeedBytesPerSecond = 0
	a.llamaDlProgress.CudaDownloadElapsedSeconds = 0
	a.llamaDlProgress.CudaDownloadRemainingSeconds = 0
	a.llamaDlProgress.CudaCompleted = false
	a.llamaDlProgress.CudaError = ""
	a.mu.Unlock()

	// Cleanup: mark CUDA phase as done when we return
	defer func() {
		a.mu.Lock()
		a.llamaDlProgress.CudaDownloading = false
		a.llamaDlProgress.CudaCompleted = a.llamaDlProgress.CudaError == ""
		a.llamaDlProgress.CudaDownloadSpeedBytesPerSecond = 0
		a.llamaDlProgress.CudaDownloadRemainingSeconds = 0
		a.mu.Unlock()
	}()

	cudaArchivePath := filepath.Join(tempDir, cudaAsset.Name)

	result, err := downloadFileAuto(a.ctx, a.httpClient, cudaAsset.DownloadURL, map[string]string{
		"Accept": "application/octet-stream",
	}, cudaArchivePath, cudaAsset.Size, func(downloaded, total int64) {
		speed, elapsed, remaining := downloadStats(downloaded, total, startedAt)
		a.mu.Lock()
		a.llamaDlProgress.CudaDownloadedBytes = downloaded
		if total > 0 {
			a.llamaDlProgress.CudaTotalBytes = total
		}
		a.llamaDlProgress.CudaDownloadSpeedBytesPerSecond = speed
		a.llamaDlProgress.CudaDownloadElapsedSeconds = elapsed
		a.llamaDlProgress.CudaDownloadRemainingSeconds = remaining
		a.mu.Unlock()
	})
	if err != nil {
		os.Remove(cudaArchivePath)
		a.setCudaCrtError(fmt.Sprintf("CUDA CRT 下载失败: %v", err))
		return
	}

	cudaDownloaded := result.DownloadedBytes
	totalSize := cudaAsset.Size
	if result.TotalBytes > 0 {
		totalSize = result.TotalBytes
	}
	speed, elapsed, _ := downloadStats(cudaDownloaded, totalSize, startedAt)
	a.mu.Lock()
	a.llamaDlProgress.CudaDownloadedBytes = cudaDownloaded
	a.llamaDlProgress.CudaDownloadSpeedBytesPerSecond = speed
	a.llamaDlProgress.CudaDownloadElapsedSeconds = elapsed
	a.llamaDlProgress.CudaDownloadRemainingSeconds = 0
	a.mu.Unlock()

	log.Infof("llama: downloaded CUDA CRT (%d bytes) to %s", cudaDownloaded, cudaArchivePath)
	log.Infof("llama: extracting CUDA CRT DLLs to %s", a.binDir)
	if err := extractArchive(cudaArchivePath, a.binDir); err != nil {
		a.setCudaCrtError(fmt.Sprintf("解压 CUDA CRT 失败: %v", err))
		return
	}

	log.Infof("llama: CUDA CRT installed successfully")
}
