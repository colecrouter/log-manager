package logmanager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"
)

type LogManager struct {
	sync.Mutex

	options      LogManagerOptions
	templater    *template.Template
	logFile      *os.File
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

func (lm *LogManager) Write(p []byte) (n int, err error) {
	lm.Lock()
	defer lm.Unlock()

	// Check if we need to rotate the log file
	// Check file size
	if lm.options.MaxFileSize > 0 {
		fi, err := lm.logFile.Stat()
		if err != nil {
			return 0, fmt.Errorf("unable to stat log file: %w", err)
		}
		if fi.Size() > lm.options.MaxFileSize {
			err = lm.Rotate()
			if err != nil {
				return 0, fmt.Errorf("unable to rotate log file: %w", err)
			}
		}
	}

	// Check time
	if lm.options.RotationInterval > 0 && time.Since(lm.lastRotation) > lm.options.RotationInterval {
		err = lm.Rotate()
		if err != nil {
			return 0, fmt.Errorf("unable to rotate log file: %w", err)
		}
	}

	return lm.logFile.Write(p)
}

func (lm *LogManager) getFormattedFilename(lt *LogTemplate) (string, error) {
	buf := new(bytes.Buffer)
	err := lm.templater.Execute(buf, lt)
	if err != nil {
		return "", fmt.Errorf("error executing template: %s", err)
	}
	return filepath.Join(lm.options.Dir, buf.String()), nil
}

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
	for {
		// Get the file's potential filename
		newFn, err = lm.getFormattedFilename(lt)
		if err != nil {
			return fmt.Errorf("unable to get formatted filename: %w", err)
		}

		// Check if the file exists
		if _, err := os.Stat(newFn); os.IsNotExist(err) {
			break
		}

		// If it does exist, increment the count and try again
		lt.Iteration++
	}

	if lm.logFile != nil {
		// Close the old log file
		err = lm.logFile.Close()
		if err != nil {
			return
		}

		// Compress the old log file
		if lm.options.GZIP {
			err = compress(lm.GetCurrentFile())
			if err != nil {
				return fmt.Errorf("unable to compress file: %w", err)
			}

			err = os.Remove(lm.GetCurrentFile())
			if err != nil {
				return fmt.Errorf("unable to old log: %w", err)
			}
		}
	}

	// New log file
	lm.logFile, err = os.OpenFile(newFn, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("unable to open new log file: %w", err)
	}

	// Delete old latest.log
	err = lm.setSymlink()
	if err != nil {
		return err
	}

	return
}

func (lm *LogManager) setSymlink() (err error) {
	latestDotLog := filepath.Join(lm.options.Dir, "latest.log")
	os.Remove(latestDotLog)
	if lm.options.LatestDotLog {
		// Create symlink to current log file
		err = os.Symlink(lm.logFile.Name(), latestDotLog)
		if err != nil {
			return fmt.Errorf("unable to create symlink: %w", err)
		}
	}
	return
}

func (lm *LogManager) GetCurrentFile() string {
	return lm.logFile.Name()
}

// Create a new LogManager. `timeFormat` is the format used in `filenameFormat`. `filenameFormat` is a template string for type LogNameTemplate.
func New(options LogManagerOptions, nextRotation time.Time) *LogManager {
	lm := LogManager{options: options}

	// Check if the directory exists and create it if it doesn't
	options.Dir = filepath.Clean(options.Dir)
	_, err := os.Stat(options.Dir)
	if os.IsNotExist(err) {
		os.Mkdir(options.Dir, 0755)
	}

	// Check if filename format is set
	if options.FilenameFormat == "" {
		options.FilenameFormat = `{{ .Time.Format "2006-01-02" }}`
	}

	// Validate template string
	lm.templater, err = template.New("").Parse(options.FilenameFormat + ".log")
	if err != nil {
		panic(err)
	}

	// Check if rotateInterval is smaller than nextRotation - current time
	if options.RotationInterval < time.Until(nextRotation) {
		panic("RotateInterval is smaller than nextRotation - current time")
	}

	// Find the newest log file
	files, err := os.ReadDir(options.Dir)
	if err != nil {
		panic(err)
	}

	// Find the newest file
	var newestFile string
	var newestTime time.Time
	for _, f := range files {
		// Check if the file is a log file
		if !f.IsDir() && filepath.Ext(f.Name()) != ".gz" {
			info, _ := f.Info()
			// If not symlink and newer than current newest file
			if info.Mode()&os.ModeSymlink == 0 && info.ModTime().After(newestTime) {
				newestTime = info.ModTime()
				newestFile = f.Name()
			}
		}
	}

	if newestFile != "" {
		lm.logFile, err = os.OpenFile(filepath.Join(lm.options.Dir, newestFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			panic(err)
		}
	} else {
		err = lm.Rotate()
		if err != nil {
			panic(err)
		}
	}

	// Delete old latest.log
	err = lm.setSymlink()
	if err != nil {
		panic(err)
	}

	// Approx "last rotation time"
	lm.lastRotation = nextRotation.Add(-options.RotationInterval)

	// Start goroutine to rotate log file
	go func() {
		<-time.After(time.Until(nextRotation))
		lm.Rotate()
	}()

	return &lm
}

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
