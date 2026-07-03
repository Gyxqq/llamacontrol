package main

import (
	"fmt"
	"io"
	"net/http"
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

	a.mu.Lock()
	a.llamaDlProgress.CudaDownloading = true
	a.llamaDlProgress.CudaAssetName = cudaAsset.Name
	a.llamaDlProgress.CudaTotalBytes = cudaAsset.Size
	a.llamaDlProgress.CudaDownloadedBytes = 0
	a.llamaDlProgress.CudaCompleted = false
	a.llamaDlProgress.CudaError = ""
	a.mu.Unlock()

	// Cleanup: mark CUDA phase as done when we return
	defer func() {
		a.mu.Lock()
		a.llamaDlProgress.CudaDownloading = false
		a.llamaDlProgress.CudaCompleted = a.llamaDlProgress.CudaError == ""
		a.mu.Unlock()
	}()

	cudaArchivePath := filepath.Join(tempDir, cudaAsset.Name)

	dlReq, err := http.NewRequestWithContext(a.ctx, http.MethodGet, cudaAsset.DownloadURL, nil)
	if err != nil {
		a.setCudaCrtError(fmt.Sprintf("创建 CUDA CRT 下载请求失败: %v", err))
		return
	}
	dlReq.Header.Set("User-Agent", "llamacontrol/1.0")
	dlReq.Header.Set("Accept", "application/octet-stream")

	dlResp, err := a.httpClient.Do(dlReq)
	if err != nil {
		a.setCudaCrtError(fmt.Sprintf("CUDA CRT 下载失败: %v", err))
		return
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK && dlResp.StatusCode != http.StatusFound {
		a.setCudaCrtError(fmt.Sprintf("CUDA CRT 下载返回 %d", dlResp.StatusCode))
		return
	}

	outFile, err := os.Create(cudaArchivePath)
	if err != nil {
		a.setCudaCrtError(fmt.Sprintf("创建 CUDA CRT 临时文件失败: %v", err))
		return
	}

	buf := make([]byte, 32*1024)
	var cudaDownloaded int64
	lastUpdate := time.Now()

	for {
		n, readErr := dlResp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := outFile.Write(buf[:n]); writeErr != nil {
				outFile.Close()
				os.Remove(cudaArchivePath)
				a.setCudaCrtError(fmt.Sprintf("写入 CUDA CRT 文件失败: %v", writeErr))
				return
			}
			cudaDownloaded += int64(n)
			if time.Since(lastUpdate) > 200*time.Millisecond {
				a.mu.Lock()
				a.llamaDlProgress.CudaDownloadedBytes = cudaDownloaded
				a.mu.Unlock()
				lastUpdate = time.Now()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			outFile.Close()
			os.Remove(cudaArchivePath)
			a.setCudaCrtError(fmt.Sprintf("CUDA CRT 下载失败: %v", readErr))
			return
		}
	}

	outFile.Close()

	a.mu.Lock()
	a.llamaDlProgress.CudaDownloadedBytes = cudaDownloaded
	a.mu.Unlock()

	log.Infof("llama: downloaded CUDA CRT (%d bytes) to %s", cudaDownloaded, cudaArchivePath)
	log.Infof("llama: extracting CUDA CRT DLLs to %s", a.binDir)
	if err := extractArchive(cudaArchivePath, a.binDir); err != nil {
		a.setCudaCrtError(fmt.Sprintf("解压 CUDA CRT 失败: %v", err))
		return
	}

	log.Infof("llama: CUDA CRT installed successfully")
}
