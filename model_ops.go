package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
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

// ImportModelFile lets the user choose a local GGUF file, copies it into the
// managed models directory, and registers it as a ready model.
func (a *App) ImportModelFile() (ModelRecord, error) {
	if a.ctx == nil {
		return ModelRecord{}, fmt.Errorf("应用尚未初始化")
	}
	if a.modelsDir == "" {
		return ModelRecord{}, fmt.Errorf("模型目录未初始化")
	}

	sourcePath, err := wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "选择 GGUF 模型文件",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "GGUF 模型文件 (*.gguf)", Pattern: "*.gguf"},
		},
	})
	if err != nil {
		return ModelRecord{}, fmt.Errorf("选择模型文件失败: %w", err)
	}
	if sourcePath == "" {
		return ModelRecord{}, fmt.Errorf("已取消导入")
	}

	return a.importModelFile(sourcePath)
}

func (a *App) importModelFile(sourcePath string) (ModelRecord, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return ModelRecord{}, fmt.Errorf("模型文件路径不能为空")
	}
	if !strings.EqualFold(filepath.Ext(sourcePath), ".gguf") {
		return ModelRecord{}, fmt.Errorf("只能导入 .gguf 模型文件")
	}

	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return ModelRecord{}, fmt.Errorf("读取模型文件失败: %w", err)
	}
	if sourceInfo.IsDir() {
		return ModelRecord{}, fmt.Errorf("请选择 .gguf 模型文件，而不是目录")
	}

	filename := filepath.Base(sourcePath)

	a.mu.Lock()
	id := a.uniqueImportedModelID(filename)
	destPath := a.modelFilePath(id)
	a.mu.Unlock()

	tmpPath := destPath + ".importing"

	sourceAbs, _ := filepath.Abs(sourcePath)
	destAbs, _ := filepath.Abs(destPath)
	if !samePath(sourceAbs, destAbs) {
		if err := copyFileAtomic(sourcePath, tmpPath, destPath); err != nil {
			return ModelRecord{}, fmt.Errorf("复制模型文件失败: %w", err)
		}
	} else {
		os.Remove(tmpPath)
	}

	importedInfo, err := os.Stat(destPath)
	if err != nil {
		return ModelRecord{}, fmt.Errorf("读取导入后的模型文件失败: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	record := ModelRecord{
		ID:                id,
		DisplayName:       importedModelName(filename),
		RepoID:            "manual",
		Filename:          filename,
		Revision:          "local",
		LocalPath:         destPath,
		SizeBytes:         importedInfo.Size(),
		DownloadedBytes:   importedInfo.Size(),
		DownloadStartedAt: now,
		State:             "ready",
		Error:             "",
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.findModel(id) >= 0 {
		os.Remove(destPath)
		return ModelRecord{}, fmt.Errorf("模型已存在: %s", id)
	}

	a.models = append(a.models, record)
	if err := a.saveMetadata(); err != nil {
		os.Remove(destPath)
		return ModelRecord{}, fmt.Errorf("保存元数据失败: %w", err)
	}

	log.Infof("import: registered model %s from %s", id, sourcePath)
	return record, nil
}

func (a *App) uniqueImportedModelID(filename string) string {
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	for i := 0; ; i++ {
		candidateName := filename
		if i > 0 {
			candidateName = fmt.Sprintf("%s_%d%s", base, i+1, ext)
		}

		id := modelID("manual", candidateName)
		if a.findModel(id) >= 0 {
			continue
		}
		if _, err := os.Stat(a.modelFilePath(id)); err == nil {
			continue
		}
		return id
	}
}

func importedModelName(filename string) string {
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	if name == "" {
		return filename
	}
	return name
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func copyFileAtomic(sourcePath, tmpPath, destPath string) error {
	if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(tmp, source)
	closeErr := tmp.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return closeErr
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
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
