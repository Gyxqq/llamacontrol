package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ──────────────────────────────────────────────
// Hugging Face API
// ──────────────────────────────────────────────

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

// ListModelGguFiles lists all .gguf files (with sizes) in a Hugging Face model repo.
func (a *App) ListModelGguFiles(repoId string) ([]GguFileInfo, error) {
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

	var files []GguFileInfo
	for _, entry := range entries {
		if entry.Type == "file" && strings.HasSuffix(strings.ToLower(entry.Path), ".gguf") {
			files = append(files, GguFileInfo{
				Path: entry.Path,
				Size: entry.Size,
			})
		}
	}

	return files, nil
}

// truncateString truncates a string to the given max length, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}