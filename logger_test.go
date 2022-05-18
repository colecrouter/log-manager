package main

import (
	"fmt"
	"log"
	"testing"
	"time"
)

var tmpDir string

func setup() {
	if tmpDir != "" {
		return
	}

	// Set up temp dir
	// tmpDir, _ = os.MkdirTemp("", "test")
	tmpDir = "/var/log/mcscreen"

	// defer os.RemoveAll(tmpDir)

	// Set up log rotation
	l := New(LogManagerOptions{
		Dir:              tmpDir,
		FilenameFormat:   `{{ .Time.Format "2006-01-02" }}{{ if .Iteration }}_{{ .Iteration }}{{ end }}`,
		RotationInterval: time.Hour * 24,
	}, time.Now().Add(time.Hour*24).Truncate(time.Hour*24))

	log.SetOutput(l)
	// fmt.Println(l.Rotate())
	fmt.Println(l.GetCurrentFile())
}

func TestFormat(t *testing.T) {
	setup()

	for i := 0; i < 10; i++ {
		log.Println("Hello world!")
		time.Sleep(time.Second)
	}
}
