package logmanager

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func setup(options LogManagerOptions) *LogManager {
	// Create temp folder to hold log files
	dir, err := os.MkdirTemp("", "logmanager_test")
	if err != nil {
		panic(err)
	}
	options.Dir = dir

	// Setup log manager
	lm := NewLogManager(options)

	// Set logger as default logger
	// log.SetOutput(lm)

	return lm
}

func TestNextRotation(t *testing.T) {
	// This shouldn't work, because we haven't included a variation for interval, so the file should not rotate
	lm := setup(LogManagerOptions{
		RotationInterval: time.Millisecond,
		FilenameFormat:   `{{ .Time.Format "2006-01-02" }}.log`,
	})

	old := lm.currentFile.Name()
	time.Sleep(time.Millisecond * 200)
	new := lm.currentFile.Name()

	if old != new {
		t.Error("Log file rotated (was it midnight?)")
	}

	// This should work, because we have included a variation for interval, so the file should rotate
	lm = setup(LogManagerOptions{
		RotationInterval: time.Millisecond,
	})

	old = lm.currentFile.Name()
	time.Sleep(time.Millisecond * 200)
	new = lm.currentFile.Name()

	if old == new {
		t.Error("Log file did not rotate")
	}
}

func TestRotation(t *testing.T) {
	lm := setup(LogManagerOptions{
		RotationInterval: time.Hour,
		FilenameFormat:   "{{.Time.Format \"2006-01-02\"}}.{{.Iteration}}.log",
	})

	old := lm.currentFile.Name()
	err := lm.Rotate()
	if err != nil {
		t.Fatal(err)
	}
	new := lm.currentFile.Name()

	if old == new {
		t.Fatal("Log file did not rotate")
	}
}

func TestGZIP(t *testing.T) {
	lm := setup(LogManagerOptions{
		GZIP: true,
	})

	old := lm.currentFile.Name()
	err := lm.Rotate()
	if err != nil {
		t.Fatal(err)
	}

	// Check if file is gzipped
	_, err = os.Stat(strings.TrimSuffix(old, ".log") + ".tar.gz")
	if err != nil {
		t.Error(err)
	}

	// Check if old file is deleted
	_, err = os.Stat(old)
	if !errors.Is(err, os.ErrNotExist) {
		t.Error("Old log file was not deleted")
	}

	// Try again, but with gzip disabled
	lm = setup(LogManagerOptions{
		GZIP: false,
	})

	old = lm.currentFile.Name()
	err = lm.Rotate()
	if err != nil {
		t.Fatal(err)
	}

	// Check if file is not gzipped
	_, err = os.Stat(strings.TrimSuffix(old, ".log") + ".tar.gz")
	if !errors.Is(err, os.ErrNotExist) {
		t.Error("Log file was gzipped, but gzip was disabled")
	}

	// Check if old file is not deleted
	_, err = os.Stat(old)
	if err != nil {
		t.Error(err)
	}
}

func TestLatestDotLog(t *testing.T) {
	lm := setup(LogManagerOptions{
		LatestDotLog: true,
	})

	// Check that latest.log exists
	l := filepath.Join(lm.options.Dir, "latest.log")
	if _, err := os.Stat(l); err != nil {
		t.Error(err)
	}

	// Check where lates.log is pointing
	old, err := os.Readlink(l)
	if err != nil {
		t.Fatal(err)
	}

	// Rotate
	err = lm.Rotate()
	if err != nil {
		t.Fatal(err)
	}

	// Check that latest.log is still pointing to a different file
	new, err := os.Readlink(l)
	if err != nil {
		t.Fatal(err)
	}

	if old == new {
		t.Fatal("latest.log is still pointing to the same file")
	}

	// Retry with LatestDotLog disabled
	lm = setup(LogManagerOptions{
		LatestDotLog: false,
	})

	// New path to latest.log
	l = filepath.Join(lm.options.Dir, "latest.log")

	// Check that latest.log does not exist
	if _, err := os.Stat(l); !errors.Is(err, os.ErrNotExist) {
		t.Error(err)
	}
}

func TestWrite(t *testing.T) {
	lm := setup(LogManagerOptions{})

	// Write to log
	lm.Write([]byte("test"))

	// Reopen log file w/ RD and check if it contains the string
	lm.currentFile.Close()
	lm.currentFile, _ = os.OpenFile(lm.currentFile.Name(), os.O_RDONLY, 0644)
	b := make([]byte, 4)
	_, err := lm.currentFile.Read(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "test" {
		t.Error("Log file does not contain the string 'test'")
	}
}

func TestWriteRotate(t *testing.T) {
	lm := setup(LogManagerOptions{})

	// Write to log
	lm.Write([]byte("test1"))

	// Rotate log file
	err := lm.Rotate()
	if err != nil {
		t.Fatal(err)
	}

	// Write to log
	lm.Write([]byte("test2"))

	// Reopen log file w/ RD and check if it contains the string
	lm.currentFile.Close()
	lm.currentFile, _ = os.OpenFile(lm.currentFile.Name(), os.O_RDONLY, 0644)
	b := make([]byte, 5)
	_, err = lm.currentFile.Read(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "test2" {
		t.Error("Log file does not contain the string 'test2'")
	}
}

func TestFilenameTemplate(t *testing.T) {
	lm := setup(LogManagerOptions{
		FilenameFormat: `{{.Time.Format "2006-01-02"}}_{{.Iteration}}.log`,
	})

	// Check filename via regex
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{1,}.log$`).MatchString(filepath.Base(lm.currentFile.Name())) {
		t.Error("Filename does not match regex")
	}

	// Check if filename is correct
	filename := lm.currentFile.Name()
	if !strings.HasSuffix(filename, "0.log") {
		t.Error("Filename is not correct")
	}

	// Rotate
	err := lm.Rotate()
	if err != nil {
		t.Fatal(err)
	}

	// Check if filename is correct
	filename = lm.currentFile.Name()
	if !strings.HasSuffix(filename, "1.log") {
		t.Error("Filename is not correct")
	}
}

func TestFileDeleted(t *testing.T) {
	lm := setup(LogManagerOptions{})

	// Write to log
	lm.Write([]byte("test"))

	// Close log file
	lm.currentFile.Close()

	// Check if file exists
	_, err := os.Stat(lm.currentFile.Name())
	if err != nil {
		t.Error(err)
	}

	// Delete log file
	err = os.Remove(lm.currentFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Write to log
	lm.Write([]byte("test"))

	// Check if file exists
	_, err = os.Stat(lm.currentFile.Name())
	if err != nil {
		t.Error(err)
	}
}
