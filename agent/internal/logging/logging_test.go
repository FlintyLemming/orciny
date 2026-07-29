package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/logging"
)

func TestNewEmitsJSONLines(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(&buf, slog.LevelInfo)

	log.Info("已连接 hub", "fingerprint", "abc")

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	require.Equal(t, "已连接 hub", line["msg"])
	require.Equal(t, "abc", line["fingerprint"])
	require.Equal(t, "INFO", line["level"])
}

func TestRedactTokenKeepsOnlyPrefix(t *testing.T) {
	require.Equal(t, "abcd1234", logging.RedactToken("abcd1234efghijklmnop"))
}

func TestRedactTokenHandlesShortInput(t *testing.T) {
	require.Equal(t, "abc", logging.RedactToken("abc"))
	require.Equal(t, "", logging.RedactToken(""))
}

func TestFileWriterCreatesDatedFile(t *testing.T) {
	dir := t.TempDir()
	w, err := logging.FileWriter(dir, 14)
	require.NoError(t, err)
	defer w.Close()

	_, err = w.Write([]byte("{\"msg\":\"x\"}\n"))
	require.NoError(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Name(), time.Now().Format("2006-01-02"))
}

func TestFileWriterPrunesOldLogs(t *testing.T) {
	dir := t.TempDir()

	old := filepath.Join(dir, "agent-2020-01-01.log")
	require.NoError(t, os.WriteFile(old, []byte("旧的"), 0o600))
	recent := filepath.Join(dir, "agent-"+time.Now().AddDate(0, 0, -3).Format("2006-01-02")+".log")
	require.NoError(t, os.WriteFile(recent, []byte("近的"), 0o600))

	w, err := logging.FileWriter(dir, 14)
	require.NoError(t, err)
	defer w.Close()

	require.NoFileExists(t, old, "超过保留期的日志应被清理")
	require.FileExists(t, recent)
}
