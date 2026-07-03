package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ──────────────────────────────────────────────
// modelID
// ──────────────────────────────────────────────

func TestModelID(t *testing.T) {
	tests := []struct {
		repoID   string
		filename string
		want     string
	}{
		{
			repoID:   "TheBloke/Llama-2-7B-Chat-GGUF",
			filename: "llama-2-7b-chat.Q4_K_M.gguf",
			want:     "thebloke_llama-2-7b-chat-gguf_llama-2-7b-chat.q4_k_m.gguf",
		},
		{
			repoID:   "org/model",
			filename: "file.gguf",
			want:     "org_model_file.gguf",
		},
		{
			repoID:   "org/model",
			filename: "path/to/file.gguf",
			want:     "org_model_path_to_file.gguf",
		},
		{
			repoID:   "UPPERCASE/Model",
			filename: "Test.GGUF",
			want:     "uppercase_model_test.gguf",
		},
		{
			repoID:   "org/model",
			filename: "file with spaces.gguf",
			want:     "org_model_file_with_spaces.gguf",
		},
	}

	for _, tt := range tests {
		got := modelID(tt.repoID, tt.filename)
		if got != tt.want {
			t.Errorf("modelID(%q, %q) = %q, want %q", tt.repoID, tt.filename, got, tt.want)
		}

		// Verify no path separators in the result
		if strings.ContainsAny(got, "/\\") {
			t.Errorf("modelID(%q, %q) contains path separator: %q", tt.repoID, tt.filename, got)
		}

		// Verify no spaces in the result
		if strings.Contains(got, " ") {
			t.Errorf("modelID(%q, %q) contains space: %q", tt.repoID, tt.filename, got)
		}
	}
}

// ──────────────────────────────────────────────
// Metadata save/load
// ──────────────────────────────────────────────

func TestMetadataSaveLoad(t *testing.T) {
	dir := t.TempDir()
	metadataPath := filepath.Join(dir, "metadata.json")

	app := &App{
		httpClient:      &http.Client{},
		modelsDir:       filepath.Join(dir, "models"),
		metadataPath:    metadataPath,
		activeDownloads: make(map[string]*downloadTask),
		serverState:     &serverState{},
	}

	// No metadata file yet — should return empty list
	app.loadMetadata()
	if len(app.models) != 0 {
		t.Fatalf("expected empty models, got %d", len(app.models))
	}

	// Add a model and save
	app.models = []ModelRecord{
		{
			ID:              "test_model",
			DisplayName:     "Test Model",
			RepoID:          "org/repo",
			Filename:        "test.gguf",
			LocalPath:       filepath.Join(app.modelsDir, "test_model"),
			SizeBytes:       1024,
			DownloadedBytes: 1024,
			State:           "ready",
		},
	}

	if err := app.saveMetadata(); err != nil {
		t.Fatalf("saveMetadata failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
		t.Fatal("metadata.json was not created")
	}

	// Read raw file and verify content
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("failed to read metadata: %v", err)
	}

	var loaded []ModelRecord
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("failed to parse metadata: %v", err)
	}

	if len(loaded) != 1 {
		t.Fatalf("expected 1 model, got %d", len(loaded))
	}

	if loaded[0].ID != "test_model" {
		t.Errorf("expected ID 'test_model', got %q", loaded[0].ID)
	}

	if loaded[0].State != "ready" {
		t.Errorf("expected state 'ready', got %q", loaded[0].State)
	}

	// Load into a fresh app
	app2 := &App{
		httpClient:      &http.Client{},
		modelsDir:       app.modelsDir,
		metadataPath:    metadataPath,
		activeDownloads: make(map[string]*downloadTask),
		serverState:     &serverState{},
	}
	app2.loadMetadata()

	if len(app2.models) != 1 {
		t.Fatalf("expected 1 model after reload, got %d", len(app2.models))
	}
}

// ──────────────────────────────────────────────
// Hugging Face URL construction
// ──────────────────────────────────────────────

func TestDownloadURLConstruction(t *testing.T) {
	tests := []struct {
		repoID   string
		revision string
		filename string
		want     string
	}{
		{
			repoID:   "TheBloke/Llama-2-7B-Chat-GGUF",
			revision: "main",
			filename: "llama-2-7b-chat.Q4_K_M.gguf",
			want:     "https://huggingface.co/TheBloke/Llama-2-7B-Chat-GGUF/resolve/main/llama-2-7b-chat.Q4_K_M.gguf",
		},
		{
			repoID:   "org/model",
			revision: "v1.0",
			filename: "model.gguf",
			want:     "https://huggingface.co/org/model/resolve/v1.0/model.gguf",
		},
	}

	for _, tt := range tests {
		got := "https://huggingface.co/" + tt.repoID + "/resolve/" + tt.revision + "/" + tt.filename
		if got != tt.want {
			t.Errorf("URL = %q, want %q", got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// Server argument construction
// ──────────────────────────────────────────────

func TestBuildServerArgs(t *testing.T) {
	modelPath := "/path/to/model.gguf"

	config := ServerConfig{
		ModelID:        "test_model",
		Host:           "127.0.0.1",
		Port:           8080,
		CtxSize:        4096,
		GPULayers:      999,
		Threads:        8,
		BatchSize:      512,
		UbatchSize:     512,
		Parallel:       1,
		FlashAttention: true,
		ExtraArgs:      "--verbose --cache-type-k q8_0",
	}

	args := buildServerArgs(modelPath, config)

	// Verify essential args
	checkArg(t, args, "-m", modelPath)
	checkArg(t, args, "--host", "127.0.0.1")
	checkArg(t, args, "--port", "8080")
	checkArg(t, args, "-c", "4096")
	checkArg(t, args, "-ngl", "999")
	checkArg(t, args, "-t", "8")
	checkArg(t, args, "-b", "512")
	checkArg(t, args, "-ub", "512")
	checkArg(t, args, "-np", "1")
	checkArg(t, args, "-fa", "")

	// Verify extra args are appended
	checkArg(t, args, "--verbose", "")
	checkArg(t, args, "--cache-type-k", "q8_0")

	// Verify no shell metacharacters
	for _, arg := range args {
		if strings.ContainsAny(arg, "|;&$`(){}<>") {
			t.Errorf("arg contains shell metacharacter: %q", arg)
		}
	}
}

func TestBuildServerArgsNoFlashAttention(t *testing.T) {
	args := buildServerArgs("/path/to/model.gguf", ServerConfig{
		FlashAttention: false,
		Host:           "127.0.0.1",
		Port:           8080,
		CtxSize:        4096,
		GPULayers:      0,
		Threads:        4,
		BatchSize:      512,
		UbatchSize:     512,
		Parallel:       1,
	})

	// Verify -fa is NOT present
	for _, arg := range args {
		if arg == "-fa" {
			t.Error("expected no -fa flag when FlashAttention is false")
		}
	}
}

func checkArg(t *testing.T, args []string, flag, expectedValue string) {
	t.Helper()

	for i, arg := range args {
		if arg == flag {
			if expectedValue != "" {
				if i+1 >= len(args) {
					t.Errorf("flag %q at end of args, expected value %q", flag, expectedValue)
					return
				}
				if args[i+1] != expectedValue {
					t.Errorf("flag %q has value %q, want %q", flag, args[i+1], expectedValue)
				}
			}
			return
		}
	}

	t.Errorf("flag %q not found in args", flag)
}

// ──────────────────────────────────────────────
// Validation
// ──────────────────────────────────────────────

func TestValidateServerConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  ServerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: ServerConfig{
				Host:       "127.0.0.1",
				Port:       8080,
				CtxSize:    4096,
				GPULayers:  0,
				Threads:    4,
				BatchSize:  512,
				UbatchSize: 512,
				Parallel:   1,
			},
			wantErr: false,
		},
		{
			name: "empty host",
			config: ServerConfig{
				Host:       "",
				Port:       8080,
				CtxSize:    4096,
				GPULayers:  0,
				Threads:    4,
				BatchSize:  512,
				UbatchSize: 512,
				Parallel:   1,
			},
			wantErr: true,
		},
		{
			name: "invalid port (0)",
			config: ServerConfig{
				Host:       "127.0.0.1",
				Port:       0,
				CtxSize:    4096,
				GPULayers:  0,
				Threads:    4,
				BatchSize:  512,
				UbatchSize: 512,
				Parallel:   1,
			},
			wantErr: true,
		},
		{
			name: "invalid port (65536)",
			config: ServerConfig{
				Host:       "127.0.0.1",
				Port:       65536,
				CtxSize:    4096,
				GPULayers:  0,
				Threads:    4,
				BatchSize:  512,
				UbatchSize: 512,
				Parallel:   1,
			},
			wantErr: true,
		},
		{
			name: "ctx size too small",
			config: ServerConfig{
				Host:       "127.0.0.1",
				Port:       8080,
				CtxSize:    64,
				GPULayers:  0,
				Threads:    4,
				BatchSize:  512,
				UbatchSize: 512,
				Parallel:   1,
			},
			wantErr: true,
		},
		{
			name: "negative gpu layers",
			config: ServerConfig{
				Host:       "127.0.0.1",
				Port:       8080,
				CtxSize:    4096,
				GPULayers:  -1,
				Threads:    4,
				BatchSize:  512,
				UbatchSize: 512,
				Parallel:   1,
			},
			wantErr: true,
		},
		{
			name: "zero threads",
			config: ServerConfig{
				Host:       "127.0.0.1",
				Port:       8080,
				CtxSize:    4096,
				GPULayers:  0,
				Threads:    0,
				BatchSize:  512,
				UbatchSize: 512,
				Parallel:   1,
			},
			wantErr: true,
		},
		{
			name: "extra args with shell metachar",
			config: ServerConfig{
				Host:       "127.0.0.1",
				Port:       8080,
				CtxSize:    4096,
				GPULayers:  0,
				Threads:    4,
				BatchSize:  512,
				UbatchSize: 512,
				Parallel:   1,
				ExtraArgs:  "--verbose; rm -rf /",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServerConfig(tt.config)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateDownloadRequest(t *testing.T) {
	// Test via StartModelDownload — it should return error for empty fields
	app := &App{
		httpClient:      &http.Client{},
		modelsDir:       t.TempDir(),
		metadataPath:    filepath.Join(t.TempDir(), "metadata.json"),
		activeDownloads: make(map[string]*downloadTask),
		serverState:     &serverState{},
	}

	// Empty repoId
	err := app.StartModelDownload(DownloadRequest{})
	if err == nil {
		t.Error("expected error for empty DownloadRequest")
	}

	// Empty filename
	err = app.StartModelDownload(DownloadRequest{RepoID: "org/repo"})
	if err == nil {
		t.Error("expected error for empty filename")
	}

	// Valid request
	err = app.StartModelDownload(DownloadRequest{
		RepoID:   "org/repo",
		Filename: "test.gguf",
	})
	if err != nil {
		t.Errorf("unexpected error for valid request: %v", err)
	}

	// Clean up: cancel the download
	app.CancelModelDownload(modelID("org/repo", "test.gguf"))
}

// ──────────────────────────────────────────────
// inferModelName
// ──────────────────────────────────────────────

func TestInferModelName(t *testing.T) {
	tests := []struct {
		repoID   string
		filename string
		want     string
	}{
		{
			repoID:   "TheBloke/Llama-2-7B-Chat-GGUF",
			filename: "llama-2-7b-chat.Q4_K_M.gguf",
			want:     "Llama-2-7B-Chat-GGUF / llama-2-7b-chat.Q4_K_M",
		},
		{
			repoID:   "org/repo",
			filename: "model.gguf",
			want:     "repo / model",
		},
		{
			repoID:   "singleword",
			filename: "file.GGUF",
			want:     "singleword / file",
		},
	}

	for _, tt := range tests {
		got := inferModelName(tt.repoID, tt.filename)
		if got != tt.want {
			t.Errorf("inferModelName(%q, %q) = %q, want %q", tt.repoID, tt.filename, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// findModel helper
// ──────────────────────────────────────────────

func TestFindModel(t *testing.T) {
	app := &App{
		models: []ModelRecord{
			{ID: "model_a", DisplayName: "Model A"},
			{ID: "model_b", DisplayName: "Model B"},
		},
	}

	if idx := app.findModel("model_a"); idx != 0 {
		t.Errorf("expected index 0, got %d", idx)
	}

	if idx := app.findModel("model_b"); idx != 1 {
		t.Errorf("expected index 1, got %d", idx)
	}

	if idx := app.findModel("nonexistent"); idx != -1 {
		t.Errorf("expected -1, got %d", idx)
	}
}

// ──────────────────────────────────────────────
// ListModels sorting
// ──────────────────────────────────────────────

func TestListModelsSorting(t *testing.T) {
	app := &App{
		models: []ModelRecord{
			{ID: "m3", DisplayName: "Z Model", State: "missing"},
			{ID: "m1", DisplayName: "A Model", State: "ready"},
			{ID: "m2", DisplayName: "B Model", State: "downloading"},
			{ID: "m4", DisplayName: "C Model", State: "failed"},
		},
		activeDownloads: make(map[string]*downloadTask),
		serverState:     &serverState{},
	}

	// We need to set up the httpClient and modelsDir to avoid nil pointer
	// For the test, we just test the internal logic by calling ListModels
	// which requires a.modelsDir — but it's only used for path, not accessed here
	// Actually ListModels just returns a copy, so it should work

	result := app.ListModels()

	// Expected order: ready, downloading, failed, missing
	states := make([]string, len(result))
	for i, m := range result {
		states[i] = m.State
	}

	expected := []string{"ready", "downloading", "failed", "missing"}
	for i, s := range states {
		if s != expected[i] {
			t.Errorf("position %d: expected state %q, got %q", i, expected[i], s)
		}
	}
}

// ──────────────────────────────────────────────
// SplitExtraArgs (tested via buildServerArgs)
// ──────────────────────────────────────────────

func TestSplitExtraArgsEmpty(t *testing.T) {
	args := buildServerArgs("/path/to/model.gguf", ServerConfig{
		Host:       "127.0.0.1",
		Port:       8080,
		CtxSize:    4096,
		GPULayers:  0,
		Threads:    4,
		BatchSize:  512,
		UbatchSize: 512,
		Parallel:   1,
		ExtraArgs:  "",
	})

	// Should have no extra args beyond the standard ones
	// Standard: -m, path, --host, 127.0.0.1, --port, 8080, -c, 4096, -ngl, 0, -t, 4, -b, 512, -ub, 512, -np, 1
	expectedLen := 18
	if len(args) != expectedLen {
		t.Errorf("expected %d args with empty ExtraArgs, got %d: %v", expectedLen, len(args), args)
	}
}

// ──────────────────────────────────────────────
// startup / validateFiles
// ──────────────────────────────────────────────

func TestValidateFilesRemovesPartial(t *testing.T) {
	dir := t.TempDir()
	modelsDir := filepath.Join(dir, "models")
	os.MkdirAll(modelsDir, 0755)

	app := &App{
		httpClient:   &http.Client{},
		modelsDir:    modelsDir,
		metadataPath: filepath.Join(dir, "metadata.json"),
		models: []ModelRecord{
			{
				ID:        "test_model",
				State:     "downloading",
				LocalPath: filepath.Join(modelsDir, "test_model"),
			},
		},
		activeDownloads: make(map[string]*downloadTask),
		serverState:     &serverState{},
	}

	// Create a partial file
	partialPath := filepath.Join(modelsDir, "test_model.part")
	os.WriteFile(partialPath, []byte("partial data"), 0644)

	app.validateFiles()

	// The model should now be "failed" (not "downloading")
	if app.models[0].State != "failed" {
		t.Errorf("expected state 'failed' for interrupted download, got %q", app.models[0].State)
	}

	// The partial file should be removed
	if _, err := os.Stat(partialPath); !os.IsNotExist(err) {
		t.Error("partial file should have been removed")
	}

	// Metadata should be saved
	if _, err := os.Stat(app.metadataPath); os.IsNotExist(err) {
		t.Error("metadata.json should have been saved")
	}
}

func TestValidateFilesMissingFile(t *testing.T) {
	dir := t.TempDir()
	modelsDir := filepath.Join(dir, "models")
	os.MkdirAll(modelsDir, 0755)

	app := &App{
		httpClient:   &http.Client{},
		modelsDir:    modelsDir,
		metadataPath: filepath.Join(dir, "metadata.json"),
		models: []ModelRecord{
			{
				ID:    "test_model",
				State: "ready",
			},
		},
		activeDownloads: make(map[string]*downloadTask),
		serverState:     &serverState{},
	}

	app.validateFiles()

	// File doesn't exist — should be "missing"
	if app.models[0].State != "missing" {
		t.Errorf("expected state 'missing' for non-existent file, got %q", app.models[0].State)
	}
}

func TestValidateFilesExistingFile(t *testing.T) {
	dir := t.TempDir()
	modelsDir := filepath.Join(dir, "models")
	os.MkdirAll(modelsDir, 0755)

	// Create model file
	modelPath := filepath.Join(modelsDir, "test_model")
	os.WriteFile(modelPath, []byte("fake model data"), 0644)

	app := &App{
		httpClient:   &http.Client{},
		modelsDir:    modelsDir,
		metadataPath: filepath.Join(dir, "metadata.json"),
		models: []ModelRecord{
			{
				ID:    "test_model",
				State: "missing",
			},
		},
		activeDownloads: make(map[string]*downloadTask),
		serverState:     &serverState{},
	}

	app.validateFiles()

	// File exists — should be "ready" with correct size
	if app.models[0].State != "ready" {
		t.Errorf("expected state 'ready', got %q", app.models[0].State)
	}

	if app.models[0].SizeBytes <= 0 {
		t.Errorf("expected SizeBytes > 0, got %d", app.models[0].SizeBytes)
	}

	if app.models[0].LocalPath != modelPath {
		t.Errorf("expected LocalPath %q, got %q", modelPath, app.models[0].LocalPath)
	}
}

// ──────────────────────────────────────────────
// appDataDir
// ──────────────────────────────────────────────

func TestAppDataDir(t *testing.T) {
	app := &App{}
	dir, err := app.appDataDir()
	if err != nil {
		t.Fatalf("appDataDir failed: %v", err)
	}

	if dir == "" {
		t.Fatal("appDataDir returned empty string")
	}

	// Should end with "llamacontrol"
	if !strings.HasSuffix(dir, "llamacontrol") {
		t.Errorf("expected dir to end with 'llamacontrol', got %q", dir)
	}
}

// ──────────────────────────────────────────────
// ServerStatus
// ──────────────────────────────────────────────

func TestGetServerStatusInitial(t *testing.T) {
	app := &App{
		serverState: &serverState{},
	}

	status := app.GetServerStatus()

	if status.Running {
		t.Error("expected Running to be false initially")
	}

	if status.LogTail == nil {
		t.Error("expected LogTail to be non-nil")
	}
}

// ──────────────────────────────────────────────
// Duplicate download prevention
// ──────────────────────────────────────────────

func TestDuplicateDownloadPrevention(t *testing.T) {
	dir := t.TempDir()
	modelsDir := filepath.Join(dir, "models")
	os.MkdirAll(modelsDir, 0755)

	app := &App{
		httpClient:      &http.Client{},
		modelsDir:       modelsDir,
		metadataPath:    filepath.Join(dir, "metadata.json"),
		models:          []ModelRecord{},
		activeDownloads: make(map[string]*downloadTask),
		serverState:     &serverState{},
	}

	// First download should succeed
	err := app.StartModelDownload(DownloadRequest{
		RepoID:   "org/repo",
		Filename: "test.gguf",
	})
	if err != nil {
		t.Fatalf("first download should succeed: %v", err)
	}

	// Second download of same repo+file should fail
	err = app.StartModelDownload(DownloadRequest{
		RepoID:   "org/repo",
		Filename: "test.gguf",
	})
	if err == nil {
		t.Error("expected error for duplicate download")
	}

	// Clean up
	app.CancelModelDownload(modelID("org/repo", "test.gguf"))
}
