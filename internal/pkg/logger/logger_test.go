package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotatePattern(t *testing.T) {
	pattern, glob, err := rotatePattern(filepath.Join("logs", "ragflow-x.log"))
	if err != nil {
		t.Fatalf("rotatePattern failed: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(pattern), "logs/ragflow-x-%Y%m%d.log") {
		t.Fatalf("unexpected date pattern: %q", pattern)
	}
	if !strings.HasSuffix(filepath.ToSlash(glob), "logs/ragflow-x-*.log*") {
		t.Fatalf("unexpected glob: %q", glob)
	}
}

func TestRotatePatternDefaultExtension(t *testing.T) {
	pattern, _, err := rotatePattern(filepath.Join("logs", "server"))
	if err != nil {
		t.Fatalf("rotatePattern failed: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(pattern), "logs/server-%Y%m%d.log") {
		t.Fatalf("expected default .log extension, got %q", pattern)
	}
}

func TestPruneByCount(t *testing.T) {
	dir := t.TempDir()
	w := &rotatingWriter{glob: filepath.Join(dir, "app-*.log*"), count: 3}

	base := time.Now().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		name := filepath.Join(dir, fmt.Sprintf("app-2026082%d.log", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		ts := base.Add(time.Duration(i) * time.Minute)
		_ = os.Chtimes(name, ts, ts)
	}

	w.prune()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir failed: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 newest files kept, got %d", len(entries))
	}
	for _, i := range []int{2, 3, 4} {
		if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("app-2026082%d.log", i))); err != nil {
			t.Fatalf("expected file %d to be kept: %v", i, err)
		}
	}
}

func TestPruneByAge(t *testing.T) {
	dir := t.TempDir()
	w := &rotatingWriter{glob: filepath.Join(dir, "app-*.log"), age: 2 * 24 * time.Hour}

	oldPath := filepath.Join(dir, "app-20260818.log")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	old := time.Now().Add(-3 * 24 * time.Hour)
	_ = os.Chtimes(oldPath, old, old)

	freshPath := filepath.Join(dir, "app-20260820.log")
	if err := os.WriteFile(freshPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	w.prune()

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old file to be pruned by age")
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("expected fresh file to be kept: %v", err)
	}
}

func TestInitCreatesDatedFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "ragflow-x.log")

	if err := Init(&Config{
		Level:         "info",
		Format:        "json",
		OutputPath:    logPath,
		RotateDaily:   true,
		RotationCount: 2,
	}); err != nil {
		t.Fatalf("init logger failed: %v", err)
	}
	t.Cleanup(Close)

	Info("logger test message", "key", "value")
	Sync()
	Close()

	matches, err := filepath.Glob(filepath.Join(dir, "ragflow-x-*.log"))
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected a dated log file in %s", dir)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read log file failed: %v", err)
	}
	if !strings.Contains(string(data), "logger test message") {
		t.Fatalf("expected structured log content in %s", matches[0])
	}
}

// TestFileOutputDoesNotMirrorStdout guards against re-introducing a stdout tee
// when OutputPath is a file: daemon launches redirect stdout to a capture file
// (logs/server.out.log), and a tee would bypass file rotation and let that
// capture file grow unbounded.
func TestFileOutputDoesNotMirrorStdout(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")

	orig := os.Stdout
	errPipeR, errPipeW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe failed: %v", err)
	}
	os.Stdout = errPipeW
	defer func() { os.Stdout = orig }()

	readCh := make(chan string, 1)
	go func() {
		buf, _ := io.ReadAll(errPipeR)
		readCh <- string(buf)
	}()

	if err := Init(&Config{
		Level: "info", Format: "json", OutputPath: logPath,
		RotateDaily: true, RotationCount: 2,
	}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	Info("file-only-message", "k", "v")
	Sync()
	Close()
	_ = errPipeW.Close()

	if got := <-readCh; strings.Contains(got, "file-only-message") {
		t.Fatalf("file-mode log leaked to stdout: %q", got)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "app-*.log"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("expected a dated log file in %s (err=%v)", dir, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read log file failed: %v", err)
	}
	if !strings.Contains(string(data), "file-only-message") {
		t.Fatal("expected message in the rotating file")
	}
}
