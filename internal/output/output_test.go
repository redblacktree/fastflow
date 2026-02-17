package output

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnsiStripper(t *testing.T) {
	var buf bytes.Buffer
	s := &ansiStripper{w: &buf}

	input := "\x1b[31mred\x1b[0m normal \x1b[1;32mbold green\x1b[0m"
	n, err := s.Write([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(input) {
		t.Errorf("Write returned %d, want %d", n, len(input))
	}
	got := buf.String()
	if got != "red normal bold green" {
		t.Errorf("got %q, want %q", got, "red normal bold green")
	}
}

func TestAnsiStripperPlainText(t *testing.T) {
	var buf bytes.Buffer
	s := &ansiStripper{w: &buf}

	input := "no escape codes here"
	s.Write([]byte(input))
	if buf.String() != input {
		t.Errorf("got %q, want %q", buf.String(), input)
	}
}

func TestPrintfRoutesToWriter(t *testing.T) {
	var buf bytes.Buffer
	origWriter := Writer
	Writer = &buf
	defer func() { Writer = origWriter }()

	Printf("hello %s %d", "world", 42)
	if buf.String() != "hello world 42" {
		t.Errorf("got %q, want %q", buf.String(), "hello world 42")
	}
}

func TestPrintlnRoutesToWriter(t *testing.T) {
	var buf bytes.Buffer
	origWriter := Writer
	Writer = &buf
	defer func() { Writer = origWriter }()

	Println("line one")
	if !strings.HasPrefix(buf.String(), "line one") {
		t.Errorf("got %q, want prefix %q", buf.String(), "line one")
	}
}

func TestSetupTeesToFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	// Capture stdout side by swapping Writer after Setup
	if err := Setup(logPath); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer Close()

	// Write colored output through Writer
	colored := "\x1b[36m>>> Stage 1\x1b[0m\n"
	Writer.Write([]byte(colored))

	Close() // flush

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	got := string(content)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("log file contains ANSI codes: %q", got)
	}
	if !strings.Contains(got, ">>> Stage 1") {
		t.Errorf("log file missing expected text, got: %q", got)
	}
}
