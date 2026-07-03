package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

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