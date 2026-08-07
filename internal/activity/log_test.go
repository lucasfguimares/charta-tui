package activity

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	t.Parallel()

	value := "postgres://user:secret@localhost/db password=hunter2; pwd=another"
	redacted := Redact(value)
	if strings.Contains(redacted, "secret") || strings.Contains(redacted, "hunter2") || strings.Contains(redacted, "another") {
		t.Fatalf("Redact() leaked secret: %q", redacted)
	}
}

func TestLogRoundTrip(t *testing.T) {
	t.Parallel()

	log, err := Open(filepath.Join(t.TempDir(), "activity.jsonl"), slog.LevelDebug)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	log.Record(context.Background(), Event{
		Level: slog.LevelError, Message: "query failed", Connection: "password=profile-secret", Engine: "sqlite",
		Rows: 2, Error: errors.New("password=do-not-log"),
	})
	entries, err := log.Entries(10)
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Rows != 2 || strings.Contains(entries[0].Error, "do-not-log") {
		t.Fatalf("Entries() = %#v", entries)
	}
	if strings.Contains(entries[0].Connection, "profile-secret") {
		t.Fatalf("Entries() leaked connection secret: %#v", entries)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRotatingWriter(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "activity.jsonl")
	writer, err := newRotatingWriter(path, 10, 2)
	if err != nil {
		t.Fatalf("newRotatingWriter() error = %v", err)
	}
	for range 3 {
		if _, err := writer.Write([]byte("12345678\n")); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := readFile(path + ".1"); err != nil {
		t.Fatalf("rotated log error = %v", err)
	}
}
