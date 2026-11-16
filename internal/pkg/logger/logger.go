// Package logger provides the process-wide structured logger used by ragflow-x.
//
// It wraps the standard library log/slog and supports JSON or console
// encoding, daily rotating log files named by date, size-based rotation,
// and retention by backup count or age. The rotating writer is built on
// github.com/lestrrat-go/file-rotatelogs, which names files like
// "app-20260820.log" and appends ".1", ".2" suffixes when multiple files
// are produced within a single day by size-based rotation.
//
// Records go where the output setting says (see Resolve):
//
//	both        console and rotating file — the built-in default
//	stdout      console only
//	file        rotating file only, at Config.FilePath
//	<path>      rotating file only at that path (pre-0.6.1 config style)
//
// The default is deliberately both: an unpack-and-run archive gets its log on
// the terminal *and* a dated file that survives the terminal, which is the whole
// point of shipping a log story. When the setting names a file only, stdout is
// NOT mirrored — a daemon whose stdout is redirected to a capture file would
// otherwise write unbounded bytes into a file that rotation knows nothing about.
//
// A log file that cannot be opened never stops the process: the console half
// keeps the records and Destination() reports why (a service that refuses to
// boot because ./logs is missing is a worse failure than a missing log file).
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
	globalDest   Dest
	mu           sync.Mutex
)

const (
	// DefaultOutput is "both": console and a rotating file.
	DefaultOutput = "both"
	// DefaultFilePath is where the rotating file lives when the config does not
	// name a path (relative to the process working directory).
	DefaultFilePath = "./logs/ragflow-x.log"
)

// Config controls logger initialization.
type Config struct {
	Level         string // debug | info | warn | error
	Format        string // json | console
	OutputPath    string // both (default) | stdout | file | a file path (legacy)
	FilePath      string // rotating file path for "file"/"both"; DefaultFilePath when empty
	RotateDaily   bool   // rotate once per day; the active file name carries the date
	RotationSize  int64  // maximum bytes per file before size rotation; 0 disables
	RotationCount uint   // maximum backup files to keep; 0 falls back to age retention
	RetentionDays int    // maximum age in days; used when RotationCount is 0
	Service       string // value placed on the "service" structured field
}

// Dest is the resolved set of log sinks.
type Dest struct {
	Console   bool   // records also go to stdout
	File      string // rotating file path; empty when file output is off
	Legacy    bool   // the setting was a bare path rather than a keyword
	FileError string // why a requested file is unavailable (startup reports it)
}

// Resolve maps the output setting onto the sinks it names.
//
// Keywords are case-insensitive: "both"/"" (defaults), "stdout" (or "console"),
// "file". Anything else is treated as a file path — the pre-0.6.1 meaning of the
// field, kept so existing YAML does not silently switch to console logging.
func Resolve(outputPath, filePath string) Dest {
	out := strings.TrimSpace(outputPath)
	file := strings.TrimSpace(filePath)
	if file == "" {
		file = DefaultFilePath
	}
	switch strings.ToLower(out) {
	case "", "both", "console+file", "file+console":
		return Dest{Console: true, File: file}
	case "stdout", "console":
		return Dest{Console: true}
	case "file":
		return Dest{File: file}
	default:
		return Dest{File: out, Legacy: true}
	}
}

// DefaultConfig returns the default logger configuration.
func DefaultConfig() *Config {
	return &Config{
		Level:         "info",
		Format:        "json",
		OutputPath:    DefaultOutput,
		FilePath:      DefaultFilePath,
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
	if strings.TrimSpace(out.OutputPath) == "" {
		out.OutputPath = DefaultOutput
	}
	if strings.TrimSpace(out.FilePath) == "" {
		out.FilePath = DefaultFilePath
	}
	if out.RotationCount == 0 && out.RetentionDays <= 0 {
		out.RotationCount = 7
	}
	return &out
}

// Init initializes the global logger from cfg (see Resolve for where records
// land). It is safe to call Init multiple times.
//
// A file that cannot be opened is reported through Destination().FileError
// instead of failing the call: the process keeps logging to the console.
func Init(cfg *Config) error {
	Close()

	if cfg == nil {
		cfg = DefaultConfig()
	} else {
		cfg = normalizeConfig(cfg)
	}

	dest := Resolve(cfg.OutputPath, cfg.FilePath)
	writer, closer, err := buildWriter(cfg, dest)
	if err != nil {
		dest.Console = true
		dest.FileError = err.Error()
		dest.File = ""
		writer, closer = os.Stdout, nil
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
	globalDest = dest
	mu.Unlock()
	return nil
}

func buildWriter(cfg *Config, dest Dest) (io.Writer, io.Closer, error) {
	var file io.Writer
	var closer io.Closer
	if dest.File != "" {
		rw, err := newRotatingWriter(cfg, dest.File)
		if err != nil {
			return nil, nil, err
		}
		file, closer = rw, rw
	}
	switch {
	case file != nil && dest.Console:
		// File first: slog ignores Write errors, so a console that is gone (closed
		// pipe, full capture file) must not cost us the rotated record.
		return io.MultiWriter(file, os.Stdout), closer, nil
	case file != nil:
		return file, closer, nil
	default:
		return os.Stdout, nil, nil
	}
}

// Destination reports where the process is actually logging, including the reason
// a requested file is unavailable (FileError), so startup can say it out loud
// instead of leaving the operator to wonder where the file went.
func Destination() Dest {
	mu.Lock()
	defer mu.Unlock()
	return globalDest
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

func newRotatingWriter(cfg *Config, path string) (*rotatingWriter, error) {
	pattern, glob, err := rotatePattern(path)
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
