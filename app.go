package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// App struct
type App struct {
	ctx context.Context
	httpClient *http.Client
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Greet returns a greeting for the given name
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

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

// SearchHuggingFaceModels searches Hugging Face for GGUF model repos
func (a *App) SearchHuggingFaceModels(query string) ([]HuggingFaceModel, error) {
	params := url.Values{}
	params.Set("search", query)
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

	// Filter to models that look like GGUF repos and deduplicate
	seen := make(map[string]bool)
	models := make([]HuggingFaceModel, 0, len(raw))

	for _, m := range raw {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true

		// Only include models with GGUF in the ID or description
		idLower := strings.ToLower(m.ID)
		descLower := strings.ToLower(m.Description)

		if !strings.Contains(idLower, "gguf") && !strings.Contains(descLower, "gguf") {
			// Still include if pipeline_tag is text-generation
			if m.PipelineTag != "text-generation" {
				continue
			}
		}

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

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}