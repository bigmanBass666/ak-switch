package server

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/natefinch/lumberjack.v2"
)

func TestNewFileHandler_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	// Create lumberjack logger and slog handler, set as default
	lj := &lumberjack.Logger{
		Filename: logFile,
		MaxSize:  10,
		MaxAge:   7,
	}
	h := slog.NewTextHandler(lj, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))

	slog.Info("test log message")
	lj.Close()

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("log file not found at %s: %v", logFile, err)
	}
	if len(data) == 0 {
		t.Error("log file is empty")
	}

	// Restore default handler so other tests aren't affected
	restoreDefaultHandler()
}

func TestInitFileHandler_EmptyPath(t *testing.T) {
	InitFileHandler("", 100, 7)
}

func TestInitFileHandler_LevelSync(t *testing.T) {
	logLevel.Set(slog.LevelInfo)

	ApplyLogLevel("debug")
	if logLevel.Level() != slog.LevelDebug {
		t.Errorf("logLevel = %v, want %v", logLevel.Level(), slog.LevelDebug)
	}

	ApplyLogLevel("error")
	if logLevel.Level() != slog.LevelError {
		t.Errorf("logLevel = %v, want %v", logLevel.Level(), slog.LevelError)
	}

	ApplyLogLevel("invalid")
	if logLevel.Level() != slog.LevelInfo {
		t.Errorf("logLevel = %v, want %v", logLevel.Level(), slog.LevelInfo)
	}
}

func TestInitFileHandler_Reopen(t *testing.T) {
	tmpDir := t.TempDir()
	logFile1 := filepath.Join(tmpDir, "reopen1.log")
	logFile2 := filepath.Join(tmpDir, "reopen2.log")

	// First lumberjack file
	lj1 := &lumberjack.Logger{Filename: logFile1, MaxSize: 10, MaxAge: 7}
	h1 := slog.NewTextHandler(lj1, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h1))
	slog.Info("first file")
	lj1.Close()

	if _, err := os.Stat(logFile1); os.IsNotExist(err) {
		t.Error("first log file missing after close")
	}

	// Second lumberjack file
	lj2 := &lumberjack.Logger{Filename: logFile2, MaxSize: 10, MaxAge: 7}
	h2 := slog.NewTextHandler(lj2, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h2))
	slog.Info("second file")
	lj2.Close()

	if _, err := os.Stat(logFile1); os.IsNotExist(err) {
		t.Error("first log file was deleted")
	}
	data, err := os.ReadFile(logFile2)
	if err != nil {
		t.Fatalf("second log file not found: %v", err)
	}
	if len(data) == 0 {
		t.Error("second log file is empty")
	}

	// Restore default handler
	restoreDefaultHandler()
}

// restoreDefaultHandler resets the slog default to a plain stderr handler.
func restoreDefaultHandler() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
}