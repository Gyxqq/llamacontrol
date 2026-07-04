package main

import (
	"context"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// ──────────────────────────────────────────────
// App struct
// ──────────────────────────────────────────────

type App struct {
	ctx          context.Context
	allowQuit    bool
	httpClient   *http.Client
	modelsDir    string
	binDir       string
	metadataPath string
	mu           sync.RWMutex

	// Model metadata (persisted to metadata.json)
	models []ModelRecord

	// Server config (persisted to server-config.json)
	serverConfig     ServerConfig
	serverConfigPath string

	// Active downloads (in-memory only)
	activeDownloads map[string]*downloadTask

	// llama-server process state (in-memory only)
	serverState *serverState

	// GitHub releases cache
	releasesCache    []LlamaServerRelease
	releasesCachedAt time.Time

	// llama.cpp download progress
	llamaDlProgress LlamaServerDownloadProgress

	// App log buffer for frontend log display
	appLogs *appLogBuffer
}

// downloadTask tracks an in-flight download for cancellation
type downloadTask struct {
	cancel  context.CancelFunc
	modelID string
	done    chan struct{}
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
	ID                          string `json:"id"`
	DisplayName                 string `json:"displayName"`
	RepoID                      string `json:"repoId"`
	Filename                    string `json:"filename"`
	Revision                    string `json:"revision"`
	LocalPath                   string `json:"localPath"`
	SizeBytes                   int64  `json:"sizeBytes"`
	DownloadedBytes             int64  `json:"downloadedBytes"`
	DownloadStartedAt           string `json:"downloadStartedAt"`
	DownloadSpeedBytesPerSecond int64  `json:"downloadSpeedBytesPerSecond"`
	DownloadElapsedSeconds      int64  `json:"downloadElapsedSeconds"`
	DownloadRemainingSeconds    int64  `json:"downloadRemainingSeconds"`
	State                       string `json:"state"` // "missing" | "downloading" | "ready" | "failed"
	Error                       string `json:"error"`
	CreatedAt                   string `json:"createdAt"`
	UpdatedAt                   string `json:"updatedAt"`
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

	// Enabled flags — each controls whether its parameter is included in the llama-server command
	HostEnabled           bool `json:"hostEnabled"`
	PortEnabled           bool `json:"portEnabled"`
	CtxSizeEnabled        bool `json:"ctxSizeEnabled"`
	GPULayersEnabled      bool `json:"gpuLayersEnabled"`
	ThreadsEnabled        bool `json:"threadsEnabled"`
	BatchSizeEnabled      bool `json:"batchSizeEnabled"`
	UbatchSizeEnabled     bool `json:"ubatchSizeEnabled"`
	ParallelEnabled       bool `json:"parallelEnabled"`
	FlashAttentionEnabled bool `json:"flashAttentionEnabled"`
	ExtraArgsEnabled      bool `json:"extraArgsEnabled"`
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

// LlamaServerDownloadProgress tracks llama.cpp download progress for the frontend.
type LlamaServerDownloadProgress struct {
	Downloading                 bool   `json:"downloading"`
	ReleaseTag                  string `json:"releaseTag"`
	AssetName                   string `json:"assetName"`
	TotalBytes                  int64  `json:"totalBytes"`
	DownloadedBytes             int64  `json:"downloadedBytes"`
	DownloadStartedAt           string `json:"downloadStartedAt"`
	DownloadSpeedBytesPerSecond int64  `json:"downloadSpeedBytesPerSecond"`
	DownloadElapsedSeconds      int64  `json:"downloadElapsedSeconds"`
	DownloadRemainingSeconds    int64  `json:"downloadRemainingSeconds"`
	Completed                   bool   `json:"completed"`
	Error                       string `json:"error"`
	Found                       bool   `json:"found"`
	Version                     string `json:"version"`
	Path                        string `json:"path"`

	// CUDA Runtime download progress
	CudaDownloading                 bool   `json:"cudaDownloading"`
	CudaAssetName                   string `json:"cudaAssetName"`
	CudaTotalBytes                  int64  `json:"cudaTotalBytes"`
	CudaDownloadedBytes             int64  `json:"cudaDownloadedBytes"`
	CudaDownloadStartedAt           string `json:"cudaDownloadStartedAt"`
	CudaDownloadSpeedBytesPerSecond int64  `json:"cudaDownloadSpeedBytesPerSecond"`
	CudaDownloadElapsedSeconds      int64  `json:"cudaDownloadElapsedSeconds"`
	CudaDownloadRemainingSeconds    int64  `json:"cudaDownloadRemainingSeconds"`
	CudaCompleted                   bool   `json:"cudaCompleted"`
	CudaError                       string `json:"cudaError"`
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

// ──────────────────────────────────────────────
// Hugging Face API types
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

// fileTreeEntry is a single entry from the HF API file tree
type fileTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// GguFileInfo carries path + size for a .gguf file listed from a HF repo.
type GguFileInfo struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}
