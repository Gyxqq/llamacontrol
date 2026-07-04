package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

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
	args := []string{"-m", modelPath}

	if config.Host != "" {
		args = append(args, "--host", config.Host)
	}
	if config.Port >= 1 && config.Port <= 65535 {
		args = append(args, "--port", strconv.Itoa(config.Port))
	}
	if config.CtxSize >= 128 {
		args = append(args, "-c", strconv.Itoa(config.CtxSize))
	}
	args = append(args, "-ngl", strconv.Itoa(config.GPULayers))
	if config.Threads >= 1 {
		args = append(args, "-t", strconv.Itoa(config.Threads))
	}
	if config.BatchSize >= 1 {
		args = append(args, "-b", strconv.Itoa(config.BatchSize))
	}
	if config.UbatchSize >= 1 {
		args = append(args, "-ub", strconv.Itoa(config.UbatchSize))
	}
	if config.Parallel >= 1 {
		args = append(args, "-np", strconv.Itoa(config.Parallel))
	}
	if config.FlashAttention {
		args = append(args, "--flash-attn", "on")
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
	configureHiddenCommandWindow(cmd)

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
		exitMsg := fmt.Sprintf("llama-server 已退出: %v", err)
		log.Infof("server: process exited: %v", err)

		a.serverState.mu.Lock()
		defer a.serverState.mu.Unlock()

		// Append exit message to log tail then capture
		logMu.Lock()
		a.serverState.status.LogTail = make([]string, len(logTail))
		if len(logTail) >= 100 {
			logTail = logTail[1:]
		}
		logTail = append(logTail, exitMsg)
		copy(a.serverState.status.LogTail, logTail)
		logMu.Unlock()

		a.serverState.status.Running = false
		a.serverState.cmd = nil
	}()

	// Persist the config for next launch
	a.serverConfig = config
	if err := a.saveServerConfigFile(); err != nil {
		log.Warnf("server-config: failed to persist after start: %v", err)
	}

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
