// LogManager implements io.Writer from [os], and is meant to be used directly with the [log] package.
// Use NewLogManager() to create a new LogManager with your desired settings. Upon Write(), it will
// manage rotation, compression, etc. rather than scheduling rotation.
package logmanager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"
)

// LogManager is the main struct of the package. It implements io.Writer, and is safe for concurrent use.
type LogManager struct {
	sync.Mutex

	options      LogManagerOptions
	templater    *template.Template
	currentFile  *os.File
	lastRotation time.Time
}

type LogManagerOptions struct {
	Dir              string
	FilenameFormat   string
	RotationInterval time.Duration
	MaxFileSize      int64
	GZIP             bool
	LatestDotLog     bool
}

type LogTemplate struct {
	Time      time.Time
	Iteration uint
}

// Rotate manually triggers a log rotation
func (lm *LogManager) Rotate() (err error) {
	lm.Lock()
	defer lm.Unlock()

	var newFn string

	lt := &LogTemplate{
		Time:      time.Now(),
		Iteration: 0,
	}

	// Get correct iteration by checking for existing files
	// Start at 0, generate a filename, check if it exists, if it does, increment and try again
	var oldFn string // Check to make sure that the file names are different, otherwise we'll get an infinite loop
	for {
		// Get the file's potential filename
		buf := new(bytes.Buffer)
		err = lm.templater.Execute(buf, lt)
		if err != nil {
			return fmt.Errorf("error executing template: %s", err)
		}
		newFn = filepath.Join(lm.options.Dir, buf.String())

		// Check if filename is different from old filename, otherwise return nothing, keep current file
		if oldFn == newFn {
			return
		}
		oldFn = newFn

		// Check if the file exists
		if _, err := os.Stat(newFn); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return fmt.Errorf("unable to stat file: %w", err)
		}

		// If it does exist, increment the count and try again
		lt.Iteration++
	}

	if lm.currentFile != nil {
		// Close the old log file
		err = lm.currentFile.Close()
		if err != nil {
			return
		}

		// Compress the old log file
		if lm.options.GZIP {
			// This won't throw an error if the file is empty(?), but it won't create a gzip file
			err = compress(lm.currentFile.Name())
			if err != nil {
				return fmt.Errorf("unable to compress file: %w", err)
			}

			err = os.Remove(lm.currentFile.Name())
			if err != nil {
				return fmt.Errorf("unable to old log: %w", err)
			}
		}
	}

	// New log file
	lm.currentFile, err = os.OpenFile(newFn, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("unable to open new log file: %w", err)
	}

	// Update last rotation time
	lm.lastRotation = time.Now()

	fmt.Println("Rotated log file to:", lm.currentFile.Name())

	// Delete old latest.log
	err = lm.setSymlink()
	if err != nil {
		return err
	}

	return
}

// Write checks all of the log manager's conditions, potentially triggers a rotation, then writes to a corresponding log file
func (lm *LogManager) Write(p []byte) (n int, err error) {
	lm.Lock()
	defer lm.Unlock()

	// Stat the file
	fi, err := os.Stat(lm.currentFile.Name())

	// Catch any errors
	if err != nil {
		// Check if file exists, if it doesn't, create it (might have gotten deleted)
		if errors.Is(err, os.ErrNotExist) {
			_, err = os.OpenFile(lm.currentFile.Name(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return
			}
			// Otherwise, return the error
		} else {
			err = fmt.Errorf("unable to stat file: %w", err)
			return
		}
	}

	switch {
	// If we have a configured max file size, check if file + our write is greater than the max file size
	case lm.options.MaxFileSize > 0 && fi.Size()+int64(len(p)) >= lm.options.MaxFileSize:
		fallthrough
	// If we have a configured rotation interval, check if the current time is greater than the last rotation + the rotation interval
	case lm.options.RotationInterval > 0 && time.Since(lm.lastRotation) > lm.options.RotationInterval:
		// Unlock the mutex so we can rotate without deadlocking
		lm.Unlock()
		err = lm.Rotate()
		lm.Lock()
		if err != nil {
			return 0, fmt.Errorf("unable to rotate log file: %w", err)
		}
	}

	return lm.currentFile.Write(p)
}

// setSymlink is a helper function to update/create the "latest" symlink in the log directory
func (lm *LogManager) setSymlink() (err error) {
	latestDotLog := filepath.Join(lm.options.Dir, "latest")
	os.Remove(latestDotLog)
	if lm.options.LatestDotLog && lm.currentFile != nil {
		// Create symlink to current log file
		err = os.Symlink(lm.currentFile.Name(), latestDotLog)
		if err != nil {
			return fmt.Errorf("unable to create symlink: %w", err)
		}
	}

	return
}

// Create a new LogManager. `timeFormat` is the format used in `filenameFormat`. `filenameFormat` is a template string for type LogNameTemplate.
func NewLogManager(options LogManagerOptions) *LogManager {
	lm := LogManager{options: options}

	// Check if the directory exists and create it if it doesn't
	options.Dir = filepath.Clean(options.Dir)
	_, err := os.Stat(options.Dir)
	if os.IsNotExist(err) {
		os.Mkdir(options.Dir, 0755)
	}

	// Check if filename format is set, otherwise use default
	if options.FilenameFormat == "" {
		options.FilenameFormat = `{{ .Time.Format "2006-01-02" }}_{{ .Iteration }}.log`
	}

	// Validate template string
	lm.templater, err = template.New("").Parse(options.FilenameFormat)
	if err != nil {
		panic(err)
	}

	// If latest.log exists, but options.LatestDotLog is false, remove it
	latestDotLog := filepath.Join(options.Dir, "latest.log")
	os.Remove(latestDotLog)
	if !options.LatestDotLog {
		latestDotLog = filepath.Join(options.Dir, "latest")
		os.Remove(latestDotLog)
	}

	// Read all files in the directory, find the latest one
	var newestFile *os.FileInfo
	filepath.Walk(options.Dir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() || info.Name() == "latest" {
			return nil
		}

		if newestFile == nil || info.ModTime().After((*newestFile).ModTime()) {
			newestFile = &info
		}

		return nil
	})

	if newestFile == nil {
		// If there is no newest file, create one
		lm.Rotate()
	} else {
		// Otherwise, open it
		lm.currentFile, err = os.OpenFile(filepath.Join(options.Dir, (*newestFile).Name()), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			panic(err)
		}
	}
	fmt.Println("Current file:", lm.currentFile.Name())

	// Set symlink
	err = lm.setSymlink()
	if err != nil {
		panic(err)
	}

	if options.RotationInterval != 0 {
		if newestFile != nil {
			// Since we have a rotation interval, we can accurately estimate the time of the last rotation
			// We'll look at the modtime of the current file and truncate it to the nearest rotation interval (floor, basically)
			lm.lastRotation = (*newestFile).ModTime().Truncate(options.RotationInterval)
		}
	}

	return &lm
}

// compress is a helper function to gzip a file
func compress(filename string) (err error) {
	// Referenced from https://www.arthurkoziel.com/writing-tar-gz-files-in-go/

	// Create writer for our destination archive
	buf, err := os.Create(filepath.Join(filepath.Dir(filename), strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))) + ".tar.gz")
	if err != nil {
		return
	}

	gw := gzip.NewWriter(buf)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Open the file which will be written into the archive
	file, err := os.Open(filename)
	if err != nil {
		return err
	}

	// Get FileInfo about our file providing file size, mode, etc.
	info, err := file.Stat()
	if err != nil {
		return err
	}

	// Create a tar Header from the FileInfo data
	header, err := tar.FileInfoHeader(info, info.Name())
	if err != nil {
		return err
	}

	// Use full path as name (FileInfoHeader only takes the basename)
	header.Name = filename

	// Write file header to the tar archive
	err = tw.WriteHeader(header)
	if err != nil {
		return err
	}

	// Copy file content to tar archive
	_, err = io.Copy(tw, file)
	if err != nil {
		return err
	}

	return
}
