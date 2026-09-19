// Package logger provides the process-wide structured logger used by ragflow-x.
//
// It wraps the standard library log/slog and supports JSON or console
// encoding, daily rotating log files named by date, size-based rotation,
// and retention by backup count or age. The rotating writer is built on
// github.com/lestrrat-go/file-rotatelogs, which names files like
// "app-20260820.log" and appends ".1", ".2" suffixes when multiple files
// are produced within a single day by size-based rotation.
//
// When OutputPath is a file, log records go ONLY to the rotating file — stdout
// is not mirrored. Mirroring to stdout would bypass rotation whenever the
// process runs as a daemon with stdout redirected to a capture file (e.g.
// logs/server.out.log), letting that file grow unbounded. Use `output: stdout`
// instead when console/collector logging is wanted.
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
)

const pruneInterval = time.Minute

var (
	globalLogger *slog.Logger
	globalCloser io.Closer
	mu           sync.Mutex
)

// Config controls logger initialization.
type Config struct {
	Level         string // debug | info | warn | error
	Format        string // json | console
	OutputPath    string // "stdout" or a file path such as ./logs/ragflow-x.log
	RotateDaily   bool   // rotate once per day; the active file name carries the date
	RotationSize  int64  // maximum bytes per file before size rotation; 0 disables
	RotationCount uint   // maximum backup files to keep; 0 falls back to age retention
	RetentionDays int    // maximum age in days; used when RotationCount is 0
	Service       string // value placed on the "service" structured field
}

// DefaultConfig returns the default logger configuration.
func DefaultConfig() *Config {
	return &Config{
		Level:         "info",
		Format:        "json",
		OutputPath:    "stdout",
		RotateDaily:   true,
		RotationCount: 7,
	}
}

func normalizeConfig(cfg *Config) *Config {
	out := *cfg
	if out.Level == "" {
		out.Level = "info"
	}
	if out.Format == "" {
		out.Format = "json"
	}
	if out.OutputPath == "" {
		out.OutputPath = "stdout"
	}
	if out.RotationCount == 0 && out.RetentionDays <= 0 {
		out.RotationCount = 7
	}
	return &out
}

// Init initializes the global logger. If OutputPath is "stdout" or empty,
// logs are written to stdout only. Otherwise logs are written to stdout and
// a rotating file. It is safe to call Init multiple times.
func Init(cfg *Config) error {
	Close()

	if cfg == nil {
		cfg = DefaultConfig()
	} else {
		cfg = normalizeConfig(cfg)
	}

	writer, closer, err := buildWriter(cfg)
	if err != nil {
		return err
	}

	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level), AddSource: true}
	var handler slog.Handler
	if formatConsole(cfg.Format) {
		handler = slog.NewTextHandler(writer, opts)
	} else {
		handler = slog.NewJSONHandler(writer, opts)
	}

	hostname, _ := os.Hostname()
	logger := slog.New(handler).With("hostname", hostname)
	if cfg.Service != "" {
		logger = logger.With("service", cfg.Service)
	}

	mu.Lock()
	globalLogger = logger
	globalCloser = closer
	mu.Unlock()
	return nil
}

func buildWriter(cfg *Config) (io.Writer, io.Closer, error) {
	if cfg.OutputPath == "" || cfg.OutputPath == "stdout" {
		return os.Stdout, nil, nil
	}
	rw, err := newRotatingWriter(cfg)
	if err != nil {
		return nil, nil, err
	}
	return rw, rw, nil
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func formatConsole(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "console", "text", "pretty":
		return true
	default:
		return false
	}
}

// rotatingWriter combines date-named, time/size rotation from
// file-rotatelogs with our own retention pruning. We prune ourselves
// because file-rotatelogs' built-in cleanup only globs the plain
// "app-YYYYMMDD.log" names and misses the ".1/.2" generational files that
// size rotation produces.
type rotatingWriter struct {
	rot       *rotatelogs.RotateLogs
	glob      string
	count     uint
	age       time.Duration
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newRotatingWriter(cfg *Config) (*rotatingWriter, error) {
	pattern, glob, err := rotatePattern(cfg.OutputPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(pattern), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create log directory %s: %w", filepath.Dir(pattern), err)
	}

	opts := []rotatelogs.Option{
		rotatelogs.WithLocation(time.Local),
		// Disable the library's own retention so the count/age pruning below
		// is the single source of truth (see package comment).
		rotatelogs.WithRotationCount(maxUint()),
	}
	if cfg.RotateDaily {
		opts = append(opts, rotatelogs.WithRotationTime(24*time.Hour))
	}
	if cfg.RotationSize > 0 {
		opts = append(opts, rotatelogs.WithRotationSize(cfg.RotationSize))
	}

	rot, err := rotatelogs.New(pattern, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create rotating log writer: %w", err)
	}

	w := &rotatingWriter{
		rot:   rot,
		glob:  glob,
		count: cfg.RotationCount,
		age:   time.Duration(cfg.RetentionDays) * 24 * time.Hour,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	w.prune()
	w.startPruneLoop()
	return w, nil
}

// rotatePattern builds the strftime pattern used for date-named files and the
// glob used to discover files for retention pruning. For an output path of
// "logs/app.log" the pattern is "logs/app-%Y%m%d.log" and the glob is
// "logs/app-*.log*" (the trailing "*" also matches "*.log.1" generations).
func rotatePattern(outputPath string) (pattern, glob string, err error) {
	clean := filepath.Clean(outputPath)
	base := filepath.Base(clean)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", "", fmt.Errorf("invalid log output path %q", outputPath)
	}
	dir := filepath.Dir(clean)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if ext == "" {
		ext = ".log"
	}
	if name == "" {
		name = "app"
	}
	pattern = filepath.Join(dir, fmt.Sprintf("%s-%%Y%%m%%d%s", name, ext))
	glob = filepath.Join(dir, fmt.Sprintf("%s-*%s*", name, ext))
	return pattern, glob, nil
}

func maxUint() uint {
	return ^uint(0)
}

type rotFile struct {
	path string
	mod  time.Time
}

// listFiles returns matching log files sorted newest first, ignoring lock
// and symlink artifacts that file-rotatelogs manages transiently.
func (w *rotatingWriter) listFiles() []rotFile {
	matches, err := filepath.Glob(w.glob)
	if err != nil {
		return nil
	}
	files := make([]rotFile, 0, len(matches))
	for _, path := range matches {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_lock") || strings.HasSuffix(base, "_symlink") {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil || fi.IsDir() {
			continue
		}
		files = append(files, rotFile{path: path, mod: fi.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].mod.After(files[j].mod)
	})
	return files
}

func (w *rotatingWriter) prune() {
	files := w.listFiles()
	if len(files) == 0 {
		return
	}

	var toRemove []rotFile
	switch {
	case w.count > 0:
		if uint(len(files)) <= w.count {
			return
		}
		// files is sorted newest-first, so the tail holds the oldest entries.
		toRemove = files[w.count:]
	case w.age > 0:
		cutoff := time.Now().Add(-w.age)
		for _, f := range files {
			if f.mod.Before(cutoff) {
				toRemove = append(toRemove, f)
			}
		}
	default:
		return
	}

	for _, f := range toRemove {
		_ = os.Remove(f.path)
	}
}

func (w *rotatingWriter) startPruneLoop() {
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(pruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-w.stop:
				w.prune()
				return
			case <-ticker.C:
				w.prune()
			}
		}
	}()
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	return w.rot.Write(p)
}

func (w *rotatingWriter) Close() error {
	w.closeOnce.Do(func() {
		close(w.stop)
		<-w.done
		_ = w.rot.Close()
	})
	return nil
}

// L returns the global logger, falling back to a default JSON logger when
// Init has not been called.
func L() *slog.Logger {
	mu.Lock()
	defer mu.Unlock()
	if globalLogger == nil {
		return slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return globalLogger
}

// With returns a derived logger with additional structured fields.
func With(args ...any) *slog.Logger {
	return L().With(args...)
}

// Debug logs at debug level.
func Debug(msg string, args ...any) { L().Debug(msg, args...) }

// Info logs at info level.
func Info(msg string, args ...any) { L().Info(msg, args...) }

// Warn logs at warn level.
func Warn(msg string, args ...any) { L().Warn(msg, args...) }

// Error logs at error level.
func Error(msg string, args ...any) { L().Error(msg, args...) }

// Sync is retained for API parity with common logging packages. slog writes
// records synchronously and file writes are unbuffered, so there is nothing
// extra to flush.
func Sync() {}

// Close flushes and releases logger resources.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if globalCloser != nil {
		_ = globalCloser.Close()
		globalCloser = nil
	}
	globalLogger = nil
}
