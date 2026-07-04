package server

import (
	"context"
	"log/slog"
)

// multiHandler dispatches log records to the stderr handler and an optional file handler.
// This enables simultaneous output to terminal (colorized) and file (plain text).
type multiHandler struct {
	stderr slog.Handler
	file   slog.Handler
}

func (h *multiHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.stderr.Enabled(ctx, lvl) || (h.file != nil && h.file.Enabled(ctx, lvl))
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	_ = h.stderr.Handle(ctx, r)
	if h.file != nil {
		_ = h.file.Handle(ctx, r)
	}
	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newH := &multiHandler{stderr: h.stderr.WithAttrs(attrs)}
	if h.file != nil {
		newH.file = h.file.WithAttrs(attrs)
	}
	return newH
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	newH := &multiHandler{stderr: h.stderr.WithGroup(name)}
	if h.file != nil {
		newH.file = h.file.WithGroup(name)
	}
	return newH
}