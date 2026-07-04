package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorGray   = "\033[90m"
	colorWhite  = "\033[97m"
)

// ColorHandler is a slog.Handler wrapper that adds ANSI color to output.
type ColorHandler struct {
	inner     slog.Handler
	writer    io.Writer
	addSource bool
}

// newHandler creates an appropriate slog.Handler based on the output destination.
// - If NO_COLOR env var is set → plain TextHandler
// - If w is a terminal → ColorHandler (ANSI colored)
// - Otherwise → plain TextHandler
// lvl should be a *slog.LevelVar for dynamic level updates, or a fixed slog.Level.
func newHandler(w io.Writer, lvl slog.Leveler) slog.Handler {
	// NO_COLOR convention: https://no-color.org/
	if os.Getenv("NO_COLOR") != "" {
		return slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl})
	}

	// Check if it's a terminal
	if f, ok := w.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return &ColorHandler{
			inner:     slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl, AddSource: true}),
			writer:    w,
			addSource: lvl.Level() <= slog.LevelDebug, // only show caller in debug
		}
	}

	return slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl})
}

// Implement slog.Handler interface

func (h *ColorHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h *ColorHandler) Handle(ctx context.Context, r slog.Record) error {
	// Time
	ts := r.Time.Format("15:04:05.000")

	// Level color and label
	var levelColor string
	var levelLabel string
	switch {
	case r.Level >= slog.LevelError:
		levelColor = colorRed
		levelLabel = "ERRO"
	case r.Level >= slog.LevelWarn:
		levelColor = colorYellow
		levelLabel = "WARN"
	case r.Level >= slog.LevelInfo:
		levelColor = colorGreen
		levelLabel = "INFO"
	default:
		levelColor = colorGray
		levelLabel = "DEBU"
	}

	// Message
	msg := r.Message

	// Collect attrs
	var attrs strings.Builder
	r.Attrs(func(a slog.Attr) bool {
		if attrs.Len() > 0 {
			attrs.WriteByte(' ')
		}
		attrs.WriteString(fmt.Sprintf("%s%s%s=%s%v%s",
			colorGray, a.Key, colorReset,
			colorWhite, a.Value.Any(), colorReset))
		return true
	})

	// Add source info for debug
	var source string
	if h.addSource && r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		if f, _ := fs.Next(); f.File != "" {
			// Short file name
			shortFile := f.File
			if idx := strings.LastIndex(shortFile, "/"); idx >= 0 {
				shortFile = shortFile[idx+1:]
			}
			source = fmt.Sprintf("%s %s:%d%s", colorGray, shortFile, f.Line, colorReset)
		}
	}

	// Build suffix (source + attrs) with proper spacing
	attrsStr := attrs.String()
	var suffix string
	switch {
	case source != "" && attrsStr != "":
		suffix = source + " " + attrsStr
	case source != "":
		suffix = source
	case attrsStr != "":
		suffix = attrsStr
	}

	// Format: time [LEVEL] message source attrs
	// time is gray, level is colored+bold, source is gray (debug only)
	fmt.Fprintf(h.writer, "%s%s%s %s%s%s %s%s\n",
		colorGray, ts, colorReset,
		levelColor, levelLabel, colorReset,
		msg,
		suffix,
	)

	return nil
}

func (h *ColorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ColorHandler{
		inner:     h.inner.WithAttrs(attrs),
		writer:    h.writer,
		addSource: h.addSource,
	}
}

func (h *ColorHandler) WithGroup(name string) slog.Handler {
	return &ColorHandler{
		inner:     h.inner.WithGroup(name),
		writer:    h.writer,
		addSource: h.addSource,
	}
}