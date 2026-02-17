// Package output provides a centralized writer for all CLI output.
// It supports teeing output to a log file with ANSI codes stripped,
// making fastflow pipe/redirect friendly.
package output

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sync"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Writer is the destination for all user-facing output.
// It defaults to os.Stdout and is replaced by Setup when --log-file is used.
var Writer io.Writer = os.Stdout

var (
	logFile *os.File
	mu      sync.Mutex
)

// Setup configures the output writer. If logPath is non-empty, output is
// teed to both os.Stdout and the log file (with ANSI codes stripped in the file).
func Setup(logPath string) error {
	if logPath == "" {
		return nil
	}

	mu.Lock()
	defer mu.Unlock()

	f, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	logFile = f
	Writer = io.MultiWriter(os.Stdout, &ansiStripper{w: f})
	return nil
}

// Close closes the log file if one was opened.
func Close() {
	mu.Lock()
	defer mu.Unlock()

	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
}

// Printf formats and writes to the output Writer.
func Printf(format string, a ...interface{}) {
	fmt.Fprintf(Writer, format, a...)
}

// Println writes a line to the output Writer.
func Println(a ...interface{}) {
	fmt.Fprintln(Writer, a...)
}

// ansiStripper is an io.Writer that strips ANSI escape sequences before writing.
type ansiStripper struct {
	w io.Writer
}

func (s *ansiStripper) Write(p []byte) (int, error) {
	cleaned := ansiRegex.ReplaceAll(p, nil)
	_, err := s.w.Write(cleaned)
	// Return original length so the caller (io.MultiWriter) doesn't error.
	return len(p), err
}
