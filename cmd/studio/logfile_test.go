package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileLoggerKeepsTheConsoleToWarnings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	var console strings.Builder
	log, path, closeLog, err := fileLogger(dir, &console)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("listening")
	log.Warn("disk is low")
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file), "listening") || !strings.Contains(string(file), "disk is low") {
		t.Errorf("log file = %q, want both lines", file)
	}
	if strings.Contains(console.String(), "listening") || !strings.Contains(console.String(), "disk is low") {
		t.Errorf("console = %q, want only the warning", console.String())
	}
}

func TestFileLoggerMovesALargeLogAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "studio.log")
	if err := os.WriteFile(path, make([]byte, maxLogSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, closeLog, err := fileLogger(dir, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeLog() }()
	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != maxLogSize+1 {
		t.Fatalf("studio.log.1 = %v, %v", info, err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("studio.log = %v, %v", info, err)
	}
}
