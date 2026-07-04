package server

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

// CrashLogDir is the subdirectory under the user's home/config dir for crash logs.
const CrashLogDir = ".akswitch"

// CrashLogFilename is the crash log file name.
const CrashLogFilename = "crash.log"

// defaultCrashLogPath returns the default crash log path (~/.akswitch/crash.log).
func defaultCrashLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return CrashLogFilename
	}
	return filepath.Join(home, CrashLogDir, CrashLogFilename)
}

// CrashRecover wraps a function with panic recovery that writes crash details
// to the crash log file and stderr. Returns the recovered value (nil if no panic).
//
// Usage in startServer:
//
//	defer CrashRecover("startServer")
func CrashRecover(context string) (recovered any) {
	recovered = recover()
	if recovered == nil {
		return nil
	}

	crashPath := defaultCrashLogPath()

	// Ensure dir exists
	if dir := filepath.Dir(crashPath); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "[CRASH] failed to create crash log directory %s: %v\n", dir, err)
		}
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	stack := debug.Stack()

	report := fmt.Sprintf("\n%s\n[CRASH] %s — %s\n%s\nMessage: %v\n\nStack:\n%s\n%s\n",
		"══════════════════════════════════════════════════",
		timestamp, context,
		"──────────────────────────────────────────────────",
		recovered,
		string(stack),
		"══════════════════════════════════════════════════\n",
	)

	// Append to crash log
	if f, err := os.OpenFile(crashPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		_, _ = f.WriteString(report)
		_ = f.Close()
	} else {
		fmt.Fprintf(os.Stderr, "[CRASH] failed to write crash log: %v\n", err)
	}

	// Also output to stderr
	fmt.Fprint(os.Stderr, report)

	return recovered
}

// SetupCrashLogDir ensures the crash log directory exists.
// Returns the crash log path.
func SetupCrashLogDir() string {
	path := defaultCrashLogPath()
	dir := filepath.Dir(path)
	if dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}
	return path
}