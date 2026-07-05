//go:build unit

package server

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// Compile-time check: ColorHandler implements slog.Handler
var _ slog.Handler = (*ColorHandler)(nil)

func TestNewHandler_NonTTY_ReturnsTextHandler(t *testing.T) {
	var buf bytes.Buffer
	h := newHandler(&buf, slog.LevelInfo)
	if _, ok := h.(*slog.TextHandler); !ok {
		t.Errorf("expected *slog.TextHandler, got %T", h)
	}
}

func TestColorHandler_OutputContainsANSICodes(t *testing.T) {
	var buf bytes.Buffer
	handler := &ColorHandler{
		inner:     slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}),
		writer:    &buf,
		addSource: false,
	}
	logger := slog.New(handler)
	logger.Info("test message", "key", "value")

	output := buf.String()
	if !strings.Contains(output, "\033[") {
		t.Errorf("expected ANSI escape codes in output, got: %q", output)
	}
}

func TestNewHandler_NOCOLOR_ReturnsTextHandler(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	h := newHandler(os.Stderr, slog.LevelInfo)
	if _, ok := h.(*slog.TextHandler); !ok {
		t.Errorf("expected *slog.TextHandler, got %T", h)
	}
}

func TestColorHandler_AllLevels(t *testing.T) {
	levels := []slog.Level{
		slog.LevelDebug,
		slog.LevelInfo,
		slog.LevelWarn,
		slog.LevelError,
	}

	for _, lvl := range levels {
		t.Run(lvl.String(), func(t *testing.T) {
			var buf bytes.Buffer
			handler := &ColorHandler{
				inner:     slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
				writer:    &buf,
				addSource: false,
			}
			logger := slog.New(handler)

			// Log at the specified level with a unique message
			msg := "test at " + lvl.String()
			switch lvl {
			case slog.LevelDebug:
				logger.Debug(msg)
			case slog.LevelInfo:
				logger.Info(msg)
			case slog.LevelWarn:
				logger.Warn(msg)
			case slog.LevelError:
				logger.Error(msg)
			}

			output := buf.String()
			if !strings.Contains(output, "\033[") {
				t.Errorf("expected ANSI codes for level %s, got: %q", lvl, output)
			}
			if !strings.Contains(output, msg) {
				t.Errorf("expected message %q in output, got: %q", msg, output)
			}
		})
	}
}
