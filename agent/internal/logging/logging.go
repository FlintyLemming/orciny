// Package logging 装配 agent 的日志。
//
// JSON lines 输出到 stdout，由 systemd / launchd 收集——这是默认且唯一
// 开启的通道。agent.yml 里可另行开启文件日志（spec §7.5）。
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TokenPrefixLen 是 token 在日志里保留的位数。
//
// 这条规矩在 M0 就要立好：M1 引入凭据下发后，脱敏漏一处就是事故。
const TokenPrefixLen = 8

func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// RedactToken 只保留 token 的前 8 位，够在事件流里对上号，又泄不出去。
func RedactToken(token string) string {
	if len(token) <= TokenPrefixLen {
		return token
	}
	return token[:TokenPrefixLen]
}

// fileWriter 是按天切分的日志写入器，并清理超过保留期的旧文件。
type fileWriter struct {
	dir        string
	retainDays int

	mu   sync.Mutex
	day  string
	file *os.File
}

// FileWriter 返回按天切分的日志写入器，并清理超过保留期的旧文件。
func FileWriter(dir string, retainDays int) (io.WriteCloser, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("logging: 创建日志目录: %w", err)
	}
	w := &fileWriter{dir: dir, retainDays: retainDays}
	w.prune()
	if err := w.rotate(time.Now()); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *fileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	if now.Format("2006-01-02") != w.day {
		if err := w.rotateLocked(now); err != nil {
			return 0, err
		}
		w.prune()
	}
	return w.file.Write(p)
}

func (w *fileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	return w.file.Close()
}

func (w *fileWriter) rotate(now time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotateLocked(now)
}

func (w *fileWriter) rotateLocked(now time.Time) error {
	if w.file != nil {
		_ = w.file.Close()
	}
	day := now.Format("2006-01-02")
	f, err := os.OpenFile(filepath.Join(w.dir, "agent-"+day+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("logging: 打开日志文件: %w", err)
	}
	w.file, w.day = f, day
	return nil
}

// prune 删除超过保留期的日志。失败只忽略——日志清理不该拖垮 agent。
func (w *fileWriter) prune() {
	cutoff := time.Now().AddDate(0, 0, -w.retainDays)
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".log")
		ts, err := time.Parse("2006-01-02", day)
		if err != nil {
			continue
		}
		if ts.Before(cutoff) {
			_ = os.Remove(filepath.Join(w.dir, name))
		}
	}
}
