// Package activity provides bounded, structured application activity logs.
package activity

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultMaxBytes = int64(1 << 20)
	DefaultBackups  = 5
	DefaultReadMax  = 5000
)

var (
	passwordPattern  = regexp.MustCompile(`(?i)(password|pwd)(\s*=\s*|%3[dD])([^&;\s]+)`)
	urlSecretPattern = regexp.MustCompile(`(?i)(postgres(?:ql)?|sqlserver)://([^/@:]+):([^@]*)@`)
)

// Entry is the sanitized representation consumed by the log screen.
type Entry struct {
	Time       time.Time
	Level      string
	Message    string
	Connection string
	Engine     string
	Duration   time.Duration
	Rows       int64
	Error      string
}

// Event describes one connection, query, or application lifecycle event.
type Event struct {
	Level      slog.Level
	Message    string
	Connection string
	Engine     string
	Duration   time.Duration
	Rows       int64
	Error      error
}

// Log writes structured events to a bounded rotating JSONL file.
type Log struct {
	path   string
	writer *rotatingWriter
	logger *slog.Logger
}

// Open creates a rotating JSON log at path.
func Open(path string, level slog.Level) (*Log, error) {
	writer, err := newRotatingWriter(path, DefaultMaxBytes, DefaultBackups)
	if err != nil {
		return nil, err
	}
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})
	return &Log{
		path:   path,
		writer: writer,
		logger: slog.New(handler),
	}, nil
}

// Record appends one sanitized event.
func (l *Log) Record(ctx context.Context, event Event) {
	attrs := []slog.Attr{
		slog.String("connection", Redact(event.Connection)),
		slog.String("engine", Redact(event.Engine)),
		slog.Duration("duration", event.Duration),
		slog.Int64("rows", event.Rows),
	}
	if event.Error != nil {
		attrs = append(attrs, slog.String("error", Redact(event.Error.Error())))
	}
	l.logger.LogAttrs(ctx, event.Level, Redact(event.Message), attrs...)
}

// Entries reads the newest bounded set of events across rotated files.
func (l *Log) Entries(limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = DefaultReadMax
	}
	entries := make([]Entry, 0, limit)
	for i := l.writer.backups; i >= 0; i-- {
		path := l.path
		if i > 0 {
			path += "." + strconv.Itoa(i)
		}
		fileEntries, err := readFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
		if len(entries) > limit {
			entries = slices.Clone(entries[len(entries)-limit:])
		}
	}
	return entries, nil
}

// Close flushes and closes the current log file.
func (l *Log) Close() error {
	return l.writer.Close()
}

// Redact removes common password forms from diagnostic text.
func Redact(value string) string {
	value = passwordPattern.ReplaceAllString(value, `${1}${2}[REDACTED]`)
	return urlSecretPattern.ReplaceAllString(value, `${1}://${2}:[REDACTED]@`)
}

func readFile(path string) ([]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening activity log: %w", err)
	}
	defer file.Close()

	entries := []Entry{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var raw struct {
			Time       time.Time `json:"time"`
			Level      string    `json:"level"`
			Message    string    `json:"msg"`
			Connection string    `json:"connection"`
			Engine     string    `json:"engine"`
			Duration   int64     `json:"duration"`
			Rows       int64     `json:"rows"`
			Error      string    `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			continue
		}
		entries = append(entries, Entry{
			Time:       raw.Time,
			Level:      raw.Level,
			Message:    Redact(raw.Message),
			Connection: Redact(raw.Connection),
			Engine:     Redact(raw.Engine),
			Duration:   time.Duration(raw.Duration),
			Rows:       raw.Rows,
			Error:      Redact(raw.Error),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading activity log: %w", err)
	}
	return entries, nil
}

type rotatingWriter struct {
	path     string
	maxBytes int64
	backups  int
	mu       sync.Mutex
	file     *os.File
	size     int64
}

func newRotatingWriter(path string, maxBytes int64, backups int) (*rotatingWriter, error) {
	if maxBytes < 1 || backups < 1 {
		return nil, errors.New("activity: invalid rotation limits")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating activity directory: %w", err)
	}
	w := &rotatingWriter{path: path, maxBytes: maxBytes, backups: backups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(data)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(data)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening activity log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("stating activity log: %w", err)
	}
	w.file = file
	w.size = info.Size()
	return nil
}

func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("closing activity log for rotation: %w", err)
	}
	w.file = nil
	oldest := w.path + "." + strconv.Itoa(w.backups)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing oldest activity log: %w", err)
	}
	for i := w.backups - 1; i >= 1; i-- {
		oldPath := w.path + "." + strconv.Itoa(i)
		newPath := w.path + "." + strconv.Itoa(i+1)
		if err := os.Rename(oldPath, newPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("rotating activity log: %w", err)
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("rotating current activity log: %w", err)
	}
	return w.open()
}

var _ io.WriteCloser = (*rotatingWriter)(nil)

// ParseLevel validates a CLI log level.
func ParseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("activity: unsupported log level %q", value)
	}
}
