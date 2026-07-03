package main

import (
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

// ──────────────────────────────────────────────
// App log buffer — captures all logrus output for the frontend
// ──────────────────────────────────────────────

// appLogBuffer is a thread-safe ring buffer for log messages.
type appLogBuffer struct {
	mu    sync.Mutex
	max   int
	lines []string
}

func (b *appLogBuffer) Append(level, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	line := "[" + level + "] " + msg
	if len(b.lines) >= b.max {
		b.lines = b.lines[1:]
	}
	b.lines = append(b.lines, line)
}

func (b *appLogBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]string, len(b.lines))
	copy(result, b.lines)
	return result
}

// appLogHook captures all logrus log entries into the app log buffer.
type appLogHook struct {
	buffer *appLogBuffer
}

func (h *appLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *appLogHook) Fire(entry *logrus.Entry) error {
	h.buffer.Append(strings.ToUpper(entry.Level.String()), entry.Message)
	return nil
}

// GetAppLogs returns the accumulated app log entries for the frontend.
func (a *App) GetAppLogs() []string {
	if a.appLogs == nil {
		return []string{}
	}
	return a.appLogs.Lines()
}